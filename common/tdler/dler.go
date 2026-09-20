package tdler

import (
	"context"
	"errors"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gotd/td/rpc"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/utils/dlutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/consts/tglimit"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func NewDownloader(file tfile.TGFile) *downloader.Builder {
	return downloader.NewDownloader().WithPartSize(tglimit.MaxPartSize).
		Download(eofAwareClient{Client: file.Dler(), size: file.Size()}, file.Location()).
		WithThreads(dlutil.BestThreads(file.Size(), config.C().Threads))
}

// eofAwareClient answers upload.getFile requests at or past the end of the
// file with an empty chunk. gotd's downloader is size-unaware: for files
// whose size is an exact multiple of the part size it issues one final
// request at offset == size and expects an empty chunk, but Telegram rejects
// it with 400 OFFSET_INVALID and the whole download fails.
type eofAwareClient struct {
	downloader.Client
	size int64
}

func (c eofAwareClient) UploadGetFile(ctx context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	if c.size > 0 && req.Offset >= c.size {
		return &tg.UploadFile{}, nil
	}
	// 分块请求在任务中执行，可以等待重连；只重试当前块，避免流式输出重复数据。
	b := backoff.NewExponentialBackOff(
		backoff.WithMaxInterval(10*time.Second),
		backoff.WithMaxElapsedTime(5*time.Minute),
	)
	return backoff.RetryWithData(func() (tg.UploadFileClass, error) {
		if err := ctx.Err(); err != nil {
			return nil, backoff.Permanent(err)
		}
		file, err := c.Client.UploadGetFile(ctx, req)
		if err != nil && !errors.Is(err, rpc.ErrEngineClosed) {
			return file, backoff.Permanent(err)
		}
		return file, err
	}, backoff.WithContext(b, ctx))
}
