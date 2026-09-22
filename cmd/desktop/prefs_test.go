package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/stretchr/testify/require"
)

func TestEnsureDesktopLiteEditionOverridesDefaultStandard(t *testing.T) {
	old := handler.Edition
	t.Cleanup(func() { handler.Edition = old })
	handler.Edition = "standard"
	ensureDesktopLiteEdition()
	require.Equal(t, "lite", handler.Edition)
}

func TestApprovalModeDefaultsToAuto(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	require.Equal(t, "auto", LoadApprovalMode())
}

func TestSaveApprovalModeRejectsUnknownValue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	require.Error(t, SaveApprovalMode("yolo"))
}

// Only "auto" is shipped. Accepting "ask" would promise an approval card
// that does not exist yet (the command is just denied); accepting "full" makes
// Build return ErrFullAccessHasNoPolicy, i.e. nothing runs at all.
func TestSaveApprovalModeRejectsModesNotShippedYet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	require.Error(t, SaveApprovalMode("ask"))
	require.Error(t, SaveApprovalMode("full"))
}

func TestSaveApprovalModeRoundTrips(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	require.NoError(t, SaveApprovalMode("auto"))
	require.Equal(t, "auto", LoadApprovalMode())
}

func TestProjectDirsRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dirs := []string{"/Users/dev/My Project", "/Users/dev/other"}
	require.NoError(t, SaveProjectDirs(dirs))
	require.Equal(t, dirs, LoadProjectDirs())
}

func TestSaveProjectDirsRejectsRelativePath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	require.Error(t, SaveProjectDirs([]string{"relative/dir"}))
}

func TestAppendApprovedProjectDirDedupes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	got, err := appendApprovedProjectDir(dir)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(dir), got)
	again, err := appendApprovedProjectDir(dir)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(dir), again)
	require.Equal(t, []string{filepath.Clean(dir)}, LoadProjectDirs())
}

func TestAppendApprovedProjectDirFromFileParent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	got, err := appendApprovedProjectDir(filepath.Dir(file))
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(dir), got)
	require.Equal(t, []string{filepath.Clean(dir)}, LoadProjectDirs())
}

func TestRemoveApprovedProjectDirRemovesOnlyThatPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keep := t.TempDir()
	drop := t.TempDir()
	_, err := appendApprovedProjectDir(keep)
	require.NoError(t, err)
	_, err = appendApprovedProjectDir(drop)
	require.NoError(t, err)

	require.NoError(t, removeApprovedProjectDir(drop+"/"))
	require.Equal(t, []string{filepath.Clean(keep)}, LoadProjectDirs())
}

func TestRemoveApprovedProjectDirIsIdempotent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	require.NoError(t, removeApprovedProjectDir(dir))
	require.Empty(t, LoadProjectDirs())
}

func TestSetProjectDirsIsNotAWailsMethod(t *testing.T) {
	typ := reflect.TypeOf(&App{})
	_, ok := typ.MethodByName("SetProjectDirs")
	require.False(t, ok, "replacing the allowlist from the webview is not an authorization path")
	_, ok = typ.MethodByName("RemoveProjectDir")
	require.True(t, ok)
	_, ok = typ.MethodByName("PickProjectDir")
	require.True(t, ok)
}
