package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// A workspace can point an agent at a named Cube/E2B config while the
// deployment default stays docker/local. In that combination the agent still
// registers shell/skill tools against the remote sandbox (initializeSkillsManager
// resolves the same per-config manager), so gating attachment staging on the
// process-wide manager would leave those tools running against an empty
// /workspace/input.
func TestSessionSandboxInputStoreUsesNamedConfigNotDeploymentDefault(t *testing.T) {
	t.Setenv("WEKNORA_SANDBOX_MODE", "local")

	// The deployment default advertises no session filesystem, standing in for
	// docker/local/disabled.
	deploymentDefault := &stagingSandboxManager{
		sandboxType:  sandbox.SandboxTypeLocal,
		disableFiles: true,
	}
	// The workspace's named config does advertise one.
	named := &stagingSandboxManager{sandboxType: sandbox.SandboxTypeCube}

	svc := &agentService{
		sandboxMgr:      deploymentDefault,
		sandboxResolver: stubSandboxResolver{mgr: named},
		sandboxPinner:   NewSessionSandboxPinner(newPinTestDB(t)),
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	store, err := svc.sessionSandboxInputStore(ctx, "s-1", "cfg-remote")
	require.NoError(t, err)
	require.NotNil(t, store, "named remote config must still stage attachments")
}

// The mirror case: no named config means the deployment default decides, and a
// backend without a session filesystem must not stage anything.
func TestSessionSandboxInputStoreSkipsWhenDeploymentDefaultHasNoFilesystem(t *testing.T) {
	t.Setenv("WEKNORA_SANDBOX_MODE", "local")

	deploymentDefault := &stagingSandboxManager{
		sandboxType:  sandbox.SandboxTypeLocal,
		disableFiles: true,
	}
	svc := &agentService{
		sandboxMgr:      deploymentDefault,
		sandboxResolver: stubSandboxResolver{},
		sandboxPinner:   NewSessionSandboxPinner(newPinTestDB(t)),
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	store, err := svc.sessionSandboxInputStore(ctx, "s-1", "")
	require.NoError(t, err)
	require.Nil(t, store)
}
