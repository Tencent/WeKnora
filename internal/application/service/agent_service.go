package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/gorm"
)

const MAX_ITERATIONS = 100 // Max iterations for agent execution

// dedupStrings removes duplicate strings while preserving the first occurrence order.
func dedupStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// withoutString returns the slice with every occurrence of drop removed,
// preserving order.
func withoutString(in []string, drop string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// agentHasKnowledgeScope reports whether the agent has any KB retrieval scope for
// this turn. Tag-only @mentions populate SearchTargets (with TagIDs) but leave
// KnowledgeBases / KnowledgeIDs empty — those must still count as in-scope.
func agentHasKnowledgeScope(config *types.AgentConfig) bool {
	if config == nil {
		return false
	}
	return types.HasKnowledgeRetrievalScope(
		config.SearchTargets,
		config.KnowledgeBases,
		config.KnowledgeIDs,
	)
}

// knowledgeBaseScopesForPrompt returns the KB IDs to show in runtime_context
// metadata, together with the tenant each KB should be queried under.
//
// The tenant map always comes from SearchTargets, which buildSearchTargets has
// already resolved (and authorized) per KB: a directly shared KB carries its
// source tenant there. KnowledgeBases alone cannot tell own from shared KBs —
// both KBSelectionMode="all" and an @mention put shared KB IDs into it.
// KBs missing from the map fall back to the caller's tenant.
func knowledgeBaseScopesForPrompt(config *types.AgentConfig) ([]string, map[string]uint64) {
	if config == nil {
		return nil, nil
	}
	kbTenantMap := config.SearchTargets.GetKBTenantMap()
	if len(config.KnowledgeBases) > 0 {
		return config.KnowledgeBases, kbTenantMap
	}
	return config.SearchTargets.GetAllKnowledgeBaseIDs(), kbTenantMap
}

// agentService implements agent-related business logic
type agentService struct {
	cfg                   *config.Config
	modelService          interfaces.ModelService
	mcpServiceService     interfaces.MCPServiceService
	mcpManager            *mcp.MCPManager
	eventBus              *event.EventBus
	db                    *gorm.DB
	webSearchService      interfaces.WebSearchService
	knowledgeBaseService  interfaces.KnowledgeBaseService
	knowledgeService      interfaces.KnowledgeService
	fileService           interfaces.FileService
	chunkService          interfaces.ChunkService
	duckdb                *sql.DB
	webSearchStateService interfaces.WebSearchStateService
	wikiPageService       interfaces.WikiPageService
	tenantService         interfaces.TenantService
	messageService        interfaces.MessageService
	memoryService         interfaces.MemoryService
	storageResolver       interfaces.StorageBackendResolver
	toolApprovalGate      approval.MCPApproval
	sandboxMgr            sandbox.Manager
	sandboxResolver       sandbox.TenantSandboxResolver
	sandboxPinner         *SessionSandboxPinner
	sandboxPolicy         WorkspaceSandboxPolicy
}

// NewAgentService creates a new agent service
func NewAgentService(
	cfg *config.Config,
	modelService interfaces.ModelService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	fileService interfaces.FileService,
	chunkService interfaces.ChunkService,
	mcpServiceService interfaces.MCPServiceService,
	mcpManager *mcp.MCPManager,
	eventBus *event.EventBus,
	db *gorm.DB,
	webSearchService interfaces.WebSearchService,
	duckdb *sql.DB,
	webSearchStateService interfaces.WebSearchStateService,
	wikiPageService interfaces.WikiPageService,
	tenantService interfaces.TenantService,
	messageService interfaces.MessageService,
	memoryService interfaces.MemoryService,
	storageResolver interfaces.StorageBackendResolver,
	toolApprovalGate approval.MCPApproval,
	sandboxMgr sandbox.Manager,
	sandboxResolver sandbox.TenantSandboxResolver,
	sandboxPinner *SessionSandboxPinner,
	sandboxPolicy WorkspaceSandboxPolicy,
) interfaces.AgentService {
	return &agentService{
		cfg:                   cfg,
		modelService:          modelService,
		knowledgeBaseService:  knowledgeBaseService,
		knowledgeService:      knowledgeService,
		fileService:           fileService,
		chunkService:          chunkService,
		mcpServiceService:     mcpServiceService,
		mcpManager:            mcpManager,
		eventBus:              eventBus,
		db:                    db,
		webSearchService:      webSearchService,
		duckdb:                duckdb,
		webSearchStateService: webSearchStateService,
		wikiPageService:       wikiPageService,
		tenantService:         tenantService,
		messageService:        messageService,
		memoryService:         memoryService,
		storageResolver:       storageResolver,
		toolApprovalGate:      toolApprovalGate,
		sandboxMgr:            sandboxMgr,
		sandboxResolver:       sandboxResolver,
		sandboxPinner:         sandboxPinner,
		sandboxPolicy:         sandboxPolicy,
	}
}

// CreateAgentEngine creates an agent engine with the given configuration and EventBus.
// History is loaded once per turn by the caller (see service.LoadAgentHistory)
// and handed to AgentEngine.Execute as llmContext; the engine is stateless across turns.
func (s *agentService) CreateAgentEngine(
	ctx context.Context,
	config *types.AgentConfig,
	chatModel chat.Chat,
	rerankModel rerank.Reranker,
	eventBus *event.EventBus,
	sessionID, assistantMessageID string,
) (interfaces.AgentEngine, error) {
	logger.Infof(ctx, "Creating agent engine with custom EventBus")

	// 1. Validate config
	if err := s.ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid agent config: %w", err)
	}
	if chatModel == nil {
		return nil, fmt.Errorf("chat model is nil after initialization")
	}

	// 2. Build tool registry
	toolRegistry := tools.NewToolRegistry()
	if config.MaxToolOutputChars > 0 {
		toolRegistry.SetMaxToolOutputSize(config.MaxToolOutputChars)
	}
	if err := s.registerTools(ctx, toolRegistry, config, rerankModel, chatModel, sessionID); err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}
	s.registerMCPTools(ctx, toolRegistry, config, eventBus, sessionID, assistantMessageID)

	// 3. Resolve knowledge base and selected document metadata
	kbInfos, selectedDocs := s.resolveKBAndDocInfos(ctx, config)

	// 4. Resolve system prompt template
	systemPromptTemplate := ""
	if config.UseCustomSystemPrompt || config.SystemPrompt != "" {
		systemPromptTemplate = config.ResolveSystemPrompt(config.WebSearchEnabled)
	}

	// 5. Create engine
	engine := agent.NewAgentEngine(
		config, chatModel, toolRegistry, eventBus,
		kbInfos, selectedDocs, sessionID,
		systemPromptTemplate,
	)
	engine.SetAppConfig(s.cfg)
	pinnedMCP := s.resolvePinnedMCPServiceInfos(ctx, config)
	s.attachPinnedMCPToolNames(toolRegistry, pinnedMCP)
	engine.SetPinnedMentions(
		pinnedMCP,
		s.resolvePinnedSkillInfos(config),
	)

	// Set VLM image describer for MCP tool result image analysis.
	// When an MCP tool returns images, the engine uses VLM to generate text descriptions
	// and appends them to the tool result content (since Chat Completions API does not
	// reliably support images in tool role messages across providers).
	if config.VLMModelID != "" {
		if vlmModel, err := s.modelService.GetVLMModel(ctx, config.VLMModelID); err == nil {
			engine.SetImageDescriber(func(ctx context.Context, imgBytes []byte, prompt string) (string, error) {
				return vlmModel.Predict(ctx, [][]byte{imgBytes}, prompt)
			})
			logger.Infof(ctx, "VLM image describer set for MCP tool result analysis (model: %s)", config.VLMModelID)
		} else {
			logger.Warnf(ctx, "Failed to load VLM model %s for MCP image fallback: %v", config.VLMModelID, err)
		}
	}

	// TenantSkills is the sandbox image. SkillDirs is the host
	// skills/preloaded tree and is no longer filled on the QA path; it
	// remains so tests (and any caller that still points at a host
	// directory) can construct a manager. Install mode initializes for
	// the shell without hanging a skills manager on the engine.
	offerSkills := config.SkillsEnabled &&
		(len(config.SkillDirs) > 0 || len(config.TenantSkills) > 0)
	if offerSkills || config.SkillInstallMode() {
		skillsManager, err := s.initializeSkillsManager(ctx, sessionID, config, toolRegistry)
		if err != nil {
			logger.Warnf(ctx, "Failed to initialize skills manager: %v", err)
		} else if offerSkills && skillsManager != nil {
			engine.SetSkillsManager(skillsManager)
			logger.Infof(ctx, "Skills manager initialized with %d skills",
				len(skillsManager.GetAllMetadata()))
		}
	}

	return engine, nil
}

// registerMCPTools registers MCP tools from enabled services for this tenant.
func (s *agentService) registerMCPTools(
	ctx context.Context,
	toolRegistry *tools.ToolRegistry,
	config *types.AgentConfig,
	eventBus *event.EventBus,
	sessionID, assistantMessageID string,
) {
	tenantID := uint64(0)
	if tid, ok := types.TenantIDFromContext(ctx); ok {
		tenantID = tid
	}
	if tenantID == 0 || s.mcpServiceService == nil || s.mcpManager == nil {
		return
	}

	mcpMode := config.MCPSelectionMode
	if mcpMode == "" {
		mcpMode = "all"
	}
	if mcpMode == "none" {
		logger.Infof(ctx, "MCP services disabled by agent config (mode: none)")
		return
	}

	var mcpServices []*types.MCPService
	var err error

	if mcpMode == "selected" {
		if len(config.MCPServices) == 0 {
			logger.Infof(ctx, "MCP services disabled by agent config (mode: selected, no services)")
			return
		}
		mcpServices, err = s.mcpServiceService.ListMCPServicesByIDs(ctx, tenantID, config.MCPServices)
		if err != nil {
			logger.Warnf(ctx, "Failed to list selected MCP services: %v", err)
			return
		}
		logger.Infof(ctx, "Using %d selected MCP services from agent config", len(mcpServices))
	} else {
		mcpServices, err = s.mcpServiceService.ListMCPServices(ctx, tenantID)
		if err != nil {
			logger.Warnf(ctx, "Failed to list MCP services: %v", err)
			return
		}
	}

	enabledServices := make([]*types.MCPService, 0)
	for _, svc := range mcpServices {
		if svc != nil && svc.Enabled {
			enabledServices = append(enabledServices, svc)
		}
	}
	if len(enabledServices) > 0 {
		var regCtx *tools.MCPOAuthSession
		if eventBus != nil && sessionID != "" && assistantMessageID != "" {
			regCtx = &tools.MCPOAuthSession{
				EventBus:               eventBus,
				SessionID:              sessionID,
				AssistantMessageID:     assistantMessageID,
				ApprovalCtx:            ctx,
				AuthWaitTimeoutSeconds: config.MCPAuthWaitTimeout,
			}
		}
		registered, err := tools.RegisterMCPTools(
			ctx, toolRegistry, enabledServices, s.mcpManager, s.toolApprovalGate, regCtx,
		)
		if err != nil {
			logger.Warnf(ctx, "Failed to register MCP tools: %v", err)
		} else if registered == 0 {
			logger.Warnf(ctx, "No MCP tools registered from %d enabled service(s)", len(enabledServices))
		} else {
			logger.Infof(ctx, "Registered %d MCP tool(s) from %d enabled service(s)", registered, len(enabledServices))
		}
	}
}

// resolveKBAndDocInfos loads knowledge base metadata and selected document info for prompt.
func (s *agentService) resolveKBAndDocInfos(
	ctx context.Context,
	config *types.AgentConfig,
) ([]*agent.KnowledgeBaseInfo, []*agent.SelectedDocumentInfo) {
	kbIDs, kbTenantMap := knowledgeBaseScopesForPrompt(config)
	kbInfos, err := s.getKnowledgeBaseInfos(ctx, kbIDs, kbTenantMap)
	if err != nil {
		logger.Warnf(ctx, "Failed to get knowledge base details, using IDs only: %v", err)
		kbInfos = make([]*agent.KnowledgeBaseInfo, 0, len(kbIDs))
		for _, kbID := range kbIDs {
			kbInfos = append(kbInfos, &agent.KnowledgeBaseInfo{
				ID:          kbID,
				Name:        kbID,
				Description: "",
				DocCount:    0,
			})
		}
	}

	selectedDocs, err := s.getSelectedDocumentInfos(ctx, config.KnowledgeIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to get selected document details: %v", err)
		selectedDocs = []*agent.SelectedDocumentInfo{}
	}

	return kbInfos, selectedDocs
}

// initializeSkillsManager creates and initializes the skills manager.
//
// The sandbox manager is resolved per workspace: backends differ in
// capability (remote MicroVMs expose a session file store, local does not), so tool
// registration below must inspect this workspace's real manager rather than a
// process-wide singleton. Workspaces without a selected configuration resolve
// to the disabled manager.
func (s *agentService) initializeSkillsManager(
	ctx context.Context,
	sessionID string,
	config *types.AgentConfig,
	toolRegistry *tools.ToolRegistry,
) (*skills.Manager, error) {
	tenantID, _ := types.TenantIDFromContext(ctx)
	sandboxMgr, configID, err := resolveSandboxForExecution(
		ctx, s.sandboxResolver, s.sandboxMgr, s.sandboxPinner,
		tenantID, sessionID, config.SandboxConfigID, s.sandboxPolicy,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox config for session %s: %w", sessionID, err)
	}
	if sandboxMgr == nil {
		sandboxMgr = sandbox.NewDisabledManager()
	}

	logger.Infof(ctx, "Workspace sandbox in use: config=%s type=%s", configID, sandboxMgr.GetType())

	// Create skills manager
	skillsConfig := &skills.ManagerConfig{
		SkillDirs:     config.SkillDirs,
		AllowedSkills: config.AllowedSkills,
		Enabled:       config.SkillsEnabled,
	}

	skillsManager := skills.NewManager(skillsConfig, sandboxMgr)
	if source := s.tenantSkillSource(ctx, config); source != nil {
		skillsManager.WithTenantSource(source)
	}
	if resolver := s.userEnvResolver(ctx, config); resolver != nil {
		skillsManager.WithEnvResolver(resolver)
	}

	// Initialize (discover skills)
	if err := skillsManager.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skills: %w", err)
	}

	// Skill tools follow SkillsEnabled, not merely sandbox availability: the
	// skill installer agent must have shell_exec WITHOUT
	// execute_skill_script, since it is the thing installing skills.
	if config.SkillsEnabled {
		toolRegistry.RegisterTool(tools.NewReadSkillTool(skillsManager))
		logger.Infof(ctx, "Registered read_skill tool")
	}

	if sandboxMgr.GetType() != sandbox.SandboxTypeDisabled {
		if config.SkillsEnabled {
			toolRegistry.RegisterTool(tools.NewExecuteSkillScriptTool(skillsManager))
			logger.Infof(ctx, "Registered execute_skill_script tool")
		}

		// list_sandbox_files / read_sandbox_file expose per-session
		// filesystem inspection. Registration is gated on the sandbox
		// manager advertising a SessionFileStore capability. A manager that
		// cannot honour that capability returns nil so we never surface
		// tenant-isolated tools against a backend that cannot isolate them.
		//
		// They follow SkillsEnabled for the same reason the skill tools do:
		// the installer agent is here only for the shell it needs to install
		// dependencies, and its prompt tells it to use shell_exec alone. A
		// root shell can already read and list anything these two could, so
		// offering them adds nothing but tool surface and iteration budget.
		if config.SkillsEnabled {
			if store := sessionSandboxFileStore(sandboxMgr); store != nil {
				toolRegistry.RegisterTool(tools.NewListSandboxFilesTool(store))
				toolRegistry.RegisterTool(tools.NewReadSandboxFileTool(store))
				logger.Infof(ctx, "Registered list_sandbox_files and read_sandbox_file tools")
			} else {
				logger.Infof(ctx, "Sandbox backend does not advertise session filesystem capability; "+
					"list_sandbox_files/read_sandbox_file not registered")
			}
		}

		s.registerSandboxShellTool(ctx, toolRegistry, sandboxMgr, config)
	}

	return skillsManager, nil
}

// tenantSkillSource builds the source for the skills installed into this run's
// sandbox image, or nil when the run has none. The set was already narrowed to
// what this run can invoke when the config was built; nothing here re-decides
// it.
func (s *agentService) tenantSkillSource(
	ctx context.Context, config *types.AgentConfig,
) skills.SkillSource {
	rows := config.TenantSkills
	if len(rows) == 0 {
		return nil
	}
	// The rows were fetched under the caller's workspace, so this is the
	// caller's ID; it is read off the row rather than the context so the
	// bundle download cannot resolve a different workspace's storage than the
	// one the rows came from.
	ownerTenantID := rows[0].TenantID
	// The closure captures the engine-creation context because loadBundle
	// takes no context of its own. That is the turn's context today -
	// CreateAgentEngine and engine.Execute are called back to back with the
	// same ctx - so it stays live for as long as read_skill can be called. If
	// a caller ever creates the engine under a shorter-lived context, bundle
	// downloads start failing for installed skills only, and loadBundle needs
	// a ctx parameter.
	return skills.NewTenantSkillSource(rows, func(ref string) ([]byte, error) {
		return s.readSkillBundle(ctx, ownerTenantID, ref)
	})
}

// userEnvResolver builds the per-caller environment resolver for this run, or
// nil when there is no sandbox config to resolve against.
//
// It is built even when the run has no installed skills: the caller's
// config-wide variables are injected into every execution, skills or not.
//
// The repository is constructed from s.db here rather than injected:
// NewAgentService already takes 23 parameters, and this is the only place in
// the agent path that touches the user env table.
func (s *agentService) userEnvResolver(
	ctx context.Context, config *types.AgentConfig,
) skills.SkillEnvResolver {
	configID := config.SandboxConfigID
	if configID == "" {
		return nil
	}
	rows := config.TenantSkills
	if s.db == nil {
		// Unreachable in production, but a wiring regression here would drop
		// every admin and user value silently and present to a member as "my
		// key stopped working" with nothing in the logs.
		logger.Warnf(ctx,
			"[skill] no database handle: sandbox config %s will run without "+
				"any configured environment variable", configID)
		return nil
	}
	// The workspace is read off a row for the same reason tenantSkillSource
	// does it: the rows were fetched under the caller's workspace, and a value
	// must never be looked up in a different one. With no rows there is nothing
	// to disagree with the context.
	tenantID, _ := types.TenantIDFromContext(ctx)
	if len(rows) > 0 {
		tenantID = rows[0].TenantID
	}
	if tenantID == 0 {
		return nil
	}
	return NewUserEnvResolver(rows, repository.NewTenantSkillRepository(s.db), tenantID, configID)
}

// skillEnvCapture writes declared skill credentials a successful shell_exec
// already used, for the current principal. Nil when there is nothing to write
// against. Errors stay inside the callback so a failed persist cannot change
// the tool result the model already received.
func (s *agentService) skillEnvCapture(config *types.AgentConfig) tools.SkillEnvCapture {
	if s.db == nil || config == nil || config.SandboxConfigID == "" {
		return nil
	}
	configID := config.SandboxConfigID
	return func(ctx context.Context, skillName string, pairs map[string]string) {
		svc := NewUserEnvService(
			repository.NewTenantSkillRepository(s.db),
			repository.NewTenantSandboxConfigRepository(s.db),
		)
		if err := svc.CaptureSkillEnv(ctx, configID, skillName, pairs); err != nil {
			logger.Warnf(ctx, "[skill] capture env for %s failed: %v", skillName, err)
		}
	}
}

// readSkillBundle downloads one uploaded skill archive. It backs read_skill for
// installed skills: the image holds the executable copy, but reading a file out
// of it would need a live sandbox, and the archive is byte-identical to what
// was installed.
func (s *agentService) readSkillBundle(
	ctx context.Context, tenantID uint64, ref string,
) ([]byte, error) {
	if s.storageResolver == nil {
		return nil, errors.New("storage resolver is not configured")
	}
	fs, _, err := s.storageResolver.ResolveFileService(
		ctx, &types.Tenant{ID: tenantID}, "", "", "",
	)
	if err != nil {
		return nil, err
	}
	if fs == nil {
		return nil, errors.New("file service is not configured")
	}
	reader, err := fs.GetFile(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	// Bounded because the object is read into memory: the upload limit is the
	// only thing that says how large a legitimate archive can be.
	archive, err := io.ReadAll(io.LimitReader(reader, maxSkillBundleTotalBytes+1))
	if err != nil {
		return nil, err
	}
	if len(archive) > maxSkillBundleTotalBytes {
		return nil, fmt.Errorf("skill bundle %s is larger than the upload limit", ref)
	}
	return archive, nil
}

// registerSandboxShellTool registers the one shell_exec variant this run is
// entitled to.
//
// shell_exec is a remote-only capability: the capability accessors yield nil
// for backends that cannot run session-scoped shell, so the same check works
// for every provider.
//
// The skill installer agent gets the install-mode variant, which runs as root
// and may work inside the skills image root — it exists to install
// dependencies into the image, which the ordinary contract forbids on both
// counts. Every other agent keeps the non-root, /workspace-only executor.
// AgentConfig.SkillInstallMode is settable only through
// EnableSkillInstallMode, which refuses every agent but the built-in
// installer, so no tenant agent can reach this branch.
func (s *agentService) registerSandboxShellTool(
	ctx context.Context,
	toolRegistry *tools.ToolRegistry,
	sandboxMgr sandbox.Manager,
	config *types.AgentConfig,
) {
	if config.SkillInstallMode() {
		if executor := sessionSandboxInstallShellExecutor(sandboxMgr); executor != nil {
			toolRegistry.RegisterTool(tools.NewInstallShellExecTool(executor))
			logger.Infof(ctx, "Registered install-mode shell_exec tool")
		} else {
			logger.Warnf(ctx, "Sandbox backend does not advertise install-mode shell; skill install cannot run")
		}
		return
	}
	if executor := sessionSandboxShellExecutor(sandboxMgr); executor != nil {
		resolver := s.userEnvResolver(ctx, config)
		toolRegistry.RegisterTool(
			tools.NewShellExecTool(executor, resolver).WithEnvCapture(s.skillEnvCapture(config)),
		)
		logger.Infof(ctx, "Registered shell_exec tool")
	} else {
		logger.Infof(ctx, "Sandbox backend does not advertise remote shell capability; shell_exec not registered")
	}
}

// registerTools registers tools based on the agent configuration
func (s *agentService) registerTools(
	ctx context.Context,
	registry *tools.ToolRegistry,
	config *types.AgentConfig,
	rerankModel rerank.Reranker,
	chatModel chat.Chat,
	sessionID string,
) error {
	// Source of truth policy:
	//   - `config.AllowedTools` is the explicit, user-editable whitelist —
	//     populated by the agent-type preset on create and freely editable
	//     afterwards.
	//   - We never silently *inject* tools the user didn't pick.
	//   - We still *filter out* tools whose capability prerequisites are missing
	//     (no KB in scope, no Wiki-capable KB, etc.) so the LLM can't call tools
	//     that would error at runtime.
	//   - Legacy agents without AllowedTools fall back to DefaultAllowedTools().
	var allowedTools []string
	if len(config.AllowedTools) > 0 {
		allowedTools = make([]string, len(config.AllowedTools))
		copy(allowedTools, config.AllowedTools)
		logger.Infof(ctx, "Using custom allowed tools from config: %v", allowedTools)
	} else {
		allowedTools = tools.DefaultAllowedTools()
		logger.Infof(ctx, "Using default allowed tools: %v", allowedTools)
	}
	if config.SharedAgentReadOnly {
		allowedTools = filterSharedAgentWriteTools(allowedTools)
	}

	// ---- Capability detection from SearchTargets ----
	var hasVectorKB bool
	var wikiKBIDs []string
	wikiRoutes := tools.NewWikiRouteResolver()
	for _, target := range config.SearchTargets {
		if target == nil || target.KnowledgeBaseID == "" {
			continue
		}
		kb, err := s.knowledgeBaseService.GetKnowledgeBaseByIDOnly(ctx, target.KnowledgeBaseID)
		if err != nil {
			continue
		}
		if kb.IsVectorEnabled() || kb.IsKeywordEnabled() {
			hasVectorKB = true
		}
		if kb.IsWikiEnabled() {
			wikiKBIDs = append(wikiKBIDs, kb.ID)
		}
	}
	wikiKBIDs = dedupStrings(wikiKBIDs)
	wikiScopes := tools.NewWikiScopesFromSearchTargets(config.SearchTargets, wikiKBIDs)
	// Narrow to the KBs that survived scope resolution. Build a fresh slice
	// rather than truncating in place, so the argument passed above can never
	// be overwritten through a shared backing array.
	scopedWikiKBIDs := make([]string, 0, len(wikiScopes))
	for _, scope := range wikiScopes {
		scopedWikiKBIDs = append(scopedWikiKBIDs, scope.KnowledgeBaseID)
	}
	wikiKBIDs = scopedWikiKBIDs
	hasWikiKB := len(wikiKBIDs) > 0

	// Filter out knowledge base tools if no knowledge scope is configured for this turn.
	hasKnowledge := agentHasKnowledgeScope(config)
	if !hasKnowledge {
		filteredTools := make([]string, 0)
		kbTools := map[string]bool{
			tools.ToolKnowledgeSearch:     true,
			tools.ToolGrepChunks:          true,
			tools.ToolListKnowledgeChunks: true,
			tools.ToolQueryKnowledgeGraph: true,
			tools.ToolGetDocumentInfo:     true,
			tools.ToolDatabaseQuery:       true,
			tools.ToolDataAnalysis:        true,
			tools.ToolDataSchema:          true,
			// Wiki tools also require at least one KB in scope.
			tools.ToolWikiReadPage:      true,
			tools.ToolWikiSearch:        true,
			tools.ToolWikiReadSourceDoc: true,
			tools.ToolWikiFlagIssue:     true,
			tools.ToolWikiWritePage:     true,
			tools.ToolWikiReplaceText:   true,
			tools.ToolWikiRenamePage:    true,
			tools.ToolWikiDeletePage:    true,
			tools.ToolWikiReadIssue:     true,
			tools.ToolWikiUpdateIssue:   true,
		}

		// If no knowledge and no web search, also disable todo_write (not useful for simple chat)
		if !config.WebSearchEnabled {
			kbTools[tools.ToolTodoWrite] = true
		}

		for _, toolName := range allowedTools {
			if !kbTools[toolName] {
				filteredTools = append(filteredTools, toolName)
			}
		}
		allowedTools = filteredTools
		logger.Infof(ctx, "Pure Agent Mode: Knowledge base tools filtered out, remaining: %v", allowedTools)
	}

	// If web search is enabled, add web_search to allowedTools
	if config.WebSearchEnabled {
		allowedTools = append(allowedTools, tools.ToolWebSearch)
		allowedTools = append(allowedTools, tools.ToolWebFetch)
	}

	// Long-term memory search follows the memory switches, not the tool list.
	// Being able to read memory is already a decision the workspace, the user
	// and the agent each get a say in; asking for it a fourth time as a tool
	// checkbox would only produce configurations where memory is on but the
	// agent cannot reach past what each turn injects for it.
	//
	// The tool is dropped before it is re-added so that an allowlist which
	// still names it — a preset, an API caller, or a config saved while memory
	// was on — cannot outlive the switch being turned off.
	allowedTools = withoutString(allowedTools, tools.ToolSearchMemory)
	if s.memoryService != nil &&
		s.memoryService.MemoryAvailable(types.ApplyAgentMemoryPreference(ctx, config.MemoryEnabled)) {
		allowedTools = append(allowedTools, tools.ToolSearchMemory)
	} else {
		logger.Infof(ctx, "search_memory not registered: long-term memory is off for this request")
	}

	// Tool capability sets — used by the hard safety nets below to drop tools
	// whose runtime prerequisite (a matching KB surface) is missing.
	//
	// NOTE: ragToolSet must stay in sync with frontend `knowledgeBaseTools`
	// in AgentEditorModal.vue. These are *all* tools that retrieve/inspect
	// content from RAG-style knowledge bases.
	ragToolSet := map[string]bool{
		tools.ToolKnowledgeSearch:     true,
		tools.ToolGrepChunks:          true,
		tools.ToolListKnowledgeChunks: true,
		tools.ToolQueryKnowledgeGraph: true,
		tools.ToolGetDocumentInfo:     true,
		tools.ToolDatabaseQuery:       true,
	}
	allWikiToolSet := map[string]bool{
		tools.ToolWikiReadPage:      true,
		tools.ToolWikiSearch:        true,
		tools.ToolWikiReadSourceDoc: true,
		tools.ToolWikiFlagIssue:     true,
		tools.ToolWikiWritePage:     true,
		tools.ToolWikiReplaceText:   true,
		tools.ToolWikiRenamePage:    true,
		tools.ToolWikiDeletePage:    true,
		tools.ToolWikiReadIssue:     true,
		tools.ToolWikiUpdateIssue:   true,
	}

	// Hard safety nets: drop tools whose runtime prerequisite is missing.
	// This guards against stale configs where e.g. the user ticked wiki tools
	// earlier but later swapped in a non-wiki KB (or vice versa for RAG).
	if !hasWikiKB {
		filtered := make([]string, 0, len(allowedTools))
		dropped := make([]string, 0)
		for _, t := range allowedTools {
			if allWikiToolSet[t] {
				dropped = append(dropped, t)
				continue
			}
			filtered = append(filtered, t)
		}
		allowedTools = filtered
		if len(dropped) > 0 {
			logger.Warnf(ctx, "Dropped wiki tools %v because no wiki-capable KB is in scope", dropped)
		}
	}
	if !hasVectorKB {
		filtered := make([]string, 0, len(allowedTools))
		dropped := make([]string, 0)
		for _, t := range allowedTools {
			if ragToolSet[t] {
				dropped = append(dropped, t)
				continue
			}
			filtered = append(filtered, t)
		}
		allowedTools = filtered
		if len(dropped) > 0 {
			logger.Warnf(ctx, "Dropped RAG tools %v because no RAG-capable KB is in scope", dropped)
		}
	}

	// Deduplicate while preserving original order.
	allowedTools = dedupStrings(allowedTools)

	// logger.Infof(ctx, "Registering tools: %v, webSearchEnabled: %v", allowedTools, config.WebSearchEnabled)
	// Register each allowed tool
	for _, toolName := range allowedTools {
		var toolToRegister types.Tool

		switch toolName {
		case tools.ToolThinking:
			toolToRegister = tools.NewSequentialThinkingTool()
		case tools.ToolTodoWrite:
			toolToRegister = tools.NewTodoWriteTool()
		case tools.ToolKnowledgeSearch:
			toolToRegister = tools.NewKnowledgeSearchTool(
				s.knowledgeBaseService,
				s.knowledgeService,
				s.chunkService,
				config.SearchTargets,
				rerankModel,
				s.cfg,
			)
		case tools.ToolGrepChunks:
			toolToRegister = tools.NewGrepChunksTool(s.db, config.SearchTargets)
			logger.Infof(ctx, "Registered grep_chunks tool with searchTargets: %d targets", len(config.SearchTargets))
		case tools.ToolListKnowledgeChunks:
			toolToRegister = tools.NewListKnowledgeChunksTool(s.knowledgeService, s.chunkService, config.SearchTargets)
		case tools.ToolQueryKnowledgeGraph:
			toolToRegister = tools.NewQueryKnowledgeGraphTool(s.knowledgeBaseService, config.SearchTargets).
				WithKnowledgeScope(s.knowledgeService)
		case tools.ToolGetDocumentInfo:
			toolToRegister = tools.NewGetDocumentInfoTool(s.knowledgeService, s.chunkService, config.SearchTargets)
		case tools.ToolSearchConversations:
			// The owner is captured from the caller's identity here, not read
			// from the model's arguments, so no prompt can redirect the search
			// at somebody else's conversations.
			toolToRegister = tools.NewSearchConversationsTool(
				s.messageService, types.SessionOwnerIDFromContext(ctx), sessionID)
		case tools.ToolSearchMemory:
			// Reaching this case means the memory switches were already
			// checked above, where the tool is injected. Which memory space is
			// read is resolved from the request context inside the service, so
			// this tool needs no owner argument and none can be supplied.
			toolToRegister = tools.NewSearchMemoryTool(s.memoryService)
		case tools.ToolDatabaseQuery:
			toolToRegister = tools.NewDatabaseQueryTool(s.db, config.SearchTargets)
		case tools.ToolWebSearch:
			toolToRegister = tools.NewWebSearchTool(
				s.webSearchService,
				s.knowledgeBaseService,
				s.knowledgeService,
				s.webSearchStateService,
				sessionID,
				config.WebSearchMaxResults,
				config.WebSearchProviderID,
			)
			logger.Infof(ctx, "Registered web_search tool for session: %s, maxResults: %d, providerID: %s", sessionID, config.WebSearchMaxResults, config.WebSearchProviderID)

		case tools.ToolWebFetch:
			toolToRegister = tools.NewWebFetchTool(chatModel)
			logger.Infof(ctx, "Registered web_fetch tool for session: %s", sessionID)

		case tools.ToolDataAnalysis:
			toolToRegister = tools.NewDataAnalysisTool(s.knowledgeBaseService, s.knowledgeService, s.tenantService, s.fileService, s.duckdb, sessionID, s.storageResolver).
				WithSearchTargets(config.SearchTargets)
			logger.Infof(ctx, "Registered data_analysis tool for session: %s", sessionID)

		case tools.ToolDataSchema:
			toolToRegister = tools.NewDataSchemaTool(s.knowledgeService, s.chunkService.GetRepository()).
				WithSearchTargets(config.SearchTargets)
			logger.Infof(ctx, "Registered data_schema tool")

		// Wiki tools — only registered when wiki KBs are detected
		case tools.ToolWikiReadPage:
			toolToRegister = tools.NewWikiReadPageTool(s.wikiPageService, s.knowledgeService, wikiScopes, wikiRoutes)
		case tools.ToolWikiSearch:
			toolToRegister = tools.NewWikiSearchTool(s.wikiPageService, s.knowledgeService, wikiScopes, wikiRoutes)
		case tools.ToolWikiReadSourceDoc:
			toolToRegister = tools.NewWikiReadSourceDocTool(s.knowledgeService, s.chunkService, config.SearchTargets)
		case tools.ToolWikiFlagIssue:
			toolToRegister = tools.NewWikiFlagIssueTool(s.wikiPageService, wikiKBIDs, wikiRoutes).
				WithKnowledgeScope(s.knowledgeService, config.SearchTargets)
		case tools.ToolWikiReadIssue:
			toolToRegister = tools.NewWikiReadIssueTool(s.wikiPageService, wikiKBIDs)
		case tools.ToolWikiUpdateIssue:
			toolToRegister = tools.NewWikiUpdateIssueTool(s.wikiPageService, wikiKBIDs)
		case tools.ToolWikiWritePage:
			toolToRegister = tools.NewWikiWritePageTool(s.wikiPageService, wikiKBIDs, s.knowledgeService, wikiRoutes).
				WithSearchTargets(config.SearchTargets)
		case tools.ToolWikiReplaceText:
			toolToRegister = tools.NewWikiReplaceTextTool(s.wikiPageService, wikiKBIDs, s.knowledgeService, wikiRoutes).
				WithSearchTargets(config.SearchTargets)
		case tools.ToolWikiRenamePage:
			toolToRegister = tools.NewWikiRenamePageTool(s.wikiPageService, wikiKBIDs, wikiRoutes)
		case tools.ToolWikiDeletePage:
			toolToRegister = tools.NewWikiDeletePageTool(s.wikiPageService, wikiKBIDs, wikiRoutes)

		case tools.ToolShellExec, tools.ToolReadSkill, tools.ToolExecuteSkillScript,
			tools.ToolListSandboxFiles, tools.ToolReadSandboxFile:
			// Bound to the resolved sandbox manager in initializeSkillsManager
			// / registerSandboxShellTool. Listing them here would warn
			// "Unknown tool: shell_exec" on every skill install, then register
			// the real tool a few lines later.
			continue

		default:
			logger.Warnf(ctx, "Unknown tool: %s", toolName)
		}

		if toolToRegister != nil {
			if toolToRegister.Name() != toolName {
				logger.Warnf(ctx, "Tool name mismatch: expected %s, got %s", toolName, toolToRegister.Name())
			}
			registry.RegisterTool(toolToRegister)
		}
	}

	logger.Infof(ctx, "Registered %d tools", len(registry.ListTools()))
	return nil
}

// filterSharedAgentWriteTools enforces the read-only contract of AgentShare.
// These tools write source-workspace Wiki state and otherwise bypass the HTTP
// KB permission middleware because they execute inside the agent engine.
func filterSharedAgentWriteTools(allowed []string) []string {
	sourceWorkspaceWrites := map[string]bool{
		tools.ToolWikiFlagIssue:   true,
		tools.ToolWikiUpdateIssue: true,
		tools.ToolWikiWritePage:   true,
		tools.ToolWikiReplaceText: true,
		tools.ToolWikiRenamePage:  true,
		tools.ToolWikiDeletePage:  true,
	}
	filtered := make([]string, 0, len(allowed))
	for _, name := range allowed {
		if !sourceWorkspaceWrites[name] {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

// ValidateConfig validates the agent configuration
func (s *agentService) ValidateConfig(config *types.AgentConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	if config.MaxIterations <= 0 {
		config.MaxIterations = 5 // Default
	}

	if config.MaxIterations > MAX_ITERATIONS {
		return fmt.Errorf("max iterations too high: %d (max %d)", config.MaxIterations, MAX_ITERATIONS)
	}

	return nil
}

// getKnowledgeBaseInfos retrieves detailed information for knowledge bases.
// kbTenantMap carries the tenant each KB should be queried under (source tenant
// for directly shared KBs); a missing entry falls back to the request tenant.
func (s *agentService) getKnowledgeBaseInfos(ctx context.Context, kbIDs []string, kbTenantMap map[string]uint64) ([]*agent.KnowledgeBaseInfo, error) {
	if len(kbIDs) == 0 {
		return []*agent.KnowledgeBaseInfo{}, nil
	}

	kbInfos := make([]*agent.KnowledgeBaseInfo, 0, len(kbIDs))

	for _, kbID := range kbIDs {
		// Get knowledge base details
		kb, err := s.knowledgeBaseService.GetKnowledgeBaseByID(ctx, kbID)
		if err != nil {
			logger.Warnf(ctx, "Failed to get knowledge base %s: %v", secutils.SanitizeForLog(kbID), err)
			kbInfos = append(kbInfos, &agent.KnowledgeBaseInfo{
				ID:          kbID,
				Name:        kbID,
				Type:        "document",
				Description: "",
				DocCount:    0,
				RecentDocs:  []agent.RecentDocInfo{},
			})
			continue
		}

		// Skip hidden/system-managed knowledge bases (e.g., __chat_history__)
		if kb.IsTemporary {
			logger.Debugf(ctx, "Skipping temporary knowledge base %s (%s) from prompt", kb.ID, kb.Name)
			continue
		}

		// Document/FAQ listing below is tenant-scoped, so a directly shared KB
		// must be queried under its source tenant — the request context belongs
		// to the receiving tenant and would silently yield doc_count=0. The
		// tenant comes from the SearchTarget that buildSearchTargets already
		// authorized; this only widens the metadata query, never the KB set.
		metaCtx := ctx
		if scopeTenantID := kbTenantMap[kbID]; scopeTenantID != 0 {
			metaCtx = context.WithValue(ctx, types.TenantIDContextKey, scopeTenantID)
		}

		// Get document count and recent documents
		docCount := 0
		recentDocs := []agent.RecentDocInfo{}

		if kb.Type == types.KnowledgeBaseTypeFAQ {
			pageResult, err := s.knowledgeService.ListFAQEntries(metaCtx, kbID, &types.Pagination{
				Page:     1,
				PageSize: 10,
			}, nil, 0, "", "", "")
			if err == nil && pageResult != nil {
				docCount = int(pageResult.Total)
				if entries, ok := pageResult.Data.([]*types.FAQEntry); ok {
					for _, entry := range entries {
						if len(recentDocs) >= 10 {
							break
						}
						recentDocs = append(recentDocs, agent.RecentDocInfo{
							ChunkID:             entry.ChunkID,
							KnowledgeID:         entry.KnowledgeID,
							KnowledgeBaseID:     entry.KnowledgeBaseID,
							Title:               entry.StandardQuestion,
							Type:                string(types.ChunkTypeFAQ),
							CreatedAt:           entry.CreatedAt.Format("2006-01-02"),
							FAQStandardQuestion: entry.StandardQuestion,
							FAQSimilarQuestions: entry.SimilarQuestions,
							FAQAnswers:          entry.Answers,
						})
					}
				}
			} else if err != nil {
				logger.Warnf(ctx, "Failed to list FAQ entries for %s: %v", kbID, err)
			}
		}

		// Fallback to generic knowledge listing when not FAQ or FAQ retrieval failed
		if kb.Type != types.KnowledgeBaseTypeFAQ || len(recentDocs) == 0 {
			pageResult, err := s.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(metaCtx, kbID, &types.Pagination{
				Page:     1,
				PageSize: 10,
			}, types.KnowledgeListFilter{
				ParseStatus: types.ParseStatusCompleted,
			})

			if err == nil && pageResult != nil {
				docCount = int(pageResult.Total)

				// Convert to Knowledge slice
				if knowledges, ok := pageResult.Data.([]*types.Knowledge); ok {
					for _, k := range knowledges {
						if len(recentDocs) >= 10 {
							break
						}
						recentDocs = append(recentDocs, agent.RecentDocInfo{
							KnowledgeID: k.ID,
							Title:       k.Title,
							Description: k.Description,
							FileName:    k.FileName,
							Type:        k.FileType,
							CreatedAt:   k.CreatedAt.Format("2006-01-02"),
							FileSize:    k.FileSize,
						})
					}
				}
			}
		}

		kbType := kb.Type
		if kbType == "" {
			kbType = "document" // Default type
		}
		kbInfos = append(kbInfos, &agent.KnowledgeBaseInfo{
			ID:           kb.ID,
			Name:         kb.Name,
			Type:         kbType,
			Description:  kb.Description,
			DocCount:     docCount,
			Capabilities: kbRetrievalCapabilities(kb),
			RecentDocs:   recentDocs,
		})
	}

	return kbInfos, nil
}

// kbRetrievalCapabilities reports which retrieval surfaces a KB exposes.
// Surfaces are the static facts the hybrid agent prompt consults to pick its
// retrieval strategy — the agent should NOT need to probe this via search.
//
// Returned values are a subset of {"wiki", "chunks"}:
//   - "wiki"   → the KB has wiki ingestion enabled (wiki_search / wiki_read_page)
//   - "chunks" → the KB has vector and/or keyword (BM25) indexing enabled
//     (knowledge_search / grep_chunks)
func kbRetrievalCapabilities(kb *types.KnowledgeBase) []string {
	if kb == nil {
		return nil
	}
	caps := make([]string, 0, 2)
	if kb.IsWikiEnabled() {
		caps = append(caps, "wiki")
	}
	if kb.IsVectorEnabled() || kb.IsKeywordEnabled() {
		caps = append(caps, "chunks")
	}
	return caps
}

// getSelectedDocumentInfos retrieves detailed information for user-selected documents (via @ mention)
// This loads the actual content of the documents to include in the system prompt
func (s *agentService) getSelectedDocumentInfos(ctx context.Context, knowledgeIDs []string) ([]*agent.SelectedDocumentInfo, error) {
	if len(knowledgeIDs) == 0 {
		return []*agent.SelectedDocumentInfo{}, nil
	}

	// Get tenant ID from context
	tenantID := uint64(0)
	if tid, ok := types.TenantIDFromContext(ctx); ok {
		tenantID = tid
	}

	// Fetch knowledge metadata (include docs from shared KBs the user has access to)
	knowledges, err := s.knowledgeService.GetKnowledgeBatchWithSharedAccess(ctx, tenantID, knowledgeIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to get knowledge batch: %w", err)
	}

	// Build map for quick lookup
	knowledgeMap := make(map[string]*types.Knowledge)
	for _, k := range knowledges {
		if k != nil {
			knowledgeMap[k.ID] = k
		}
	}

	selectedDocs := make([]*agent.SelectedDocumentInfo, 0, len(knowledgeIDs))

	for _, kid := range knowledgeIDs {
		k, ok := knowledgeMap[kid]
		if !ok {
			logger.Warnf(ctx, "Selected knowledge %s not found", kid)
			continue
		}

		docInfo := &agent.SelectedDocumentInfo{
			KnowledgeID:     k.ID,
			KnowledgeBaseID: k.KnowledgeBaseID,
			Title:           k.Title,
			FileName:        k.FileName,
			FileType:        k.FileType,
		}

		selectedDocs = append(selectedDocs, docInfo)
	}

	logger.Infof(ctx, "Loaded %d selected documents metadata for prompt", len(selectedDocs))
	return selectedDocs, nil
}

func (s *agentService) resolvePinnedMCPServiceInfos(
	ctx context.Context,
	config *types.AgentConfig,
) []*agent.PinnedMCPServiceInfo {
	if len(config.PinnedMCPServiceIDs) == 0 || s.mcpServiceService == nil {
		return nil
	}
	tenantID := uint64(0)
	if tid, ok := types.TenantIDFromContext(ctx); ok {
		tenantID = tid
	}
	if tenantID == 0 {
		return fallbackPinnedMCPInfos(config.PinnedMCPServiceIDs)
	}

	services, err := s.mcpServiceService.ListMCPServicesByIDs(ctx, tenantID, config.PinnedMCPServiceIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to resolve pinned MCP services: %v", err)
		return fallbackPinnedMCPInfos(config.PinnedMCPServiceIDs)
	}
	byID := make(map[string]*types.MCPService, len(services))
	for _, svc := range services {
		if svc != nil {
			byID[svc.ID] = svc
		}
	}
	result := make([]*agent.PinnedMCPServiceInfo, 0, len(config.PinnedMCPServiceIDs))
	for _, id := range config.PinnedMCPServiceIDs {
		if id == "" {
			continue
		}
		if svc, ok := byID[id]; ok {
			result = append(result, &agent.PinnedMCPServiceInfo{
				ID:          svc.ID,
				Name:        svc.Name,
				Description: svc.Description,
			})
			continue
		}
		result = append(result, &agent.PinnedMCPServiceInfo{ID: id, Name: id})
	}
	return result
}

func (s *agentService) attachPinnedMCPToolNames(
	registry *tools.ToolRegistry,
	pinned []*agent.PinnedMCPServiceInfo,
) {
	if registry == nil || len(pinned) == 0 {
		return
	}
	byService := tools.MCPToolNamesByServiceID(registry)
	for _, info := range pinned {
		if info == nil || info.ID == "" {
			continue
		}
		info.ToolNames = append([]string(nil), byService[info.ID]...)
	}
}

func fallbackPinnedMCPInfos(ids []string) []*agent.PinnedMCPServiceInfo {
	result := make([]*agent.PinnedMCPServiceInfo, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		result = append(result, &agent.PinnedMCPServiceInfo{ID: id, Name: id})
	}
	return result
}

func (s *agentService) resolvePinnedSkillInfos(config *types.AgentConfig) []*agent.PinnedSkillInfo {
	if len(config.PinnedSkillNames) == 0 {
		return nil
	}

	descByName := make(map[string]string)
	if len(config.SkillDirs) > 0 {
		loader := skills.NewLoader(config.SkillDirs)
		if metadata, err := loader.DiscoverSkills(); err == nil {
			for _, meta := range metadata {
				if meta != nil {
					descByName[meta.Name] = meta.Description
				}
			}
		}
	}
	for _, row := range config.TenantSkills {
		if row != nil && row.Name != "" {
			descByName[row.Name] = row.Description
		}
	}

	result := make([]*agent.PinnedSkillInfo, 0, len(config.PinnedSkillNames))
	for _, name := range config.PinnedSkillNames {
		if name == "" {
			continue
		}
		result = append(result, &agent.PinnedSkillInfo{
			Name:        name,
			Description: descByName[name],
		})
	}
	return result
}
