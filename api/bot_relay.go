package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	gotgtypes "github.com/celestix/gotgproto/types"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/storage"
	"gorm.io/gorm"
)

const (
	defaultRelayTimeoutSeconds = 900
	defaultRelayQuietSeconds   = 5
	maxRelayTimeoutSeconds     = 24 * 60 * 60
	maxRelayQuietSeconds       = 5 * 60
)

type botRelayRequest struct {
	UserID         uint   `json:"user_id"`
	SourceChat     string `json:"source_chat"`
	TargetBot      string `json:"target_bot"`
	Enabled        bool   `json:"enabled"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	QuietSeconds   int    `json:"quiet_seconds"`
}

type botRelayResponse struct {
	ID             uint   `json:"id"`
	UserID         uint   `json:"user_id"`
	SourceChatID   int64  `json:"source_chat_id"`
	SourceChat     string `json:"source_chat"`
	TargetBotID    int64  `json:"target_bot_id"`
	TargetBot      string `json:"target_bot"`
	Enabled        bool   `json:"enabled"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	QuietSeconds   int    `json:"quiet_seconds"`
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
		relays, err := database.GetAllBotRelays(r.Context())
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
		relay.UserID = updated.UserID
		relay.SourceChatID = updated.SourceChatID
		relay.SourceChat = updated.SourceChat
		relay.TargetBotID = updated.TargetBotID
		relay.TargetBot = updated.TargetBot
		relay.Enabled = updated.Enabled
		relay.TimeoutSeconds = updated.TimeoutSeconds
		relay.QuietSeconds = updated.QuietSeconds
		if err := database.UpdateBotRelay(r.Context(), relay); err != nil {
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

	userCtx := userclient.GetCtx()
	if userCtx == nil {
		return nil, errors.New("userbot must be enabled before configuring Bot Relay")
	}
	sourceName := strings.TrimPrefix(strings.TrimSpace(req.SourceChat), "@")
	targetName := strings.TrimPrefix(strings.TrimSpace(req.TargetBot), "@")
	if sourceName == "" || targetName == "" {
		return nil, errors.New("source_chat and target_bot are required")
	}
	source, err := userCtx.ResolveUsername(sourceName)
	if err != nil {
		return nil, fmt.Errorf("resolve source channel: %w", err)
	}
	sourceChannel, ok := source.(*gotgtypes.Channel)
	if !ok || !sourceChannel.Broadcast {
		return nil, errors.New("source_chat must be a Telegram channel username")
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
		UserID:         owner.ID,
		SourceChatID:   source.GetID(),
		SourceChat:     sourceName,
		TargetBotID:    target.GetID(),
		TargetBot:      targetName,
		Enabled:        req.Enabled,
		TimeoutSeconds: req.TimeoutSeconds,
		QuietSeconds:   req.QuietSeconds,
	}, nil
}

func botRelayToResponse(relay database.BotRelay) botRelayResponse {
	return botRelayResponse{
		ID:             relay.ID,
		UserID:         relay.UserID,
		SourceChatID:   relay.SourceChatID,
		SourceChat:     relay.SourceChat,
		TargetBotID:    relay.TargetBotID,
		TargetBot:      relay.TargetBot,
		Enabled:        relay.Enabled,
		TimeoutSeconds: relay.TimeoutSeconds,
		QuietSeconds:   relay.QuietSeconds,
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
