package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"

	"github.com/Tencent/WeKnora/internal/agent"
	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type preinstalledManager struct {
	*capableManager
	lookups int
}

func (m *preinstalledManager) BuiltinSkillsForRun(context.Context, string) (*types.BuiltinSkillsManifest, error) {
	m.lookups++
	manifest := builtin.PublishedManifest()
	for _, entry := range builtin.List() {
		if entry.Distribution == "builtin" && entry.Category != "office" {
			manifest.Skills = append(manifest.Skills, types.BuiltinSkillDeclaration{
				Name: entry.Name, Digest: entry.Digest, Verified: true,
			})
		}
	}
	return manifest, nil
}

func TestAgentOffersBuiltinSkillsWithoutAnyInstallationRecords(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	mgr := &preinstalledManager{
		capableManager: &capableManager{typ: sandbox.SandboxTypeCube, shell: &stubShellExecutor{}},
	}
	svc := &agentService{sandboxResolver: stubSandboxResolver{mgr: mgr}}
	engine, err := svc.CreateAgentEngine(
		ctx,
		&types.AgentConfig{
			SandboxConfigID: "cfg-office",
			SkillsEnabled:   true,
			AllowedSkills:   []string{"pdf", "exploratory-data-analysis"},
		},
		&fakeAgentChatModel{},
		nil,
		nil,
		"sess-1",
		"msg-1",
	)
	require.NoError(t, err)
	skills := engine.(*agent.AgentEngine).GetSkillsManager()
	require.NotNil(t, skills)
	require.Len(t, skills.GetAllMetadata(), 2)
	dir, ok := skills.SandboxSkillDir("pdf")
	require.True(t, ok)
	require.Equal(t, builtin.ImageRoot+"/pdf", dir)
	require.Equal(t, 1, mgr.lookups)
	_, err = svc.CreateAgentEngine(
		ctx,
		&types.AgentConfig{SandboxConfigID: "cfg-office", SkillsEnabled: false},
		&fakeAgentChatModel{},
		nil,
		nil,
		"sess-1",
		"msg-1",
	)
	require.NoError(t, err)
	require.Equal(t, 1, mgr.lookups, "disabled agent skills must not query or expose builtins")
}

func TestPreinstalledMentionsUseRuntimeMetadataAndAgentSelection(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	for _, tc := range []struct {
		name    string
		enabled bool
		allowed []string
		count   int
	}{
		{"all", true, nil, 7}, {"selected", true, []string{"pdf"}, 1}, {"none", false, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &preinstalledManager{
				capableManager: &capableManager{typ: sandbox.SandboxTypeCube, shell: &stubShellExecutor{}},
			}
			svc := &agentService{sandboxResolver: stubSandboxResolver{mgr: mgr}}
			cfg := &types.AgentConfig{
				SandboxConfigID:  "office",
				SkillsEnabled:    tc.enabled,
				AllowedSkills:    tc.allowed,
				PinnedSkillNames: []string{"pdf", "exploratory-data-analysis", "absent"},
			}
			engine, err := svc.CreateAgentEngine(ctx, cfg, &fakeAgentChatModel{}, nil, nil, "session", "message")
			require.NoError(t, err)
			runtime := engine.(*agent.AgentEngine).GetSkillsManager()
			if tc.count == 0 {
				require.Nil(t, runtime)
			} else {
				require.Len(t, runtime.GetAllMetadata(), tc.count)
			}
			mentions := svc.resolvePinnedSkillInfos(cfg, runtime)
			expected := 0
			switch tc.name {
			case "all":
				expected = 2
			case "selected":
				expected = 1
			}
			require.Len(t, mentions, expected)
			for _, mention := range mentions {
				require.NotEmpty(t, mention.Description)
				require.NotEqual(t, "absent", mention.Name)
			}
		})
	}
}

type sessionSkillResolver struct {
	mgr      sandbox.Manager
	configID string
}

func (r *sessionSkillResolver) Resolve(_ context.Context, _ uint64, configID string) (sandbox.Manager, error) {
	r.configID = configID
	return r.mgr, nil
}

func TestSessionPickerResolvesPinnedSandboxWithoutExecuting(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	_, err := pinner.Pin(ctx, "s-1", "cfg-office")
	require.NoError(t, err)
	mgr := &preinstalledManager{capableManager: &capableManager{typ: sandbox.SandboxTypeCube}}
	resolver := &sessionSkillResolver{mgr: mgr}
	svc := &sessionService{sandboxPinner: pinner, sandboxResolver: resolver}
	rows, manifest, err := svc.SessionSkillResources(ctx, 7, "s-1", "cfg-empty")
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Equal(t, "cfg-office", resolver.configID)
	require.Len(t, manifest.Skills, 7)
	require.Equal(t, 1, mgr.lookups)
}

type unownedSkillSession struct{ interfaces.SessionService }

func (*unownedSkillSession) GetOwnedSession(context.Context, string) (*types.Session, error) {
	return nil, fmt.Errorf("not owned")
}

func (*unownedSkillSession) SessionSkillResources(
	context.Context,
	uint64,
	string,
	string,
) ([]*types.TenantSkillEntity, *types.BuiltinSkillsManifest, error) {
	panic("must check session ownership before reading metadata")
}

func TestSessionSkillResourcesRequireOwnership(t *testing.T) {
	svc := &TenantSkillService{sessions: &unownedSkillSession{}}
	rows, manifest, err := svc.ListSessionSkillResources(context.Background(), 7, "foreign", "office")
	require.Error(t, err)
	require.Nil(t, rows)
	require.Nil(t, manifest)
}
