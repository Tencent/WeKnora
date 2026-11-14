package workspace

import (
	"path"
	"testing"

	"github.com/stretchr/testify/require"
)

func userLayout(t *testing.T) Layout {
	t.Helper()
	l, err := UserSession(testSessionID)
	require.NoError(t, err)
	return l
}

func TestCleanWriteFileInRemoteMatchesHistoricalErrors(t *testing.T) {
	l := Remote()
	got, err := CleanWriteFileIn(l, "notes/a.md")
	require.NoError(t, err)
	require.Equal(t, "/workspace/notes/a.md", got)

	got, err = CleanWriteFileIn(l, "/opt/elsewhere.txt")
	require.NoError(t, err, "a session-owned machine still lets the manager write anywhere; tools enforce WriteRoots")
	require.Equal(t, "/opt/elsewhere.txt", got)

	_, err = CleanWriteFileIn(l, "/workspace/output")
	require.EqualError(t, err, `sandbox: workspace write path "/workspace/output" is a directory, not a file`)
	_, err = CleanWriteFileIn(l, "/workspace/input/a.pdf")
	require.EqualError(t, err, "sandbox: session input /workspace/input is read-only")
	_, err = CleanWriteFileIn(l, "/")
	require.EqualError(t, err, `sandbox: workspace write path "/" must be an absolute file path`)
}

func TestCleanWriteFileInSharedAllowsTheWholeMachine(t *testing.T) {
	l := userLayout(t)
	got, err := CleanWriteFileIn(l, "report.md")
	require.NoError(t, err)
	require.Equal(t, l.Root+"/report.md", got)

	got, err = CleanWriteFileIn(l, "/workspace/sessions/11111111-2222-4333-8444-555555555555/a.md")
	require.NoError(t, err, "a sibling session's directory is not refused any more")
	require.Equal(t, "/workspace/sessions/11111111-2222-4333-8444-555555555555/a.md", got)
	_, err = CleanWriteFileIn(l, l.InputDir+"/x")
	require.ErrorContains(t, err, "read-only")
}

func TestSharedWritesProtectEverySessionInput(t *testing.T) {
	l := userLayout(t)
	sibling, err := UserSession("7c9e6679-7425-40de-944b-e07fc1f90ae7")
	require.NoError(t, err)
	for _, p := range []string{
		l.InputDir + "/original.txt",
		sibling.InputDir,
		sibling.InputDir + "/original.txt",
		sibling.InputDir + "/nested/original.txt",
		sibling.Root + "/output/../input/original.txt",
		"../" + path.Base(sibling.Root) + "/input/original.txt",
		InputDir + "/original.txt",
	} {
		_, err := CleanWriteFileIn(l, p)
		require.ErrorContains(t, err, "read-only", p)
	}
	for _, p := range []string{
		sibling.Root + "/notes.txt",
		sibling.OutputDir + "/report.txt",
		sibling.Root + "/input-copy/original.txt",
		sibling.Root + "/project/input/data.txt",
	} {
		got, err := CleanWriteFileIn(l, p)
		require.NoError(t, err, p)
		require.Equal(t, p, got)
	}
	// A host project may legitimately contain a sessions/name/input tree.
	host := Layout{Origin: OriginHost, Root: Root}
	require.Empty(t, host.ReadOnlyInputRoot(sibling.InputDir+"/data.txt"))
	require.Empty(t, Remote().ReadOnlyInputRoot(sibling.InputDir+"/data.txt"))
}

func TestCleanWorkDirIn(t *testing.T) {
	remote := Remote()
	got, err := CleanWorkDirIn(remote, "/opt/build", false)
	require.NoError(t, err)
	require.Equal(t, "/opt/build", got)
	_, err = CleanWorkDirIn(remote, "/opt/build", true)
	require.EqualError(t, err,
		`sandbox: work dir "/opt/build" is outside allowed roots (/workspace, /opt/weknora/tenant/skills)`)
	_, err = CleanWorkDirIn(remote, "rel", false)
	require.EqualError(t, err, `sandbox: work dir "rel" must be absolute`)

	shared := userLayout(t)
	for _, ok := range []string{shared.Root, "/workspace", "/workspace/sessions", "/tmp", "/opt", "/"} {
		_, err := CleanWorkDirIn(shared, ok, false)
		require.NoError(t, err, ok)
	}
	_, err = CleanWorkDirIn(shared, "/opt/build", true)
	require.EqualError(t, err,
		`sandbox: work dir "/opt/build" is outside allowed roots (`+shared.Root+`, /opt/weknora/tenant/skills)`)
}

func TestCleanInputPathIn(t *testing.T) {
	shared := userLayout(t)
	got, err := CleanInputPathIn(shared, shared.InputDir+"/a.pdf")
	require.NoError(t, err)
	require.Equal(t, shared.InputDir+"/a.pdf", got)
	_, err = CleanInputPathIn(shared, "/workspace/input/a.pdf")
	require.Error(t, err)
}

func TestValidOutputDirAndRunnableScript(t *testing.T) {
	remote := Remote()
	got, ok := ValidOutputDirIn(remote, "/workspace/out2/")
	require.True(t, ok)
	require.Equal(t, "/workspace/out2", got)
	_, ok = ValidOutputDirIn(remote, "/workspace")
	require.False(t, ok)

	shared := userLayout(t)
	_, ok = ValidOutputDirIn(shared, "/workspace/output")
	require.False(t, ok, "another session's tree is not a delivery directory")
	_, ok = RunnableScriptIn(shared, shared.Root+"/run.py")
	require.True(t, ok)
	_, ok = RunnableScriptIn(shared, shared.InputDir+"/run.py")
	require.False(t, ok)
	_, ok = RunnableScriptIn(shared, "/workspace/run.py")
	require.False(t, ok)
}
