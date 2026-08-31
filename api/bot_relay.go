package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	gotgstorage "github.com/celestix/gotgproto/storage"
	gotgtypes "github.com/celestix/gotgproto/types"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/storage"
	"gorm.io/gorm"
)

const (
	defaultRelayTimeoutSeconds      = 900
	defaultRelayQuietSeconds        = 5
	defaultRelayScanIntervalMinutes = database.DefaultBotRelayScanIntervalMinutes
	maxRelayTimeoutSeconds          = 24 * 60 * 60
	maxRelayQuietSeconds            = 5 * 60
	maxRelayScanIntervalMinutes     = 24 * 60
)

type botRelayRequest struct {
	UserID              uint   `json:"user_id"`
	SourceChat          string `json:"source_chat"`
	TargetBot           string `json:"target_bot"`
	Enabled             bool   `json:"enabled"`
	TimeoutSeconds      int    `json:"timeout_seconds"`
	QuietSeconds        int    `json:"quiet_seconds"`
	ScanIntervalMinutes int    `json:"scan_interval_minutes"`
}

type botRelayResponse struct {
	ID                  uint                      `json:"id"`
	UserID              uint                      `json:"user_id"`
	SourceChatID        int64                     `json:"source_chat_id"`
	SourceChat          string                    `json:"source_chat"`
	TargetBotID         int64                     `json:"target_bot_id"`
	TargetBot           string                    `json:"target_bot"`
	Enabled             bool                      `json:"enabled"`
	TimeoutSeconds      int                       `json:"timeout_seconds"`
	QuietSeconds        int                       `json:"quiet_seconds"`
	ScanIntervalMinutes int                       `json:"scan_interval_minutes"`
	LastMessageID       *int                      `json:"last_message_id"`
	History             []botRelayHistoryResponse `json:"history"`
}

type botRelayHistoryResponse struct {
	MessageID   int       `json:"message_id"`
	Success     bool      `json:"success"`
	Error       string    `json:"error,omitempty"`
	ProcessedAt time.Time `json:"processed_at"`
}

type relayUserResponse struct {
	ID             uint   `json:"id"`
	ChatID         int64  `json:"chat_id"`
	DefaultStorage string `json:"default_storage"`
	Ready          bool   `json:"ready"`
}

// BotRelaysHandler lists or creates Deep Link Relay routes.
func (h *Handlers) BotRelaysHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		relays, err := database.GetAllBotRelaysWithHistory(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "relay_list_failed", "failed to list bot relays")
			return
		}
		response := make([]botRelayResponse, 0, len(relays))
		for _, relay := range relays {
			response = append(response, botRelayToResponse(relay))
		}
		WriteJSON(w, http.StatusOK, map[string]any{"bot_relays": response})
	case http.MethodPost:
		req, ok := decodeBotRelayRequest(w, r)
		if !ok {
			return
		}
		relay, err := resolveBotRelay(r, req)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_relay", err.Error())
			return
		}
		if err := database.CreateBotRelay(r.Context(), relay); err != nil {
			WriteError(w, http.StatusBadRequest, "relay_save_failed", "this source channel and target bot route already exists")
			return
		}
		WriteJSON(w, http.StatusCreated, botRelayToResponse(*relay))
	default:
		MethodNotAllowedHandler(w, r)
	}
}

// BotRelayHandler updates or deletes one Deep Link Relay route.
func (h *Handlers) BotRelayHandler(w http.ResponseWriter, r *http.Request) {
	id, err := botRelayIDFromPath(r.URL.Path)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "invalid bot relay ID")
		return
	}
	relay, err := database.GetBotRelayByID(r.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		WriteError(w, http.StatusNotFound, "relay_not_found", "bot relay not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "relay_load_failed", "failed to load bot relay")
		return
	}

	switch r.Method {
	case http.MethodPut:
		req, ok := decodeBotRelayRequest(w, r)
		if !ok {
			return
		}
		updated, err := resolveBotRelay(r, req)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_relay", err.Error())
			return
		}
		routeChanged := relay.SourceChatID != updated.SourceChatID || relay.TargetBotID != updated.TargetBotID
		relay.UserID = updated.UserID
		relay.SourceChatID = updated.SourceChatID
		relay.SourceChat = updated.SourceChat
		relay.TargetBotID = updated.TargetBotID
		relay.TargetBot = updated.TargetBot
		relay.Enabled = updated.Enabled
		relay.TimeoutSeconds = updated.TimeoutSeconds
		relay.QuietSeconds = updated.QuietSeconds
		relay.ScanIntervalMinutes = updated.ScanIntervalMinutes
		if routeChanged {
			relay.LastMessageID = nil
		}
		if err := database.UpdateBotRelay(r.Context(), relay, routeChanged); err != nil {
			WriteError(w, http.StatusBadRequest, "relay_save_failed", "this source channel and target bot route already exists")
			return
		}
		WriteJSON(w, http.StatusOK, botRelayToResponse(*relay))
	case http.MethodDelete:
		if err := database.DeleteBotRelay(r.Context(), relay); err != nil {
			WriteError(w, http.StatusInternalServerError, "relay_delete_failed", "failed to delete bot relay")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"message": "bot relay deleted"})
	default:
		MethodNotAllowedHandler(w, r)
	}
}

// ListUsersHandler returns SaveAny users that can own a Relay route.
func (h *Handlers) ListUsersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		MethodNotAllowedHandler(w, r)
		return
	}
	users, err := database.GetAllUsers(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "user_list_failed", "failed to list users")
		return
	}
	response := make([]relayUserResponse, 0, len(users))
	for _, user := range users {
		_, storageErr := storage.GetStorageByUserIDAndName(r.Context(), user.ChatID, user.DefaultStorage)
		response = append(response, relayUserResponse{
			ID:             user.ID,
			ChatID:         user.ChatID,
			DefaultStorage: user.DefaultStorage,
			Ready:          storageErr == nil,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"users": response})
}

func decodeBotRelayRequest(w http.ResponseWriter, r *http.Request) (botRelayRequest, bool) {
	var req botRelayRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "failed to decode request body: "+err.Error())
		return req, false
	}
	if req.TimeoutSeconds == 0 {
		req.TimeoutSeconds = defaultRelayTimeoutSeconds
	}
	if req.QuietSeconds == 0 {
		req.QuietSeconds = defaultRelayQuietSeconds
	}
	if req.ScanIntervalMinutes == 0 {
		req.ScanIntervalMinutes = defaultRelayScanIntervalMinutes
	}
	return req, true
}

func resolveBotRelay(r *http.Request, req botRelayRequest) (*database.BotRelay, error) {
	owner, err := database.GetUserByID(r.Context(), req.UserID)
	if err != nil {
		return nil, errors.New("owner must be an existing SaveAny user")
	}
	if _, err := storage.GetStorageByUserIDAndName(r.Context(), owner.ChatID, owner.DefaultStorage); err != nil {
		return nil, errors.New("owner must have an available default storage")
	}
	if req.TimeoutSeconds < 1 || req.TimeoutSeconds > maxRelayTimeoutSeconds {
		return nil, fmt.Errorf("timeout_seconds must be between 1 and %d", maxRelayTimeoutSeconds)
	}
	if req.QuietSeconds < 1 || req.QuietSeconds > maxRelayQuietSeconds {
		return nil, fmt.Errorf("quiet_seconds must be between 1 and %d", maxRelayQuietSeconds)
	}
	if req.QuietSeconds > req.TimeoutSeconds {
		return nil, errors.New("quiet_seconds cannot exceed timeout_seconds")
	}
	if req.ScanIntervalMinutes < 1 || req.ScanIntervalMinutes > maxRelayScanIntervalMinutes {
		return nil, fmt.Errorf("scan_interval_minutes must be between 1 and %d", maxRelayScanIntervalMinutes)
	}

	userCtx := userclient.GetCtx()
	if userCtx == nil {
		return nil, errors.New("userbot must be enabled before configuring Bot Relay")
	}
	sourceName, sourceChatID, err := parseRelaySource(req.SourceChat)
	if err != nil {
		return nil, err
	}
	targetName := strings.TrimPrefix(strings.TrimSpace(req.TargetBot), "@")
	if sourceName == "" || targetName == "" {
		return nil, errors.New("source_chat and target_bot are required")
	}
	if sourceChatID != 0 {
		inputPeer, err := userCtx.ResolveInputPeerById(sourceChatID)
		if err != nil {
			gotgstorage.AddPeersFromDialogs(r.Context(), userCtx.Raw, userCtx.PeerStorage)
			inputPeer, err = userCtx.ResolveInputPeerById(sourceChatID)
			if err != nil {
				return nil, fmt.Errorf("resolve source channel ID: %w", err)
			}
		}
		inputChannel, ok := inputPeer.(*tg.InputPeerChannel)
		if !ok {
			return nil, errors.New("source_chat ID must identify a Telegram channel")
		}
		chats, err := userCtx.Raw.ChannelsGetChannels(r.Context(), []tg.InputChannelClass{&tg.InputChannel{
			ChannelID:  inputChannel.ChannelID,
			AccessHash: inputChannel.AccessHash,
		}})
		if err != nil {
			return nil, fmt.Errorf("load source channel: %w", err)
		}
		chat, ok := chats.MapChats().First()
		sourceChannel, ok := chat.(*tg.Channel)
		if !ok || !sourceChannel.Broadcast {
			return nil, errors.New("source_chat ID must identify a Telegram channel")
		}
	} else {
		source, err := userCtx.ResolveUsername(sourceName)
		if err != nil {
			return nil, fmt.Errorf("resolve source channel: %w", err)
		}
		sourceChannel, ok := source.(*gotgtypes.Channel)
		if !ok || !sourceChannel.Broadcast {
			return nil, errors.New("source_chat must be a Telegram channel username or -100... ID")
		}
		sourceChatID = source.GetID()
	}
	target, err := userCtx.ResolveUsername(targetName)
	if err != nil {
		return nil, fmt.Errorf("resolve target bot: %w", err)
	}
	targetUser, ok := target.(*gotgtypes.User)
	if !ok || !targetUser.Bot || targetUser.Username == "" {
		return nil, errors.New("target_bot must be a Telegram bot username")
	}

	return &database.BotRelay{
		UserID:              owner.ID,
		SourceChatID:        sourceChatID,
		SourceChat:          sourceName,
		TargetBotID:         target.GetID(),
		TargetBot:           targetName,
		Enabled:             req.Enabled,
		TimeoutSeconds:      req.TimeoutSeconds,
		QuietSeconds:        req.QuietSeconds,
		ScanIntervalMinutes: req.ScanIntervalMinutes,
	}, nil
}

// parseRelaySource 区分频道用户名与 Telegram Bot API 风格的频道 ID。
func parseRelaySource(raw string) (string, int64, error) {
	source := strings.TrimPrefix(strings.TrimSpace(raw), "@")
	if source == "" || !strings.HasPrefix(source, "-") {
		return source, 0, nil
	}
	id, err := strconv.ParseInt(source, 10, 64)
	if err != nil || !constant.TDLibPeerID(id).IsChannel() {
		return "", 0, errors.New("source_chat ID must use Telegram -100... channel format")
	}
	return source, id, nil
}

func botRelayToResponse(relay database.BotRelay) botRelayResponse {
	scanIntervalMinutes := relay.ScanIntervalMinutes
	if scanIntervalMinutes == 0 {
		scanIntervalMinutes = defaultRelayScanIntervalMinutes
	}
	history := make([]botRelayHistoryResponse, 0, len(relay.History))
	for _, item := range relay.History {
		history = append(history, botRelayHistoryResponse{
			MessageID:   item.MessageID,
			Success:     item.Success,
			Error:       item.Error,
			ProcessedAt: item.UpdatedAt,
		})
	}
	return botRelayResponse{
		ID:                  relay.ID,
		UserID:              relay.UserID,
		SourceChatID:        relay.SourceChatID,
		SourceChat:          relay.SourceChat,
		TargetBotID:         relay.TargetBotID,
		TargetBot:           relay.TargetBot,
		Enabled:             relay.Enabled,
		TimeoutSeconds:      relay.TimeoutSeconds,
		QuietSeconds:        relay.QuietSeconds,
		ScanIntervalMinutes: scanIntervalMinutes,
		LastMessageID:       relay.LastMessageID,
		History:             history,
	}
}

func botRelayIDFromPath(path string) (uint, error) {
	raw := strings.TrimPrefix(path, "/api/v1/bot-relays/")
	if raw == "" || strings.Contains(raw, "/") {
		return 0, errors.New("invalid bot relay path")
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	return uint(id), err
}
