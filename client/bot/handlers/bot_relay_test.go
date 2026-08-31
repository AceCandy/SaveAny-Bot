package handlers

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/types"
	"github.com/gotd/td/tg"
)

func TestBotRelayAcceptsPrivateResponseWithoutSender(t *testing.T) {
	const targetID int64 = 42
	manager := &botRelayManager{
		ctx:       t.Context(),
		responses: make(chan botRelayResponse, 1),
	}
	manager.activeTarget.Store(targetID)
	update := &ext.Update{
		EffectiveMessage: &types.Message{Message: &tg.Message{
			ID:     7,
			PeerID: &tg.PeerUser{UserID: targetID},
		}},
		Entities: &tg.Entities{Users: map[int64]*tg.User{targetID: {ID: targetID}}},
	}
	ctx := &ext.Context{Context: t.Context()}

	if err := manager.handleUpdate(ctx, update); err != dispatcher.EndGroups {
		t.Fatalf("handleUpdate() error = %v; want %v", err, dispatcher.EndGroups)
	}
	if got := (<-manager.responses).message.ID; got != 7 {
		t.Fatalf("response message ID = %d; want 7", got)
	}

	update.EffectiveMessage.FromID = &tg.PeerUser{UserID: 99}
	if err := manager.handleUpdate(ctx, update); err != dispatcher.ContinueGroups {
		t.Fatalf("handleUpdate() error = %v; want %v", err, dispatcher.ContinueGroups)
	}
	if len(manager.responses) != 0 {
		t.Fatal("handleUpdate() queued a response from a mismatched sender")
	}
}

func TestParseRelayDeepLink(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		targetBot string
		want      string
		ok        bool
	}{
		{name: "valid", url: "https://t.me/btfffbot?start=20260805152009709", targetBot: "btfffbot", want: "20260805152009709", ok: true},
		{name: "target mismatch", url: "https://t.me/otherbot?start=abc", targetBot: "btfffbot"},
		{name: "invalid payload", url: "https://t.me/btfffbot?start=bad%20payload", targetBot: "btfffbot"},
		{name: "payload too long", url: "https://t.me/btfffbot?start=" + strings.Repeat("a", 65), targetBot: "btfffbot"},
		{name: "http rejected", url: "http://t.me/btfffbot?start=abc", targetBot: "btfffbot"},
		{name: "lookalike host", url: "https://t.me.example/btfffbot?start=abc", targetBot: "btfffbot"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseRelayDeepLink(test.url, test.targetBot)
			if got != test.want || ok != test.ok {
				t.Fatalf("parseRelayDeepLink() = %q, %v; want %q, %v", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestRelayPayloadsDeduplicatesLinks(t *testing.T) {
	link := "https://t.me/btfffbot?start=20260805152009709"
	got := relayPayloads(&tg.Message{Message: link + "\n" + link}, "@btfffbot")
	want := []string{"20260805152009709"}
	if !slices.Equal(got, want) {
		t.Fatalf("relayPayloads() = %v; want %v", got, want)
	}
}

func TestRelayPayloadsRejectsInvalidPlainTextPayload(t *testing.T) {
	message := &tg.Message{Message: "https://t.me/btfffbot?start=" + strings.Repeat("a", 65) + " https://t.me/btfffbot?start=bad%20payload"}
	if got := relayPayloads(message, "btfffbot"); len(got) != 0 {
		t.Fatalf("relayPayloads() = %v; want no payloads", got)
	}
}

func TestRelayResponseMatchesCurrentRequest(t *testing.T) {
	tests := []struct {
		name    string
		message *tg.Message
		want    bool
	}{
		{name: "older message", message: &tg.Message{ID: 99}},
		{name: "new message", message: &tg.Message{ID: 101}, want: true},
		{name: "reply to older request", message: &tg.Message{ID: 101, ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 90}}},
		{name: "reply to current request", message: &tg.Message{ID: 101, ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 100}}, want: true},
		{name: "reply within current response", message: &tg.Message{ID: 102, ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 101}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := relayResponseMatches(test.message, 100); got != test.want {
				t.Fatalf("relayResponseMatches() = %v; want %v", got, test.want)
			}
		})
	}
}

func TestCollectRelayHistoryMessagesInitialScanUsesLatestPage(t *testing.T) {
	var offsets []int
	got, err := collectRelayHistoryMessages(t.Context(), nil, func(_ context.Context, offsetID int) ([]tg.MessageClass, error) {
		offsets = append(offsets, offsetID)
		return relayMessageClasses(14, 13, 12, 11, 10, 9, 8, 7, 6, 5), nil
	})
	if err != nil {
		t.Fatalf("collectRelayHistoryMessages() error = %v", err)
	}
	if !slices.Equal(relayHistoryMessageIDs(got), []int{5, 6, 7, 8, 9, 10, 11, 12, 13, 14}) {
		t.Fatalf("message IDs = %v; want latest page in ascending order", relayHistoryMessageIDs(got))
	}
	if !slices.Equal(offsets, []int{0}) {
		t.Fatalf("offsets = %v; want one initial page", offsets)
	}
}

func TestInitialRelayCursorPrecedesLatestPage(t *testing.T) {
	if got := initialRelayCursor(nil); got != 0 {
		t.Fatalf("initialRelayCursor(nil) = %d; want 0", got)
	}
	if got := initialRelayCursor([]relayHistoryMessage{{id: 5}, {id: 6}}); got != 4 {
		t.Fatalf("initialRelayCursor() = %d; want 4", got)
	}
}

func TestCollectRelayHistoryMessagesPaginatesToCursor(t *testing.T) {
	var offsets []int
	lastMessageID := 2
	got, err := collectRelayHistoryMessages(t.Context(), &lastMessageID, func(_ context.Context, offsetID int) ([]tg.MessageClass, error) {
		offsets = append(offsets, offsetID)
		if offsetID == 0 {
			return relayMessageClasses(20, 19, 18, 17, 16, 15, 14, 13, 12, 11), nil
		}
		return relayMessageClasses(10, 9, 8, 7, 6, 5, 4, 3, 2, 1), nil
	})
	if err != nil {
		t.Fatalf("collectRelayHistoryMessages() error = %v", err)
	}
	want := []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	if !slices.Equal(relayHistoryMessageIDs(got), want) {
		t.Fatalf("message IDs = %v; want %v", relayHistoryMessageIDs(got), want)
	}
	if !slices.Equal(offsets, []int{0, 11}) {
		t.Fatalf("offsets = %v; want [0 11]", offsets)
	}
}

func TestProcessRelayHistoryMessagesStopsBeforeFailedMessage(t *testing.T) {
	messages := []relayHistoryMessage{
		{id: 8, message: &tg.Message{ID: 8, Message: "https://t.me/btfffbot?start=p8"}},
		{id: 9, message: &tg.Message{ID: 9, Message: "https://t.me/btfffbot?start=p9"}},
		{id: 10, message: &tg.Message{ID: 10, Message: "https://t.me/btfffbot?start=p10"}},
	}
	wantErr := errors.New("failed")
	var processed []string
	var recorded []int
	var recordErrors []error
	var advanced []int
	err := processRelayHistoryMessages(messages, "btfffbot", func(payload string) error {
		processed = append(processed, payload)
		if payload == "p9" {
			return wantErr
		}
		return nil
	}, func(messageID int, err error) {
		recorded = append(recorded, messageID)
		recordErrors = append(recordErrors, err)
	}, func(messageID int) error {
		advanced = append(advanced, messageID)
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("processRelayHistoryMessages() error = %v; want %v", err, wantErr)
	}
	if !slices.Equal(processed, []string{"p8", "p9"}) {
		t.Fatalf("processed = %v; want [p8 p9]", processed)
	}
	if !slices.Equal(recorded, []int{8, 9}) || recordErrors[0] != nil || !errors.Is(recordErrors[1], wantErr) {
		t.Fatalf("recorded = %v, errors = %v; want [8 9], [nil failed]", recorded, recordErrors)
	}
	if !slices.Equal(advanced, []int{8}) {
		t.Fatalf("advanced = %v; want [8]", advanced)
	}
}

func relayMessageClasses(ids ...int) []tg.MessageClass {
	messages := make([]tg.MessageClass, 0, len(ids))
	for _, id := range ids {
		messages = append(messages, &tg.Message{ID: id})
	}
	return messages
}

func relayHistoryMessageIDs(messages []relayHistoryMessage) []int {
	ids := make([]int, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.id)
	}
	return ids
}
