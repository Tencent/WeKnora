package skills

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestShellEnvironmentSelectsOnlyAllowedInstalledSkills(t *testing.T) {
	mgr := NewManager(&ManagerConfig{Enabled: true, AllowedSkills: []string{"pdf"}}, &recordingSandboxManager{})
	mgr.WithTenantSource(NewTenantSkillSource([]*types.TenantSkillEntity{
		{Name: "pdf", Status: types.SkillStatusReady, Enabled: true},
		{Name: "other", Status: types.SkillStatusReady, Enabled: true},
	}, nil))
	require.NoError(t, mgr.Initialize(context.Background()))
	env := map[string]string{"TOKEN": "caller", "PYTHONPATH": "/workspace/custom"}
	command := `python3 -c 'print("a; b")'`
	wrapped, actual, err := mgr.PrepareShellEnvironment(context.Background(), "session", "pdf", command, env)
	require.NoError(t, err)
	require.Contains(t, wrapped, sandbox.ShellQuote(command))
	require.Contains(t, wrapped, "/pdf/.venv/bin")
	require.Contains(t, wrapped, "--noprofile --norc")
	require.Equal(t, "caller", actual["TOKEN"])
	require.Equal(t, "/workspace/.skill-packages/pdf:/workspace/custom", actual["PYTHONPATH"])
	require.Contains(t, actual["NODE_PATH"], "/pdf/node_modules")
	require.Equal(t, "/workspace/custom", env["PYTHONPATH"], "caller environment must not be mutated")
	require.Len(t, env, 2)
	for _, name := range []string{"other", "missing", "../pdf"} {
		_, _, err := mgr.PrepareShellEnvironment(context.Background(), "session", name, command, nil)
		require.Error(t, err)
	}
}
