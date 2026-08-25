package user

import (
	"context"
	"errors"
	"testing"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/ext"
)

func TestLogoutClearsClientOnlyAfterTelegramLogout(t *testing.T) {
	originalLogout := logoutClient
	t.Cleanup(func() {
		logoutClient = originalLogout
		clientMu.Lock()
		uc = nil
		ectx = nil
		clientMu.Unlock()
	})

	telegramError := errors.New("telegram unavailable")
	for _, test := range []struct {
		name    string
		err     error
		cleared bool
	}{
		{name: "success", cleared: true},
		{name: "failure keeps client", err: telegramError},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &gotgproto.Client{}
			clientMu.Lock()
			uc = client
			ectx = &ext.Context{}
			clientMu.Unlock()
			logoutClient = func(context.Context, *gotgproto.Client) error { return test.err }

			err := Logout(t.Context())
			if !errors.Is(err, test.err) {
				t.Fatalf("Logout() error = %v, want %v", err, test.err)
			}
			if got := GetCtx() == nil; got != test.cleared {
				t.Fatalf("client cleared = %t, want %t", got, test.cleared)
			}
		})
	}
}
