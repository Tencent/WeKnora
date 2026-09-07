package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type chatInstallerStub struct{ configID string }

func (s *chatInstallerStub) InstallSkill(_ context.Context, _ uint64, configID string, _ []byte) (string, error) {
	s.configID = configID
	return "installed", nil
}

func (s *chatInstallerStub) InstallSkillFromSource(_ context.Context, _ uint64, configID, _ string) (string, error) {
	s.configID = configID
	return "installed", nil
}

func (*chatInstallerStub) GetSkill(context.Context, uint64, string, string) (*types.TenantSkillEntity, error) {
	return &types.TenantSkillEntity{Name: "pdf", Status: types.SkillStatusInstalling}, nil
}

func TestChatInstallerUsesPinnedSandbox(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	_, err := pinner.Pin(ctx, "s-1", "original-config")
	require.NoError(t, err)
	installer := &chatInstallerStub{}
	svc := &agentService{
		skillInstaller:  installer,
		sandboxPinner:   pinner,
		sandboxResolver: stubSandboxResolver{mgr: &capableManager{typ: sandbox.SandboxTypeCube}},
	}
	registry := tools.NewToolRegistry()
	m, err := svc.initializeSkillsManager(ctx, "s-1", &types.AgentConfig{
		SkillsEnabled: true, SandboxConfigID: "new-agent-config",
	}, registry)
	require.NoError(t, err)
	require.Len(t, m.GetAllMetadata(), 1)
	require.Equal(t, skills.InstallerSkillName, m.GetAllMetadata()[0].Name)
	result, err := registry.ExecuteTool(ctx, tools.ToolInstallSkill, json.RawMessage(`{"source":"@owner/pdf"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, "original-config", installer.configID)
}

func TestChatInstallerOfferedOnlyWithAuthorizedSandbox(t *testing.T) {
	for _, tc := range []struct {
		name     string
		role     types.TenantRole
		enabled  bool
		configID string
		want     bool
	}{
		{"empty sandbox admin", types.TenantRoleAdmin, true, "cfg", true},
		{"viewer", types.TenantRoleViewer, true, "cfg", false},
		{"disabled skills", types.TenantRoleAdmin, false, "cfg", false},
		{"no sandbox", types.TenantRoleAdmin, true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			ctx = context.WithValue(ctx, types.TenantRoleContextKey, tc.role)
			svc := &agentService{
				skillInstaller:  &chatInstallerStub{},
				sandboxResolver: stubSandboxResolver{mgr: &capableManager{typ: sandbox.SandboxTypeCube}},
			}
			model := &fakeAgentChatModel{}
			engine, err := svc.CreateAgentEngine(ctx, &types.AgentConfig{
				SkillsEnabled: tc.enabled, SandboxConfigID: tc.configID,
			}, model, nil, nil, "session", "message")
			require.NoError(t, err)
			_, err = engine.Execute(ctx, "session", "message", "install a skill", nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, toolOffered(model.lastToolNames, tools.ToolInstallSkill))
			if tc.want {
				m := engine.(*agent.AgentEngine).GetSkillsManager()
				require.NotNil(t, m)
				require.True(t, m.IsBuiltin(skills.InstallerSkillName))
			}
		})
	}
}
