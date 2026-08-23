package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/shortcut"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

const relayQueueCapacity = 100

var (
	deepLinkPattern = regexp.MustCompile(`(?i)https://(?:www\.)?t\.me/[a-z0-9_]{5,32}\?start=[^\s<>"']+`)
	payloadPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

type botRelayRequest struct {
	relay           database.BotRelay
	payload         string
	ownerChatID     int64
	statusMessageID int
}

type botRelayResponse struct {
	ctx     *ext.Context
	message *tg.Message
}

type botRelayManager struct {
	ctx          context.Context
	botCtx       *ext.Context
	requests     chan botRelayRequest
	responses    chan botRelayResponse
	activeTarget atomic.Int64
}

// StartBotRelay connects userbot updates to the isolated Deep Link Relay runtime.
func StartBotRelay(ctx context.Context, botCtx *ext.Context) {
	manager := &botRelayManager{
		ctx:       ctx,
		botCtx:    botCtx,
		requests:  make(chan botRelayRequest, relayQueueCapacity),
		responses: make(chan botRelayResponse, relayQueueCapacity),
	}
	userclient.SetRelayMessageHandler(manager.handleUpdate)
	go manager.run()
	go func() {
		<-ctx.Done()
		userclient.SetRelayMessageHandler(nil)
	}()
}

func (m *botRelayManager) handleUpdate(ctx *ext.Context, update *ext.Update) error {
	message := update.EffectiveMessage
	if message == nil || message.Message == nil || message.Out {
		return dispatcher.ContinueGroups
	}
	switch update.UpdateClass.(type) {
	case *tg.UpdateEditChannelMessage, *tg.UpdateEditMessage, *tg.UpdateDeleteChannelMessages, *tg.UpdateDeleteMessages:
		return dispatcher.ContinueGroups
	}

	chatID := update.EffectiveChat().GetID()
	switch message.PeerID.(type) {
	case *tg.PeerChannel:
		relays, err := database.GetEnabledBotRelaysBySourceChatID(ctx, chatID)
		if err != nil {
			log.FromContext(ctx).Error("Failed to load Bot Relay routes", "error", err)
			return dispatcher.ContinueGroups
		}
		for _, relay := range relays {
			for _, payload := range relayPayloads(message.Message, relay.TargetBot) {
				m.enqueue(ctx, relay, payload)
			}
		}
		return dispatcher.ContinueGroups
	case *tg.PeerUser:
		if m.activeTarget.Load() != chatID {
			return dispatcher.ContinueGroups
		}
		sender, ok := message.FromID.(*tg.PeerUser)
		if !ok || sender.UserID != chatID {
			return dispatcher.ContinueGroups
		}
		select {
		case m.responses <- botRelayResponse{ctx: ctx, message: message.Message}:
		case <-m.ctx.Done():
		}
		return dispatcher.EndGroups
	default:
		return dispatcher.ContinueGroups
	}
}

func (m *botRelayManager) enqueue(ctx *ext.Context, relay database.BotRelay, payload string) {
	owner, err := database.GetUserByID(ctx, relay.UserID)
	if err != nil {
		log.FromContext(ctx).Error("Failed to load Bot Relay owner", "relay_id", relay.ID, "error", err)
		return
	}
	message, err := m.sendStatus(owner.ChatID, i18nk.BotMsgRelayQueued, map[string]any{
		"Bot":      relay.TargetBot,
		"Payload":  payload,
		"Position": len(m.requests) + 1,
	})
	if err != nil {
		log.FromContext(ctx).Error("Failed to send Bot Relay status", "relay_id", relay.ID, "error", err)
		return
	}
	request := botRelayRequest{
		relay:           relay,
		payload:         payload,
		ownerChatID:     owner.ChatID,
		statusMessageID: message.ID,
	}
	select {
	case m.requests <- request:
	case <-m.ctx.Done():
	}
}

func (m *botRelayManager) run() {
	// ponytail: one queue prevents uncorrelated bot replies from crossing; split
	// per target bot only if independent relay throughput becomes necessary.
	for {
		select {
		case request := <-m.requests:
			m.process(request)
		case <-m.ctx.Done():
			return
		}
	}
}

func (m *botRelayManager) process(request botRelayRequest) {
	logger := log.FromContext(m.ctx).With("relay_id", request.relay.ID)
	m.activeTarget.Store(request.relay.TargetBotID)
	defer m.activeTarget.Store(0)

	m.editStatus(request, i18nk.BotMsgRelayWaiting, map[string]any{
		"Bot":     request.relay.TargetBot,
		"Payload": request.payload,
	})
	userCtx := userclient.GetCtx()
	if userCtx == nil {
		m.fail(request, errors.New("userbot is not available"))
		return
	}
	sent, err := userCtx.SendMessage(request.relay.TargetBotID, &tg.MessagesSendMessageRequest{
		Message: "/start " + request.payload,
	})
	if err != nil {
		m.fail(request, fmt.Errorf("send relay command: %w", err))
		return
	}

	timeout := time.NewTimer(time.Duration(max(request.relay.TimeoutSeconds, 1)) * time.Second)
	defer timeout.Stop()
	var quiet *time.Timer
	var quietC <-chan time.Time
	files := make([]tfile.TGFileMessage, 0)

	for {
		select {
		case response := <-m.responses:
			if !relayResponseMatches(response.message, sent.Message.ID) {
				continue
			}
			media, ok := response.message.GetMedia()
			if !ok || media == nil {
				if strings.Contains(strings.ToLower(response.message.GetMessage()), "pending") {
					m.editStatus(request, i18nk.BotMsgRelayPending, map[string]any{"Bot": request.relay.TargetBot})
				}
				continue
			}
			file, err := relayFile(response.ctx, response.message, media)
			if err != nil {
				logger.Warn("Ignoring unsupported relay media", "error", err)
				continue
			}
			files = append(files, file)
			m.editStatus(request, i18nk.BotMsgRelayReceived, map[string]any{"Count": len(files)})
			if quiet == nil {
				quiet = time.NewTimer(time.Duration(max(request.relay.QuietSeconds, 1)) * time.Second)
			} else {
				if !quiet.Stop() {
					select {
					case <-quiet.C:
					default:
					}
				}
				quiet.Reset(time.Duration(max(request.relay.QuietSeconds, 1)) * time.Second)
			}
			quietC = quiet.C
		case <-quietC:
			m.addTasks(request, files)
			return
		case <-timeout.C:
			if len(files) > 0 {
				m.addTasks(request, files)
				return
			}
			m.fail(request, fmt.Errorf("timed out waiting for @%s", request.relay.TargetBot))
			return
		case <-m.ctx.Done():
			return
		}
	}
}

func relayResponseMatches(message *tg.Message, startMessageID int) bool {
	if message == nil || message.ID <= startMessageID {
		return false
	}
	if reply, ok := message.ReplyTo.(*tg.MessageReplyHeader); ok && reply.ReplyToMsgID > 0 {
		return reply.ReplyToMsgID >= startMessageID
	}
	return true
}

func relayFile(ctx *ext.Context, message *tg.Message, media tg.MessageMediaClass) (tfile.TGFileMessage, error) {
	switch media.(type) {
	case *tg.MessageMediaDocument, *tg.MessageMediaPhoto:
	default:
		return nil, fmt.Errorf("unsupported media type: %s", media.TypeName())
	}
	return tfile.FromMediaMessage(media, ctx.Raw, message, tfile.WithNameIfEmpty(
		tgutil.GenFileNameFromMessage(*message),
	))
}

func (m *botRelayManager) addTasks(request botRelayRequest, files []tfile.TGFileMessage) {
	user, err := database.GetUserByID(m.ctx, request.relay.UserID)
	if err != nil {
		m.fail(request, fmt.Errorf("load relay owner: %w", err))
		return
	}
	if user.DefaultStorage == "" {
		m.fail(request, errors.New(i18n.T(i18nk.BotMsgCommonErrorDefaultStorageNotSet)))
		return
	}
	stor, err := storage.GetStorageByUserIDAndName(m.ctx, user.ChatID, user.DefaultStorage)
	if err != nil {
		m.fail(request, fmt.Errorf("load default storage: %w", err))
		return
	}
	dirPath := ""
	if user.DefaultDir != 0 {
		dir, err := database.GetDirByID(m.ctx, user.DefaultDir)
		if err != nil {
			m.fail(request, fmt.Errorf("load default directory: %w", err))
			return
		}
		dirPath = dir.Path
	}

	var result error
	if len(files) == 1 {
		result = shortcut.CreateAndAddTGFileTaskWithEdit(m.botCtx, user.ChatID, stor, dirPath, files[0], request.statusMessageID)
	} else {
		result = shortcut.CreateAndAddBatchTGFileTaskWithEdit(m.botCtx, user.ChatID, stor, dirPath, files, request.statusMessageID)
	}
	if result != nil && !errors.Is(result, dispatcher.EndGroups) {
		m.fail(request, result)
	}
}

func (m *botRelayManager) fail(request botRelayRequest, err error) {
	log.FromContext(m.ctx).Error("Bot Relay failed", "relay_id", request.relay.ID, "error", err)
	m.editStatus(request, i18nk.BotMsgRelayFailed, map[string]any{"Reason": err.Error()})
}

func (m *botRelayManager) sendStatus(chatID int64, key i18nk.Key, data map[string]any) (*tg.Message, error) {
	text, entities, err := renderRelayStatus(key, data)
	if err != nil {
		return nil, err
	}
	message, err := m.botCtx.SendMessage(chatID, &tg.MessagesSendMessageRequest{Message: text, Entities: entities})
	if err != nil {
		return nil, err
	}
	return message.Message, nil
}

func (m *botRelayManager) editStatus(request botRelayRequest, key i18nk.Key, data map[string]any) {
	text, entities, err := renderRelayStatus(key, data)
	if err != nil {
		log.FromContext(m.ctx).Error("Failed to render Bot Relay status", "error", err)
		return
	}
	if _, err := m.botCtx.EditMessage(request.ownerChatID, &tg.MessagesEditMessageRequest{
		ID:       request.statusMessageID,
		Message:  text,
		Entities: entities,
	}); err != nil {
		log.FromContext(m.ctx).Error("Failed to edit Bot Relay status", "error", err)
	}
}

func renderRelayStatus(key i18nk.Key, data map[string]any) (string, []tg.MessageEntityClass, error) {
	return tgutil.RenderHTML(i18n.T(key, tgutil.EscapeHTMLTemplateData(data)))
}

func relayPayloads(message *tg.Message, targetBot string) []string {
	urls := tgutil.ExtractMessageEntityUrls(message)
	urls = append(urls, deepLinkPattern.FindAllString(message.GetMessage(), -1)...)
	seen := make(map[string]struct{})
	payloads := make([]string, 0)
	for _, rawURL := range urls {
		payload, ok := parseRelayDeepLink(rawURL, targetBot)
		if !ok {
			continue
		}
		if _, exists := seen[payload]; exists {
			continue
		}
		seen[payload] = struct{}{}
		payloads = append(payloads, payload)
	}
	return payloads
}

func parseRelayDeepLink(rawURL, targetBot string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "t.me") {
		return "", false
	}
	username := strings.TrimPrefix(strings.TrimSpace(parsed.Path), "/")
	if username == parsed.Path || strings.Contains(username, "/") {
		return "", false
	}
	if !strings.EqualFold(username, strings.TrimPrefix(strings.TrimSpace(targetBot), "@")) {
		return "", false
	}
	payload := parsed.Query().Get("start")
	if !payloadPattern.MatchString(payload) {
		return "", false
	}
	return payload, true
}
