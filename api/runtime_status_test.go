package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
)

func TestRuntimeStatusHandlerReportsBotAccount(t *testing.T) {
	original := botContext
	t.Cleanup(func() { botContext = original })
	botContext = func() *ext.Context {
		return &ext.Context{Self: &tg.User{Username: "saveany_test_bot"}}
	}

	recorder := httptest.NewRecorder()
	runtimeStatusHandler(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response runtimeStatusResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.InstanceID == "" || response.TelegramBot.Status != "connected" || response.TelegramBot.Message != "Telegram Bot 运行中：@saveany_test_bot" {
		t.Fatalf("unexpected response: %+v", response)
	}
}
