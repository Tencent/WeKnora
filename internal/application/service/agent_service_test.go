package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAgentKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *fakeAgentKnowledgeBaseService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	if s.kb == nil {
		return nil, errors.New("knowledge base not found")
	}
	return s.kb, nil
}

type fakeAgentKnowledgeService struct {
	interfaces.KnowledgeService
	knowledges []*types.Knowledge
	lastFilter types.KnowledgeListFilter
	lastTenant uint64
}

type fakeAgentChatModel struct {
	lastToolNames []string
}

func (*fakeAgentChatModel) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{}, nil
}

func (m *fakeAgentChatModel) ChatStream(
	_ context.Context, _ []chat.Message, opts *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	m.lastToolNames = nil
	if opts != nil {
		for _, tool := range opts.Tools {
			m.lastToolNames = append(m.lastToolNames, tool.Function.Name)
		}
	}

	ch := make(chan types.StreamResponse, 1)
	ch <- types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "ok",
		Done:         true,
		FinishReason: "stop",
	}
	close(ch)
	return ch, nil
}

func (*fakeAgentChatModel) GetModelName() string { return "fake-chat" }
func (*fakeAgentChatModel) GetModelID() string   { return "fake-chat-id" }

type stubSessionFileStore struct{}

func (stubSessionFileStore) EnsureSessionDir(context.Context, string, string) error { return nil }
func (stubSessionFileStore) ListSessionFiles(context.Context, string, string) ([]sandbox.RemoteDirEntry, error) {
	return nil, nil
}

func (stubSessionFileStore) StatSessionFile(context.Context, string, string) (*sandbox.RemoteStatEntry, error) {
	return nil, nil
}

func (stubSessionFileStore) ReadSessionFile(context.Context, string, string) ([]byte, error) {
	return nil, nil
}

func (stubSessionFileStore) WriteSessionInputFile(context.Context, string, string, []byte) error {
	return nil
}
func (stubSessionFileStore) WriteSessionWorkspaceFile(context.Context, string, string, []byte) error {
	return nil
}
func (stubSessionFileStore) WriteSessionWorkspaceFiles(context.Context, string, []sandbox.SessionWorkspaceFile) error {
	return nil
}
func (stubSessionFileStore) RemoveSessionInputPath(context.Context, string, string) error { return nil }

func (s *fakeAgentKnowledgeService) ListPagedKnowledgeByKnowledgeBaseID(
	ctx context.Context,
	_ string,
	page *types.Pagination,
	filter types.KnowledgeListFilter,
) (*types.PageResult, error) {
	s.lastFilter = filter
	s.lastTenant, _ = types.TenantIDFromContext(ctx)

	filtered := make([]*types.Knowledge, 0, len(s.knowledges))
	for _, knowledge := range s.knowledges {
		if filter.ParseStatus != "" && knowledge.ParseStatus != filter.ParseStatus {
			continue
		}
		filtered = append(filtered, knowledge)
	}
	return types.NewPageResult(int64(len(filtered)), page, filtered), nil
}

func toolRegistered(registry *tools.ToolRegistry, name string) bool {
	_, err := registry.GetTool(name)
	return err == nil
}

func toolOffered(names []string, name string) bool {
	for _, got := range names {
		if got == name {
			return true
		}
	}
	return false
}

// TestCreateAgentEngineOpensSandboxToolsOnlyForInstallMode pins the skill
// gate on the skill installer alone for shell_exec. shell_exec follows
// SkillsEnabled (or install mode). list/read/write/edit sandbox file tools
// follow SessionFileStore for ordinary agents, including when skills are
// disabled, but are withheld from the installer: those tools only accept
// /workspace, and the installer must write the skill tree via shell_exec.
func TestCreateAgentEngineOpensSandboxToolsOnlyForInstallMode(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	t.Run("install-mode config gets shell_exec without skill tools", func(t *testing.T) {
		chatModel := &fakeAgentChatModel{}
		svc := &agentService{
			sandboxResolver: stubSandboxResolver{
				mgr: &capableManager{
					typ:   sandbox.SandboxTypeCube,
					shell: &stubShellExecutor{},
					// The manager advertises a session file store, the way
					// every real remote backend does. The installer still
					// must not receive those tools: they only write /workspace.
					files:        stubSessionFileStore{},
					installShell: &stubInstallShellExecutor{},
				},
			},
		}
		config := &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   false,
			AllowedTools:    []string{tools.ToolShellExec},
		}
		config.EnableSkillInstallMode(types.BuiltinSkillInstallerID, sandbox.SkillsImageRoot+"/pptx")

		engine, err := svc.CreateAgentEngine(ctx, config, chatModel, nil, nil, "sess-1", "msg-1")

		require.NoError(t, err)
		_, err = engine.Execute(ctx, "sess-1", "msg-1", "hello", nil)
		require.NoError(t, err)
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolShellExec))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolReadSkill))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolExecuteSkillScript))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.ToolListSandboxFiles),
			"session file tools only accept /workspace; the installer must write the skill tree via shell_exec")
		require.False(t, toolOffered(chatModel.lastToolNames, tools.ToolReadFile))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.ToolWriteSandboxFile))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.ToolEditSandboxFile))
		require.Nil(t, engine.(*agent.AgentEngine).GetSkillsManager())
	})

	t.Run("an ordinary agent with skills off gets no shell but keeps file tools", func(t *testing.T) {
		chatModel := &fakeAgentChatModel{}
		svc := &agentService{
			sandboxResolver: stubSandboxResolver{
				mgr: &capableManager{
					typ:          sandbox.SandboxTypeCube,
					shell:        &stubShellExecutor{},
					files:        stubSessionFileStore{},
					installShell: &stubInstallShellExecutor{},
				},
			},
		}

		engine, err := svc.CreateAgentEngine(ctx, &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   false,
			AllowedTools:    []string{tools.ToolShellExec, tools.ToolThinking},
		}, chatModel, nil, nil, "sess-1", "msg-1")

		require.NoError(t, err)
		_, err = engine.Execute(ctx, "sess-1", "msg-1", "hello", nil)
		require.NoError(t, err)
		require.False(t, toolOffered(chatModel.lastToolNames, tools.ToolShellExec),
			"shell_exec follows SkillsEnabled; an agent with skills off gets no shell")
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolListSandboxFiles))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolReadFile))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolWriteSandboxFile))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolEditSandboxFile))
		require.Nil(t, engine.(*agent.AgentEngine).GetSkillsManager())
	})

	t.Run("skills disabled without skills or install mode gets no shell or skill tools but keeps file tools", func(t *testing.T) {
		chatModel := &fakeAgentChatModel{}
		svc := &agentService{
			sandboxResolver: stubSandboxResolver{
				mgr: &capableManager{
					typ:   sandbox.SandboxTypeCube,
					shell: &stubShellExecutor{},
					files: stubSessionFileStore{},
				},
			},
		}

		engine, err := svc.CreateAgentEngine(ctx, &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   false,
			AllowedTools:    []string{tools.ToolThinking},
		}, chatModel, nil, nil, "sess-1", "msg-1")

		require.NoError(t, err)
		_, err = engine.Execute(ctx, "sess-1", "msg-1", "hello", nil)
		require.NoError(t, err)
		require.False(t, toolOffered(chatModel.lastToolNames, tools.ToolShellExec))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolListSandboxFiles))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolReadFile))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolWriteSandboxFile))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolEditSandboxFile))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolReadSkill))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolExecuteSkillScript))
		require.Nil(t, engine.(*agent.AgentEngine).GetSkillsManager())
	})

	t.Run("skills enabled with skill dirs keeps existing behavior", func(t *testing.T) {
		chatModel := &fakeAgentChatModel{}
		svc := &agentService{
			sandboxResolver: stubSandboxResolver{
				mgr: &capableManager{
					typ:   sandbox.SandboxTypeCube,
					shell: &stubShellExecutor{},
					files: stubSessionFileStore{},
				},
			},
		}

		engine, err := svc.CreateAgentEngine(ctx, &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   true,
			SkillDirs:       []string{t.TempDir()},
		}, chatModel, nil, nil, "sess-1", "msg-1")

		require.NoError(t, err)
		_, err = engine.Execute(ctx, "sess-1", "msg-1", "hello", nil)
		require.NoError(t, err)
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolShellExec))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.ToolListSandboxFiles))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolReadFile))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolWriteSandboxFile))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolEditSandboxFile))
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolReadFile))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolReadSkill))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolReadSandboxFile))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolExecuteSkillScript))
		require.NotNil(t, engine.(*agent.AgentEngine).GetSkillsManager())
	})

	t.Run("skills enabled with tenant skills and no host dirs still offers skill tools", func(t *testing.T) {
		chatModel := &fakeAgentChatModel{}
		svc := &agentService{
			sandboxResolver: stubSandboxResolver{
				mgr: &capableManager{
					typ:   sandbox.SandboxTypeCube,
					shell: &stubShellExecutor{},
					files: stubSessionFileStore{},
				},
			},
		}

		engine, err := svc.CreateAgentEngine(ctx, &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   true,
			TenantSkills: []*types.TenantSkillEntity{{
				ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-remote",
				Name: "pdf-tools", Description: "PDF helpers",
				Status: types.SkillStatusReady, Enabled: true,
			}},
		}, chatModel, nil, nil, "sess-1", "msg-1")

		require.NoError(t, err)
		_, err = engine.Execute(ctx, "sess-1", "msg-1", "hello", nil)
		require.NoError(t, err)
		require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolReadFile))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolReadSkill))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolReadSandboxFile))
		require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolExecuteSkillScript))
		mgr := engine.(*agent.AgentEngine).GetSkillsManager()
		require.NotNil(t, mgr)
		var names []string
		for _, meta := range mgr.GetAllMetadata() {
			names = append(names, meta.Name)
		}
		require.Equal(t, []string{"pdf-tools"}, names)
	})
}

func TestSkillToolsFollowSkillsEnabled(t *testing.T) {
	t.Run("skills disabled: initializeSkillsManager registers no skill tools or shell", func(t *testing.T) {
		registry := tools.NewToolRegistry()
		svc := &agentService{
			sandboxResolver: stubSandboxResolver{
				mgr: &capableManager{typ: sandbox.SandboxTypeCube, shell: &stubShellExecutor{}},
			},
		}
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

		_, err := svc.initializeSkillsManager(ctx, "sess-1", &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   false,
			AllowedTools:    []string{tools.ToolShellExec},
		}, registry)

		require.NoError(t, err)
		require.False(t, toolRegistered(registry, tools.LegacyToolReadSkill))
		require.False(t, toolRegistered(registry, tools.LegacyToolExecuteSkillScript))
		require.False(t, toolRegistered(registry, tools.ToolShellExec),
			"shell_exec is registered by registerSandboxShellIfAllowed, not initializeSkillsManager")
	})

	t.Run("skills enabled: skill tools without shell", func(t *testing.T) {
		registry := tools.NewToolRegistry()
		svc := &agentService{
			sandboxResolver: stubSandboxResolver{
				mgr: &capableManager{typ: sandbox.SandboxTypeCube, shell: &stubShellExecutor{}},
			},
		}
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

		_, err := svc.initializeSkillsManager(ctx, "sess-1", &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   true,
			SkillDirs:       []string{t.TempDir()},
		}, registry)

		require.NoError(t, err)
		require.True(t, toolRegistered(registry, tools.ToolReadFile))
		require.False(t, toolRegistered(registry, tools.LegacyToolReadSkill))
		require.False(t, toolRegistered(registry, tools.LegacyToolExecuteSkillScript))
		require.False(t, toolRegistered(registry, tools.ToolShellExec),
			"shell_exec is registered separately from the skills manager")
	})
}

// shell_exec can execute skill scripts, so it follows SkillsEnabled rather
// than the presence of a ready skill. An agent whose skills are enabled but
// whose sandbox currently carries no ready skill must still receive the shell,
// otherwise it has no way to inspect a fresh or still-installing sandbox.
func TestCreateAgentEngineShellFollowsSkillsEnabledWithoutInstalledSkills(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	chatModel := &fakeAgentChatModel{}
	svc := &agentService{
		sandboxResolver: stubSandboxResolver{
			mgr: &capableManager{
				typ:          sandbox.SandboxTypeCube,
				shell:        &stubShellExecutor{},
				files:        stubSessionFileStore{},
				installShell: &stubInstallShellExecutor{},
			},
		},
	}

	engine, err := svc.CreateAgentEngine(ctx, &types.AgentConfig{
		SandboxConfigID: "cfg-remote",
		SkillsEnabled:   true,
		// No SkillDirs and no TenantSkills: skills are enabled, but the sandbox
		// image carries none.
	}, chatModel, nil, nil, "sess-1", "msg-1")

	require.NoError(t, err)
	_, err = engine.Execute(ctx, "sess-1", "msg-1", "hello", nil)
	require.NoError(t, err)
	require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolShellExec),
		"an agent with skills enabled must have shell_exec even when no ready skill exists yet")
	require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolReadSkill),
		"an empty sandbox must not be offered skill tools that cannot succeed")
	require.False(t, toolOffered(chatModel.lastToolNames, tools.LegacyToolExecuteSkillScript))
	require.False(t, toolOffered(chatModel.lastToolNames, tools.ToolListSandboxFiles))
	require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolReadFile))
	require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolWriteSandboxFile))
	require.True(t, toolOffered(chatModel.lastToolNames, tools.ToolEditSandboxFile))
	require.Nil(t, engine.(*agent.AgentEngine).GetSkillsManager())
}

// Whether an installed skill is invocable is decided when the AgentConfig is
// built (effectiveTenantSkills). This is the other half of that contract: the
// manager the model is described from must carry exactly the rows it was
// handed, and nothing when it was handed none.
func TestSkillsManagerOffersTheInjectedInstalledSkills(t *testing.T) {
	newSvc := func() *agentService {
		return &agentService{
			sandboxResolver: stubSandboxResolver{
				mgr: &capableManager{typ: sandbox.SandboxTypeCube, shell: &stubShellExecutor{}},
			},
		}
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	skillNamesOf := func(mgr *skills.Manager) []string {
		var names []string
		for _, meta := range mgr.GetAllMetadata() {
			names = append(names, meta.Name)
		}
		return names
	}

	t.Run("injected rows are offered without a host skill directory", func(t *testing.T) {
		mgr, err := newSvc().initializeSkillsManager(ctx, "sess-1", &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   true,
			TenantSkills: []*types.TenantSkillEntity{{
				ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-remote",
				Name: "pdf-tools", Description: "PDF helpers",
				Status: types.SkillStatusReady, Enabled: true,
			}},
		}, tools.NewToolRegistry())

		require.NoError(t, err)
		require.Equal(t, []string{"pdf-tools"}, skillNamesOf(mgr))
	})

	t.Run("host skills are not offered beside the sandbox image", func(t *testing.T) {
		dir := t.TempDir()
		skillDir := filepath.Join(dir, "document-analyzer")
		require.NoError(t, os.MkdirAll(skillDir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(skillDir, "SKILL.md"),
			[]byte("---\nname: document-analyzer\ndescription: old host copy\n---\n"),
			0o644,
		))

		mgr, err := newSvc().initializeSkillsManager(ctx, "sess-1", &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   true,
			SkillDirs:       []string{dir},
			TenantSkills: []*types.TenantSkillEntity{{
				ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-remote",
				Name: "pdf-tools", Description: "PDF helpers",
				Status: types.SkillStatusReady, Enabled: true,
			}},
		}, tools.NewToolRegistry())

		require.NoError(t, err)
		require.Equal(t, []string{"pdf-tools"}, skillNamesOf(mgr),
			"a host skill directory is not what the sandbox image carries")
	})

	t.Run("injected rows are offered", func(t *testing.T) {
		mgr, err := newSvc().initializeSkillsManager(ctx, "sess-1", &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   true,
			SkillDirs:       []string{t.TempDir()},
			TenantSkills: []*types.TenantSkillEntity{{
				ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-remote",
				Name: "pdf-tools", Description: "PDF helpers",
				Status: types.SkillStatusReady, Enabled: true,
			}},
		}, tools.NewToolRegistry())

		require.NoError(t, err)
		require.Equal(t, []string{"pdf-tools"}, skillNamesOf(mgr))
	})

	t.Run("an unusable image injects no rows and offers no skills", func(t *testing.T) {
		mgr, err := newSvc().initializeSkillsManager(ctx, "sess-1", &types.AgentConfig{
			SandboxConfigID: "cfg-remote",
			SkillsEnabled:   true,
			SkillDirs:       []string{t.TempDir()},
		}, tools.NewToolRegistry())

		require.NoError(t, err)
		require.Empty(t, skillNamesOf(mgr),
			"a skill the model is told about but cannot invoke burns turns for nothing")
	})
}

func TestSkillInstallerIsHiddenFromThePicker(t *testing.T) {
	require.NoError(t, types.LoadBuiltinAgentsConfig(filepath.Join("..", "..", "..", "config")))

	require.True(t, types.IsBuiltinAgentID(types.BuiltinSkillInstallerID),
		"the server must still be able to resolve it by ID")
	require.NotContains(t, types.GetBuiltinAgentIDs(), types.BuiltinSkillInstallerID,
		"it must not clutter the tenant's agent picker")
}

func TestGetKnowledgeBaseInfos_SharedKnowledgeBaseUsesSourceTenant(t *testing.T) {
	const (
		receiverTenantID = uint64(7)
		sourceTenantID   = uint64(42)
	)
	now := time.Now()
	knowledgeService := &fakeAgentKnowledgeService{
		knowledges: []*types.Knowledge{
			{
				ID:              "shared-doc",
				KnowledgeBaseID: "shared-kb",
				Title:           "shared document",
				ParseStatus:     types.ParseStatusCompleted,
				CreatedAt:       now,
			},
		},
	}
	service := &agentService{
		knowledgeBaseService: &fakeAgentKnowledgeBaseService{
			kb: &types.KnowledgeBase{
				ID:       "shared-kb",
				Name:     "Shared KB",
				Type:     types.KnowledgeBaseTypeDocument,
				TenantID: sourceTenantID,
			},
		},
		knowledgeService: knowledgeService,
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, receiverTenantID)

	infos, err := service.getKnowledgeBaseInfos(ctx, []string{"shared-kb"}, map[string]uint64{"shared-kb": sourceTenantID})

	require.NoError(t, err)
	require.Len(t, infos, 1)
	assert.Equal(t, 1, infos[0].DocCount)
	require.Len(t, infos[0].RecentDocs, 1)
	assert.Equal(t, "shared-doc", infos[0].RecentDocs[0].KnowledgeID)
	assert.Equal(t, sourceTenantID, knowledgeService.lastTenant)
}

func TestGetKnowledgeBaseInfos_ExcludesUnprocessedDocuments(t *testing.T) {
	now := time.Now()
	knowledgeService := &fakeAgentKnowledgeService{
		knowledges: []*types.Knowledge{
			{
				ID:              "doc-processing",
				KnowledgeBaseID: "kb-1",
				Title:           "still parsing",
				FileName:        "processing.pdf",
				FileType:        "pdf",
				ParseStatus:     types.ParseStatusProcessing,
				CreatedAt:       now,
			},
			{
				ID:              "doc-completed",
				KnowledgeBaseID: "kb-1",
				Title:           "ready document",
				FileName:        "ready.pdf",
				FileType:        "pdf",
				ParseStatus:     types.ParseStatusCompleted,
				CreatedAt:       now.Add(-time.Minute),
			},
		},
	}
	service := &agentService{
		knowledgeBaseService: &fakeAgentKnowledgeBaseService{
			kb: &types.KnowledgeBase{
				ID:       "kb-1",
				Name:     "KB",
				Type:     types.KnowledgeBaseTypeDocument,
				TenantID: 1,
			},
		},
		knowledgeService: knowledgeService,
	}

	infos, err := service.getKnowledgeBaseInfos(context.Background(), []string{"kb-1"}, nil)

	require.NoError(t, err)
	require.Len(t, infos, 1)
	assert.Equal(t, types.ParseStatusCompleted, knowledgeService.lastFilter.ParseStatus)
	assert.Equal(t, 1, infos[0].DocCount)
	require.Len(t, infos[0].RecentDocs, 1)
	assert.Equal(t, "doc-completed", infos[0].RecentDocs[0].KnowledgeID)
}

func TestValidateConfigMaxIterationsUnlimited(t *testing.T) {
	s := &agentService{}

	unlimited := &types.AgentConfig{MaxIterations: -1}
	require.NoError(t, s.ValidateConfig(unlimited))
	assert.Equal(t, types.UnlimitedMaxIterations, unlimited.MaxIterations)

	normalized := &types.AgentConfig{MaxIterations: -9}
	require.NoError(t, s.ValidateConfig(normalized))
	assert.Equal(t, types.UnlimitedMaxIterations, normalized.MaxIterations)

	unset := &types.AgentConfig{}
	require.NoError(t, s.ValidateConfig(unset))
	assert.Equal(t, 5, unset.MaxIterations)

	tooHigh := &types.AgentConfig{MaxIterations: MAX_ITERATIONS + 1}
	require.Error(t, s.ValidateConfig(tooHigh))
}
