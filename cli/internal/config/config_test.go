package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

func TestLoad_FileMissing(t *testing.T) {
	testutil.XDGTempDir(t)
	c, err := Load()
	require.NoError(t, err, "missing file must not error (commands like --help must work)")
	assert.NotNil(t, c)
	assert.Empty(t, c.Contexts)
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	testutil.XDGTempDir(t)
	in := &Config{
		CurrentContext: "prod",
		Contexts: map[string]Context{
			"prod": {Host: "https://kb.example.com", TenantID: 7, APIKeyRef: "keychain://weknora/prod/access"},
		},
	}
	require.NoError(t, Save(in))

	out, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "prod", out.CurrentContext)
	assert.Equal(t, "https://kb.example.com", out.Contexts["prod"].Host)
	assert.Equal(t, uint64(7), out.Contexts["prod"].TenantID)

	p, err := Path()
	require.NoError(t, err)
	st, err := os.Stat(p)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		// Windows file ACLs don't map to Unix mode bits; os.Chmod is
		// effectively a no-op for owner/group/other distinctions, so the
		// 0600 invariant only meaningfully holds on Unix-like platforms.
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	}
}

func TestLoad_Corrupt(t *testing.T) {
	dir := testutil.XDGTempDir(t)
	p := filepath.Join(dir, "weknora", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, []byte("not: valid: yaml: ::"), 0o600))

	_, err := Load()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCorrupt))
}

func TestPath_HonorsXDG(t *testing.T) {
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	p, err := Path()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(xdg, "weknora", "config.yaml"), p)
}

func TestPath_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
	p, err := Path()
	require.NoError(t, err)
	// Compare via slash-normalized form so the assertion is platform-agnostic
	// (Windows uses backslash; semantics here is "the relative tail").
	assert.Contains(t, filepath.ToSlash(p), "/.config/weknora/config.yaml")
}

func TestSave_AtomicReplace(t *testing.T) {
	dir := testutil.XDGTempDir(t)
	require.NoError(t, Save(&Config{CurrentContext: "a"}))
	require.NoError(t, Save(&Config{CurrentContext: "b"}))
	out, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "b", out.CurrentContext)
	matches, _ := filepath.Glob(filepath.Join(dir, "weknora", "*.tmp"))
	assert.Empty(t, matches)
}
