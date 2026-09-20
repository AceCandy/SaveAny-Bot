package recovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/rpc"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type recovery struct {
	ctx        context.Context
	newBackoff func() backoff.BackOff
}

// New returns a recovery middleware.
//
// newBackoff is a factory that must return a fresh backoff.BackOff on every call: backoff implementations in
// cenkalti/backoff/v4 (notably ExponentialBackOff) are not safe for concurrent
// use, and the Telegram client invokes RPCs from many goroutines in parallel.
//
// Sharing a single instance corrupts its internal counters, breaks the
// exponential interval, and defeats MaxElapsedTime - see issue #218.
func New(ctx context.Context, newBackoff func() backoff.BackOff) telegram.Middleware {
	return &recovery{
		ctx:        ctx,
		newBackoff: newBackoff,
	}
}

func (r *recovery) Handle(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		b := r.newBackoff()

		return backoff.RetryNotify(func() error {
			if err := next.Invoke(ctx, input, output); err != nil {
				if r.shouldRecover(ctx, err) {
					return fmt.Errorf("recovery: %w", err)
				}

				return backoff.Permanent(err)
			}

			return nil
		}, b, func(err error, duration time.Duration) {
			log.FromContext(ctx).Debug("Wait for connection recovery", "error", err, "duration", duration)
		})
	}
}

func (r *recovery) shouldRecover(ctx context.Context, err error) bool {
	// 连接退出会等待 update handler 返回；在 handler 中等待已关闭的 engine
	// 恢复会反过来阻塞重连。下载续传由任务中的分块请求负责。
	if errors.Is(err, rpc.ErrEngineClosed) {
		return false
	}
	// context in recovery is used to stop recovery process by external os signal, otherwise we will wait till max retries when user press ctrl+c
	select {
	case <-r.ctx.Done():
		return false
	case <-ctx.Done():
		return false
	default:
	}

	// we try recover when encountered any error that is not telegram business error
	_, ok := tgerr.As(err)

	return !ok
}
