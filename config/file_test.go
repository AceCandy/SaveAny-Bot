package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testConfigTOML = `# keep me
workers = 4 # keep workers comment
custom = { password = 'inline-secret', nested = [{ url = "https://user:url-secret@example.com" }] }

[telegram]
token = "telegram-secret" # keep token comment
app_hash = "app-secret"

[api]
enable = true
host = "127.0.0.1"
port = 8080
token = "api-secret"

[[storages]]
name = "local"
type = "local"
enable = true
base_path = "./downloads"

[[storages]]
name = "webdav"
type = "webdav"
enable = false
url = "https://example.com/dav"
username = "user"
password = "storage-secret"
base_path = "/files"
`

func TestRedactedTOMLPreservesSecretsOnSave(t *testing.T) {
	redacted, err := redactTOML([]byte(testConfigTOML))
	if err != nil {
		t.Fatalf("redact config: %v", err)
	}
	redactedText := string(redacted)
	secrets := []string{"inline-secret", "url-secret", "telegram-secret", "app-secret", "api-secret", "storage-secret"}
	for _, secret := range secrets {
		if strings.Contains(redactedText, secret) {
			t.Fatalf("redacted config contains %q", secret)
		}
	}
	for index := range secrets {
		if !strings.Contains(redactedText, redactedValue(index+1)) {
			t.Fatalf("redacted config does not contain placeholder %d", index+1)
		}
	}

	submitted := strings.Replace(redactedText, "workers = 4", "workers = 7", 1)
	prepared, err := prepareTOML([]byte(testConfigTOML), []byte(submitted))
	if err != nil {
		t.Fatalf("prepare config: %v", err)
	}
	preparedText := string(prepared)
	want := strings.Replace(testConfigTOML, "workers = 4", "workers = 7", 1)
	if preparedText != want {
		t.Fatalf("prepared config changed comments or formatting:\n%s", preparedText)
	}

	unknown := strings.Replace(redactedText, redactedValue(1), redactedValue(999), 1)
	if _, err := prepareTOML([]byte(testConfigTOML), []byte(unknown)); err == nil {
		t.Fatal("expected unknown redacted value to fail")
	}
}

func TestRedactedTOMLPreservesSecretsWhenReordered(t *testing.T) {
	original := []byte("[first]\ntoken = \"first-secret\"\n\n[second]\ntoken = \"second-secret\"\n")
	redacted, err := redactTOML(original)
	if err != nil {
		t.Fatalf("redact config: %v", err)
	}
	first := "[first]\ntoken = \"" + redactedValue(1) + "\""
	second := "[second]\ntoken = \"" + redactedValue(2) + "\""
	submitted := strings.Replace(string(redacted), first+"\n\n"+second, second+"\n\n"+first, 1)
	prepared, err := prepareTOML(original, []byte(submitted))
	if err != nil {
		t.Fatalf("prepare reordered config: %v", err)
	}
	want := "[second]\ntoken = \"second-secret\"\n\n[first]\ntoken = \"first-secret\"\n"
	if string(prepared) != want {
		t.Fatalf("reordered config restored wrong secrets:\n%s", prepared)
	}
}

func TestSaveManagedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(testConfigTOML), 0o640); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	setTestConfigSource(t, path, false)

	file, err := ReadManagedConfig()
	if err != nil {
		t.Fatalf("read managed config: %v", err)
	}
	if file.ReadOnly || file.Source != path || strings.Contains(file.Content, "api-secret") {
		t.Fatalf("unexpected managed config: %+v", file)
	}
	updated := strings.Replace(file.Content, "workers = 4", "workers = 9", 1)
	if err := SaveManagedConfig(updated); err != nil {
		t.Fatalf("save managed config: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(saved), "workers = 9") || !strings.Contains(string(saved), "api-secret") {
		t.Fatalf("saved config is incomplete:\n%s", saved)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat saved config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("saved mode = %o, want 640", got)
	}
}

func TestRemoteManagedConfigIsReadOnly(t *testing.T) {
	setTestConfigSource(t, "https://example.com/config.toml", true)
	file, err := ReadManagedConfig()
	if err != nil {
		t.Fatalf("read remote config metadata: %v", err)
	}
	if !file.ReadOnly || file.Source != "https://example.com/config.toml" {
		t.Fatalf("unexpected remote config metadata: %+v", file)
	}
	if err := SaveManagedConfig(testConfigTOML); err == nil {
		t.Fatal("expected remote config save to fail")
	}
}

func setTestConfigSource(t *testing.T, source string, remote bool) {
	t.Helper()
	oldSource, oldRemote := configSource()
	if err := setConfigSource(source, remote); err != nil {
		t.Fatalf("set config source: %v", err)
	}
	t.Cleanup(func() {
		managedConfig.Lock()
		managedConfig.source = oldSource
		managedConfig.remote = oldRemote
		managedConfig.Unlock()
	})
}
