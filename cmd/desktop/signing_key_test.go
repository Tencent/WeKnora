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
	t.Setenv("JWT_SECRET", "")
	t.Setenv("SYSTEM_AES_KEY", "weknora-system-aes-key-32bytes!!")
	require.NoError(t, ensureDesktopSigningKeyInDir(dir))
	first := string(utils.SystemHMACKey())
	firstJWT := os.Getenv("JWT_SECRET")
	require.GreaterOrEqual(t, len(first), 32)
	require.Equal(t, first, firstJWT)
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("JWT_SECRET", "")
	require.NoError(t, ensureDesktopSigningKeyInDir(dir))
	require.Equal(t, first, string(utils.SystemHMACKey()))
	require.Equal(t, firstJWT, os.Getenv("JWT_SECRET"))
	require.Equal(t, "weknora-system-aes-key-32bytes!!", os.Getenv("SYSTEM_AES_KEY"))
	info, err := os.Stat(filepath.Join(dir, "signing.key"))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Zero(t, info.Mode().Perm()&0o077)
	}
}

func TestDesktopJWTSecretIsNotTheAESKey(t *testing.T) {
	dir := t.TempDir()
	const aes = "0123456789abcdef0123456789abcdef"
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("JWT_SECRET", "weknora-jwt-secret")
	t.Setenv("SYSTEM_AES_KEY", aes)

	require.NoError(t, ensureDesktopSigningKeyInDir(dir))
	jwt := os.Getenv("JWT_SECRET")
	require.GreaterOrEqual(t, len(jwt), 32)
	require.NotEqual(t, aes, jwt)
	require.Equal(t, aes, string(utils.SystemHMACKey()))
	require.Equal(t, aes, os.Getenv("SYSTEM_AES_KEY"))

	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("JWT_SECRET", "CHANGE-ME-jwt-secret")
	require.NoError(t, ensureDesktopSigningKeyInDir(dir))
	require.Equal(t, jwt, os.Getenv("JWT_SECRET"))
	require.Equal(t, aes, string(utils.SystemHMACKey()))
	require.Equal(t, aes, os.Getenv("SYSTEM_AES_KEY"))
	_, err := os.Stat(filepath.Join(dir, "signing.key"))
	require.True(t, os.IsNotExist(err), "the AES key must not be copied into signing.key")
}

func TestDesktopSigningKeyKeepsAnExplicitKey(t *testing.T) {
	dir := t.TempDir()
	stale := []byte("stale-file-key-0123456789abcdefgh")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "signing.key"), stale, 0o600))
	const explicit = "explicit-24-byte-signkey"
	t.Setenv("SYSTEM_SIGNING_KEY", explicit)
	t.Setenv("SYSTEM_AES_KEY", "")
	t.Setenv("JWT_SECRET", "")

	require.NoError(t, ensureDesktopSigningKeyInDir(dir))
	require.Equal(t, explicit, string(utils.SystemHMACKey()))
	require.NotEqual(t, explicit, os.Getenv("JWT_SECRET"))
}
