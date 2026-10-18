package cli

import (
	"bytes"
	"testing"
)

func TestVersion(t *testing.T) {
	var b bytes.Buffer
	c := New(&b, &b)
	c.SetArgs([]string{"version"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b.Bytes(), []byte("ddb dev")) {
		t.Fatal(b.String())
	}
}

func TestExclusiveImportFlags(t *testing.T) {
	for _, args := range [][]string{{"import"}, {"import", "--table", "one", "--table-file", "table.json", "--input", "data.jsonl"}} {
		var b bytes.Buffer
		c := New(&b, &b)
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
