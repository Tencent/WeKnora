package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const testSessionID = "0f8fad5b-d9cb-469f-a165-70867728950e"

func TestRemoteLayoutKeepsTheHistoricalContract(t *testing.T) {
	l := Remote()
	require.Equal(t, OriginRemote, l.Origin)
	require.Equal(t, "/workspace", l.Root)
	require.Equal(t, []string{"/workspace", "/workspace/output"}, l.WriteRoots)
	require.Equal(t, []string{"/workspace/output", "/workspace/input", "/workspace"}, l.ReadRoots)
	require.Equal(t, "/workspace/input", l.InputDir)
	require.Equal(t, "/workspace/output", l.OutputDir)
	require.Equal(t, "/workspace", l.Hint)
	require.Equal(t, "/var/lib/weknora/workspace.git", l.GitDir)
	require.Empty(t, l.WorkDirRoots, "a session-owned machine keeps unrestricted work_dir")
	require.False(t, l.IsSharedRemote())
}

func TestUserSessionLayoutScopesEveryPathToTheSession(t *testing.T) {
	l, err := UserSession(testSessionID)
	require.NoError(t, err)
	root := "/workspace/sessions/" + testSessionID
	require.Equal(t, OriginRemoteShared, l.Origin)
	require.True(t, l.IsSharedRemote())
	require.False(t, l.IsHost())
	require.Equal(t, root, l.Root)
	require.Equal(t, root+"/input", l.InputDir)
	require.Equal(t, root+"/output", l.OutputDir)
	require.Equal(t, []string{root, root + "/output"}, l.WriteRoots)
	require.Equal(t, []string{root + "/output", root + "/input", root, SkillsImageRoot}, l.ReadRoots)
	require.Equal(t, []string{root, TmpRoot, SkillsImageRoot}, l.WorkDirRoots)
	require.Equal(t, "/var/lib/weknora/sessions/"+testSessionID+".git", l.GitDir)
	require.Equal(t, root, l.Hint)
}

func TestUserSessionRejectsNonCanonicalIDs(t *testing.T) {
	for _, id := range []string{
		"", "abc", "../etc", testSessionID + "x",
		"0F8FAD5B-D9CB-469F-A165-70867728950E", // upper case is not what uuid.New().String() emits
		"0f8fad5b_d9cb_469f_a165_70867728950e",
	} {
		_, err := UserSession(id)
		require.Error(t, err, id)
	}
}

func TestNormalizedCleansNewFields(t *testing.T) {
	l := Layout{GitDir: " /var/lib/x.git/ ", WorkDirRoots: []string{"/tmp/", "/tmp", ""}}.Normalized()
	require.Equal(t, "/var/lib/x.git", l.GitDir)
	require.Equal(t, []string{"/tmp"}, l.WorkDirRoots)
}
