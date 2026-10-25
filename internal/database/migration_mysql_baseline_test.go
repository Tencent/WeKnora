package database

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMySQLBaselineVersioned(t *testing.T) {
	root := sqliteRepoRoot(t)
	_, err := os.Stat(filepath.Join(root, "migrations/mysql/000000_init.up.sql"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "migrations/mysql/000000_init.down.sql"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "migrations/mysql/00-init-db.sql"))
	require.True(t, os.IsNotExist(err))
}

func TestMySQLBaselineCoverageMatchesPostgresMigrations(t *testing.T) {
	root := sqliteRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "migrations/mysql/coverage.json"))
	require.NoError(t, err)
	var manifest struct {
		PostgresMigrations []struct {
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
			Effect string `json:"effect"`
		} `json:"postgres_migrations"`
	}
	require.NoError(t, json.Unmarshal(data, &manifest))
	files, err := filepath.Glob(filepath.Join(root, "migrations/versioned/*.up.sql"))
	require.NoError(t, err)
	require.Equal(t, len(files), len(manifest.PostgresMigrations), "new PostgreSQL migrations require an explicit MySQL coverage update")
	seen := map[string]bool{}
	for _, item := range manifest.PostgresMigrations {
		require.False(t, seen[item.File], "duplicate coverage entry")
		seen[item.File] = true
		require.Equal(t, filepath.Base(item.File), item.File)
		require.NotEmpty(t, item.Effect)
		data, err := os.ReadFile(filepath.Join(root, "migrations/versioned", item.File))
		require.NoError(t, err)
		// Git stores these text migrations with LF; Windows may check them
		// out with CRLF. Hash the canonical content on both platforms.
		sum := sha256.Sum256(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
		require.Equal(t, item.SHA256, hex.EncodeToString(sum[:]), "changed PostgreSQL migration requires MySQL review: %s", item.File)
	}
	for _, path := range files {
		require.True(t, seen[filepath.Base(path)], "missing coverage: %s", path)
	}
}
