package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type liveFilesManagerStub struct {
	pinTestManager
	listSession string
	listPath    string
}

func (m *liveFilesManagerStub) SessionLiveFileManager() sandbox.SessionLiveFileManager { return m }
func (m *liveFilesManagerStub) ListSessionLiveFiles(
	_ context.Context, sessionID, relativeDir string,
) ([]sandbox.SessionLiveFileEntry, error) {
	m.listSession, m.listPath = sessionID, relativeDir
	return []sandbox.SessionLiveFileEntry{{Name: "result.txt", Path: "result.txt"}}, nil
}

func (*liveFilesManagerStub) ReadSessionLiveFile(context.Context, string, string) ([]byte, error) {
	return nil, nil
}

func (*liveFilesManagerStub) WriteSessionLiveFile(context.Context, string, string, []byte) error {
	return nil
}

func (*liveFilesManagerStub) RenameSessionLiveFile(context.Context, string, string, string) error {
	return nil
}

func (*liveFilesManagerStub) RemoveSessionLiveFile(context.Context, string, string) error {
	return nil
}

func TestSandboxLiveFilesServiceIsLookupOnlyWhenSessionIsUnpinned(t *testing.T) {
	svc := NewSandboxLiveFilesService(NewSessionSandboxPinner(newPinTestDB(t)), nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, err := svc.List(ctx, "s-1", "")
	require.ErrorIs(t, err, sandbox.ErrNoLiveSessionSandbox)
}

func TestSandboxLiveFilesServiceFollowsPinnedConfig(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	_, err := pinner.Pin(context.Background(), "s-1", "cfg-docker")
	require.NoError(t, err)

	manager := &liveFilesManagerStub{pinTestManager: pinTestManager{typ: sandbox.SandboxTypeDocker}}
	svc := NewSandboxLiveFilesService(
		pinner,
		stubSandboxResolver{mgr: manager},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	entries, err := svc.List(ctx, "s-1", "reports")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "s-1", manager.listSession)
	require.Equal(t, "reports", manager.listPath)
}

func TestSandboxLiveFilesServiceRejectsManagerWithoutCapability(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	_, err := pinner.Pin(context.Background(), "s-1", "cfg-cube")
	require.NoError(t, err)
	svc := NewSandboxLiveFilesService(
		pinner,
		stubSandboxResolver{mgr: &pinTestManager{typ: sandbox.SandboxTypeCube}},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, err = svc.List(ctx, "s-1", "")
	require.ErrorIs(t, err, ErrLiveFilesUnsupported)
}
