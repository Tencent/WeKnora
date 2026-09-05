package file

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractTenantIDFromPresignedURL pulls the tenant_id query parameter from a
// signed URL. Returns "" when the URL is not parseable as a presigned URL.
func extractTenantIDFromPresignedURL(t *testing.T, presigned string) string {
	t.Helper()
	u, err := url.Parse(presigned)
	require.NoError(t, err)
	return u.Query().Get("tenant_id")
}

// TestLocalGetFileURL_TenantIDFromPath verifies that tenant ID is extracted
// from the storage path — which encodes the resource owner, so cross-tenant
// shared resources resolve to the correct owning tenant's storage config.
func TestLocalGetFileURL_TenantIDFromPath(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")

	svc := NewLocalFileService("/data/files", "https://weknora.example.com")

	got, err := svc.GetFileURL(context.Background(), "local://7/abc/img.png")
	require.NoError(t, err)
	assert.Equal(t, "7", extractTenantIDFromPresignedURL(t, got))
}

// TestLocalGetFileURL_NoExternalURL verifies backward compatibility: without
// APP_EXTERNAL_URL, GetFileURL still returns the local:// path unchanged.
func TestLocalGetFileURL_NoExternalURL(t *testing.T) {
	svc := NewLocalFileService("/data/files", "")

	got, err := svc.GetFileURL(context.Background(), "local://1/abc/img.png")
	require.NoError(t, err)
	assert.Equal(t, "local://1/abc/img.png", got)
}

func TestLocalSaveBytesRecordsExactIntentBeforeWrite(t *testing.T) {
	baseDir := t.TempDir()
	svc := NewLocalFileService(baseDir, "")
	var intentPath string
	ctx := interfaces.WithFileWriteIntent(context.Background(), func(_ context.Context, path string) error {
		intentPath = path
		resolved := filepath.Join(baseDir, filepath.FromSlash(strings.TrimPrefix(path, localScheme)))
		_, err := os.Stat(resolved)
		require.ErrorIs(t, err, os.ErrNotExist)
		return nil
	})

	got, err := svc.SaveBytes(ctx, []byte("preview"), 7, "preview.docx", false)
	require.NoError(t, err)
	require.Equal(t, got, intentPath)
	require.True(t, strings.HasPrefix(got, "local://7/exports/preview_"))

	reader, err := svc.GetFile(context.Background(), got)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
}

func TestLocalSaveBytesRejectingIntentLeavesNoFile(t *testing.T) {
	baseDir := t.TempDir()
	svc := NewLocalFileService(baseDir, "")
	wantErr := errors.New("intent persistence failed")
	var intentPath string
	ctx := interfaces.WithFileWriteIntent(context.Background(), func(_ context.Context, path string) error {
		intentPath = path
		return wantErr
	})

	got, err := svc.SaveBytes(ctx, []byte("preview"), 7, "preview.docx", false)
	require.Empty(t, got)
	require.ErrorIs(t, err, wantErr)
	require.NotEmpty(t, intentPath)
	_, statErr := os.Stat(filepath.Join(baseDir, "7"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestLocalSaveBytesCanceledContextSkipsIntentAndWrite(t *testing.T) {
	baseDir := t.TempDir()
	svc := NewLocalFileService(baseDir, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	ctx = interfaces.WithFileWriteIntent(ctx, func(context.Context, string) error {
		called = true
		return nil
	})

	got, err := svc.SaveBytes(ctx, []byte("preview"), 7, "preview.docx", false)
	require.Empty(t, got)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, called)
	_, statErr := os.Stat(filepath.Join(baseDir, "7"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestLocalSaveBytesIntentCancellationAbortsWrite(t *testing.T) {
	baseDir := t.TempDir()
	svc := NewLocalFileService(baseDir, "")
	ctx, cancel := context.WithCancel(context.Background())
	ctx = interfaces.WithFileWriteIntent(ctx, func(context.Context, string) error {
		cancel()
		return nil
	})

	got, err := svc.SaveBytes(ctx, []byte("preview"), 7, "preview.docx", false)
	require.Empty(t, got)
	require.ErrorIs(t, err, context.Canceled)
	_, statErr := os.Stat(filepath.Join(baseDir, "7"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestLocalDeleteFileIsIdempotent(t *testing.T) {
	svc := NewLocalFileService(t.TempDir(), "")
	path, err := svc.SaveBytes(context.Background(), []byte("preview"), 7, "preview.docx", false)
	require.NoError(t, err)
	require.NoError(t, svc.DeleteFile(context.Background(), path))
	require.NoError(t, svc.DeleteFile(context.Background(), path))
}
