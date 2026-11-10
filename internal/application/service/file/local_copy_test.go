package file

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedLocalObject writes a file under baseDir at the SaveBytes-style layout
// (tenant/exports) and returns its local:// path.
func seedLocalObject(t *testing.T, baseDir string, tenantID uint64, name string, data []byte) string {
	t.Helper()
	dir := filepath.Join(baseDir, "0", "src-knowledge")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	full := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(full, data, 0o644))
	rel, err := filepath.Rel(baseDir, full)
	require.NoError(t, err)
	return localScheme + filepath.ToSlash(rel)
}

func readLocal(t *testing.T, svc interface {
	GetFile(context.Context, string) (io.ReadCloser, error)
}, path string) []byte {
	t.Helper()
	rc, err := svc.GetFile(context.Background(), path)
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return b
}

// TestLocalCopyFile_IndependentCopy verifies that CopyFile creates a real,
// independent copy: it exists at a new knowledge-owned path AND survives
// deletion of the source object (the C1/C2 regression this PR fixes).
func TestLocalCopyFile_IndependentCopy(t *testing.T) {
	base := t.TempDir()
	svc := NewLocalFileService(base, "")

	content := []byte("hello deep copy")
	srcPath := seedLocalObject(t, base, 0, "doc.txt", content)

	newPath, err := svc.CopyFile(context.Background(), srcPath, 42, "dst-knowledge")
	require.NoError(t, err)

	// New path must be knowledge-owned: local://42/dst-knowledge/<unique>.txt
	require.True(t, strings.HasPrefix(newPath, localScheme+"42/dst-knowledge/"),
		"unexpected dst path: %s", newPath)
	assert.Equal(t, ".txt", filepath.Ext(newPath))

	// Copy is readable and byte-identical.
	assert.Equal(t, content, readLocal(t, svc, newPath))

	// Delete the source — the copy must remain intact.
	require.NoError(t, svc.DeleteFile(context.Background(), srcPath))
	assert.Equal(t, content, readLocal(t, svc, newPath),
		"copy should survive deletion of source")
}

// TestLocalCopyFile_CrossBackend verifies that handing a non-local provider
// scheme to the local service is rejected with ErrCrossBackendCopy.
func TestLocalCopyFile_CrossBackend(t *testing.T) {
	base := t.TempDir()
	svc := NewLocalFileService(base, "")

	_, err := svc.CopyFile(context.Background(), "s3://bucket/10/exports/a.png", 7, "k")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCrossBackendCopy),
		"expected ErrCrossBackendCopy, got %v", err)
}

// TestLocalCopyFile_TraversalRejected verifies that the same path guard used by
// GetFile/DeleteFile rejects a traversal source path.
func TestLocalCopyFile_TraversalRejected(t *testing.T) {
	base := t.TempDir()
	svc := NewLocalFileService(base, "")

	_, err := svc.CopyFile(context.Background(), localScheme+"../../etc/passwd", 7, "k")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrCrossBackendCopy),
		"traversal should be a path error, not cross-backend")
}
