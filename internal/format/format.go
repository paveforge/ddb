// Package format implements bounded, streaming transfer formats.
package format

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type Item = map[string]types.AttributeValue

const MaxRecord = 4 * 1024 * 1024

var numberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func Number(s string) error {
	if len(s) > 256 || !numberPattern.MatchString(s) {
		return fmt.Errorf("invalid DynamoDB number")
	}
	parts := strings.Split(strings.ToLower(strings.TrimPrefix(s, "-")), "e")
	exponent := 0
	if len(parts) == 2 {
		var err error
		exponent, err = strconv.Atoi(parts[1])
		if err != nil || exponent < -1000 || exponent > 1000 {
			return fmt.Errorf("invalid number exponent")
		}
	}
	decimal := strings.Split(parts[0], ".")
	digits := strings.Join(decimal, "")
	nonzero := strings.TrimLeft(digits, "0")
	if nonzero == "" {
		return nil
	}
	magnitude := len(decimal[0]) - (len(digits) - len(nonzero)) - 1 + exponent
	significant := strings.TrimRight(nonzero, "0")
	if len(significant) > 38 || magnitude < -130 || magnitude > 125 {
		return fmt.Errorf("number exceeds DynamoDB precision or range")
	}

	return nil
}

// strictValue rejects duplicate JSON object fields, which would silently lose data.
func strictValue(d *json.Decoder, depth int) (any, error) {
	if depth > 100 {
		return nil, fmt.Errorf("JSON nesting exceeds supported depth")
	}
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			key := k.(string)
			if _, ok := m[key]; ok {
				return nil, fmt.Errorf("duplicate JSON field")
			}
			v, e := strictValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			m[key] = v
		}
		_, e = d.Token()
		return m, e
	case '[':
		a := []any{}
		for d.More() {
			v, e := strictValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, e = d.Token()
		return a, e
	}
	return nil, fmt.Errorf("unexpected delimiter")
}
func parse(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, e := strictValue(d, 0)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("trailing JSON content")
	}
	return v, nil
}

func tagged(v any, depth int) (types.AttributeValue, error) {
	if depth > 32 {
		return nil, fmt.Errorf("DynamoDB nesting exceeds 32 levels")
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return nil, fmt.Errorf("attribute must contain exactly one type tag")
	}
	for tag, v := range m {
		switch tag {
		case "S":
			if s, ok := v.(string); ok {
				return &types.AttributeValueMemberS{Value: s}, nil
			}
		case "N":
			if s, ok := v.(string); ok {
				if e := Number(s); e != nil {
					return nil, e
				}
				return &types.AttributeValueMemberN{Value: s}, nil
			}
		case "B":
			if s, ok := v.(string); ok {
				b, e := base64.StdEncoding.DecodeString(s)
				return &types.AttributeValueMemberB{Value: b}, e
			}
		case "BOOL":
			if b, ok := v.(bool); ok {
				return &types.AttributeValueMemberBOOL{Value: b}, nil
			}
		case "NULL":
			if b, ok := v.(bool); ok && b {
				return &types.AttributeValueMemberNULL{Value: true}, nil
			}
		case "M":
			if x, ok := v.(map[string]any); ok {
				a, e := fromMap(x, true, depth+1)
				return &types.AttributeValueMemberM{Value: a}, e
			}
		case "L":
			if a, ok := v.([]any); ok {
				r := make([]types.AttributeValue, 0, len(a))
				for _, x := range a {
					z, e := tagged(x, depth+1)
					if e != nil {
						return nil, e
					}
					r = append(r, z)
				}
				return &types.AttributeValueMemberL{Value: r}, nil
			}
		case "SS", "NS", "BS":
			if a, ok := v.([]any); ok && len(a) > 0 {
				str := []string{}
				bs := [][]byte{}
				seen := map[string]bool{}
				for _, x := range a {
					s, ok := x.(string)
					if !ok {
						return nil, fmt.Errorf("set members must be strings")
					}
					identity := s
					if tag == "NS" {
						if e := Number(s); e != nil {
							return nil, e
						}
						f, _ := new(big.Rat).SetString(s)
						identity = f.RatString()
					}
					if tag == "BS" {
						b, e := base64.StdEncoding.DecodeString(s)
						if e != nil {
							return nil, e
						}
						identity = string(b)
						bs = append(bs, b)
					}
					if seen[identity] {
						return nil, fmt.Errorf("duplicate set member")
					}
					seen[identity] = true
					str = append(str, s)
				}
				switch tag {
				case "SS":
					return &types.AttributeValueMemberSS{Value: str}, nil
				case "NS":
					return &types.AttributeValueMemberNS{Value: str}, nil
				case "BS":
					return &types.AttributeValueMemberBS{Value: bs}, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("invalid attribute tag or value")
}
func plain(v any, depth int) (types.AttributeValue, error) {
	if depth > 32 {
		return nil, fmt.Errorf("DynamoDB nesting exceeds 32 levels")
	}
	switch x := v.(type) {
	case nil:
		return &types.AttributeValueMemberNULL{Value: true}, nil
	case string:
		return &types.AttributeValueMemberS{Value: x}, nil
	case bool:
		return &types.AttributeValueMemberBOOL{Value: x}, nil
	case json.Number:
		if e := Number(string(x)); e != nil {
			return nil, e
		}
		return &types.AttributeValueMemberN{Value: string(x)}, nil
	case map[string]any:
		m, e := fromMap(x, false, depth+1)
		return &types.AttributeValueMemberM{Value: m}, e
	case []any:
		a := []types.AttributeValue{}
		for _, v := range x {
			z, e := plain(v, depth+1)
			if e != nil {
				return nil, e
			}
			a = append(a, z)
		}
		return &types.AttributeValueMemberL{Value: a}, nil
	}
	return nil, fmt.Errorf("unsupported JSON value")
}
func fromMap(m map[string]any, tag bool, depth int) (Item, error) {
	r := Item{}
	for k, v := range m {
		if k == "" || len(k) > 65535 {
			return nil, fmt.Errorf("invalid attribute name length")
		}
		var a types.AttributeValue
		var e error
		if tag {
			a, e = tagged(v, depth)
		} else {
			a, e = plain(v, depth)
		}
		if e != nil {
			return nil, e
		}
		r[k] = a
	}
	return r, nil
}

// Decode invokes emit once per record and never retains the complete dataset.
func Decode(r io.Reader, kind string, emit func(Item) error) error {
	switch kind {
	case "dynamodb-jsonl":
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 64*1024), MaxRecord)
		var line int64
		for s.Scan() {
			line++
			if len(bytes.TrimSpace(s.Bytes())) == 0 {
				continue
			}
			v, e := parse(s.Bytes())
			if e != nil {
				return fmt.Errorf("line %d: invalid JSON", line)
			}
			m, ok := v.(map[string]any)
			if !ok || len(m) != 1 {
				return fmt.Errorf("line %d: expected Item envelope", line)
			}
			a, ok := m["Item"].(map[string]any)
			if !ok {
				return fmt.Errorf("line %d: expected Item object", line)
			}
			item, e := fromMap(a, true, 0)
			if e != nil {
				return fmt.Errorf("line %d: %w", line, e)
			}
			if e = emit(item); e != nil {
				return e
			}
		}
		return s.Err()
	case "json":
		return decodeArray(r, emit)

	}
	return fmt.Errorf("unsupported format %q", kind)
}

// Read one bounded object at a time so malformed giant records cannot exhaust memory.
func decodeArray(r io.Reader, emit func(Item) error) error {
	b := bufio.NewReaderSize(r, 64*1024)
	next := func() (byte, error) {
		for {
			c, e := b.ReadByte()
			if e != nil {
				return 0, e
			}
			if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
				return c, nil
			}
		}
	}
	c, e := next()
	if e != nil || c != '[' {
		return fmt.Errorf("expected JSON array")
	}
	c, e = next()
	if e != nil {
		return e
	}
	if c != ']' {
		for {
			if c != '{' {
				return fmt.Errorf("record must be an object")
			}
			raw := []byte{'{'}
			depth := 1
			quoted, escaped := false, false
			for depth > 0 {
				c, e = b.ReadByte()
				if e != nil {
					return e
				}
				raw = append(raw, c)
				if len(raw) > MaxRecord {
					return fmt.Errorf("record exceeds maximum encoded size")
				}
				if quoted {
					if escaped {
						escaped = false
					} else if c == '\\' {
						escaped = true
					} else if c == '"' {
						quoted = false
					}
				} else {
					switch c {
					case '"':
						quoted = true
					case '{', '[':
						depth++
						if depth > 40 {
							return fmt.Errorf("JSON nesting too deep")
						}
					case '}', ']':
						depth--
					}
				}
			}
			v, e := parse(raw)
			if e != nil {
				return fmt.Errorf("invalid JSON record")
			}
			m, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("record must be an object")
			}
			item, e := fromMap(m, false, 0)
			if e != nil {
				return e
			}
			if e = emit(item); e != nil {
				return e
			}
			c, e = next()
			if e != nil {
				return e
			}
			if c == ']' {
				break
			}
			if c != ',' {
				return fmt.Errorf("expected array comma")
			}
			c, e = next()
			if e != nil {
				return e
			}
		}
	}
	if _, e = next(); e != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func wire(a types.AttributeValue, tag bool) (any, error) {
	wrap := func(k string, v any) (any, error) {
		if tag {
			return map[string]any{k: v}, nil
		}
		return v, nil
	}
	switch x := a.(type) {
	case *types.AttributeValueMemberS:
		return wrap("S", x.Value)
	case *types.AttributeValueMemberN:
		if tag {
			return wrap("N", x.Value)
		}
		return json.Number(x.Value), nil
	case *types.AttributeValueMemberB:
		if !tag {
			return nil, fmt.Errorf("binary cannot be exported losslessly as plain JSON")
		}
		return wrap("B", base64.StdEncoding.EncodeToString(x.Value))
	case *types.AttributeValueMemberBOOL:
		return wrap("BOOL", x.Value)
	case *types.AttributeValueMemberNULL:
		if tag {
			return wrap("NULL", true)
		}
		return nil, nil
	case *types.AttributeValueMemberSS:
		if !tag {
			return nil, fmt.Errorf("sets cannot be exported losslessly as plain JSON")
		}
		return wrap("SS", x.Value)
	case *types.AttributeValueMemberNS:
		if !tag {
			return nil, fmt.Errorf("sets cannot be exported losslessly as plain JSON")
		}
		return wrap("NS", x.Value)
	case *types.AttributeValueMemberBS:
		if !tag {
			return nil, fmt.Errorf("sets cannot be exported losslessly as plain JSON")
		}
		b := make([]string, len(x.Value))
		for i, v := range x.Value {
			b[i] = base64.StdEncoding.EncodeToString(v)
		}
		return wrap("BS", b)
	case *types.AttributeValueMemberM:
		m, e := object(x.Value, tag)
		if e != nil {
			return nil, e
		}
		return wrap("M", m)
	case *types.AttributeValueMemberL:
		a := make([]any, len(x.Value))
		for i, v := range x.Value {
			z, e := wire(v, tag)
			if e != nil {
				return nil, e
			}
			a[i] = z
		}
		return wrap("L", a)
	}
	return nil, fmt.Errorf("unknown DynamoDB attribute type")
}
func object(item Item, tag bool) (map[string]any, error) {
	m := map[string]any{}
	for k, v := range item {
		z, e := wire(v, tag)
		if e != nil {
			return nil, e
		}
		m[k] = z
	}
	return m, nil
}
func Marshal(item Item, tag bool) ([]byte, error) {
	m, e := object(item, tag)
	if e != nil {
		return nil, e
	}
	if tag {
		return json.Marshal(map[string]any{"Item": m})
	}
	return json.Marshal(m)
}

type Encoder struct {
	w     io.Writer
	kind  string
	count int64
	err   error
}

func NewEncoder(w io.Writer, kind string) (*Encoder, error) {
	if kind != "dynamodb-jsonl" && kind != "json" {
		return nil, fmt.Errorf("unsupported format")
	}
	e := &Encoder{w: w, kind: kind}
	if kind == "json" {
		_, e.err = io.WriteString(w, "[\n")
	}
	return e, e.err
}
func (e *Encoder) Write(item Item) error {
	if e.err != nil {
		return e.err
	}
	b, err := Marshal(item, e.kind == "dynamodb-jsonl")
	if err != nil {
		return err
	}
	if e.kind == "json" && e.count > 0 {
		_, e.err = io.WriteString(e.w, ",\n")
	}
	if e.err == nil {
		_, e.err = e.w.Write(b)
	}
	if e.err == nil && e.kind == "dynamodb-jsonl" {
		_, e.err = io.WriteString(e.w, "\n")
	}
	e.count++
	return e.err
}
func (e *Encoder) Close() error {
	if e.err != nil {
		return e.err
	}
	if e.kind == "json" {
		_, e.err = io.WriteString(e.w, "\n]\n")
	}
	return e.err
}

// Size estimates stored DynamoDB bytes using documented per-type accounting.
// Number accounting uses its conservative documented upper bound.
func Size(item Item) int {
	n := 0
	for k, v := range item {
		n += len(k) + valueSize(v)
	}
	return n
}
func valueSize(a types.AttributeValue) int {
	switch x := a.(type) {
	case *types.AttributeValueMemberS:
		return len(x.Value)
	case *types.AttributeValueMemberN:
		return 21
	case *types.AttributeValueMemberB:
		return len(x.Value)
	case *types.AttributeValueMemberBOOL, *types.AttributeValueMemberNULL:
		return 1
	case *types.AttributeValueMemberSS:
		n := 0
		for _, v := range x.Value {
			n += len(v)
		}
		return n
	case *types.AttributeValueMemberNS:
		return 21 * len(x.Value)
	case *types.AttributeValueMemberBS:
		n := 0
		for _, v := range x.Value {
			n += len(v)
		}
		return n
	case *types.AttributeValueMemberM:
		return 3 + len(x.Value) + Size(x.Value)
	case *types.AttributeValueMemberL:
		n := 3 + len(x.Value)
		for _, v := range x.Value {
			n += valueSize(v)
		}
		return n
	}
	return 400*1024 + 1
}
