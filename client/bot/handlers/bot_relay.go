package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
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
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

const (
	relayHistoryPageSize         = 10
	relayResponseBufferSize      = 100
	relayScheduleResolution      = time.Minute
	defaultRelayScanIntervalMins = database.DefaultBotRelayScanIntervalMinutes
)

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
	message *tg.Message
}

type botRelayManager struct {
	ctx          context.Context
	botCtx       *ext.Context
	responses    chan botRelayResponse
	activeTarget atomic.Int64
}

// StartBotRelay 启动来源频道定时扫描，并仅实时接收目标 Bot 的回复。
func StartBotRelay(ctx context.Context, botCtx *ext.Context) {
	manager := &botRelayManager{
		ctx:       ctx,
		botCtx:    botCtx,
		responses: make(chan botRelayResponse, relayResponseBufferSize),
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
	chatID := update.EffectiveChat().GetID()
	if _, ok := update.UpdateClass.(*tg.UpdateEditMessage); ok {
		if m.activeTarget.Load() == chatID {
			log.FromContext(ctx).Debug("Ignoring Bot Relay response", "reason", "edited_message", "chat_id", chatID, "message_id", message.ID)
		}
		return dispatcher.ContinueGroups
	}

	if _, ok := message.PeerID.(*tg.PeerUser); ok {
		activeTarget := m.activeTarget.Load()
		if activeTarget != chatID {
			if sender, ok := message.FromID.(*tg.PeerUser); ok && sender.UserID == activeTarget {
				log.FromContext(ctx).Debug("Ignoring Bot Relay response", "reason", "chat_mismatch", "chat_id", chatID, "active_target", activeTarget, "message_id", message.ID)
			}
			return dispatcher.ContinueGroups
		}
		sender, ok := message.FromID.(*tg.PeerUser)
		if message.FromID != nil && (!ok || sender.UserID != chatID) {
			senderID := int64(0)
			if ok {
				senderID = sender.UserID
			}
			log.FromContext(ctx).Debug("Ignoring Bot Relay response", "reason", "sender_mismatch", "chat_id", chatID, "sender_id", senderID, "sender_type", fmt.Sprintf("%T", message.FromID), "message_id", message.ID)
			return dispatcher.ContinueGroups
		}
		select {
		case m.responses <- botRelayResponse{message: message.Message}:
		case <-m.ctx.Done():
		}
		return dispatcher.EndGroups
	}
	return dispatcher.ContinueGroups
}

func (m *botRelayManager) newRequest(relay database.BotRelay, payload string) (botRelayRequest, error) {
	owner, err := database.GetUserByID(m.ctx, relay.UserID)
	if err != nil {
		return botRelayRequest{}, fmt.Errorf("load relay owner: %w", err)
	}
	message, err := m.sendStatus(owner.ChatID, i18nk.BotMsgRelayQueued, map[string]any{
		"Bot":      relay.TargetBot,
		"Payload":  payload,
		"Position": 1,
	})
	if err != nil {
		return botRelayRequest{}, fmt.Errorf("send relay status: %w", err)
	}
	return botRelayRequest{
		relay:           relay,
		payload:         payload,
		ownerChatID:     owner.ChatID,
		statusMessageID: message.ID,
	}, nil
}

func (m *botRelayManager) run() {
	ticker := time.NewTicker(relayScheduleResolution)
	defer ticker.Stop()
	lastScan := make(map[uint]time.Time)
	for {
		m.scanDueRelays(lastScan, time.Now())
		select {
		case <-ticker.C:
		case <-m.ctx.Done():
			return
		}
	}
}

// scanDueRelays 串行扫描到期路由，避免不同目标 Bot 的回复互相串扰。
func (m *botRelayManager) scanDueRelays(lastScan map[uint]time.Time, now time.Time) {
	relays, err := database.GetAllBotRelays(m.ctx)
	if err != nil {
		log.FromContext(m.ctx).Error("Failed to load Bot Relay routes", "error", err)
		return
	}
	for _, relay := range relays {
		if !relay.Enabled {
			continue
		}
		intervalMinutes := relay.ScanIntervalMinutes
		if intervalMinutes < 1 {
			intervalMinutes = defaultRelayScanIntervalMins
		}
		if last, ok := lastScan[relay.ID]; ok && now.Sub(last) < time.Duration(intervalMinutes)*time.Minute {
			continue
		}
		lastScan[relay.ID] = now
		if err := m.scanRelay(relay); err != nil && !errors.Is(err, context.Canceled) {
			log.FromContext(m.ctx).Error("Bot Relay scan failed", "relay_id", relay.ID, "error", err)
		}
	}
}

// scanRelay 拉取游标之后的频道消息，并在每条消息处理完成后推进游标。
func (m *botRelayManager) scanRelay(relay database.BotRelay) error {
	userCtx := userclient.GetCtx()
	if userCtx == nil {
		return errors.New("userbot is not available")
	}
	peer, err := userCtx.ResolveInputPeerById(relay.SourceChatID)
	if err != nil {
		return fmt.Errorf("resolve source channel: %w", err)
	}
	messages, err := collectRelayHistoryMessages(m.ctx, relay.LastMessageID, func(ctx context.Context, offsetID int) ([]tg.MessageClass, error) {
		history, err := userCtx.Raw.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     peer,
			OffsetID: offsetID,
			Limit:    relayHistoryPageSize,
		})
		if err != nil {
			return nil, err
		}
		modified, ok := history.AsModified()
		if !ok {
			return nil, errors.New("source channel history was not returned")
		}
		return modified.GetMessages(), nil
	})
	if err != nil {
		return fmt.Errorf("load source channel history: %w", err)
	}
	if relay.LastMessageID == nil {
		baseline := initialRelayCursor(messages)
		if err := database.UpdateBotRelayLastMessageID(m.ctx, relay, baseline); err != nil {
			return fmt.Errorf("initialize source message cursor: %w", err)
		}
		relay.LastMessageID = &baseline
	}
	log.FromContext(m.ctx).Debug("Scanning Bot Relay history", "relay_id", relay.ID, "last_message_id", *relay.LastMessageID, "message_count", len(messages))
	return processRelayHistoryMessages(messages, relay.TargetBot, func(payload string) error {
		return m.processRelayPayload(relay, payload)
	}, func(messageID int, processErr error) {
		if err := database.RecordBotRelayHistory(m.ctx, relay, messageID, processErr); err != nil {
			log.FromContext(m.ctx).Error("Failed to record Bot Relay history", "relay_id", relay.ID, "message_id", messageID, "error", err)
		}
	}, func(messageID int) error {
		if err := database.UpdateBotRelayLastMessageID(m.ctx, relay, messageID); err != nil {
			return err
		}
		relay.LastMessageID = &messageID
		return nil
	})
}

type relayHistoryMessage struct {
	id      int
	message *tg.Message
}

func initialRelayCursor(messages []relayHistoryMessage) int {
	if len(messages) == 0 {
		return 0
	}
	return messages[0].id - 1
}

// collectRelayHistoryMessages 首次只取最新一页，之后持续翻页直到旧游标。
func collectRelayHistoryMessages(ctx context.Context, lastMessageID *int, fetch func(context.Context, int) ([]tg.MessageClass, error)) ([]relayHistoryMessage, error) {
	offsetID := 0
	seen := make(map[int]struct{})
	messages := make([]relayHistoryMessage, 0, relayHistoryPageSize)
	for {
		page, err := fetch(ctx, offsetID)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		oldestID := 0
		reachedCursor := false
		for _, item := range page {
			messageID := item.GetID()
			if messageID < 1 {
				continue
			}
			if oldestID == 0 || messageID < oldestID {
				oldestID = messageID
			}
			if lastMessageID != nil && messageID <= *lastMessageID {
				reachedCursor = true
				continue
			}
			if _, ok := seen[messageID]; ok {
				continue
			}
			seen[messageID] = struct{}{}
			message, _ := item.(*tg.Message)
			messages = append(messages, relayHistoryMessage{id: messageID, message: message})
		}
		if lastMessageID == nil || reachedCursor || len(page) < relayHistoryPageSize || oldestID == 0 {
			break
		}
		if offsetID != 0 && oldestID >= offsetID {
			return nil, errors.New("source channel history did not advance")
		}
		offsetID = oldestID
	}
	slices.SortFunc(messages, func(a, b relayHistoryMessage) int { return a.id - b.id })
	return messages, nil
}

// processRelayHistoryMessages 按消息 ID 升序处理，失败时不越过当前消息。
func processRelayHistoryMessages(messages []relayHistoryMessage, targetBot string, process func(string) error, record func(int, error), advance func(int) error) error {
	for _, item := range messages {
		var processErr error
		if item.message != nil {
			// ponytail: a message with multiple matching links is retried as a unit; persist
			// per-link state only if duplicate retries become a real problem.
			for _, payload := range relayPayloads(item.message, targetBot) {
				if err := process(payload); err != nil {
					processErr = err
					break
				}
			}
		}
		record(item.id, processErr)
		if processErr != nil {
			return fmt.Errorf("process source message %d: %w", item.id, processErr)
		}
		if err := advance(item.id); err != nil {
			return fmt.Errorf("advance source message %d: %w", item.id, err)
		}
	}
	return nil
}

func (m *botRelayManager) processRelayPayload(relay database.BotRelay, payload string) error {
	request, err := m.newRequest(relay, payload)
	if err != nil {
		return err
	}
	if err := m.process(request); err != nil {
		if !errors.Is(err, dispatcher.EndGroups) {
			m.fail(request, err)
		}
		return err
	}
	return nil
}

func (m *botRelayManager) process(request botRelayRequest) error {
	logger := log.FromContext(m.ctx).With("relay_id", request.relay.ID)
	m.activeTarget.Store(request.relay.TargetBotID)
	defer m.activeTarget.Store(0)

	m.editStatus(request, i18nk.BotMsgRelayWaiting, map[string]any{
		"Bot":     request.relay.TargetBot,
		"Payload": request.payload,
	})
	userCtx := userclient.GetCtx()
	if userCtx == nil {
		return errors.New("userbot is not available")
	}
	sent, err := userCtx.SendMessage(request.relay.TargetBotID, &tg.MessagesSendMessageRequest{
		Message: "/start " + request.payload,
	})
	if err != nil {
		return fmt.Errorf("send relay command: %w", err)
	}
	logger.Debug("Sent Bot Relay command", "target_bot", request.relay.TargetBot, "target_bot_id", request.relay.TargetBotID, "message_id", sent.Message.ID)

	timeout := time.NewTimer(time.Duration(max(request.relay.TimeoutSeconds, 1)) * time.Second)
	defer timeout.Stop()
	var quiet *time.Timer
	var quietC <-chan time.Time
	files := make([]tfile.TGFileMessage, 0)

	for {
		select {
		case response := <-m.responses:
			if !relayResponseMatches(response.message, sent.Message.ID) {
				replyToMessageID := 0
				if reply, ok := response.message.ReplyTo.(*tg.MessageReplyHeader); ok {
					replyToMessageID = reply.ReplyToMsgID
				}
				logger.Debug("Ignoring Bot Relay response", "reason", "response_not_matched", "start_message_id", sent.Message.ID, "message_id", response.message.ID, "reply_to_message_id", replyToMessageID)
				continue
			}
			media, ok := response.message.GetMedia()
			if !ok || media == nil {
				pending := strings.Contains(strings.ToLower(response.message.GetMessage()), "pending")
				logger.Debug("Ignoring Bot Relay response", "reason", "response_has_no_media", "message_id", response.message.ID, "text_length", len(response.message.GetMessage()), "pending", pending)
				if pending {
					m.editStatus(request, i18nk.BotMsgRelayPending, map[string]any{"Bot": request.relay.TargetBot})
				}
				continue
			}
			file, err := relayFile(response.message, media)
			if err != nil {
				logger.Warn("Ignoring unsupported relay media", "message_id", response.message.ID, "media_type", media.TypeName(), "error", err)
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
			return m.addTasks(request, files)
		case <-timeout.C:
			if len(files) > 0 {
				return m.addTasks(request, files)
			}
			return fmt.Errorf("timed out waiting for @%s", request.relay.TargetBot)
		case <-m.ctx.Done():
			return m.ctx.Err()
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

func relayFile(message *tg.Message, media tg.MessageMediaClass) (tfile.TGFileMessage, error) {
	switch media.(type) {
	case *tg.MessageMediaDocument, *tg.MessageMediaPhoto:
	default:
		return nil, fmt.Errorf("unsupported media type: %s", media.TypeName())
	}
	return tfile.FromMediaMessage(media, userclient.CurrentDownloader(), message, tfile.WithNameIfEmpty(
		tgutil.GenFileNameFromMessage(*message),
	))
}

func (m *botRelayManager) addTasks(request botRelayRequest, files []tfile.TGFileMessage) error {
	user, err := database.GetUserByID(m.ctx, request.relay.UserID)
	if err != nil {
		return fmt.Errorf("load relay owner: %w", err)
	}
	if user.DefaultStorage == "" {
		return errors.New(i18n.T(i18nk.BotMsgCommonErrorDefaultStorageNotSet))
	}
	stor, err := storage.GetStorageByUserIDAndName(m.ctx, user.ChatID, user.DefaultStorage)
	if err != nil {
		return fmt.Errorf("load default storage: %w", err)
	}
	dirPath := ""
	if user.DefaultDir != 0 {
		dir, err := database.GetDirByID(m.ctx, user.DefaultDir)
		if err != nil {
			return fmt.Errorf("load default directory: %w", err)
		}
		dirPath = dir.Path
	}

	var result error
	relayCtx := *m.botCtx
	relayCtx.Context = taskevent.WithSource(relayCtx.Context, taskevent.SourceRelay)
	if len(files) == 1 {
		result = shortcut.CreateAndAddTGFileTaskWithEdit(&relayCtx, user.ChatID, stor, dirPath, files[0], request.statusMessageID)
	} else {
		result = shortcut.CreateAndAddBatchTGFileTaskWithEdit(&relayCtx, user.ChatID, stor, dirPath, files, request.statusMessageID)
	}
	if result == nil || result == dispatcher.EndGroups {
		return nil
	}
	return result
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
