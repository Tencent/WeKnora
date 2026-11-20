package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/minio/minio-go/v7"
	"github.com/tencentyun/cos-go-sdk-v5"
)

type verificationBackend struct {
	interfaces.FileService
	deleteErr error
	readErr   error
	reader    io.ReadCloser
	paths     []string
}

func (b *verificationBackend) DeleteFile(_ context.Context, path string) error {
	b.paths = append(b.paths, path)
	return b.deleteErr
}

func (b *verificationBackend) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	b.paths = append(b.paths, path)
	return b.reader, b.readErr
}

type lazyErrorReader struct{ err error }

func (r lazyErrorReader) Read([]byte) (int, error) { return 0, r.err }
func (r lazyErrorReader) Close() error             { return nil }

func TestDeleteAndVerifyAbsent(t *testing.T) {
	tests := []struct {
		name      string
		deleteErr error
		readErr   error
		reader    io.ReadCloser
		wantError bool
	}{
		{name: "missing on a retry", deleteErr: context.DeadlineExceeded, readErr: fs.ErrNotExist},
		{name: "COS object missing", readErr: fmt.Errorf("get: %w", &cos.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusNotFound}, Code: "NoSuchKey",
		})},
		{name: "MinIO lazy read reports missing", reader: lazyErrorReader{minio.ErrorResponse{Code: "NoSuchKey"}}},
		{name: "read denied", readErr: errors.New("access denied"), wantError: true},
		{name: "missing bucket", readErr: minio.ErrorResponse{Code: "NoSuchBucket"}, wantError: true},
		{name: "empty object still exists", reader: io.NopCloser(strings.NewReader("")), wantError: true},
		{name: "object still exists", reader: io.NopCloser(strings.NewReader("content")), wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const path = "storage://instance/local://7/original.pdf"
			backend := &verificationBackend{deleteErr: tt.deleteErr, readErr: tt.readErr, reader: tt.reader}
			err := DeleteAndVerifyAbsent(context.Background(), backend, path)
			if (err != nil) != tt.wantError {
				t.Fatalf("unexpected verification result: %v", err)
			}
			if len(backend.paths) != 2 || backend.paths[0] != path || backend.paths[1] != path {
				t.Fatalf("delete and verify paths differ: %v", backend.paths)
			}
		})
	}
}
