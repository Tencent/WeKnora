//go:build !bindings

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config", "config.yaml"), nil, 0o644))
}

func TestDesktopConfigRootPrefersThePackagedResources(t *testing.T) {
	base := t.TempDir()
	contents := filepath.Join(base, "WeKnora Lite.app", "Contents")
	writeConfig(t, filepath.Join(contents, "Resources"))
	writeConfig(t, base)

	root, ok := desktopConfigRoot(filepath.Join(contents, "MacOS", "WeKnora Lite"))
	require.True(t, ok)
	require.Equal(t, filepath.Join(contents, "Resources"), root)
}

func TestDesktopConfigRootFindsTheRepoFromADevBundle(t *testing.T) {
	repo := t.TempDir()
	writeConfig(t, repo)
	exe := filepath.Join(repo, "cmd", "desktop", "build", "bin",
		"WeKnora Lite.app", "Contents", "MacOS", "WeKnora Lite")

	root, ok := desktopConfigRoot(exe)
	require.True(t, ok)
	require.Equal(t, repo, root)
}

func TestDesktopConfigRootIgnoresTheWorkingDirectory(t *testing.T) {
	untrusted := t.TempDir()
	writeConfig(t, untrusted)
	t.Chdir(untrusted)

	_, ok := desktopConfigRoot(filepath.Join(t.TempDir(), "bin", "WeKnora Lite"))
	require.False(t, ok)
}

func TestLoadDesktopEnvReadsTheRootNotTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	untrusted := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env.lite"), []byte("WK_A=lite\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("WK_A=env\nWK_B=env\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(untrusted, ".env.lite"), []byte("WK_C=planted\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(untrusted, ".env"), []byte("WK_C=planted\n"), 0o644))
	t.Chdir(untrusted)
	for _, name := range []string{"WK_A", "WK_B", "WK_C", "ENV_FILE"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}

	require.NoError(t, loadDesktopEnv(root, true))
	require.Equal(t, "lite", os.Getenv("WK_A"), ".env.lite wins on shared keys")
	require.Equal(t, "env", os.Getenv("WK_B"), ".env still fills in the rest")
	require.Empty(t, os.Getenv("WK_C"))
}

func TestLoadDesktopEnvWithoutARootReadsNoFiles(t *testing.T) {
	untrusted := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(untrusted, ".env"), []byte("WK_C=planted\n"), 0o644))
	t.Chdir(untrusted)
	t.Setenv("WK_C", "")
	require.NoError(t, os.Unsetenv("WK_C"))
	t.Setenv("ENV_FILE", "")

	require.NoError(t, loadDesktopEnv("", false))
	require.Empty(t, os.Getenv("WK_C"))
}

func TestLoadDesktopEnvHonoursAnExplicitFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "custom.env")
	require.NoError(t, os.WriteFile(file, []byte("WK_D=custom\n"), 0o644))
	t.Setenv("ENV_FILE", file)
	t.Setenv("WK_D", "")
	require.NoError(t, os.Unsetenv("WK_D"))

	require.NoError(t, loadDesktopEnv("", false))
	require.Equal(t, "custom", os.Getenv("WK_D"))

	t.Setenv("ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
	require.Error(t, loadDesktopEnv("", false), "a file asked for by name must exist")
}
