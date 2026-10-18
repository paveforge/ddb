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
