package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/stretchr/testify/require"
)

func TestBuiltinShellUsesPreinstalledVenvWithoutStaging(t *testing.T) {
	manager := skills.NewManager(&skills.ManagerConfig{Enabled: true}, nil).
		WithTenantSource(skills.NewBuiltinSkillSource(builtin.PublishedManifest()))
	require.NoError(t, manager.Initialize(context.Background()))
	executor := &fakeShellExecutor{}
	tool := NewShellExecTool(executor, nil).WithSkillEnvironment(manager)
	result, err := tool.Execute(
		shellExecTestContext(),
		json.RawMessage(`{"command":"python3 /workspace/output/report.py","skill_name":"pdf"}`),
	)
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Equal(t, 1, executor.calls)
	require.Equal(t, builtin.ImageRoot+"/pdf", executor.env["WEKNORA_SKILL_DIR"])
	require.Contains(t, executor.command, builtin.ImageRoot+"/pdf/.venv/bin")
	require.NotContains(t, executor.command, "ln -s")
	require.NotContains(t, executor.command, "pip install")
}
