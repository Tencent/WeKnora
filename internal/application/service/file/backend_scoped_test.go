package file

import (
	"context"
	"io"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackendScopedLocalURLRetainsBackendID(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	inner := NewLocalFileService(t.TempDir(), "https://weknora.example.com/base")
	svc := NewBackendScopedFileService("backend-local-a", inner)

	path, err := svc.SaveBytes(context.Background(), []byte("hello"), 7, "exports/image.txt", false)
	require.NoError(t, err)
	assert.Contains(t, path, "storage://backend-local-a/local://")

	signed, err := svc.GetFileURL(context.Background(), path)
	require.NoError(t, err)
	u, err := url.Parse(signed)
	require.NoError(t, err)
	assert.Equal(t, path, u.Query().Get("file_path"))

	reader, err := svc.GetFile(context.Background(), path)
	require.NoError(t, err)
	defer reader.Close()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}
