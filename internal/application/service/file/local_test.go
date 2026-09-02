package file

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"

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

// TestNormalizePathForBase_StripsStorageBackendScope verifies that paths
// carrying the storage://<backend-id>/ scope wrapper (written by
// backend-scoped chains, e.g. "storage://uuid/local://10000/doc/file.md")
// resolve to the same file as their unscoped provider path. Scoped paths
// reach the raw provider on chains without the scoped wrapper — notably KB
// deletion — and previously fell into the relative-path branch, producing a
// nonexistent path under baseDir and silently leaking physical files.
func TestNormalizePathForBase_StripsStorageBackendScope(t *testing.T) {
	svc := NewLocalFileService("/data/files", "").(*localFileService)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"scoped provider path",
			"storage://3ac51020-0fe9-4eb6-a58e-e3f8eaee9d04/local://10000/doc1/file.md",
			"/data/files/10000/doc1/file.md",
		},
		{
			"scoped path after filepath.Clean collapsed double slashes",
			"storage:/3ac51020-0fe9-4eb6-a58e-e3f8eaee9d04/local:/10000/doc1/file.md",
			"/data/files/10000/doc1/file.md",
		},
		{
			"plain provider path unaffected",
			"local://10000/doc1/file.md",
			"/data/files/10000/doc1/file.md",
		},
		{
			"plain relative path unaffected",
			"10000/doc1/file.md",
			"/data/files/10000/doc1/file.md",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, filepath.ToSlash(svc.normalizePathForBase(tc.in)))
		})
	}
}
