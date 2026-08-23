package user

import (
	"sync"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
)

var (
	relayHandlerMu sync.RWMutex
	relayHandler   func(*ext.Context, *ext.Update) error
)

// SetRelayMessageHandler connects the optional Bot Relay runtime to userbot updates.
func SetRelayMessageHandler(handler func(*ext.Context, *ext.Update) error) {
	relayHandlerMu.Lock()
	relayHandler = handler
	relayHandlerMu.Unlock()
}

func dispatchRelayMessage(ctx *ext.Context, update *ext.Update) error {
	relayHandlerMu.RLock()
	handler := relayHandler
	relayHandlerMu.RUnlock()
	if handler == nil {
		return dispatcher.ContinueGroups
	}
	return handler(ctx, update)
}
