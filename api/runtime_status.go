package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/krau/SaveAny-Bot/client/bot"
	"github.com/krau/SaveAny-Bot/config"
)

var (
	runtimeInstanceID = strconv.FormatInt(time.Now().UnixNano(), 36)
	botContext        = bot.ExtContext
)

type runtimeClientStatus struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type runtimeStatusResponse struct {
	InstanceID  string              `json:"instance_id"`
	TelegramBot runtimeClientStatus `json:"telegram_bot"`
}

func runtimeStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		MethodNotAllowedHandler(w, r)
		return
	}
	WriteJSON(w, http.StatusOK, runtimeStatusResponse{
		InstanceID:  runtimeInstanceID,
		TelegramBot: telegramBotStatus(botContext()),
	})
}

func telegramBotStatus(ctx *ext.Context) runtimeClientStatus {
	if ctx == nil {
		if config.C().Telegram.Token == "" {
			return runtimeClientStatus{Status: "not_configured", Message: "Telegram Bot 尚未配置"}
		}
		return runtimeClientStatus{Status: "starting", Message: "Telegram Bot 正在启动"}
	}
	message := "Telegram Bot 运行中"
	if ctx.Self != nil && ctx.Self.Username != "" {
		message += "：@" + ctx.Self.Username
	}
	return runtimeClientStatus{Status: "connected", Message: message}
}
