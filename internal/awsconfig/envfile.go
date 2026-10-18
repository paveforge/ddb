package awsconfig

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// readEnvFile parses a deliberately small dotenv subset. It never expands values
// or includes source text in errors, since even malformed lines can contain keys.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open env file: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect env file: %w", err)
	}
	if !st.Mode().IsRegular() || st.Size() > 1024*1024 {
		return nil, fmt.Errorf("env file must be a regular file of at most 1 MiB")
	}
	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if lineNumber == 1 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		invalid := func() (map[string]string, error) {
			return nil, fmt.Errorf("env file line %d: invalid assignment", lineNumber)
		}
		if !utf8.ValidString(line) || strings.ContainsRune(line, 0) {
			return invalid()
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimSpace(line[len("export"):])
		}
		key, raw, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || !envKey(key) {
			return invalid()
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("env file line %d: duplicate variable", lineNumber)
		}
		value, ok := envValue(strings.TrimSpace(raw))
		if !ok {
			return invalid()
		}
		values[key] = value
	}
	if scanner.Err() != nil {
		return nil, fmt.Errorf("env file line %d: unable to read assignment (maximum line size 64 KiB)", lineNumber+1)
	}
	return values, nil
}
func envKey(key string) bool {
	if key == "" {
		return false
	}
	for i, c := range key {
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}
func envValue(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	if raw[0] != '\'' && raw[0] != '"' {
		for i, c := range raw {
			if c == '#' && (i == 0 || raw[i-1] == ' ' || raw[i-1] == '\t') {
				raw = raw[:i]
				break
			}
		}
		return strings.TrimSpace(raw), true
	}
	quote := raw[0]
	var out strings.Builder
	for i := 1; i < len(raw); i++ {
		c := raw[i]
		if c == quote {
			tail := strings.TrimSpace(raw[i+1:])
			return out.String(), tail == "" || strings.HasPrefix(tail, "#")
		}
		if quote == '"' && c == '\\' {
			i++
			if i == len(raw) {
				return "", false
			}
			switch raw[i] {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case '\\', '"':
				c = raw[i]
			default:
				return "", false
			}
		}
		out.WriteByte(c)
	}
	return "", false
}

// Environment values win, including explicitly empty ones. Legacy credential
// aliases are supported in the process environment just as in the AWS SDK.
func setting(values map[string]string, key string, aliases ...string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	for _, alias := range aliases {
		if v, ok := os.LookupEnv(alias); ok {
			return v
		}
	}
	return values[key]
}
