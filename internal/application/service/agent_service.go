package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/agent/tools"
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
	toolApprovalGate      approval.MCPApproval
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
	toolApprovalGate approval.MCPApproval,
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
		toolApprovalGate:      toolApprovalGate,
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
	sessionID string,
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
	s.registerMCPTools(ctx, toolRegistry, config)

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

	// Initialize skills manager if skills are enabled
	if config.SkillsEnabled && len(config.SkillDirs) > 0 {
		skillsManager, err := s.initializeSkillsManager(ctx, config, toolRegistry)
		if err != nil {
			logger.Warnf(ctx, "Failed to initialize skills manager: %v", err)
		} else if skillsManager != nil {
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

	if mcpMode == "selected" && len(config.MCPServices) > 0 {
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
		if err := tools.RegisterMCPTools(ctx, toolRegistry, enabledServices, s.mcpManager, s.toolApprovalGate); err != nil {
			logger.Warnf(ctx, "Failed to register MCP tools: %v", err)
		} else {
			logger.Infof(ctx, "Registered MCP tools from %d enabled services", len(enabledServices))
		}
	}
}

// resolveKBAndDocInfos loads knowledge base metadata and selected document info for prompt.
func (s *agentService) resolveKBAndDocInfos(
	ctx context.Context,
	config *types.AgentConfig,
) ([]*agent.KnowledgeBaseInfo, []*agent.SelectedDocumentInfo) {
	kbInfos, err := s.getKnowledgeBaseInfos(ctx, config.KnowledgeBases)
	if err != nil {
		logger.Warnf(ctx, "Failed to get knowledge base details, using IDs only: %v", err)
		kbInfos = make([]*agent.KnowledgeBaseInfo, 0, len(config.KnowledgeBases))
		for _, kbID := range config.KnowledgeBases {
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

// initializeSkillsManager creates and initializes the skills manager
func (s *agentService) initializeSkillsManager(
	ctx context.Context,
	config *types.AgentConfig,
	toolRegistry *tools.ToolRegistry,
) (*skills.Manager, error) {
	// Initialize sandbox manager based on environment variables
	// WEKNORA_SANDBOX_MODE: "docker", "local", "disabled" (default: "disabled")
	// WEKNORA_SANDBOX_TIMEOUT: timeout in seconds (default: 60)
	// WEKNORA_SANDBOX_DOCKER_IMAGE: custom Docker image (default: wechatopenai/weknora-sandbox:latest)
	var sandboxMgr sandbox.Manager
	var err error

	sandboxMode := os.Getenv("WEKNORA_SANDBOX_MODE")
	if sandboxMode == "" {
		sandboxMode = "disabled"
	}
	dockerImage := os.Getenv("WEKNORA_SANDBOX_DOCKER_IMAGE")
	if dockerImage == "" {
		dockerImage = sandbox.DefaultDockerImage
	}
	sandboxTimeoutStr := os.Getenv("WEKNORA_SANDBOX_TIMEOUT")
	sandboxTimeout := 60
	if sandboxTimeoutStr != "" {
		if v, err := strconv.Atoi(sandboxTimeoutStr); err == nil && v > 0 {
			sandboxTimeout = v
		}
	}

	switch sandboxMode {
	case "docker":
		sandboxMgr, err = sandbox.NewManagerFromType("docker", true, dockerImage) // Enable fallback to local
		if err != nil {
			logger.Warnf(ctx, "Failed to initialize Docker sandbox, falling back to disabled: %v", err)
			sandboxMgr = sandbox.NewDisabledManager()
		}
	case "local":
		sandboxMgr, err = sandbox.NewManagerFromType("local", false, "")
		if err != nil {
			logger.Warnf(ctx, "Failed to initialize local sandbox: %v", err)
			sandboxMgr = sandbox.NewDisabledManager()
		}
	default:
		sandboxMgr = sandbox.NewDisabledManager()
	}
	logger.Infof(ctx, "Sandbox configured: mode=%s, timeout=%ds, image=%s", sandboxMode, sandboxTimeout, dockerImage)

	// Create skills manager
	skillsConfig := &skills.ManagerConfig{
		SkillDirs:     config.SkillDirs,
		AllowedSkills: config.AllowedSkills,
		Enabled:       config.SkillsEnabled,
	}

	skillsManager := skills.NewManager(skillsConfig, sandboxMgr)

	// Initialize (discover skills)
	if err := skillsManager.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skills: %w", err)
	}

	// Register skills tools
	readSkillTool := tools.NewReadSkillTool(skillsManager)
	toolRegistry.RegisterTool(readSkillTool)
	logger.Infof(ctx, "Registered read_skill tool")

	if sandboxMode != "disabled" {
		executeSkillTool := tools.NewExecuteSkillScriptTool(skillsManager)
		toolRegistry.RegisterTool(executeSkillTool)
		logger.Infof(ctx, "Registered execute_skill_script tool")
	}

	return skillsManager, nil
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

	// ---- Capability detection from SearchTargets ----
	var hasVectorKB, hasWikiKB bool
	var wikiKBIDs []string
	var wikiScopes []tools.WikiScope
	for _, target := range config.SearchTargets {
		kb, err := s.knowledgeBaseService.GetKnowledgeBaseByIDOnly(ctx, target.KnowledgeBaseID)
		if err != nil {
			continue
		}
		if kb.IsVectorEnabled() || kb.IsKeywordEnabled() {
			hasVectorKB = true
		}
		if kb.IsWikiEnabled() {
			hasWikiKB = true
			wikiKBIDs = append(wikiKBIDs, kb.ID)
			// When the user @mentioned specific documents, carry the document
			// whitelist into the wiki scope so wiki_search / wiki_read_page
			// only surface pages whose SourceRefs intersect the pinned docs.
			scope := tools.WikiScope{KnowledgeBaseID: kb.ID}
			if target.Type == types.SearchTargetTypeKnowledge && len(target.KnowledgeIDs) > 0 {
				scope.KnowledgeIDs = append([]string(nil), target.KnowledgeIDs...)
			}
			wikiScopes = append(wikiScopes, scope)
		}
	}

	// Filter out knowledge base tools if no knowledge bases or knowledge IDs are configured
	hasKnowledge := len(config.KnowledgeBases) > 0 || len(config.KnowledgeIDs) > 0
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

	logger.Infof(ctx, "Registering tools: %v, webSearchEnabled: %v", allowedTools, config.WebSearchEnabled)
	allowedTools = append(allowedTools, tools.ToolFinalAnswer)
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
				chatModel,
				s.cfg,
			)
		case tools.ToolGrepChunks:
			toolToRegister = tools.NewGrepChunksTool(s.db, config.SearchTargets)
			logger.Infof(ctx, "Registered grep_chunks tool with searchTargets: %d targets", len(config.SearchTargets))
		case tools.ToolListKnowledgeChunks:
			toolToRegister = tools.NewListKnowledgeChunksTool(s.knowledgeService, s.chunkService, config.SearchTargets)
		case tools.ToolQueryKnowledgeGraph:
			toolToRegister = tools.NewQueryKnowledgeGraphTool(s.knowledgeBaseService)
		case tools.ToolGetDocumentInfo:
			toolToRegister = tools.NewGetDocumentInfoTool(s.knowledgeService, s.chunkService, config.SearchTargets)
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
			toolToRegister = tools.NewDataAnalysisTool(s.knowledgeBaseService, s.knowledgeService, s.tenantService, s.fileService, s.duckdb, sessionID)
			logger.Infof(ctx, "Registered data_analysis tool for session: %s", sessionID)

		case tools.ToolDataSchema:
			toolToRegister = tools.NewDataSchemaTool(s.knowledgeService, s.chunkService.GetRepository())
			logger.Infof(ctx, "Registered data_schema tool")

		case tools.ToolFinalAnswer:
			toolToRegister = tools.NewFinalAnswerTool()
			logger.Infof(ctx, "Registered final_answer tool")

		// Wiki tools — only registered when wiki KBs are detected
		case tools.ToolWikiReadPage:
			toolToRegister = tools.NewWikiReadPageTool(s.wikiPageService, wikiScopes)
		case tools.ToolWikiSearch:
			toolToRegister = tools.NewWikiSearchTool(s.wikiPageService, wikiScopes)
		case tools.ToolWikiReadSourceDoc:
			toolToRegister = tools.NewWikiReadSourceDocTool(s.knowledgeService, s.chunkService)
		case tools.ToolWikiFlagIssue:
			toolToRegister = tools.NewWikiFlagIssueTool(s.wikiPageService, wikiKBIDs)
		case tools.ToolWikiReadIssue:
			toolToRegister = tools.NewWikiReadIssueTool(s.wikiPageService, wikiKBIDs)
		case tools.ToolWikiUpdateIssue:
			toolToRegister = tools.NewWikiUpdateIssueTool(s.wikiPageService, wikiKBIDs)
		case tools.ToolWikiWritePage:
			toolToRegister = tools.NewWikiWritePageTool(s.wikiPageService, wikiKBIDs, s.knowledgeService)
		case tools.ToolWikiReplaceText:
			toolToRegister = tools.NewWikiReplaceTextTool(s.wikiPageService, wikiKBIDs, s.knowledgeService)
		case tools.ToolWikiRenamePage:
			toolToRegister = tools.NewWikiRenamePageTool(s.wikiPageService, wikiKBIDs)
		case tools.ToolWikiDeletePage:
			toolToRegister = tools.NewWikiDeletePageTool(s.wikiPageService, wikiKBIDs)

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

// getKnowledgeBaseInfos retrieves detailed information for knowledge bases
func (s *agentService) getKnowledgeBaseInfos(ctx context.Context, kbIDs []string) ([]*agent.KnowledgeBaseInfo, error) {
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

		// Get document count and recent documents
		docCount := 0
		recentDocs := []agent.RecentDocInfo{}

		if kb.Type == types.KnowledgeBaseTypeFAQ {
			pageResult, err := s.knowledgeService.ListFAQEntries(ctx, kbID, &types.Pagination{
				Page:     1,
				PageSize: 10,
			}, 0, "", "", "")
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
			pageResult, err := s.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(ctx, kbID, &types.Pagination{
				Page:     1,
				PageSize: 10,
			}, types.KnowledgeListFilter{})

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
