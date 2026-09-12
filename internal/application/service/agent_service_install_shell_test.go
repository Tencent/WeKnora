package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// stubInstallShellExecutor records the options the privileged executor is
// asked for, so a test can prove the difference between the two shell_exec
// wirings rather than merely that one was registered.
type stubInstallShellExecutor struct {
	calls []sandbox.ShellExecOptions
}

func (s *stubInstallShellExecutor) ExecShellCommandWithOptions(
	_ context.Context, _ string, _ string, opts sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	s.calls = append(s.calls, opts)
	return &sandbox.ExecuteResult{ExitCode: 0}, nil
}

func installShellToolContext() context.Context {
	return tools.WithToolExecContext(context.Background(), &tools.ToolExecContext{SessionID: "sess-1"})
}

func TestInstallerAgentGetsTheRootShellWithTheSkillsRoot(t *testing.T) {
	privileged := &stubInstallShellExecutor{}
	ordinary := &stubShellExecutor{}
	mgr := &capableManager{
		typ:          sandbox.SandboxTypeE2B,
		shell:        ordinary,
		installShell: privileged,
	}
	config := &types.AgentConfig{}
	config.EnableSkillInstallMode(types.BuiltinSkillInstallerID, sandbox.SkillsImageRoot+"/pptx")
	registry := tools.NewToolRegistry()

	(&agentService{}).registerSandboxShellTool(context.Background(), registry, mgr, config)

	result, err := registry.ExecuteTool(installShellToolContext(), tools.ToolShellExec,
		json.RawMessage(`{"command":"pip install -r requirements.txt","work_dir":"`+
			sandbox.SkillsImageRoot+`/sk-1"}`))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.False(t, ordinary.called,
		"the installer must not fall through to the unprivileged executor")
	require.Len(t, privileged.calls, 1)
	require.True(t, privileged.calls[0].AsRoot)
	require.True(t, privileged.calls[0].AllowSkillsRoot)
	require.Equal(t, sandbox.SkillsImageRoot+"/sk-1", privileged.calls[0].WorkDir)
}

func TestOrdinaryAgentKeepsTheUnprivilegedShell(t *testing.T) {
	privileged := &stubInstallShellExecutor{}
	ordinary := &stubShellExecutor{}
	mgr := &capableManager{
		typ:          sandbox.SandboxTypeE2B,
		shell:        ordinary,
		installShell: privileged,
	}
	registry := tools.NewToolRegistry()

	(&agentService{}).registerSandboxShellTool(context.Background(), registry, mgr, &types.AgentConfig{})

	rejected, err := registry.ExecuteTool(installShellToolContext(), tools.ToolShellExec,
		json.RawMessage(`{"command":"pip install x","work_dir":"`+sandbox.SkillsImageRoot+`/sk-1"}`))
	require.NoError(t, err)
	require.False(t, rejected.Success, "an ordinary agent has no business under /opt")

	accepted, err := registry.ExecuteTool(installShellToolContext(), tools.ToolShellExec,
		json.RawMessage(`{"command":"ls"}`))
	require.NoError(t, err)
	require.True(t, accepted.Success)
	require.True(t, ordinary.called)
	require.Empty(t, privileged.calls,
		"nothing an ordinary agent can ask for may reach the root executor")
}

func TestInstallModeIsRefusedToEveryAgentButTheInstaller(t *testing.T) {
	config := &types.AgentConfig{}

	config.EnableSkillInstallMode("some-tenant-agent", sandbox.SkillsImageRoot+"/pptx")

	require.False(t, config.SkillInstallMode())
}

func TestInstallModeSurvivesNoJSONRoundTrip(t *testing.T) {
	// The flag must be unreachable from stored agent records and API payloads:
	// both arrive as JSON.
	var config types.AgentConfig
	require.NoError(t, json.Unmarshal(
		[]byte(`{"skill_install_mode":true,"SkillInstallMode":true}`), &config))

	require.False(t, config.SkillInstallMode())
}

func TestInstallerAgentConfigTurnsOnInstallMode(t *testing.T) {
	config := installerAgentConfig(&types.CustomAgent{
		ID:     types.BuiltinSkillInstallerID,
		Config: types.CustomAgentConfig{AllowedTools: []string{tools.ToolShellExec}},
	}, "cfg-1", sandbox.SkillsImageRoot+"/pptx")

	require.True(t, config.SkillInstallMode())
	require.Equal(t, "none", config.MCPSelectionMode,
		"empty MCPSelectionMode defaults to all tenant MCP tools on a root shell")
	require.NotNil(t, config.MemoryEnabled)
	require.False(t, *config.MemoryEnabled,
		"nil MemoryEnabled inherits the workspace and would register search_memory")
	require.False(t, config.WebSearchEnabled)
}

func TestInstallerAgentConfigKeepsMCPOffWhenThePlatformYAMLEnablesIt(t *testing.T) {
	memoryOn := true
	config := installerAgentConfig(&types.CustomAgent{
		ID: types.BuiltinSkillInstallerID,
		Config: types.CustomAgentConfig{
			AllowedTools:     []string{tools.ToolShellExec},
			MCPSelectionMode: "all",
			WebSearchEnabled: true,
			MemoryEnabled:    &memoryOn,
		},
	}, "cfg-1", sandbox.SkillsImageRoot+"/pptx")

	require.Equal(t, "none", config.MCPSelectionMode)
	require.False(t, config.WebSearchEnabled)
	require.NotNil(t, config.MemoryEnabled)
	require.False(t, *config.MemoryEnabled)
}

func TestInstallerAgentConfigLeavesInstallModeOffForAnotherAgent(t *testing.T) {
	config := installerAgentConfig(&types.CustomAgent{
		ID:     "agent-42",
		Config: types.CustomAgentConfig{AllowedTools: []string{tools.ToolShellExec}},
	}, "cfg-1", sandbox.SkillsImageRoot+"/pptx")

	require.False(t, config.SkillInstallMode())
}
