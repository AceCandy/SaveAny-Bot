package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
)

func TestConfigFileHandler(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `workers = 2
[telegram]
token = "telegram-secret"
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
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatalf("init config: %v", err)
	}

	restarted := false
	handler := configFileHandler(func() { restarted = true })
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", getRecorder.Code, getRecorder.Body.String())
	}
	var response struct {
		Content  string `json:"content"`
		Source   string `json:"source"`
		ReadOnly bool   `json:"read_only"`
	}
	if err := json.NewDecoder(getRecorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if response.ReadOnly || response.Source != path || strings.Contains(response.Content, "api-secret") {
		t.Fatalf("unexpected GET response: %+v", response)
	}

	response.Content = strings.Replace(response.Content, "workers = 2", "workers = 6", 1)
	body, err := json.Marshal(configFileRequest{Content: response.Content})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	putRecorder := httptest.NewRecorder()
	handler.ServeHTTP(putRecorder, httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body)))
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", putRecorder.Code, putRecorder.Body.String())
	}
	if !restarted {
		t.Fatal("restart callback was not called")
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(saved), "workers = 6") || !strings.Contains(string(saved), "api-secret") {
		t.Fatalf("saved config is incomplete:\n%s", saved)
	}
}
