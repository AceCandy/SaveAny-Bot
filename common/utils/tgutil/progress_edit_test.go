package tgutil

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func TestProgressEditQueueBlockedSenderKeepsFinalAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	sent := make(chan int, 4)
	q := NewProgressEditQueue(func(ctx context.Context, _ int64, req *tg.MessagesEditMessageRequest) error {
		if req.ID == 1 {
			close(started)
			<-release
		}
		sent <- req.ID
		return ctx.Err()
	})
	q.Submit(ctx, 1, &tg.MessagesEditMessageRequest{ID: 1}, false)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("sender did not start")
	}

	submitted := make(chan struct{})
	go func() {
		q.Submit(ctx, 1, &tg.MessagesEditMessageRequest{ID: 2}, false)
		q.Submit(ctx, 1, &tg.MessagesEditMessageRequest{ID: 3}, false)
		cancel()
		q.Submit(ctx, 1, &tg.MessagesEditMessageRequest{ID: 4}, true)
		q.Submit(ctx, 1, &tg.MessagesEditMessageRequest{ID: 5}, false)
		close(submitted)
	}()
	select {
	case <-submitted:
	case <-time.After(5 * time.Second):
		t.Fatal("submitting progress blocked behind the sender")
	}
	unblock.Do(func() { close(release) })
	for _, want := range []int{1, 4} {
		select {
		case got := <-sent:
			if got != want {
				t.Fatalf("sent message = %d, want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("message %d was not sent", want)
		}
	}
}
