package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/celestix/gotgproto"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/config"
)

const (
	userbotAuthIdle             = "idle"
	userbotAuthStarting         = "starting"
	userbotAuthPhoneRequired    = "phone_required"
	userbotAuthCodeRequired     = "code_required"
	userbotAuthPasswordRequired = "password_required"
	userbotAuthAuthenticating   = "authenticating"
	userbotAuthAuthenticated    = "authenticated"
	userbotAuthConnected        = "connected"
	userbotAuthFailed           = "failed"
	userbotAuthTimeout          = 10 * time.Minute
)

type userbotAuthRequest struct {
	Phone    string `json:"phone"`
	Code     string `json:"code"`
	Password string `json:"password"`
}

type userbotAuthResponse struct {
	Status       string `json:"status"`
	Message      string `json:"message"`
	AttemptsLeft int    `json:"attempts_left,omitempty"`
}

type userbotAuthManager struct {
	ctx      context.Context
	restart  func()
	mu       sync.Mutex
	attempt  *webUserbotAuth
	response userbotAuthResponse
}

// webUserbotAuth 在认证库等待输入时，通过 HTTP 请求传递敏感信息。
type webUserbotAuth struct {
	ctx      context.Context
	cancel   context.CancelFunc
	manager  *userbotAuthManager
	phone    chan string
	code     chan string
	password chan string
}

var (
	authenticateUserbot = func(ctx context.Context, conversator gotgproto.AuthConversator) error {
		client, err := userclient.Authenticate(ctx, conversator)
		if client != nil {
			client.Stop()
		}
		return err
	}
	getUserbotContext = userclient.GetCtx
	logoutUserbot     = userclient.Logout
	disableUserbot    = config.DisableUserbot
)

func newUserbotAuthManager(ctx context.Context, restart func()) *userbotAuthManager {
	return &userbotAuthManager{
		ctx:     ctx,
		restart: restart,
		response: userbotAuthResponse{
			Status:  userbotAuthIdle,
			Message: "尚未登录 Userbot",
		},
	}
}

func (m *userbotAuthManager) Handler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		WriteJSON(w, http.StatusOK, m.status())
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request userbotAuthRequest
		if err := decoder.Decode(&request); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request", "登录参数无效")
			return
		}
		if err := ensureJSONEnd(decoder); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request", "登录参数无效")
			return
		}
		response, status, err := m.submit(request)
		if err != nil {
			WriteError(w, status, "userbot_auth_failed", err.Error())
			return
		}
		WriteJSON(w, http.StatusAccepted, response)
	case http.MethodDelete:
		if getUserbotContext() == nil {
			WriteJSON(w, http.StatusOK, m.cancel())
			return
		}
		if m.restart == nil {
			WriteError(w, http.StatusServiceUnavailable, "restart_unavailable", "当前服务无法安全重启，请稍后重试")
			return
		}
		if err := disableUserbot(); err != nil {
			WriteError(w, http.StatusInternalServerError, "userbot_disable_failed", "停用 Userbot 失败，请检查配置文件后重试")
			return
		}
		if err := logoutUserbot(r.Context()); err != nil {
			WriteError(w, http.StatusBadGateway, "userbot_logout_failed", "Userbot 已停用，但 Telegram 注销失败，请重试")
			return
		}
		if err := WriteJSON(w, http.StatusOK, userbotAuthResponse{Status: userbotAuthIdle, Message: "已退出 Userbot，服务正在重启"}); err == nil {
			m.restart()
		}
	default:
		MethodNotAllowedHandler(w, r)
	}
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func (m *userbotAuthManager) status() userbotAuthResponse {
	if userCtx := getUserbotContext(); userCtx != nil {
		message := "Userbot 已连接"
		if userCtx.Self != nil {
			account := strings.TrimSpace(userCtx.Self.FirstName + " " + userCtx.Self.LastName)
			if userCtx.Self.Username != "" {
				account = strings.TrimSpace(account + " (@" + userCtx.Self.Username + ")")
			}
			if account != "" {
				message += "：" + account
			}
		}
		return userbotAuthResponse{Status: userbotAuthConnected, Message: message}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.response
}

func (m *userbotAuthManager) submit(request userbotAuthRequest) (userbotAuthResponse, int, error) {
	field, value, err := request.input()
	if err != nil {
		return userbotAuthResponse{}, http.StatusBadRequest, err
	}
	if getUserbotContext() != nil {
		return userbotAuthResponse{}, http.StatusConflict, fmt.Errorf("Userbot 已连接")
	}

	m.mu.Lock()
	if m.attempt == nil {
		if m.response.Status == userbotAuthAuthenticated {
			m.mu.Unlock()
			return userbotAuthResponse{}, http.StatusConflict, fmt.Errorf("Userbot 已认证，请保存配置并重启")
		}
		if field != "phone" {
			m.mu.Unlock()
			return userbotAuthResponse{}, http.StatusConflict, fmt.Errorf("请先提交手机号")
		}
		ctx, cancel := context.WithTimeout(m.ctx, userbotAuthTimeout)
		attempt := &webUserbotAuth{
			ctx:      ctx,
			cancel:   cancel,
			manager:  m,
			phone:    make(chan string, 1),
			code:     make(chan string, 1),
			password: make(chan string, 1),
		}
		attempt.phone <- value
		m.attempt = attempt
		m.response = userbotAuthResponse{Status: userbotAuthStarting, Message: "正在发送验证码"}
		response := m.response
		m.mu.Unlock()
		go m.run(attempt)
		return response, http.StatusAccepted, nil
	}

	expected := map[string]string{
		userbotAuthPhoneRequired:    "phone",
		userbotAuthCodeRequired:     "code",
		userbotAuthPasswordRequired: "password",
	}[m.response.Status]
	if expected == "" || field != expected {
		m.mu.Unlock()
		return userbotAuthResponse{}, http.StatusConflict, fmt.Errorf("当前登录步骤不接受该输入")
	}

	input := map[string]chan string{
		"phone":    m.attempt.phone,
		"code":     m.attempt.code,
		"password": m.attempt.password,
	}[field]
	select {
	case input <- value:
		m.response = userbotAuthResponse{Status: userbotAuthAuthenticating, Message: "正在验证登录信息"}
		response := m.response
		m.mu.Unlock()
		return response, http.StatusAccepted, nil
	default:
		m.mu.Unlock()
		return userbotAuthResponse{}, http.StatusConflict, fmt.Errorf("登录信息已提交，请稍候")
	}
}

func (r userbotAuthRequest) input() (string, string, error) {
	inputs := []struct {
		name  string
		value string
		limit int
	}{
		{"phone", strings.TrimSpace(r.Phone), 64},
		{"code", strings.TrimSpace(r.Code), 32},
		{"password", r.Password, 256},
	}
	var name, value string
	for _, input := range inputs {
		if input.value == "" {
			continue
		}
		if name != "" {
			return "", "", fmt.Errorf("每次只能提交一种登录信息")
		}
		if len(input.value) > input.limit {
			return "", "", fmt.Errorf("登录信息过长")
		}
		name, value = input.name, input.value
	}
	if name == "" {
		return "", "", fmt.Errorf("缺少登录信息")
	}
	return name, value, nil
}

func (m *userbotAuthManager) run(attempt *webUserbotAuth) {
	err := authenticateUserbot(attempt.ctx, attempt)
	attempt.cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attempt != attempt {
		return
	}
	m.attempt = nil
	if err == nil {
		m.response = userbotAuthResponse{Status: userbotAuthAuthenticated, Message: "认证成功，请保存配置并重启"}
		return
	}
	message := "Userbot 登录失败，请检查网络、代理和会话文件后重试"
	if errors.Is(err, context.DeadlineExceeded) {
		message = "Userbot 登录已超时，请重新开始"
	}
	m.response = userbotAuthResponse{Status: userbotAuthFailed, Message: message}
}

func (m *userbotAuthManager) cancel() userbotAuthResponse {
	m.mu.Lock()
	attempt := m.attempt
	m.attempt = nil
	m.response = userbotAuthResponse{Status: userbotAuthIdle, Message: "Userbot 登录已取消"}
	response := m.response
	m.mu.Unlock()
	if attempt != nil {
		attempt.cancel()
	}
	return response
}

func (a *webUserbotAuth) AskPhoneNumber() (string, error) {
	return a.read(a.phone)
}

func (a *webUserbotAuth) AskCode() (string, error) {
	return a.read(a.code)
}

func (a *webUserbotAuth) AskPassword() (string, error) {
	return a.read(a.password)
}

func (a *webUserbotAuth) read(input <-chan string) (string, error) {
	select {
	case value := <-input:
		return value, nil
	case <-a.ctx.Done():
		return "", a.ctx.Err()
	}
}

func (a *webUserbotAuth) AuthStatus(status gotgproto.AuthStatus) {
	a.manager.update(a, status)
}

func (m *userbotAuthManager) update(attempt *webUserbotAuth, status gotgproto.AuthStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attempt != attempt {
		return
	}

	switch status.Event {
	case gotgproto.AuthStatusPhoneAsked:
		m.response = userbotAuthResponse{Status: userbotAuthStarting, Message: "正在发送验证码"}
	case gotgproto.AuthStatusPhoneRetrial:
		m.response = userbotAuthResponse{Status: userbotAuthPhoneRequired, Message: "手机号无效，请重新输入", AttemptsLeft: status.AttemptsLeft}
	case gotgproto.AuthStatusPhoneFailed:
		m.response = userbotAuthResponse{Status: userbotAuthFailed, Message: "手机号验证失败"}
	case gotgproto.AuthStatusPhoneCodeAsked:
		m.response = userbotAuthResponse{Status: userbotAuthCodeRequired, Message: "验证码已发送，请检查 Telegram"}
	case gotgproto.AuthStatusPhoneCodeRetrial:
		m.response = userbotAuthResponse{Status: userbotAuthCodeRequired, Message: "验证码无效，请重试", AttemptsLeft: status.AttemptsLeft}
	case gotgproto.AuthStatusPhoneCodeFailed:
		m.response = userbotAuthResponse{Status: userbotAuthFailed, Message: "验证码验证失败"}
	case gotgproto.AuthStatusPasswordAsked:
		m.response = userbotAuthResponse{Status: userbotAuthPasswordRequired, Message: "请输入 Telegram 两步验证密码"}
	case gotgproto.AuthStatusPasswordRetrial:
		m.response = userbotAuthResponse{Status: userbotAuthPasswordRequired, Message: "两步验证密码无效，请重试", AttemptsLeft: status.AttemptsLeft}
	case gotgproto.AuthStatusPasswordFailed:
		m.response = userbotAuthResponse{Status: userbotAuthFailed, Message: "两步验证失败"}
	case gotgproto.AuthStatusPhoneCodeVerified, gotgproto.AuthStatusSuccess:
		m.response = userbotAuthResponse{Status: userbotAuthAuthenticating, Message: "正在保存 Userbot 会话"}
	}
}
