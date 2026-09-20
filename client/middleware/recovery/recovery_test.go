package recovery

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/rpc"
	"github.com/gotd/td/telegram"
)

func TestClosedEngineDoesNotBlockUpdateHandler(t *testing.T) {
	errClosed := fmt.Errorf("rpcDoRequest: %w", rpc.ErrEngineClosed)
	calls := 0
	invoke := New(t.Context(), func() backoff.BackOff {
		return backoff.WithMaxRetries(&backoff.ZeroBackOff{}, 2)
	}).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		return errClosed
	}))
	if err := invoke(t.Context(), nil, nil); !errors.Is(err, rpc.ErrEngineClosed) {
		t.Fatalf("Invoke() error = %v, want engine closed", err)
	}
	if calls != 1 {
		t.Fatalf("Invoke() calls = %d, want 1 so the update handler can release the old connection", calls)
	}
}

func TestOtherTransientErrorsStillRecover(t *testing.T) {
	calls := 0
	invoke := New(t.Context(), func() backoff.BackOff {
		return backoff.WithMaxRetries(&backoff.ZeroBackOff{}, 2)
	}).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		if calls == 1 {
			return errors.New("temporary transport error")
		}
		return nil
	}))
	if err := invoke(t.Context(), nil, nil); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("Invoke() calls = %d, want 2", calls)
	}
}
