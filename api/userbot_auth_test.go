package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/ext"
	"github.com/krau/SaveAny-Bot/config"
)

func TestUserbotAuthHandler(t *testing.T) {
	original := authenticateUserbot
	t.Cleanup(func() { authenticateUserbot = original })

	phoneInput := make(chan string, 1)
	codeInput := make(chan string, 1)
	passwordInput := make(chan string, 1)
	authenticateUserbot = func(ctx context.Context, conversator gotgproto.AuthConversator) error {
		conversator.AuthStatus(gotgproto.AuthStatus{Event: gotgproto.AuthStatusPhoneAsked})
		phone, err := conversator.AskPhoneNumber()
		if err != nil {
			return err
		}
		phoneInput <- phone

		conversator.AuthStatus(gotgproto.AuthStatus{Event: gotgproto.AuthStatusPhoneCodeAsked})
		code, err := conversator.AskCode()
		if err != nil {
			return err
		}
		codeInput <- code

		conversator.AuthStatus(gotgproto.AuthStatus{Event: gotgproto.AuthStatusPasswordAsked})
		password, err := conversator.AskPassword()
		if err != nil {
			return err
		}
		passwordInput <- password
		conversator.AuthStatus(gotgproto.AuthStatus{Event: gotgproto.AuthStatusSuccess})
		return nil
	}

	handler := newUserbotAuthManager(t.Context(), nil).Handler
	if recorder := serveUserbotAuthRequest(t, handler, http.MethodPost, map[string]string{}); recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty request status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder := serveUserbotAuthRequest(t, handler, http.MethodPost, map[string]string{"phone": "+44 123456"}); recorder.Code != http.StatusAccepted {
		t.Fatalf("phone request status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := receiveUserbotInput(t, phoneInput); got != "+44 123456" {
		t.Fatalf("phone = %q", got)
	}
	waitUserbotAuthState(t, handler, userbotAuthCodeRequired)

	if recorder := serveUserbotAuthRequest(t, handler, http.MethodPost, map[string]string{"password": "too-early"}); recorder.Code != http.StatusConflict {
		t.Fatalf("out-of-order password status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder := serveUserbotAuthRequest(t, handler, http.MethodPost, map[string]string{"code": "123456"}); recorder.Code != http.StatusAccepted {
		t.Fatalf("code request status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := receiveUserbotInput(t, codeInput); got != "123456" {
		t.Fatalf("code = %q", got)
	}
	waitUserbotAuthState(t, handler, userbotAuthPasswordRequired)

	if recorder := serveUserbotAuthRequest(t, handler, http.MethodPost, map[string]string{"password": "secret"}); recorder.Code != http.StatusAccepted {
		t.Fatalf("password request status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := receiveUserbotInput(t, passwordInput); got != "secret" {
		t.Fatalf("password = %q", got)
	}
	waitUserbotAuthState(t, handler, userbotAuthAuthenticated)
}

func TestUserbotAuthCancel(t *testing.T) {
	original := authenticateUserbot
	t.Cleanup(func() { authenticateUserbot = original })

	cancelled := make(chan struct{})
	authenticateUserbot = func(ctx context.Context, conversator gotgproto.AuthConversator) error {
		defer close(cancelled)
		conversator.AuthStatus(gotgproto.AuthStatus{Event: gotgproto.AuthStatusPhoneAsked})
		_, err := conversator.AskPhoneNumber()
		if err != nil {
			return err
		}
		conversator.AuthStatus(gotgproto.AuthStatus{Event: gotgproto.AuthStatusPhoneCodeAsked})
		_, err = conversator.AskCode()
		return err
	}

	handler := newUserbotAuthManager(t.Context(), nil).Handler
	serveUserbotAuthRequest(t, handler, http.MethodPost, map[string]string{"phone": "+44 123456"})
	waitUserbotAuthState(t, handler, userbotAuthCodeRequired)
	if recorder := serveUserbotAuthRequest(t, handler, http.MethodDelete, nil); recorder.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("authentication did not stop after cancellation")
	}
	waitUserbotAuthState(t, handler, userbotAuthIdle)
}

func TestUserbotAuthFailureHidesCause(t *testing.T) {
	original := authenticateUserbot
	t.Cleanup(func() { authenticateUserbot = original })

	authenticateUserbot = func(context.Context, gotgproto.AuthConversator) error {
		return errors.New("sensitive upstream error")
	}
	handler := newUserbotAuthManager(t.Context(), nil).Handler
	serveUserbotAuthRequest(t, handler, http.MethodPost, map[string]string{"phone": "+44 123456"})
	response := waitUserbotAuthState(t, handler, userbotAuthFailed)
	if strings.Contains(response.Message, "sensitive upstream error") {
		t.Fatalf("response leaks upstream error: %q", response.Message)
	}
}

func TestUserbotLogoutDisablesSessionBeforeRestart(t *testing.T) {
	originalContext := getUserbotContext
	originalLogout := logoutUserbot
	originalDisable := disableUserbot
	t.Cleanup(func() {
		getUserbotContext = originalContext
		logoutUserbot = originalLogout
		disableUserbot = originalDisable
	})

	getUserbotContext = func() *ext.Context { return &ext.Context{} }
	order := make([]string, 0, 3)
	disableUserbot = func() error {
		order = append(order, "disable")
		return nil
	}
	logoutUserbot = func(context.Context) error {
		order = append(order, "logout")
		return nil
	}
	recorder := httptest.NewRecorder()
	manager := newUserbotAuthManager(t.Context(), func() {
		if recorder.Body.Len() == 0 {
			t.Fatal("restart called before response was written")
		}
		order = append(order, "restart")
	})
	manager.Handler(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/userbot/auth", nil))

	if recorder.Code != http.StatusOK || len(order) != 3 || order[0] != "disable" || order[1] != "logout" || order[2] != "restart" {
		t.Fatalf("status = %d, order = %v, body = %s", recorder.Code, order, recorder.Body.String())
	}
}

func TestUserbotLogoutFailureDoesNotRestart(t *testing.T) {
	originalContext := getUserbotContext
	originalLogout := logoutUserbot
	originalDisable := disableUserbot
	t.Cleanup(func() {
		getUserbotContext = originalContext
		logoutUserbot = originalLogout
		disableUserbot = originalDisable
	})

	getUserbotContext = func() *ext.Context { return &ext.Context{} }
	disabled := false
	disableUserbot = func() error {
		disabled = true
		return nil
	}
	logoutUserbot = func(context.Context) error { return errors.New("sensitive upstream error") }
	restarted := false
	handler := newUserbotAuthManager(t.Context(), func() { restarted = true }).Handler
	recorder := serveUserbotAuthRequest(t, handler, http.MethodDelete, nil)

	if recorder.Code != http.StatusBadGateway || !disabled || restarted {
		t.Fatalf("status = %d, disabled = %t, restarted = %t, body = %s", recorder.Code, disabled, restarted, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "sensitive upstream error") {
		t.Fatalf("response leaks upstream error: %s", recorder.Body.String())
	}
}

func TestUserbotAuthRouteRequiresToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `[api]
enable = true
host = "127.0.0.1"
port = 8080
token = "api-secret"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatalf("init config: %v", err)
	}

	handler := NewServer(t.Context(), nil).httpServer.Handler
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/userbot/auth", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, body = %s", unauthorized.Code, unauthorized.Body.String())
	}

	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/userbot/auth", nil)
	request.Header.Set("Authorization", "Bearer api-secret")
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, body = %s", authorized.Code, authorized.Body.String())
	}
}

func serveUserbotAuthRequest(t *testing.T, handler http.HandlerFunc, method string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, "/api/v1/userbot/auth", &payload))
	return recorder
}

func receiveUserbotInput(t *testing.T, input <-chan string) string {
	t.Helper()
	select {
	case value := <-input:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for authentication input")
		return ""
	}
}

func waitUserbotAuthState(t *testing.T, handler http.HandlerFunc, want string) userbotAuthResponse {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		recorder := serveUserbotAuthRequest(t, handler, http.MethodGet, nil)
		var response userbotAuthResponse
		if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
			t.Fatalf("decode status response: %v", err)
		}
		if response.Status == want {
			return response
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for userbot auth state %q", want)
	return userbotAuthResponse{}
}
