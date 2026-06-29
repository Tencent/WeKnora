package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

// AgentQA performs agent-based question answering with conversation history and streaming support
// customAgent is optional - if provided, uses custom agent configuration instead of tenant defaults
// summaryModelID is optional - if provided, overrides the model from customAgent config
func (s *sessionService) AgentQA(
	ctx context.Context,
	req *types.QARequest,
	eventBus *event.EventBus,
) error {
	sessionID := req.Session.ID
	sessionJSON, err := json.Marshal(req.Session)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal session, session ID: %s, error: %v", sessionID, err)
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	// customAgent is required for AgentQA (handler has already done permission check for shared agent)
	if req.CustomAgent == nil {
		logger.Warnf(ctx, "Custom agent not provided for session: %s", sessionID)
		return errors.New("custom agent configuration is required for agent QA")
	}

	// Resolve retrieval tenant using shared helper
	agentTenantID := s.resolveRetrievalTenantID(ctx, req)
	logger.Infof(ctx, "Start agent-based question answering, session ID: %s, agent tenant ID: %d, query: %s, session: %s",
		sessionID, agentTenantID, req.Query, string(sessionJSON))

	var tenantInfo *types.Tenant
	if v := ctx.Value(types.TenantInfoContextKey); v != nil {
		tenantInfo, _ = v.(*types.Tenant)
	}
	// When agent belongs to another tenant (shared agent), use agent's tenant for KB/model scope; load tenantInfo if needed
	if tenantInfo == nil || tenantInfo.ID != agentTenantID {
		if s.tenantService != nil {
			if agentTenant, err := s.tenantService.GetTenantByID(ctx, agentTenantID); err == nil && agentTenant != nil {
				tenantInfo = agentTenant
				logger.Infof(ctx, "Using agent tenant info for retrieval scope, tenant ID: %d", agentTenantID)
			}
		}
	}
	if tenantInfo == nil {
		logger.Warnf(ctx, "Tenant info not available for agent tenant %d, proceeding with defaults", agentTenantID)
		tenantInfo = &types.Tenant{ID: agentTenantID}
	}

	// Ensure defaults are set
	req.CustomAgent.EnsureDefaults()

	// Build AgentConfig from custom agent and tenant info
	agentConfig, err := s.buildAgentConfig(ctx, req, tenantInfo, agentTenantID)
	if err != nil {
		return err
	}

	// Set VLM model ID for tool result image analysis (runtime-only field)
	if req.CustomAgent != nil && req.CustomAgent.Config.VLMModelID != "" {
		agentConfig.VLMModelID = req.CustomAgent.Config.VLMModelID
	}

	// Resolve model ID using shared helper (AgentQA requires a model, so error if not found)
	effectiveModelID, err := s.resolveChatModelID(ctx, req, agentConfig.KnowledgeBases, agentConfig.KnowledgeIDs)
	if err != nil {
		return err
	}
	if effectiveModelID == "" {
		logger.Warnf(ctx, "No summary model configured for custom agent %s", req.CustomAgent.ID)
		return errors.New("summary model (model_id) is not configured in custom agent settings")
	}

	summaryModel, err := s.modelService.GetChatModel(ctx, effectiveModelID)
	if err != nil {
		logger.Warnf(ctx, "Failed to get chat model: %v", err)
		return fmt.Errorf("failed to get chat model: %w", err)
	}

	// Get rerank model from custom agent config only when knowledge_search can
	// actually run. A disabled KB scope makes all KB tools ineffective, so it
	// must not force users to configure an otherwise-unused rerank model.
	var rerankModel rerank.Reranker
	if agentRequiresRerankModel(req.CustomAgent) {
		// Rerank model is resolved purely from the agent config now.
		// We used to fall back to ConversationConfig.RerankModelID at
		// the tenant level, but that path encouraged "leave rerank
		// blank on the agent and inherit silently" which made debugging
		// retrieval quality a guessing game across tenant settings vs
		// agent settings. Forcing the agent to declare its own rerank
		// model puts the configuration where the user actually edits
		// the agent. If a Wiki-only agent doesn't need reranking,
		// agentRequiresRerankModel() below already lets it pass.
		rerankModelID := req.CustomAgent.Config.RerankModelID
		if rerankModelID == "" {
			logger.Warnf(ctx, "No rerank model configured for custom agent %s, but knowledge_search tool is enabled", req.CustomAgent.ID)
			return errors.New("rerank model is not configured: please set rerank_model_id on the agent")
		}

		rerankModel, err = s.modelService.GetRerankModel(ctx, rerankModelID)
		if err != nil {
			logger.Warnf(ctx, "Failed to get rerank model: %v", err)
			return fmt.Errorf("failed to get rerank model: %w", err)
		}
	} else {
		logger.Infof(ctx, "knowledge_search is unavailable for the effective agent scope, skipping rerank model initialization")
	}

	// Load multi-turn history directly from DB (the single source of truth).
	// AgentSteps on each historical assistant message are expanded into proper
	// assistant_with_tool_calls + tool messages so the model can see what was
	// tried last turn — except final_answer, which is replayed as the trailing
	// canonical assistant message.
	var llmContext []chat.Message
	if agentConfig.MultiTurnEnabled {
		historyTurns := agentConfig.HistoryTurns
		if historyTurns <= 0 {
			historyTurns = 5
		}
		llmContext, err = LoadAgentHistory(ctx, s.messageRepo, sessionID, historyTurns)
		if err != nil {
			logger.Warnf(ctx, "Failed to load agent history from DB: %v, continuing without history", err)
			llmContext = []chat.Message{}
		}
		logger.Infof(ctx, "Loaded %d history messages from DB (turns=%d)", len(llmContext), historyTurns)
	} else {
		logger.Infof(ctx, "Multi-turn disabled for this agent, running without history")
		llmContext = []chat.Message{}
	}

	// Create agent engine with EventBus
	logger.Info(ctx, "Creating agent engine")
	engine, err := s.agentService.CreateAgentEngine(
		ctx,
		agentConfig,
		summaryModel,
		rerankModel,
		eventBus,
		sessionID,
	)
	if err != nil {
		logger.Errorf(ctx, "Failed to create agent engine: %v", err)
		return err
	}

	// Route image data based on agent model's vision capability
	var agentModelSupportsVision bool
	if effectiveModelID != "" {
		if modelInfo, err := s.modelService.GetModelByID(ctx, effectiveModelID); err == nil && modelInfo != nil {
			agentModelSupportsVision = modelInfo.Parameters.SupportsVision
		}
	}

	agentQuery := req.Query
	var agentImageURLs []string
	if agentModelSupportsVision && len(req.ImageURLs) > 0 {
		agentImageURLs = req.ImageURLs
		logger.Infof(ctx, "Agent model supports vision, passing %d image(s) directly", len(agentImageURLs))
	} else if req.ImageDescription != "" {
		agentQuery = req.Query + "\n\n[用户上传图片内容]\n" + req.ImageDescription
		logger.Infof(ctx, "Agent model does not support vision, appending image description (%d chars)", len(req.ImageDescription))
	}
	if req.QuotedContext != "" {
		agentQuery += "\n\n" + req.QuotedContext
	}
	// Inject attachment content (documents, audio transcripts, etc.) so the agent
	// can see uploaded files. Mirrors the behavior of the KnowledgeQA pipeline
	// (see chat_pipeline/into_chat_message.go).
	if len(req.Attachments) > 0 {
		agentQuery += req.Attachments.BuildPrompt()
		logger.Infof(ctx, "Appended %d attachment(s) to agent query", len(req.Attachments))
	}

	// Scope envelopes (runtime_context / must_use) are injected per LLM call inside
	// the agent engine only; we intentionally do not persist them on user messages
	// so multi-turn history stays clean and is not skewed by stale @mention scope.

	// Execute agent with streaming (asynchronously)
	// Events will be emitted to EventBus and handled by the Handler layer
	logger.Info(ctx, "Executing agent with streaming")
	if _, err := engine.Execute(ctx, sessionID, req.AssistantMessageID, agentQuery, llmContext, agentImageURLs); err != nil {
		logger.Errorf(ctx, "Agent execution failed: %v", err)
		// Emit error event to the EventBus used by this agent
		eventBus.Emit(ctx, event.Event{
			Type:      event.EventError,
			SessionID: sessionID,
			Data: event.ErrorData{
				Error:     err.Error(),
				Stage:     "agent_execution",
				SessionID: sessionID,
			},
		})
	}
	// Return empty - events will be handled by Handler via EventBus subscription
	return nil
}

// buildAgentConfig creates a runtime AgentConfig from the QARequest's custom agent configuration,
// tenant info, and resolved knowledge bases / search targets.
func (s *sessionService) buildAgentConfig(
	ctx context.Context,
	req *types.QARequest,
	tenantInfo *types.Tenant,
	agentTenantID uint64,
) (*types.AgentConfig, error) {
	customAgent := req.CustomAgent
	agentConfig := &types.AgentConfig{
		MaxIterations:               customAgent.Config.MaxIterations,
		Temperature:                 customAgent.Config.Temperature,
		WebSearchEnabled:            customAgent.Config.WebSearchEnabled && req.WebSearchEnabled,
		WebSearchMaxResults:         customAgent.Config.WebSearchMaxResults,
		WebSearchProviderID:         customAgent.Config.WebSearchProviderID,
		MultiTurnEnabled:            customAgent.Config.MultiTurnEnabled,
		HistoryTurns:                customAgent.Config.HistoryTurns,
		MCPSelectionMode:            customAgent.Config.MCPSelectionMode,
		MCPServices:                 customAgent.Config.MCPServices,
		Thinking:                    customAgent.Config.Thinking,
		RetrieveKBOnlyWhenMentioned: customAgent.Config.RetrieveKBOnlyWhenMentioned,
		LLMCallTimeout:              customAgent.Config.LLMCallTimeout,
		RetainRetrievalHistory:      customAgent.Config.RetainRetrievalHistory,
	}

	// Falls back to global configuration if no specific timeout is set for the agent.
	if agentConfig.LLMCallTimeout == 0 && s.cfg.Agent != nil && s.cfg.Agent.LLMCallTimeout > 0 {
		agentConfig.LLMCallTimeout = s.cfg.Agent.LLMCallTimeout
	}

	// Configure skills based on CustomAgentConfig
	s.configureSkillsFromAgent(ctx, agentConfig, customAgent)

	// Resolve knowledge bases using shared helper
	agentConfig.KnowledgeBases, agentConfig.KnowledgeIDs = s.resolveKnowledgeBases(ctx, req)

	// Use custom agent's allowed tools if specified, otherwise use defaults
	if len(customAgent.Config.AllowedTools) > 0 {
		agentConfig.AllowedTools = customAgent.Config.AllowedTools
	} else {
		agentConfig.AllowedTools = tools.DefaultAllowedTools()
	}
	if len(req.SkillNames) > 0 {
		skillsMode := customAgent.Config.SkillsSelectionMode
		if skillsMode == "none" || skillsMode == "" {
			logger.Warnf(ctx, "Ignoring @skill mention: agent skills selection is disabled (mode=%s)", skillsMode)
		} else if agentConfig.SkillsEnabled {
			switch skillsMode {
			case "selected":
				agentConfig.AllowedSkills = intersectPreservingRequestOrder(req.SkillNames, agentConfig.AllowedSkills)
				if len(agentConfig.AllowedSkills) == 0 {
					agentConfig.SkillsEnabled = false
				}
			case "all":
				agentConfig.AllowedSkills = dedupPreservingOrder(req.SkillNames)
			}
			logger.Infof(ctx, "Applied per-request @skill scope: requested=%v effective=%v", req.SkillNames, agentConfig.AllowedSkills)
		}
	}
	if len(req.MCPServiceIDs) > 0 {
		if agentConfig.MCPSelectionMode == "none" {
			logger.Warnf(ctx, "Ignoring @MCP mention: agent MCP selection is disabled (mode=none)")
		} else {
			mentioned := dedupPreservingOrder(req.MCPServiceIDs)
			isSharedAgent := req.Session != nil && req.Session.TenantID != customAgent.TenantID
			effectiveMCP, mcpMode := resolvePerRequestMCPScope(
				mentioned,
				customAgent.Config.MCPServices,
				agentConfig.MCPSelectionMode,
				isSharedAgent,
			)
			if len(effectiveMCP) > 0 {
				agentConfig.MCPSelectionMode = mcpMode
				agentConfig.MCPServices = effectiveMCP
			} else if len(mentioned) > 0 {
				logger.Warnf(ctx, "Ignoring @MCP scope outside agent preset: requested=%v agent=%v shared=%v",
					req.MCPServiceIDs, customAgent.Config.MCPServices, isSharedAgent)
			}
			logger.Infof(ctx, "Applied per-request @MCP scope: requested=%v mode=%s effective=%v",
				req.MCPServiceIDs, agentConfig.MCPSelectionMode, agentConfig.MCPServices)
		}
	}

	// Use custom agent's system prompt if specified
	if customAgent.Config.SystemPrompt != "" {
		agentConfig.UseCustomSystemPrompt = true
		agentConfig.SystemPrompt = customAgent.Config.SystemPrompt
	}

	logger.Infof(ctx, "Custom agent config applied: MaxIterations=%d, Temperature=%.2f, AllowedTools=%v, WebSearchEnabled=%v",
		agentConfig.MaxIterations, agentConfig.Temperature, agentConfig.AllowedTools, agentConfig.WebSearchEnabled)

	// Set web search max results from tenant config if not set (default: 5)
	if agentConfig.WebSearchMaxResults == 0 {
		agentConfig.WebSearchMaxResults = 5
		if tenantInfo.WebSearchConfig != nil && tenantInfo.WebSearchConfig.MaxResults > 0 {
			agentConfig.WebSearchMaxResults = tenantInfo.WebSearchConfig.MaxResults
		}
	}

	// Resolve web search provider ID: agent-level > tenant default (is_default=true)
	if agentConfig.WebSearchProviderID == "" {
		if defaultProvider, err := s.webSearchProviderRepo.GetDefault(ctx, tenantInfo.ID); err == nil && defaultProvider != nil {
			agentConfig.WebSearchProviderID = defaultProvider.ID
		}
	}

	logger.Infof(ctx, "Merged agent config from tenant %d and session %s", tenantInfo.ID, req.Session.ID)

	// Log knowledge bases if present
	if len(agentConfig.KnowledgeBases) > 0 || len(req.TagScopes) > 0 {
		if len(agentConfig.KnowledgeBases) > 0 {
			logger.Infof(ctx, "Agent configured with %d knowledge base(s): %v",
				len(agentConfig.KnowledgeBases), agentConfig.KnowledgeBases)
		} else {
			logger.Infof(ctx, "Agent configured with %d tag-scoped search target(s)", len(req.TagScopes))
		}
	} else {
		logger.Infof(ctx, "No knowledge bases specified for agent, running in pure agent mode")
	}

	// Build search targets using agent's tenant (handler has validated access for shared agent)
	searchTargets, err := s.buildSearchTargets(ctx, agentTenantID, agentConfig.KnowledgeBases, agentConfig.KnowledgeIDs, req.TagScopes)
	if err != nil {
		logger.Warnf(ctx, "Failed to build search targets for agent: %v", err)
	}
	agentConfig.SearchTargets = searchTargets
	logger.Infof(ctx, "Agent search targets built: %d targets", len(searchTargets))

	if agentConfig.MaxContextTokens <= 0 {
		agentConfig.MaxContextTokens = types.DefaultMaxContextTokens
	}

	if len(agentConfig.MCPServices) > 0 && len(req.MCPServiceIDs) > 0 {
		agentConfig.PinnedMCPServiceIDs = intersectPreservingRequestOrder(req.MCPServiceIDs, agentConfig.MCPServices)
	}
	if len(req.SkillNames) > 0 && agentConfig.SkillsEnabled {
		if len(agentConfig.AllowedSkills) > 0 {
			agentConfig.PinnedSkillNames = intersectPreservingRequestOrder(req.SkillNames, agentConfig.AllowedSkills)
		} else if customAgent.Config.SkillsSelectionMode == "all" {
			agentConfig.PinnedSkillNames = dedupPreservingOrder(req.SkillNames)
		}
	}

	return agentConfig, nil
}

// resolvePerRequestMCPScope narrows MCP registration for a per-turn @mention.
// selectionMode "none" rejects all mentions. Shared agents never register MCP
// services outside the agent preset.
func resolvePerRequestMCPScope(
	mentioned, agentMCPs []string,
	selectionMode string,
	isSharedAgent bool,
) (effective []string, mode string) {
	if len(mentioned) == 0 {
		return nil, selectionMode
	}
	if isSharedAgent {
		mentioned = intersectPreservingRequestOrder(mentioned, agentMCPs)
		if len(mentioned) == 0 {
			return nil, selectionMode
		}
	}
	switch selectionMode {
	case "none":
		return nil, selectionMode
	case "selected":
		effective = intersectPreservingRequestOrder(mentioned, agentMCPs)
	case "all", "":
		effective = mentioned
	default:
		effective = mentioned
	}
	if len(effective) == 0 {
		return nil, selectionMode
	}
	return effective, "selected"
}

func intersectPreservingRequestOrder(requested []string, allowed []string) []string {
	allowedSet := make(map[string]bool, len(allowed))
	for _, value := range allowed {
		if value != "" {
			allowedSet[value] = true
		}
	}
	result := make([]string, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, value := range requested {
		if value == "" || seen[value] || !allowedSet[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func dedupPreservingOrder(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

// configureSkillsFromAgent configures skills settings in AgentConfig based on CustomAgentConfig
// Returns the skill directories and allowed skills based on the selection mode:
//   - "all": uses all preloaded skills
//   - "selected": uses the explicitly selected skills
//   - "none" or "": skills are disabled
func (s *sessionService) configureSkillsFromAgent(
	ctx context.Context,
	agentConfig *types.AgentConfig,
	customAgent *types.CustomAgent,
) {
	if customAgent == nil {
		return
	}
	// When sandbox is disabled, skills cannot be enabled (no script execution environment)
	sandboxMode := os.Getenv("WEKNORA_SANDBOX_MODE")
	if sandboxMode == "" || sandboxMode == "disabled" {
		agentConfig.SkillsEnabled = false
		agentConfig.SkillDirs = nil
		agentConfig.AllowedSkills = nil
		logger.Infof(ctx, "Sandbox is disabled: skills are not available")
		return
	}
	dir := getPreloadedSkillsDir()
	switch customAgent.Config.SkillsSelectionMode {
	case "all":
		// Enable all preloaded skills
		agentConfig.SkillsEnabled = true
		agentConfig.SkillDirs = []string{dir}
		agentConfig.AllowedSkills = nil // Empty means all skills allowed
		logger.Infof(ctx, "SkillsSelectionMode=all: enabled all preloaded skills")
	case "selected":
		// Enable only selected skills
		if len(customAgent.Config.SelectedSkills) > 0 {
			agentConfig.SkillsEnabled = true
			agentConfig.SkillDirs = []string{dir}
			agentConfig.AllowedSkills = customAgent.Config.SelectedSkills
			logger.Infof(ctx, "SkillsSelectionMode=selected: enabled %d selected skills: %v",
				len(customAgent.Config.SelectedSkills), customAgent.Config.SelectedSkills)
		} else {
			agentConfig.SkillsEnabled = false
			logger.Infof(ctx, "SkillsSelectionMode=selected but no skills selected: skills disabled")
		}
	case "none", "":
		// Skills disabled
		agentConfig.SkillsEnabled = false
		logger.Infof(ctx, "SkillsSelectionMode=%s: skills disabled", customAgent.Config.SkillsSelectionMode)
	default:
		// Unknown mode, disable skills
		agentConfig.SkillsEnabled = false
		logger.Warnf(ctx, "Unknown SkillsSelectionMode=%s: skills disabled", customAgent.Config.SkillsSelectionMode)
	}

}
