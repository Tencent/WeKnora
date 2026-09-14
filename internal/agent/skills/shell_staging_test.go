package skills

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

type stagingStore struct {
	sandbox.SessionFileStore
	files   map[string][]byte
	err     error
	writes  int
	batches int
}

func (s *stagingStore) StatSessionFile(_ context.Context, sessionID, file string) (*sandbox.RemoteStatEntry, error) {
	data, ok := s.files[sessionID+":"+file]
	if !ok {
		return nil, nil
	}
	return &sandbox.RemoteStatEntry{Type: sandbox.RemoteEntryFile, Size: int64(len(data))}, nil
}

func (s *stagingStore) ReadSessionFile(_ context.Context, sessionID, file string) ([]byte, error) {
	data, ok := s.files[sessionID+":"+file]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), data...), nil
}

func (s *stagingStore) WriteSessionWorkspaceFile(_ context.Context, sessionID, file string, data []byte) error {
	s.writes++
	if s.err != nil {
		return s.err
	}
	s.files[sessionID+":"+file] = append([]byte(nil), data...)
	return nil
}

func (s *stagingStore) WriteSessionWorkspaceFiles(ctx context.Context, sessionID string, files []sandbox.SessionWorkspaceFile) error {
	s.batches++
	for _, file := range files {
		if err := s.WriteSessionWorkspaceFile(ctx, sessionID, file.Path, file.Content); err != nil {
			return err
		}
	}
	return nil
}

type stagingManager struct {
	recordingSandboxManager
	store *stagingStore
}

func (m *stagingManager) SessionFileStore() sandbox.SessionFileStore         { return m.store }
func (m *stagingManager) SessionShellExecutor() sandbox.SessionShellExecutor { return nil }

func TestShellStagesHostResourcesOncePerSession(t *testing.T) {
	root := hostSkillDir(t, "host-skill", "host resource staging")
	require.NoError(t, os.WriteFile(filepath.Join(root, "host-skill", "asset.bin"), []byte{0, 255, 1}, 0644))
	store := &stagingStore{files: make(map[string][]byte)}
	backend := &stagingManager{store: store}
	mgr := NewManager(&ManagerConfig{Enabled: true, SkillDirs: []string{root}, AllowedSkills: []string{"host-skill"}}, backend)
	require.NoError(t, mgr.Initialize(context.Background()))
	command := `python3 "$WEKNORA_SKILL_DIR/scripts/run.py"`
	_, env, err := mgr.PrepareShellEnvironment(context.Background(), "session-1", "host-skill", command, nil)
	require.NoError(t, err)
	dir := env[skillDirEnvVar]
	require.True(t, strings.HasPrefix(dir, "/workspace/.skills/host-skill/"), dir)
	require.Equal(t, []byte{0, 255, 1}, store.files["session-1:"+path.Join(dir, "asset.bin")])
	require.NotEmpty(t, store.files["session-1:"+path.Join(dir, "scripts/run.py")])
	require.Equal(t, sandbox.SessionOutputRoot, env[artifactOutputEnvVar])
	require.Zero(t, backend.calls, "preparation must never invoke the old script executor")
	require.Equal(t, 1, store.batches)
	writes := store.writes
	_, _, err = mgr.PrepareShellEnvironment(context.Background(), "session-1", "host-skill", command, nil)
	require.NoError(t, err)
	require.Equal(t, writes, store.writes)
	_, _, err = mgr.PrepareShellEnvironment(context.Background(), "session-2", "host-skill", command, nil)
	require.NoError(t, err)
	require.Equal(t, 2*writes, store.writes, "another session needs its own files")
	_, _, err = mgr.PrepareShellEnvironment(context.Background(), "session-1", "other", command, nil)
	require.Error(t, err)
	require.Equal(t, 2*writes, store.writes)
	delete(store.files, "session-1:"+path.Join(dir, "SKILL.md"))
	_, env, err = mgr.PrepareShellEnvironment(context.Background(), "session-1", "host-skill", command, nil)
	require.NoError(t, err)
	require.Equal(t, 3*writes, store.writes, "a removed staged directory must be prepared again")
	store.files["session-1:"+path.Join(env[skillDirEnvVar], "SKILL.md")] = []byte("mutated")
	_, _, err = mgr.PrepareShellEnvironment(context.Background(), "session-1", "host-skill", command, nil)
	require.NoError(t, err)
	require.Equal(t, 4*writes, store.writes, "a mutated staged manifest must be prepared again")
}

func TestShellStagingFailureDoesNotPublishReadyEnvironment(t *testing.T) {
	root := hostSkillDir(t, "host-skill", "staging failure")
	store := &stagingStore{files: make(map[string][]byte), err: errors.New("permission denied")}
	mgr := NewManager(&ManagerConfig{Enabled: true, SkillDirs: []string{root}}, &stagingManager{store: store})
	require.NoError(t, mgr.Initialize(context.Background()))
	_, env, err := mgr.PrepareShellEnvironment(context.Background(), "session", "host-skill", "true", nil)
	require.ErrorContains(t, err, "command was not started")
	require.Nil(t, env)
	require.Empty(t, mgr.stagedSkills)
	store.err = nil
	_, env, err = mgr.PrepareShellEnvironment(context.Background(), "session", "host-skill", "true", nil)
	require.NoError(t, err)
	require.NotEmpty(t, env[skillDirEnvVar])
}
