package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchProductTOMLPreservesUnmanagedContent(t *testing.T) {
	original := []byte(`# keep root
workers = 4 # keep workers comment

[telegram]
token = "secret"

[api]
host = "127.0.0.1"
port = 8080

[[storages]]
name = "local"
type = "local"
enable = true
base_path = "./downloads"
`)
	updated, err := patchProductTOML(original, []productConfigValue{
		{"", "workers", 8},
		{"", "threads", 6},
		{"log", "level", "info"},
		{"telegram.userbot", "enable", true},
		{"telegram.userbot", "session", "data/user.db"},
		{"api", "port", 9090},
	})
	if err != nil {
		t.Fatalf("patch product config: %v", err)
	}
	text := string(updated)
	for _, want := range []string{
		"# keep root",
		"workers = 8 # keep workers comment",
		"threads = 6",
		"token = \"secret\"",
		"port = 9090",
		"[log]\nlevel = 'info'",
		"[telegram.userbot]\nenable = true\nsession = 'data/user.db'",
		"[[storages]]\nname = \"local\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("patched config does not contain %q:\n%s", want, text)
		}
	}
	if err := ValidateTOML(updated); err != nil {
		t.Fatalf("patched config is invalid: %v\n%s", err, text)
	}
}

func TestDisableUserbotOnlyChangesEnable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "[telegram.userbot]\nenable = true\nsession = 'data/user.db'\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := Init(t.Context(), path); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := DisableUserbot(); err != nil {
		t.Fatalf("disable userbot: %v", err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if got := string(updated); !strings.Contains(got, "enable = false") || !strings.Contains(got, "session = 'data/user.db'") {
		t.Fatalf("unexpected config:\n%s", got)
	}
}

func TestSaveProductConfigRequiresTelegramBotToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[api]\ntoken = 'api-secret'\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := Init(t.Context(), path); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := SaveProductConfig(ReadProductConfig()); err == nil || !strings.Contains(err.Error(), "Telegram Bot Token") {
		t.Fatalf("expected missing Telegram Bot Token error, got %v", err)
	}
}
