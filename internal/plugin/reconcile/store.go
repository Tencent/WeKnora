package reconcile

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PackageStore keeps plugin package archives where every node can read them.
type PackageStore interface {
	// Put stores an archive and returns the URI to read it back.
	Put(ctx context.Context, digest string, data []byte) (string, error)
	Get(ctx context.Context, uri string) ([]byte, error)
	Delete(ctx context.Context, uri string) error
}

// fileStore stores packages through a raw FileService (the deployment's
// object storage, or local disk on single-node setups). Packages are not
// tenant data, so they bypass the tenant resource catalog.
type fileStore struct {
	fs interfaces.FileService
}

// NewFileStore returns a PackageStore on top of a raw FileService.
func NewFileStore(fs interfaces.FileService) PackageStore {
	return &fileStore{fs: fs}
}

func (s *fileStore) Put(ctx context.Context, digest string, data []byte) (string, error) {
	name := "plugin-" + strings.TrimPrefix(digest, "sha256:") + ".wkp"
	uri, err := s.fs.SaveBytes(ctx, data, 0, name, false)
	if err != nil {
		return "", fmt.Errorf("store plugin package: %w", err)
	}
	return uri, nil
}

func (s *fileStore) Get(ctx context.Context, uri string) ([]byte, error) {
	rc, err := s.fs.GetFile(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("read plugin package: %w", err)
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func (s *fileStore) Delete(ctx context.Context, uri string) error {
	return s.fs.DeleteFile(ctx, uri)
}
