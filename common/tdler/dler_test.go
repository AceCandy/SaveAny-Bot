package tdler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/rpc"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/krau/SaveAny-Bot/pkg/consts/tglimit"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

type reconnectingClient struct {
	downloader.Client
	getFile func(context.Context, *tg.UploadGetFileRequest) (tg.UploadFileClass, error)
}

func (c reconnectingClient) UploadGetFile(ctx context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	return c.getFile(ctx, req)
}

func TestDownloadResumesChunkAfterEngineClosed(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprintf("parallel=%v", parallel), func(t *testing.T) {
			data := make([]byte, 3*tglimit.MaxPartSize+17)
			for i := range data {
				data[i] = byte(i % 251)
			}
			server := &serverLikeClient{data: data}
			var mu sync.Mutex
			attempts := make(map[int64]int)
			client := reconnectingClient{getFile: func(ctx context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
				mu.Lock()
				attempts[req.Offset]++
				first := attempts[req.Offset] == 1
				mu.Unlock()
				if first {
					return nil, fmt.Errorf("rpcDoRequest: %w", rpc.ErrEngineClosed)
				}
				return server.UploadGetFile(ctx, req)
			}}
			file := tfile.NewTGFile(&tg.InputDocumentFileLocation{ID: 1}, client, int64(len(data)), "test.bin")
			dl := NewDownloader(file).WithThreads(4)
			var got []byte
			var err error
			if parallel {
				got = make([]byte, len(data))
				_, err = dl.Parallel(t.Context(), &memWriterAt{b: got})
			} else {
				var buf bytes.Buffer
				_, err = dl.Stream(t.Context(), &buf)
				got = buf.Bytes()
			}
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("downloaded %d bytes, error = %v; want %d matching bytes", len(got), err, len(data))
			}
			for i := range 4 {
				if got := attempts[int64(i*tglimit.MaxPartSize)]; got != 2 {
					t.Fatalf("chunk %d attempts = %d, want 2", i, got)
				}
			}
		})
	}
}

func TestDownloadRecoveryRespectsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	client := eofAwareClient{Client: reconnectingClient{getFile: func(context.Context, *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
		return nil, rpc.ErrEngineClosed
	}}}
	_, err := client.UploadGetFile(ctx, &tg.UploadGetFileRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestDownloadRecoveryStopsOnCancellationOrOtherError(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancelRequest), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			calls := 0
			client := eofAwareClient{Client: reconnectingClient{getFile: func(context.Context, *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
				calls++
				if cancelRequest {
					cancel()
					return nil, rpc.ErrEngineClosed
				}
				return nil, want
			}}}
			_, err := client.UploadGetFile(ctx, &tg.UploadGetFileRequest{})
			var expected error = want
			if cancelRequest {
				expected = context.Canceled
			}
			if !errors.Is(err, expected) || calls != 1 {
				t.Fatalf("error = %v, calls = %d; want %v, 1", err, calls, expected)
			}
		})
	}
}

// serverLikeClient mimics real Telegram upload.getFile behavior: it returns
// up to limit bytes per chunk, and answers any offset at or past the end of
// the file with 400 OFFSET_INVALID.
type serverLikeClient struct {
	data []byte

	mu        sync.Mutex
	maxOffset int64
}

func (c *serverLikeClient) UploadGetFile(_ context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	c.mu.Lock()
	if req.Offset > c.maxOffset {
		c.maxOffset = req.Offset
	}
	c.mu.Unlock()
	if req.Offset >= int64(len(c.data)) {
		return nil, tgerr.New(400, "OFFSET_INVALID")
	}
	end := min(len(c.data), int(req.Offset)+req.Limit)
	return &tg.UploadFile{Bytes: c.data[req.Offset:end]}, nil
}

func (c *serverLikeClient) UploadGetFileHashes(context.Context, *tg.UploadGetFileHashesRequest) ([]tg.FileHash, error) {
	return nil, nil
}

func (c *serverLikeClient) UploadReuploadCDNFile(context.Context, *tg.UploadReuploadCDNFileRequest) ([]tg.FileHash, error) {
	return nil, nil
}

func (c *serverLikeClient) UploadGetCDNFileHashes(context.Context, *tg.UploadGetCDNFileHashesRequest) ([]tg.FileHash, error) {
	return nil, nil
}

func (c *serverLikeClient) UploadGetWebFile(context.Context, *tg.UploadGetWebFileRequest) (*tg.UploadWebFile, error) {
	return nil, nil
}

type memWriterAt struct {
	b []byte
}

func (w *memWriterAt) WriteAt(p []byte, off int64) (int, error) {
	copy(w.b[off:], p)
	return len(p), nil
}

func TestDownloadServerLikeEOF(t *testing.T) {
	const partSize = 1024 * 1024
	tests := []struct {
		name     string
		size     int
		parallel bool
	}{
		{"stream exact multiple of part size", 2 * partSize, false},
		{"stream non-multiple", 2*partSize + 12345, false},
		{"stream smaller than part size", 1234, false},
		{"parallel exact multiple of part size", 2 * partSize, true},
		{"parallel non-multiple", 2*partSize + 12345, true},
		{"parallel smaller than part size", 1234, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := make([]byte, tt.size)
			for i := range data {
				data[i] = byte(i % 251)
			}
			client := &serverLikeClient{data: data}
			file := tfile.NewTGFile(
				&tg.InputDocumentFileLocation{ID: 1, AccessHash: 2},
				client, int64(tt.size), "test.bin",
			)

			dl := NewDownloader(file)
			var got []byte
			var err error
			if tt.parallel {
				buf := make([]byte, tt.size)
				_, err = dl.WithThreads(4).Parallel(context.Background(), &memWriterAt{b: buf})
				got = buf
			} else {
				var buf bytes.Buffer
				_, err = dl.Stream(context.Background(), &buf)
				got = buf.Bytes()
			}
			if err != nil {
				t.Fatalf("download failed: %v", err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("downloaded %d bytes, want %d matching bytes", len(got), len(data))
			}
			if client.maxOffset >= int64(tt.size) {
				t.Fatalf("requested offset %d at or past EOF (size %d)", client.maxOffset, tt.size)
			}
		})
	}
}

// Photos have no size in InputPhotoFileLocation, so TGFile.Size() is 0
// ("unknown"). The EOF guard must not fire on the very first request at
// offset 0, or the download silently yields a 0-byte file.
func TestDownloadPhotoUnknownSize(t *testing.T) {
	data := make([]byte, 89708)
	for i := range data {
		data[i] = byte(i % 251)
	}
	client := &serverLikeClient{data: data}
	file := tfile.NewTGFile(
		&tg.InputPhotoFileLocation{ID: 1, AccessHash: 2},
		client, 0, "photo.png",
	)

	dl := NewDownloader(file)
	buf := make([]byte, len(data))
	_, err := dl.WithThreads(1).Parallel(context.Background(), &memWriterAt{b: buf})
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if !bytes.Equal(buf, data) {
		t.Fatalf("downloaded %d bytes, want %d matching bytes", len(buf), len(data))
	}
	if client.maxOffset >= int64(len(data)) {
		t.Fatalf("requested offset %d at or past EOF (size %d)", client.maxOffset, len(data))
	}
}
