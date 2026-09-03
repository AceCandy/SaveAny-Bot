package user

import (
	"context"
	"errors"

	"github.com/gotd/td/rpc"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

var errUserbotUnavailable = errors.New("userbot is not available")

type currentDownloader struct {
	get func() downloader.Client
}

// CurrentDownloader 返回始终使用当前 Userbot 连接的下载客户端。
func CurrentDownloader() downloader.Client {
	return currentDownloader{get: func() downloader.Client {
		ctx := GetCtx()
		if ctx == nil {
			return nil
		}
		return ctx.Raw
	}}
}

func (c currentDownloader) client() (downloader.Client, error) {
	if c.get == nil {
		return nil, errUserbotUnavailable
	}
	client := c.get()
	if client == nil {
		return nil, errUserbotUnavailable
	}
	return client, nil
}

// callCurrent 在旧 engine 关闭时重新获取当前 Userbot client，并重试当前请求一次。
func callCurrent[T any](c currentDownloader, call func(downloader.Client) (T, error)) (T, error) {
	client, err := c.client()
	if err != nil {
		var zero T
		return zero, err
	}
	result, err := call(client)
	if !errors.Is(err, rpc.ErrEngineClosed) {
		return result, err
	}
	client, err = c.client()
	if err != nil {
		var zero T
		return zero, err
	}
	return call(client)
}

func (c currentDownloader) UploadGetFile(ctx context.Context, request *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	return callCurrent(c, func(client downloader.Client) (tg.UploadFileClass, error) {
		return client.UploadGetFile(ctx, request)
	})
}

func (c currentDownloader) UploadGetFileHashes(ctx context.Context, request *tg.UploadGetFileHashesRequest) ([]tg.FileHash, error) {
	return callCurrent(c, func(client downloader.Client) ([]tg.FileHash, error) {
		return client.UploadGetFileHashes(ctx, request)
	})
}

func (c currentDownloader) UploadReuploadCDNFile(ctx context.Context, request *tg.UploadReuploadCDNFileRequest) ([]tg.FileHash, error) {
	return callCurrent(c, func(client downloader.Client) ([]tg.FileHash, error) {
		return client.UploadReuploadCDNFile(ctx, request)
	})
}

func (c currentDownloader) UploadGetCDNFileHashes(ctx context.Context, request *tg.UploadGetCDNFileHashesRequest) ([]tg.FileHash, error) {
	return callCurrent(c, func(client downloader.Client) ([]tg.FileHash, error) {
		return client.UploadGetCDNFileHashes(ctx, request)
	})
}

func (c currentDownloader) UploadGetWebFile(ctx context.Context, request *tg.UploadGetWebFileRequest) (*tg.UploadWebFile, error) {
	return callCurrent(c, func(client downloader.Client) (*tg.UploadWebFile, error) {
		return client.UploadGetWebFile(ctx, request)
	})
}
