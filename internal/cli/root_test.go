package cli

import (
	"bytes"
	"github.com/paveforge/ddb/internal/report"
	"runtime/debug"
	"strings"
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

func TestFlagErrorExitCode(t *testing.T) {
	var b bytes.Buffer
	c := New(&b, &b)
	c.SetArgs([]string{"import", "--unknown"})
	if err := c.Execute(); report.ExitCode(err) != 2 {
		t.Fatalf("%v", err)
	}
}

func TestEnvFileFlagAndErrors(t *testing.T) {
	for _, command := range []string{"import", "export"} {
		t.Run(command, func(t *testing.T) {
			var b bytes.Buffer
			c := New(&b, &b)
			c.SetArgs([]string{command, "--help"})
			if err := c.Execute(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(b.Bytes(), []byte("--env-file")) {
				t.Fatal("missing env-file flag")
			}
			b.Reset()
			c = New(&b, &b)
			args := []string{command, "--table", "users", "--env-file", "missing.env", "--profile", "development"}
			if command == "import" {
				args = append(args, "--input", "data.jsonl", "--dry-run")
			} else {
				args = append(args, "--output", "out.jsonl")
			}
			c.SetArgs(args)
			err := c.Execute()
			if report.ExitCode(err) != 2 || !strings.Contains(err.Error(), "cannot be combined") {
				t.Fatalf("invalid conflict: %v", err)
			}
		})
	}
}

func TestBuildMetadata(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.time", Value: "today"}}}
	v, c, d := buildMetadata(info)
	if v != "v1.2.3" || c != "abc" || d != "today" {
		t.Fatalf("%s %s %s", v, c, d)
	}
	oldV, oldC, oldD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldV, oldC, oldD })
	Version, Commit, Date = "release", "injected", "date"
	v, c, d = buildMetadata(info)
	if v != "release" || c != "injected" || d != "date" {
		t.Fatalf("%s %s %s", v, c, d)
	}
}
