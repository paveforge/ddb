package format

import (
	"bytes"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	input := `{"Item":{"id":{"S":"x"},"n":{"N":"12345678901234567890123456789012345678"},"b":{"B":"AQID"},"ss":{"SS":["a","b"]},"ns":{"NS":["1","2"]},"bs":{"BS":["AQ=="]},"m":{"M":{"null":{"NULL":true},"l":{"L":[{"BOOL":false},{"S":""}]}}}}}`
	var b bytes.Buffer
	enc, _ := NewEncoder(&b, "dynamodb-jsonl")
	if err := Decode(strings.NewReader(input), "dynamodb-jsonl", enc.Write); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Decode(&b, "dynamodb-jsonl", func(i Item) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
func TestRejectInvalid(t *testing.T) {
	for _, input := range []string{`{"Item":{"x":{"N":"NaN"}}}`, `{"Item":{"x":{"NS":["1","1.0"]}}}`, `{"Item":{"x":{"SS":[]}}}`, `{"Item":{"x":{"NULL":false}}}`, `{"Item":{"x":{"S":"a","N":"1"}}}`, `{"Item":{"x":{"S":"a"},"x":{"S":"b"}}}`, `{"Item":{"x":{"N":"1e126"}}}`, `{"Item":{"x":{"N":"1e-131"}}}`} {
		if e := Decode(strings.NewReader(input), "dynamodb-jsonl", func(Item) error { return nil }); e == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
func TestPlainAndTrailing(t *testing.T) {
	var b bytes.Buffer
	e, _ := NewEncoder(&b, "json")
	if err := Decode(strings.NewReader(`[{"n":12345678901234567890,"l":[true,null,"x"]}]`), "json", e.Write); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if !strings.Contains(b.String(), "12345678901234567890") {
		t.Fatal(b.String())
	}
	if err := Decode(strings.NewReader(`[] {}`), "json", func(Item) error { return nil }); err == nil {
		t.Fatal("accepted trailing data")
	}
	if err := Decode(strings.NewReader(`{"Item":{"x":{"SS":["x"]}}}`), "dynamodb-jsonl", e.Write); err == nil {
		t.Fatal("silently exported set")
	}
}
