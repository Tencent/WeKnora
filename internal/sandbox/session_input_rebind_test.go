package sandbox

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWriteSessionInputFileRebindsAfterTheContainerVanishes(t *testing.T) {
	client := newFakeRemoteClient(SandboxTypeCube)
	client.vanishOnFirstMakeDir = true
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           NewMemorySessionSandboxBindingStore(),
		Checker:         &fakeSessionExistenceChecker{exists: true},
		SkipHealthProbe: true,
	})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	err = mgr.WriteSessionInputFile(ctx, "session-a", SessionInputRoot+"/note.txt", []byte("hi"))
	require.NoError(t, err)

	client.mu.Lock()
	defer client.mu.Unlock()
	require.Equal(t, 2, client.createCount)
	require.Len(t, client.writeFiles, 1)
	require.Equal(t, SessionInputRoot+"/note.txt", client.writeFiles[0].path)
	require.Equal(t, []byte("hi"), client.writeFiles[0].content)
}

func TestWriteSessionInputFileKeepsTheBindingWhenThePathIsMissing(t *testing.T) {
	client := newFakeRemoteClient(SandboxTypeCube)
	client.missingPathMakeDir = true
	cfg := DefaultConfig()
	cfg.CubeTemplate = "tpl-test"
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           NewMemorySessionSandboxBindingStore(),
		Checker:         &fakeSessionExistenceChecker{exists: true},
		SkipHealthProbe: true,
	})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	err = mgr.WriteSessionInputFile(ctx, "session-a", SessionInputRoot+"/note.txt", []byte("hi"))
	require.Error(t, err)

	client.mu.Lock()
	defer client.mu.Unlock()
	require.Equal(t, 1, client.createCount)
	require.Empty(t, client.writeFiles)
}
