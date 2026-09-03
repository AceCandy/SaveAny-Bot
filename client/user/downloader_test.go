package user

import (
	"context"
	"testing"

	"github.com/gotd/td/rpc"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

type downloaderStub struct {
	data   []byte
	err    error
	onCall func()
	calls  int
}

func (c *downloaderStub) UploadGetFile(context.Context, *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	c.calls++
	if c.onCall != nil {
		c.onCall()
	}
	return &tg.UploadFile{Bytes: c.data}, c.err
}

func (*downloaderStub) UploadGetFileHashes(context.Context, *tg.UploadGetFileHashesRequest) ([]tg.FileHash, error) {
	return nil, nil
}

func (*downloaderStub) UploadReuploadCDNFile(context.Context, *tg.UploadReuploadCDNFileRequest) ([]tg.FileHash, error) {
	return nil, nil
}

func (*downloaderStub) UploadGetCDNFileHashes(context.Context, *tg.UploadGetCDNFileHashesRequest) ([]tg.FileHash, error) {
	return nil, nil
}

func (*downloaderStub) UploadGetWebFile(context.Context, *tg.UploadGetWebFileRequest) (*tg.UploadWebFile, error) {
	return nil, nil
}

func TestCurrentDownloaderUsesLatestClient(t *testing.T) {
	first := &downloaderStub{data: []byte("first")}
	second := &downloaderStub{data: []byte("second")}
	active := downloader.Client(first)
	client := currentDownloader{get: func() downloader.Client { return active }}

	got, err := client.UploadGetFile(t.Context(), &tg.UploadGetFileRequest{})
	if err != nil {
		t.Fatalf("first UploadGetFile() error = %v", err)
	}
	if string(got.(*tg.UploadFile).Bytes) != "first" {
		t.Fatalf("first UploadGetFile() = %q, want %q", got.(*tg.UploadFile).Bytes, "first")
	}

	active = second
	got, err = client.UploadGetFile(t.Context(), &tg.UploadGetFileRequest{})
	if err != nil {
		t.Fatalf("second UploadGetFile() error = %v", err)
	}
	if string(got.(*tg.UploadFile).Bytes) != "second" {
		t.Fatalf("second UploadGetFile() = %q, want %q", got.(*tg.UploadFile).Bytes, "second")
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("calls = (%d, %d), want (1, 1)", first.calls, second.calls)
	}

	first = &downloaderStub{err: rpc.ErrEngineClosed}
	active = first
	first.onCall = func() { active = second }
	got, err = client.UploadGetFile(t.Context(), &tg.UploadGetFileRequest{})
	if err != nil {
		t.Fatalf("recovered UploadGetFile() error = %v", err)
	}
	if string(got.(*tg.UploadFile).Bytes) != "second" {
		t.Fatalf("recovered UploadGetFile() = %q, want %q", got.(*tg.UploadFile).Bytes, "second")
	}
}
