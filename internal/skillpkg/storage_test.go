package skillpkg

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestFileStorageMaterializesImmutableVersionAndDetectsTampering(t *testing.T) {
	root := t.TempDir()
	storage := NewFileStorage(root, NewValidator(DefaultLimits()))
	archive := validSkillZip(t)
	pkg, err := storage.Stage(context.Background(), 10000, "upload-1", bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err)

	relativePath, contentHash, err := storage.Materialize(
		context.Background(), 10000, "skill-1", "version-id-1", pkg,
	)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("10000", "skill-1", "version-id-1"), relativePath)
	require.Len(t, contentHash, 64)

	version := &types.TenantSkillVersion{
		TenantID: 10000, SkillID: "skill-1", ID: "version-id-1",
		StoragePath: relativePath, ContentHash: contentHash,
	}
	require.NoError(t, storage.VerifyVersion(context.Background(), 10000, version))

	script := filepath.Join(root, relativePath, "scripts", "run.py")
	require.NoError(t, os.WriteFile(script, []byte("print('tampered')\n"), 0o600))
	require.ErrorContains(t, storage.VerifyVersion(context.Background(), 10000, version), "content_hash_mismatch")
}

func TestFileStorageRejectsTenantPathMismatch(t *testing.T) {
	root := t.TempDir()
	storage := NewFileStorage(root, NewValidator(DefaultLimits()))
	version := &types.TenantSkillVersion{
		TenantID: 20000, SkillID: "skill-2", ID: "version-id-2",
		StoragePath: filepath.Join("10000", "skill-2", "version-id-2"),
	}
	require.ErrorContains(t, storage.VerifyVersion(context.Background(), 20000, version), "storage_path_mismatch")
}

func TestFileStorageReconcileRemovesExpiredStagingOnly(t *testing.T) {
	root := t.TempDir()
	storage := NewFileStorage(root, NewValidator(DefaultLimits()))
	oldDir := filepath.Join(root, ".staging", "10000", "old-upload")
	newDir := filepath.Join(root, ".staging", "10000", "new-upload")
	require.NoError(t, os.MkdirAll(oldDir, 0o700))
	require.NoError(t, os.MkdirAll(newDir, 0o700))
	oldTime := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(oldDir, oldTime, oldTime))

	require.NoError(t, storage.Reconcile(context.Background(), time.Now().Add(-time.Hour)))
	require.NoDirExists(t, oldDir)
	require.DirExists(t, newDir)
}
