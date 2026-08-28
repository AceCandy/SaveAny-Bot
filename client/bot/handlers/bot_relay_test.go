package handlers

import (
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
