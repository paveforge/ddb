package table

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/paveforge/go-ddb-cli/internal/format"
)

type API interface {
	DescribeTable(context.Context, *dynamodb.DescribeTableInput, ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
	CreateTable(context.Context, *dynamodb.CreateTableInput, ...func(*dynamodb.Options)) (*dynamodb.CreateTableOutput, error)
}

func Load(path string) (*dynamodb.CreateTableInput, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > 1024*1024 {
		return nil, fmt.Errorf("table definition must be a regular file of at most 1 MiB")
	}
	d := json.NewDecoder(io.LimitReader(f, 1024*1024+1))
	d.DisallowUnknownFields()
	var v dynamodb.CreateTableInput
	if e = d.Decode(&v); e != nil {
		return nil, fmt.Errorf("invalid table definition: %w", e)
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("trailing or oversized table definition")
	}
	if e = Validate(&v); e != nil {
		return nil, e
	}
	return &v, nil
}
func Validate(v *dynamodb.CreateTableInput) error {
	n := aws.ToString(v.TableName)
	if len(n) < 3 || len(n) > 255 {
		return fmt.Errorf("TableName must be 3..255 characters")
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return fmt.Errorf("invalid TableName")
		}
	}
	defs := map[string]types.ScalarAttributeType{}
	for _, d := range v.AttributeDefinitions {
		name := aws.ToString(d.AttributeName)
		if name == "" || defs[name] != "" {
			return fmt.Errorf("invalid or duplicate attribute definition")
		}
		if d.AttributeType != "S" && d.AttributeType != "N" && d.AttributeType != "B" {
			return fmt.Errorf("invalid key attribute type")
		}
		defs[name] = d.AttributeType
	}
	used := map[string]bool{}
	check := func(keys []types.KeySchemaElement) error {
		if len(keys) < 1 || len(keys) > 2 {
			return fmt.Errorf("key schema must have HASH and optional RANGE")
		}
		hash, ran := 0, 0
		seen := map[string]bool{}
		for _, k := range keys {
			n := aws.ToString(k.AttributeName)
			if defs[n] == "" || seen[n] {
				return fmt.Errorf("key missing definition or duplicated")
			}
			seen[n] = true
			used[n] = true
			switch k.KeyType {
			case types.KeyTypeHash:
				hash++
			case types.KeyTypeRange:
				ran++
			default:
				return fmt.Errorf("invalid key type")
			}
		}
		if hash != 1 || ran > 1 {
			return fmt.Errorf("invalid HASH/RANGE schema")
		}
		return nil
	}
	if e := check(v.KeySchema); e != nil {
		return e
	}
	if v.BillingMode != "" && v.BillingMode != types.BillingModePayPerRequest && v.BillingMode != types.BillingModeProvisioned {
		return fmt.Errorf("invalid BillingMode")
	}
	onDemand := v.BillingMode == types.BillingModePayPerRequest
	throughput := func(p *types.ProvisionedThroughput) error {
		if onDemand {
			if p != nil {
				return fmt.Errorf("PAY_PER_REQUEST cannot specify ProvisionedThroughput")
			}
			return nil
		}
		if p == nil || aws.ToInt64(p.ReadCapacityUnits) < 1 || aws.ToInt64(p.WriteCapacityUnits) < 1 {
			return fmt.Errorf("provisioned billing requires positive read/write throughput")
		}
		return nil
	}
	if e := throughput(v.ProvisionedThroughput); e != nil {
		return e
	}
	names := map[string]bool{}
	projection := func(p *types.Projection) error {
		if p == nil {
			return fmt.Errorf("index requires Projection")
		}
		switch p.ProjectionType {
		case types.ProjectionTypeAll, types.ProjectionTypeKeysOnly:
			if len(p.NonKeyAttributes) > 0 {
				return fmt.Errorf("NonKeyAttributes require INCLUDE")
			}
		case types.ProjectionTypeInclude:
			if len(p.NonKeyAttributes) == 0 || len(p.NonKeyAttributes) > 20 {
				return fmt.Errorf("INCLUDE needs 1..20 NonKeyAttributes")
			}
		default:
			return fmt.Errorf("invalid projection")
		}
		return nil
	}
	for _, g := range v.GlobalSecondaryIndexes {
		n := aws.ToString(g.IndexName)
		if n == "" || names[n] {
			return fmt.Errorf("invalid or duplicate index name")
		}
		names[n] = true
		if e := check(g.KeySchema); e != nil {
			return e
		}
		if e := throughput(g.ProvisionedThroughput); e != nil {
			return e
		}
		if e := projection(g.Projection); e != nil {
			return e
		}
	}
	for _, l := range v.LocalSecondaryIndexes {
		n := aws.ToString(l.IndexName)
		if n == "" || names[n] {
			return fmt.Errorf("invalid or duplicate index name")
		}
		names[n] = true
		if e := check(l.KeySchema); e != nil {
			return e
		}
		if len(l.KeySchema) != 2 || len(v.KeySchema) != 2 || hashName(l.KeySchema) != hashName(v.KeySchema) {
			return fmt.Errorf("local index needs table HASH and a RANGE")
		}
		if e := projection(l.Projection); e != nil {
			return e
		}
	}
	if len(used) != len(defs) {
		return fmt.Errorf("AttributeDefinitions must describe only table/index keys")
	}
	return nil
}
func hashName(k []types.KeySchemaElement) string {
	for _, v := range k {
		if v.KeyType == types.KeyTypeHash {
			return aws.ToString(v.AttributeName)
		}
	}
	return ""
}
func Describe(ctx context.Context, api API, name string) (*types.TableDescription, error) {
	o, e := api.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(name)})
	var missing *types.ResourceNotFoundException
	if errors.As(e, &missing) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if o == nil || o.Table == nil {
		return nil, fmt.Errorf("DescribeTable returned no table")
	}
	return o.Table, nil
}
func FromDefinition(v *dynamodb.CreateTableInput) *types.TableDescription {
	d := &types.TableDescription{TableName: v.TableName, AttributeDefinitions: v.AttributeDefinitions, KeySchema: v.KeySchema}
	for _, g := range v.GlobalSecondaryIndexes {
		d.GlobalSecondaryIndexes = append(d.GlobalSecondaryIndexes, types.GlobalSecondaryIndexDescription{IndexName: g.IndexName, KeySchema: g.KeySchema, Projection: g.Projection})
	}
	for _, l := range v.LocalSecondaryIndexes {
		d.LocalSecondaryIndexes = append(d.LocalSecondaryIndexes, types.LocalSecondaryIndexDescription{IndexName: l.IndexName, KeySchema: l.KeySchema, Projection: l.Projection})
	}
	return d
}
func keys(k []types.KeySchemaElement) []string {
	a := []string{}
	for _, v := range k {
		a = append(a, string(v.KeyType)+":"+aws.ToString(v.AttributeName))
	}
	sort.Strings(a)
	return a
}
func projections(p *types.Projection) string {
	if p == nil {
		return ""
	}
	a := append([]string{}, p.NonKeyAttributes...)
	sort.Strings(a)
	b, _ := json.Marshal(a)
	return string(p.ProjectionType) + string(b)
}
func Compatible(want *dynamodb.CreateTableInput, have *types.TableDescription) error {
	w := FromDefinition(want)
	if !reflect.DeepEqual(keys(w.KeySchema), keys(have.KeySchema)) {
		return fmt.Errorf("destination primary key schema differs from definition")
	}
	defs := func(a []types.AttributeDefinition) map[string]types.ScalarAttributeType {
		m := map[string]types.ScalarAttributeType{}
		for _, v := range a {
			m[aws.ToString(v.AttributeName)] = v.AttributeType
		}
		return m
	}
	if !reflect.DeepEqual(defs(w.AttributeDefinitions), defs(have.AttributeDefinitions)) {
		return fmt.Errorf("destination key attribute types differ")
	}
	indexes := func(d *types.TableDescription) map[string]string {
		m := map[string]string{}
		for _, g := range d.GlobalSecondaryIndexes {
			b, _ := json.Marshal(keys(g.KeySchema))
			m["G:"+aws.ToString(g.IndexName)] = string(b) + projections(g.Projection)
		}
		for _, l := range d.LocalSecondaryIndexes {
			b, _ := json.Marshal(keys(l.KeySchema))
			m["L:"+aws.ToString(l.IndexName)] = string(b) + projections(l.Projection)
		}
		return m
	}
	if !reflect.DeepEqual(indexes(w), indexes(have)) {
		return fmt.Errorf("destination index definitions differ")
	}
	return nil
}
func Ready(d *types.TableDescription) bool {
	if d.TableStatus != types.TableStatusActive {
		return false
	}
	for _, g := range d.GlobalSecondaryIndexes {
		if g.IndexStatus != types.IndexStatusActive {
			return false
		}
	}
	return true
}
func ValidateItem(item format.Item, d *types.TableDescription) error {
	if format.Size(item) > 400*1024 {
		return fmt.Errorf("item exceeds conservative 400 KiB stored-size limit")
	}
	defs := map[string]types.ScalarAttributeType{}
	for _, v := range d.AttributeDefinitions {
		defs[aws.ToString(v.AttributeName)] = v.AttributeType
	}
	required := map[string]bool{}
	for _, k := range d.KeySchema {
		required[aws.ToString(k.AttributeName)] = true
	}
	limits := map[string]int{}
	all := append([]types.KeySchemaElement{}, d.KeySchema...)
	for _, g := range d.GlobalSecondaryIndexes {
		all = append(all, g.KeySchema...)
	}
	for _, l := range d.LocalSecondaryIndexes {
		all = append(all, l.KeySchema...)
	}
	for _, k := range all {
		n := aws.ToString(k.AttributeName)
		limit := 2048
		if k.KeyType == types.KeyTypeRange {
			limit = 1024
		}
		if limits[n] == 0 || limit < limits[n] {
			limits[n] = limit
		}
	}
	for name, typ := range defs {
		a, ok := item[name]
		if !ok {
			if required[name] {
				return fmt.Errorf("missing primary key attribute")
			}
			continue
		}
		size := 0
		valid := false
		switch v := a.(type) {
		case *types.AttributeValueMemberS:
			valid = typ == types.ScalarAttributeTypeS
			size = len(v.Value)
		case *types.AttributeValueMemberB:
			valid = typ == types.ScalarAttributeTypeB
			size = len(v.Value)
		case *types.AttributeValueMemberN:
			valid = typ == types.ScalarAttributeTypeN
			size = 1
		}
		if !valid || size == 0 || size > limits[name] {
			return fmt.Errorf("invalid table/index key type or length")
		}
	}
	return nil
}
func Key(item format.Item, d *types.TableDescription) format.Item {
	m := format.Item{}
	for _, k := range d.KeySchema {
		n := aws.ToString(k.AttributeName)
		m[n] = item[n]
	}
	return m
}
