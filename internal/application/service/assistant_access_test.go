package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestAssistantEngineReadOnlyKeepsLiteReadsAndWithholdsWrites(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	for _, assistant := range []bool{true, false} {
		name := "ordinary agent"
		if assistant {
			name = "assistant"
		}
		t.Run(name, func(t *testing.T) {
			model := &fakeAgentChatModel{}
			svc := &agentService{hostSandbox: &capableManager{
				typ: sandbox.SandboxTypeHost, shell: &stubShellExecutor{}, files: stubSessionFileStore{},
			}}
			cfg := &types.AgentConfig{AllowedTools: []string{}, AssistantAccess: types.AssistantAccessReadOnly}
			if assistant {
				cfg.OperationsAgentID = types.BuiltinWeKnoraAssistantID
			}
			engine, err := svc.CreateAgentEngine(ctx, cfg, model, nil, nil, "s1", "m1")
			require.NoError(t, err)
			_, err = engine.Execute(ctx, "s1", "m1", "hello", nil)
			require.NoError(t, err)
			require.Contains(t, model.lastToolNames, tools.ToolReadFile)
			for _, name := range []string{tools.ToolShellExec, tools.ToolWriteSandboxFile, tools.ToolEditSandboxFile} {
				if assistant {
					require.NotContains(t, model.lastToolNames, name)
				} else {
					require.Contains(t, model.lastToolNames, name, "ordinary agents keep their sandbox behavior")
				}
			}
			if assistant {
				require.Contains(t, model.lastToolNames, tools.ToolListSandboxFiles)
			}
		})
	}
}

type assistantFileStore struct {
	stubSessionFileStore
	writes int
}

func (s *assistantFileStore) WriteSessionWorkspaceFile(context.Context, string, string, []byte) error {
	s.writes++
	return nil
}

func TestAssistantLiteDoesNotExecuteWithoutRequiredApproval(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = tools.WithToolExecContext(ctx, &tools.ToolExecContext{SessionID: "s1"})
	for _, level := range []types.AssistantAccessLevel{
		types.AssistantAccessReadOnly, types.AssistantAccessConfirm,
		types.AssistantAccessAutoWrite, types.AssistantAccessFullAuto,
	} {
		t.Run(string(level), func(t *testing.T) {
			shell, files := &stubShellExecutor{}, &assistantFileStore{}
			svc := &agentService{hostSandbox: &capableManager{typ: sandbox.SandboxTypeHost, shell: shell, files: files}}
			registry := tools.NewToolRegistry()
			registry.SetAssistantAccess(level, nil)
			cfg := &types.AgentConfig{OperationsAgentID: types.BuiltinWeKnoraAssistantID, AssistantAccess: level}
			svc.registerSandboxShellIfAllowed(ctx, registry, "s1", cfg)
			svc.registerSandboxFileTools(ctx, registry, "s1", cfg)
			registry.BindSession("s1")
			res, _ := registry.ExecuteTool(ctx, tools.ToolShellExec,
				json.RawMessage(`{"command":"echo changed > /workspace/output.txt"}`))
			require.Equal(t, level == types.AssistantAccessFullAuto, res.Success)
			require.Equal(t, level == types.AssistantAccessFullAuto, shell.called)
			res, err := registry.ExecuteTool(ctx, tools.ToolWriteSandboxFile,
				json.RawMessage(`{"path":"/workspace/output.txt","content":"changed"}`))
			if level == types.AssistantAccessFullAuto {
				require.NoError(t, err)
				require.True(t, res.Success, res.Error)
				require.Equal(t, 1, files.writes)
			} else {
				require.False(t, res.Success)
				require.Zero(t, files.writes)
			}
		})
	}
}

type assistantMCPService struct {
	interfaces.MCPServiceService
	lists int
}

func (s *assistantMCPService) ListMCPServices(context.Context, uint64) ([]*types.MCPService, error) {
	s.lists++
	return nil, nil
}

func (s *assistantMCPService) ListMCPServicesByIDs(context.Context, uint64, []string) ([]*types.MCPService, error) {
	s.lists++
	return nil, nil
}

func TestAssistantMCPRequiresExplicitScopeAndWriteAccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		assistant bool
		mode      string
		level     types.AssistantAccessLevel
		lists     int
	}{
		{"legacy empty assistant scope", true, "", types.AssistantAccessConfirm, 0},
		{"unknown assistant scope", true, "unknown", types.AssistantAccessConfirm, 0},
		{"explicit none", true, "none", types.AssistantAccessConfirm, 0},
		{"read-only all", true, "all", types.AssistantAccessReadOnly, 0},
		{"read-only selected", true, "selected", types.AssistantAccessReadOnly, 0},
		{"explicit selected", true, "selected", types.AssistantAccessConfirm, 1},
		{"explicit all", true, "all", types.AssistantAccessAutoWrite, 1},
		{"ordinary legacy scope", false, "", types.AssistantAccessReadOnly, 1},
		{"full auto still needs explicit scope", true, "", types.AssistantAccessFullAuto, 0},
		{"full auto none", true, "none", types.AssistantAccessFullAuto, 0},
		{"full auto selected", true, "selected", types.AssistantAccessFullAuto, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := &assistantMCPService{}
			svc := &agentService{mcpServiceService: services, mcpManager: mcp.NewMCPManager(nil)}
			t.Cleanup(svc.mcpManager.Shutdown)
			cfg := &types.AgentConfig{
				MCPSelectionMode: tc.mode, MCPServices: []string{"service"}, AssistantAccess: tc.level,
			}
			if tc.assistant {
				cfg.OperationsAgentID = types.BuiltinWeKnoraAssistantID
			}
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			svc.registerMCPTools(ctx, tools.NewToolRegistry(), cfg)
			require.Equal(t, tc.lists, services.lists)
		})
	}
}

func TestAssistantReadOnlyWebToolsFollowWebSwitch(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
		svc := &agentService{}
		model := &fakeAgentChatModel{}
		cfg := &types.AgentConfig{
			OperationsAgentID: types.BuiltinWeKnoraAssistantID,
			AssistantAccess:   types.AssistantAccessReadOnly, WebSearchEnabled: enabled, AllowedTools: []string{},
		}
		engine, err := svc.CreateAgentEngine(ctx, cfg, model, nil, nil, "s1", "m1")
		require.NoError(t, err)
		_, err = engine.Execute(ctx, "s1", "m1", "hello", nil)
		require.NoError(t, err)
		for _, name := range []string{tools.ToolWebSearch, tools.ToolWebFetch} {
			if enabled {
				require.Contains(t, model.lastToolNames, name)
			} else {
				require.NotContains(t, model.lastToolNames, name)
			}
		}
	}
}
