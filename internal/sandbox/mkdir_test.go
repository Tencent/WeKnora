package sandbox

import (
	"errors"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMakeDirTreeCreatesEachParentAndTreatsExistsAsSuccess(t *testing.T) {
	var created []string
	existing := map[string]bool{"/workspace": true}
	err := makeDirTree("/workspace/.skills/host-probe/abc123/scripts", func(dir string) error {
		if existing[dir] {
			return NewRemoteError(SandboxTypeCube, "MakeDir", RemoteErrorKindInternal,
				"failed to make dir "+dir+": directory already exists: "+dir, nil)
		}
		if dir != "/workspace" && !existing[path.Dir(dir)] {
			return errors.New("parent missing: " + dir)
		}
		existing[dir] = true
		created = append(created, dir)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"/workspace/.skills",
		"/workspace/.skills/host-probe",
		"/workspace/.skills/host-probe/abc123",
		"/workspace/.skills/host-probe/abc123/scripts",
	}, created)
}

func TestMakeDirTreeStopsWhenALevelFails(t *testing.T) {
	var created []string
	err := makeDirTree("/workspace/.skills/name", func(dir string) error {
		created = append(created, dir)
		if dir == "/workspace/.skills" {
			return errors.New("permission denied")
		}
		return nil
	})
	require.EqualError(t, err, "permission denied")
	require.Equal(t, []string{"/workspace", "/workspace/.skills"}, created)
}
