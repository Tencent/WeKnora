package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/localsandbox/adapter"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// Deleting a chat session must never delete the user's files. The host adapter
// therefore must not implement SessionDestroyer: destroyBoundSandbox resolves
// the host manager for unpinned sessions and would call it.
func TestHostAdapterIsNotASessionDestroyer(t *testing.T) {
	var a any = adapter.New(nil)
	_, isDestroyer := a.(interface {
		DestroySession(context.Context, string) error
	})
	require.False(t, isDestroyer,
		"a host workspace is the user's own directory; deleting a session must not delete it")
}

// End-to-end: delete a session whose workspace has files, assert the files and
// the directory are still there afterwards.
//
// The host manager here deliberately implements DestroySession and would wipe
// the directory if asked. destroyBoundSandbox must refuse that by host type,
// not by hoping the method is absent.
func TestDeleteSessionLeavesHostWorkspaceIntact(t *testing.T) {
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "notes.md")
	require.NoError(t, os.WriteFile(marker, []byte("keep me"), 0o644))

	host := &destroyingSandboxManager{
		typ:       sandbox.SandboxTypeHost,
		workspace: workspace,
	}
	svc, db := newSessionServiceForHostDeleteTest(t, host, nil)
	ctx := testSessionScopeContext(1, "u1")
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: "host-s1", TenantID: 1, UserID: "u1", Title: "chat",
	}).Error)

	require.NoError(t, svc.DeleteSession(ctx, "host-s1"))

	got, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, []byte("keep me"), got)
	info, err := os.Stat(workspace)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Empty(t, host.destroyed,
		"deleting a chat must not call DestroySession on a host workspace")
}

func TestDeleteSessionStillDestroysRemoteSandbox(t *testing.T) {
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "scratch.txt")
	require.NoError(t, os.WriteFile(marker, []byte("remote"), 0o644))

	remote := &destroyingSandboxManager{
		typ:       sandbox.SandboxTypeCube,
		workspace: workspace,
	}
	svc, db := newSessionServiceForHostDeleteTest(t, stubHostManager{}, stubSandboxResolver{mgr: remote})
	ctx := testSessionScopeContext(1, "u1")
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: "remote-s1", TenantID: 1, UserID: "u1", Title: "chat",
	}).Error)
	pinned, err := svc.sandboxPinner.Pin(ctx, "remote-s1", SandboxPin{ConfigID: "cfg-1"})
	require.NoError(t, err)
	require.Equal(t, "cfg-1", pinned.ConfigID)

	require.NoError(t, svc.DeleteSession(ctx, "remote-s1"))

	require.Equal(t, []string{"remote-s1"}, remote.destroyed)
	_, err = os.Stat(marker)
	require.Error(t, err)
}

func newSessionServiceForHostDeleteTest(
	t *testing.T,
	host sandbox.Manager,
	resolver sandbox.TenantSandboxResolver,
) (*sessionService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Session{}))
	return &sessionService{
		sessionRepo:        repository.NewSessionRepository(db),
		messageRepo:        deleteForkMessageRepo{},
		webSearchStateRepo: deleteForkWebSearchState{},
		sandboxPinner:      NewSessionSandboxPinner(db),
		sandboxResolver:    resolver,
		hostSandbox:        host,
		forkSnapshots:      &fakeForkSessionSnapshotDeleter{},
	}, db
}

// destroyingSandboxManager is a trap: DestroySession deletes workspace. A host
// manager must never be asked to do this; a remote manager still must.
type destroyingSandboxManager struct {
	typ       sandbox.SandboxType
	workspace string
	destroyed []string
}

func (m *destroyingSandboxManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return nil, nil
}
func (m *destroyingSandboxManager) Cleanup(context.Context) error { return nil }
func (m *destroyingSandboxManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *destroyingSandboxManager) GetType() sandbox.SandboxType  { return m.typ }
func (m *destroyingSandboxManager) DestroySession(_ context.Context, sessionID string) error {
	m.destroyed = append(m.destroyed, sessionID)
	return os.RemoveAll(m.workspace)
}
