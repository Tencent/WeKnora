package file

import (
	"context"
	"net/url"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
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

// TestLocalGetFileURL_TenantIDFromContext verifies that tenant context wins
// over path parsing — critical when the first numeric segment of the path is
// a bucket name or region (not the tenant).
func TestLocalGetFileURL_TenantIDFromContext(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")

	svc := NewLocalFileService("/data/files", "https://weknora.example.com")

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	got, err := svc.GetFileURL(ctx, "local://1/abc/img.png")
	require.NoError(t, err)
	// Context tenant (42) must override the path's first numeric segment (1).
	assert.Equal(t, "42", extractTenantIDFromPresignedURL(t, got))
}

// TestLocalGetFileURL_FallbackToPathParse verifies that when context is
// missing, the service falls back to parsing the tenant ID from the path.
func TestLocalGetFileURL_FallbackToPathParse(t *testing.T) {
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
