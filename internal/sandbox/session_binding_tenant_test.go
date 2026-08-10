package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func newTenantKeyTestManager(t *testing.T) (*SessionBoundManager, *fakeRemoteClient) {
	t.Helper()

	client := newFakeRemoteClient(SandboxTypeCube)
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
	return mgr, client
}

// A shared agent executes under the agent owner's tenant so its models, KBs and
// named sandbox config resolve in the sharing workspace. Session deletion,
// however, runs from a plain DELETE whose only tenant is the session's own.
//
// If the binding were keyed by the borrowed tenant, that teardown would look up
// a key nobody wrote, silently leave the MicroVM running, and — because session
// sandboxes are created with onTimeout=pause — leak a paused instance that keeps
// billing with nobody holding its ID.
func TestSessionBindingSurvivesBorrowedTenantAcrossDestroy(t *testing.T) {
	const sessionOwner, agentOwner = uint64(7), uint64(99)

	mgr, client := newTenantKeyTestManager(t)

	// Execution: borrowed tenant in TenantID, session owner recorded alongside.
	execCtx := context.WithValue(context.Background(), types.TenantIDContextKey, agentOwner)
	execCtx = types.WithSandboxTenantID(execCtx, sessionOwner)
	_, err := mgr.Execute(execCtx, &ExecuteConfig{
		SessionID:      "session-shared",
		SkipValidation: true,
		ScriptContent:  "print('ok')\n",
		Script:         "hello.py",
	})
	require.NoError(t, err)

	client.mu.Lock()
	created := client.createCount
	client.mu.Unlock()
	require.Equal(t, 1, created, "execution should have provisioned one sandbox")

	// Teardown: only the session's own tenant is known here, and no sandbox
	// tenant key is set — exactly what sessionService.destroyBoundSandbox does.
	destroyCtx := context.WithValue(context.Background(), types.TenantIDContextKey, sessionOwner)
	require.NoError(t, mgr.DestroySession(destroyCtx, "session-shared"))

	client.mu.Lock()
	deleted := append([]string(nil), client.deleteIDs...)
	client.mu.Unlock()
	require.Len(t, deleted, 1, "teardown must reach the sandbox execution created")
}
