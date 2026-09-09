//go:build docker_integration

package sandbox

import (
	"context"
	"fmt"
	"testing"
	"time"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDockerBuiltinSkillsIntegration(t *testing.T) {
	cfg := dockerIntegrationConfig(t)
	mgr := newDockerIntegrationManager(t, cfg)
	ctx, cancel := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
		3*time.Minute,
	)
	defer cancel()
	sessionID := fmt.Sprintf("builtin-metadata-%d", time.Now().UnixNano())
	key := SessionSandboxKey{TenantID: dockerIntegrationTenantID, SessionID: sessionID}
	manifest, err := mgr.BuiltinSkills(ctx, sessionID)
	require.NoError(t, err)
	require.Len(
		t,
		builtin.CompatibleEntries(manifest),
		8,
		"build the office-browser image with its manifest label first",
	)
	binding, err := mgr.bindings.Get(ctx, key)
	require.NoError(t, err)
	require.Nil(t, binding, "metadata discovery must not create a sandbox")
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(
			types.WithSandboxTenantID(context.Background(), dockerIntegrationTenantID),
			time.Minute,
		)
		defer cancel()
		require.NoError(t, mgr.DestroySession(cleanup, sessionID))
	})
	// Execute all real smoke checks from preinstalled roots, without staging or
	// linking even one skill into the tenant tree.
	command := "set -eu; test ! -e /opt/weknora/tenant/skills/pdf; "
	for _, e := range builtin.CompatibleEntries(manifest) {
		root := builtin.ImageRoot + "/" + e.Name
		command += ShellQuote(root+"/.venv/bin/python") + " " + ShellQuote(root+"/scripts/weknora_smoke.py") + "; "
	}
	result, err := mgr.ExecShellCommand(ctx, sessionID, command, "", 2*time.Minute, nil)
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode, result.Stderr)
	bound, err := mgr.BuiltinSkills(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, manifest, bound)
}
