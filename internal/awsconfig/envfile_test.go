package awsconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "connection.env")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func cleanAWS(t *testing.T) {
	t.Helper()
	// Isolate tests from local credentials and shared AWS files, without parallel
	// tests mutating the environment concurrently.
	for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_ACCESS_KEY", "AWS_SECRET_KEY", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"} {
		original, present := os.LookupEnv(name)
		os.Unsetenv(name)
		t.Cleanup(func() {
			if present {
				os.Setenv(name, original)
			} else {
				os.Unsetenv(name)
			}
		})
	}
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}
func TestReadEnvFile(t *testing.T) {
	path := writeEnv(t, "\ufeff# connection\r\n\r\n export AWS_ACCESS_KEY_ID = 'file-access' # comment\r\nAWS_SECRET_ACCESS_KEY=\"file-secret#literal\"\r\nAWS_SESSION_TOKEN=token#literal # trailing comment\r\nAWS_REGION = us-east-1\r\nOTHER=\"literal-${VALUE}\\nline\"\r\nEMPTY=\r\n")
	got, err := readEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"AWS_ACCESS_KEY_ID": "file-access", "AWS_SECRET_ACCESS_KEY": "file-secret#literal", "AWS_SESSION_TOKEN": "token#literal", "AWS_REGION": "us-east-1", "OTHER": "literal-${VALUE}\nline", "EMPTY": ""} {
		if got[key] != want {
			t.Errorf("unexpected parsed value for %s", key)
		}
	}
}
func TestEnvErrorsRedactValues(t *testing.T) {
	for _, body := range []string{"AWS_SECRET_ACCESS_KEY=\"sensitive-unclosed", "AWS_SECRET_ACCESS_KEY='sensitive-value' trailing", "sensitive-value", "AWS_SECRET_ACCESS_KEY=sensitive-value\nAWS_SECRET_ACCESS_KEY=sensitive-duplicate", "AWS_SECRET_ACCESS_KEY=\"sensitive-value\\q\"", "AWS_SECRET_ACCESS_KEY=sensitive\x00value"} {
		_, err := readEnvFile(writeEnv(t, body))
		if err == nil || strings.Contains(err.Error(), "sensitive") || !strings.Contains(err.Error(), "line ") {
			t.Fatalf("invalid or unredacted error: %v", err)
		}
	}
	_, err := readEnvFile(filepath.Join(t.TempDir(), "missing.env"))
	if err == nil {
		t.Fatal("missing file accepted")
	}
	_, err = readEnvFile(writeEnv(t, strings.Repeat("x", 1024*1024+1)))
	if err == nil {
		t.Fatal("oversized file accepted")
	}
	_, err = readEnvFile(writeEnv(t, "AWS_SECRET_ACCESS_KEY="+strings.Repeat("sensitive", 10000)))
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("oversized line accepted or leaked")
	}
}
func TestLoadEnvFilePrecedence(t *testing.T) {
	cases := []struct {
		name                                      string
		env                                       map[string]string
		region, access, secret, token, wantRegion string
	}{
		{name: "file", access: "file-access", secret: "file-secret", token: "file-token", wantRegion: "us-east-1"},
		{name: "environment", env: map[string]string{"AWS_ACCESS_KEY_ID": "env-access", "AWS_SECRET_ACCESS_KEY": "env-secret", "AWS_SESSION_TOKEN": "env-token", "AWS_REGION": "eu-west-1"}, access: "env-access", secret: "env-secret", token: "env-token", wantRegion: "eu-west-1"},
		{name: "cli-region", env: map[string]string{"AWS_REGION": "eu-west-1"}, region: "ap-southeast-1", access: "file-access", secret: "file-secret", token: "file-token", wantRegion: "ap-southeast-1"},
		{name: "legacy-environment", env: map[string]string{"AWS_ACCESS_KEY": "alias-access", "AWS_SECRET_KEY": "alias-secret", "AWS_DEFAULT_REGION": "eu-central-1"}, access: "alias-access", secret: "alias-secret", token: "file-token", wantRegion: "eu-central-1"},
		{name: "empty-token", env: map[string]string{"AWS_SESSION_TOKEN": ""}, access: "file-access", secret: "file-secret", wantRegion: "us-east-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanAWS(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			path := writeEnv(t, "AWS_ACCESS_KEY_ID=file-access\nAWS_SECRET_ACCESS_KEY=file-secret\nAWS_SESSION_TOKEN=file-token\nAWS_REGION=us-east-1\n")
			client, region, err := Load(context.Background(), Options{EnvFile: path, Region: tc.region})
			if err != nil {
				t.Fatal(err)
			}
			cred, err := client.Options().Credentials.Retrieve(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if cred.AccessKeyID != tc.access || cred.SecretAccessKey != tc.secret || cred.SessionToken != tc.token || region != tc.wantRegion {
				t.Fatal("credential or region precedence mismatch")
			}
			// File-only settings must not leak into subsequent commands or other clients.
			if _, ok := tc.env["AWS_ACCESS_KEY_ID"]; !ok {
				if _, present := os.LookupEnv("AWS_ACCESS_KEY_ID"); present {
					t.Fatal("process environment was changed")
				}
			}
		})
	}
}
func TestInvalidEffectiveCredentials(t *testing.T) {
	for _, body := range []string{"AWS_ACCESS_KEY_ID=sensitive-access", "AWS_SECRET_ACCESS_KEY=sensitive-secret", "AWS_SESSION_TOKEN=sensitive-token"} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			cleanAWS(t)
			_, _, err := Load(context.Background(), Options{EnvFile: writeEnv(t, body), Region: "us-east-1"})
			if err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("invalid credentials accepted or leaked: %v", err)
			}
		})
	}
	cleanAWS(t)
	_, _, err := Load(context.Background(), Options{Profile: "development", EnvFile: "missing.env"})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("profile conflict: %v", err)
	}
}
func TestNormalDiscoveryUnchanged(t *testing.T) {
	for _, withFile := range []bool{false, true} {
		t.Run(fmt.Sprint(withFile), func(t *testing.T) {
			cleanAWS(t)
			os.WriteFile(os.Getenv("AWS_CONFIG_FILE"), []byte("[default]\nregion = eu-west-2\n"), 0600)
			os.WriteFile(os.Getenv("AWS_SHARED_CREDENTIALS_FILE"), []byte("[default]\naws_access_key_id = shared-access\naws_secret_access_key = shared-secret\n"), 0600)
			o := Options{}
			if withFile {
				o.EnvFile = writeEnv(t, "# no static credentials\nOTHER=ignored\n")
			}
			client, region, err := Load(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			creds, err := client.Options().Credentials.Retrieve(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if region != "eu-west-2" || creds.AccessKeyID != "shared-access" {
				t.Fatal("standard AWS discovery changed")
			}
		})
	}
}
