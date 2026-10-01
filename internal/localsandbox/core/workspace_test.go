package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func osMkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }

type fakeProjects struct{ dir string }

func (f fakeProjects) ProjectDirForSession(context.Context, string) (string, bool, error) {
	if f.dir == "" {
		return "", false, nil
	}
	return f.dir, true, nil
}

type revokedProjects struct{}

func (revokedProjects) ProjectDirForSession(context.Context, string) (string, bool, error) {
	return "", false, ErrProjectDirRevoked
}

func workspaceFixture(t *testing.T, projectDir string) WorkspaceResolver {
	t.Helper()
	base := t.TempDir()
	return NewWorkspaceResolver(DirLayout{
		SessionRoot: filepath.Join(base, "Documents", "WeKnora"),
	}, fakeProjects{dir: projectDir})
}

func TestResolveProjectWorkspaceUsesSelectedDirectory(t *testing.T) {
	project := filepath.Join(t.TempDir(), "My Project")
	require.NoError(t, osMkdirAll(project))
	ws, err := workspaceFixture(t, project).Resolve(context.Background(), "sess-1")
	require.NoError(t, err)

	// macOS t.TempDir is /var/folders/...; Resolve EvalSymlinks it to /private/var.
	resolved, err := filepath.EvalSymlinks(project)
	require.NoError(t, err)
	require.Equal(t, WorkspaceProject, ws.Kind)
	require.Equal(t, resolved, ws.Root)
	require.True(t, ws.ProtectGit)
	require.NoDirExists(t, filepath.Join(resolved, "input"))
	require.NoDirExists(t, filepath.Join(resolved, "output"))
}

// The user's directory is the workspace. Creating input/ or output/ next to
// their files, or under app data, would be leftover remote-sandbox shape.
func TestResolveProjectWorkspaceDoesNotCreateInputOrOutput(t *testing.T) {
	project := filepath.Join(t.TempDir(), "My Project")
	require.NoError(t, osMkdirAll(project))
	ws, err := workspaceFixture(t, project).Resolve(context.Background(), "sess-1")
	require.NoError(t, err)

	entries, err := os.ReadDir(ws.Root)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// Sessions opened on the same project share one directory, so their files
// accumulate the way a real project does.
func TestResolveProjectWorkspaceIsSharedAcrossSessions(t *testing.T) {
	project := filepath.Join(t.TempDir(), "My Project")
	require.NoError(t, osMkdirAll(project))
	r := workspaceFixture(t, project)

	a, err := r.Resolve(context.Background(), "sess-1")
	require.NoError(t, err)
	b, err := r.Resolve(context.Background(), "sess-2")
	require.NoError(t, err)
	require.Equal(t, a.Root, b.Root)
}

func TestResolveSessionWorkspaceCreatesDatedDirectory(t *testing.T) {
	ws, err := workspaceFixture(t, "").Resolve(context.Background(), "session-abcdef123456")
	require.NoError(t, err)

	require.Equal(t, WorkspaceSession, ws.Kind)
	require.DirExists(t, ws.Root)
	require.Contains(t, ws.Root, "session-")
	require.False(t, ws.ProtectGit)
	require.NoDirExists(t, filepath.Join(ws.Root, "input"))
	require.NoDirExists(t, filepath.Join(ws.Root, "output"))
}

// Directory names we create must never contain spaces; they cross shell,
// sbpl and Windows command-line escaping every single call.
func TestResolveSessionWorkspaceHasNoSpacesInCreatedNames(t *testing.T) {
	base := t.TempDir()
	r := NewWorkspaceResolver(DirLayout{
		SessionRoot: filepath.Join(base, "Documents", "WeKnora"),
	}, fakeProjects{})

	ws, err := r.Resolve(context.Background(), "session-abcdef123456")
	require.NoError(t, err)

	created := strings.TrimPrefix(ws.Root, filepath.Join(base, "Documents"))
	require.NotContains(t, created, " ")
}

func TestResolveSessionWorkspaceIsPerSession(t *testing.T) {
	r := workspaceFixture(t, "")
	a, err := r.Resolve(context.Background(), "sess-1")
	require.NoError(t, err)
	b, err := r.Resolve(context.Background(), "sess-2")
	require.NoError(t, err)
	require.NotEqual(t, a.Root, b.Root)
}

func TestResolveRejectsEmptySessionID(t *testing.T) {
	_, err := workspaceFixture(t, "").Resolve(context.Background(), "")
	require.Error(t, err)
}

func TestResolveFailsClosedWhenBoundProjectIsRevoked(t *testing.T) {
	base := t.TempDir()
	sessionRoot := filepath.Join(base, "Documents", "WeKnora")
	r := NewWorkspaceResolver(DirLayout{SessionRoot: sessionRoot}, revokedProjects{})

	_, err := r.Resolve(context.Background(), "sess-revoked")
	require.ErrorIs(t, err, ErrProjectDirRevoked)

	if _, statErr := os.Stat(sessionRoot); !os.IsNotExist(statErr) {
		entries, readErr := os.ReadDir(sessionRoot)
		require.NoError(t, readErr)
		require.Empty(t, entries, "revoking a project must not allocate a replacement workspace")
	}
}

func TestResolveSessionWorkspaceKeepsPathAfterMidnight(t *testing.T) {
	base := t.TempDir()
	r := NewWorkspaceResolver(DirLayout{
		SessionRoot: filepath.Join(base, "Documents", "WeKnora"),
	}, fakeProjects{}).(*workspaceResolver)

	day1 := time.Date(2026, 9, 20, 23, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return day1 }
	first, err := r.Resolve(context.Background(), "sess-keep")
	require.NoError(t, err)

	r.now = func() time.Time { return day1.Add(2 * time.Hour) }
	second, err := r.Resolve(context.Background(), "sess-keep")
	require.NoError(t, err)
	require.Equal(t, first.Root, second.Root)
}

func TestResolveSessionWorkspaceUsesFullSessionID(t *testing.T) {
	r := workspaceFixture(t, "")
	a, err := r.Resolve(context.Background(), "session-abcdef123456")
	require.NoError(t, err)
	b, err := r.Resolve(context.Background(), "session-abcdef999999")
	require.NoError(t, err)
	require.NotEqual(t, a.Root, b.Root)
	require.Contains(t, a.Root, "session-abcdef123456")
	require.Contains(t, b.Root, "session-abcdef999999")
}

func TestSanitizeSegmentDistinguishesCollidingIDs(t *testing.T) {
	require.NotEqual(t, sanitizeSegment("foo/bar"), sanitizeSegment("foo-bar"))
	require.NotEqual(t, sanitizeSegment("!!!"), sanitizeSegment("@@@"))
	require.Equal(t, sanitizeSegment("sess-keep"), sanitizeSegment("sess-keep"))
}

func TestResolveSessionWorkspaceIsOwnerPrivate(t *testing.T) {
	ws, err := workspaceFixture(t, "").Resolve(context.Background(), "sess-mode")
	require.NoError(t, err)
	info, err := os.Stat(ws.Root)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}
