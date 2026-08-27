package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	NewServer(t.Context(), nil).httpServer.Handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("expected HTML content type, got %q", got)
	}
	if got := recorder.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("expected a content security policy")
	}
	if body := recorder.Body.String(); !strings.Contains(body, "SaveAny") || !strings.Contains(body, `id="login-view"`) || !strings.Contains(body, `id="console-shell"`) || !strings.Contains(body, `id="product-form"`) || !strings.Contains(body, `id="userbot-auth"`) || !strings.Contains(body, `id="task-form"`) || !strings.Contains(body, `id="relay-form"`) || !strings.Contains(body, `id="log-output"`) || !strings.Contains(body, `id="config-form"`) {
		t.Fatal("dashboard content is incomplete")
	}
	if body := recorder.Body.String(); !strings.Contains(body, "需重启") || !strings.Contains(body, "即时生效") {
		t.Fatal("dashboard does not distinguish configuration effects")
	}
	if body := recorder.Body.String(); !strings.Contains(body, `id="relay-userbot-required"`) || !strings.Contains(body, "Bot Relay 依赖已连接的 Userbot") || !strings.Contains(body, `relayFields.disabled = !available`) {
		t.Fatal("dashboard does not explain the Bot Relay prerequisite")
	}
	if body := recorder.Body.String(); strings.Contains(body, "界面语言") || !strings.Contains(body, "扩展能力（按需配置）") || !strings.Contains(body, `id="api-settings-popover"`) {
		t.Fatal("dashboard configuration hierarchy is incomplete")
	}
}
