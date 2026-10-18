package scripts

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestShellInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PowerShell installer tested separately")
	}
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			dir := t.TempDir()
			fixture := filepath.Join(dir, "fixture")
			bin := filepath.Join(dir, "tools")
			dest := filepath.Join(dir, "install with spaces")
			os.Mkdir(fixture, 0700)
			os.Mkdir(bin, 0700)
			asset := "ddb_0.1.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
			f, e := os.Create(filepath.Join(fixture, asset))
			if e != nil {
				t.Fatal(e)
			}
			z := gzip.NewWriter(f)
			tw := tar.NewWriter(z)
			body := []byte("fixture-binary")
			tw.WriteHeader(&tar.Header{Name: "ddb", Mode: 0755, Size: int64(len(body))})
			tw.Write(body)
			tw.Close()
			z.Close()
			f.Close()
			data, _ := os.ReadFile(filepath.Join(fixture, asset))
			hash := fmt.Sprintf("%x", sha256.Sum256(data))
			if corrupt {
				hash = strings.Repeat("0", 64)
			}
			os.WriteFile(filepath.Join(fixture, "checksums.txt"), []byte(hash+"  "+asset+"\n"), 0600)
			os.WriteFile(filepath.Join(fixture, "latest"), []byte(`{"tag_name":"v0.1.0"}`), 0600)
			os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\nset -eu\nurl=\nout=\nwhile [ $# -gt 0 ]; do\n case \"$1\" in -o) out=$2; shift 2;; -*) shift;; *) url=$1; shift;; esac\ndone\ncp \"$DDB_FIXTURE_DIR/${url##*/}\" \"$out\"\n"), 0700)
			cmd := exec.Command("sh", "install.sh", "latest", dest)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "DDB_FIXTURE_DIR="+fixture, "DDB_RELEASE_BASE_URL=https://fixtures.invalid/releases", "DDB_LATEST_API=https://fixtures.invalid/latest")
			out, e := cmd.CombinedOutput()
			if corrupt {
				if e == nil {
					t.Fatal("accepted bad checksum")
				}
				if _, e = os.Stat(filepath.Join(dest, "ddb")); !os.IsNotExist(e) {
					t.Fatal("installed corrupt binary")
				}
				return
			}
			if e != nil {
				t.Fatalf("%v: %s", e, out)
			}
			got, e := os.ReadFile(filepath.Join(dest, "ddb"))
			if e != nil || string(got) != string(body) {
				t.Fatalf("installed=%s err=%v", got, e)
			}
		})
	}
}
