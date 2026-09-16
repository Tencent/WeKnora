package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestDesktopSigningKeyPersistsWithoutChangingAES(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "weknora-system-aes-key-32bytes!!")
	require.NoError(t, ensureDesktopSigningKeyInDir(dir))
	first := string(utils.SystemHMACKey())
	require.GreaterOrEqual(t, len(first), 32)
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	require.NoError(t, ensureDesktopSigningKeyInDir(dir))
	require.Equal(t, first, string(utils.SystemHMACKey()))
	require.Equal(t, "weknora-system-aes-key-32bytes!!", os.Getenv("SYSTEM_AES_KEY"))
	info, err := os.Stat(filepath.Join(dir, "signing.key"))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Zero(t, info.Mode().Perm()&0o077)
	}
}
