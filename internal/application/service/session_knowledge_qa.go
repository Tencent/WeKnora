package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/common"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/llmreference"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
)

type knowledgeQARuntimeProjection struct {
	prepared                 bool
	knowledgeBaseIDs         []string
	knowledgeIDs             []string
	searchTargets            types.SearchTargets
	hasLocalKnowledge        bool
	retrievalExplicitlyEmpty bool
}

func projectKnowledgeQARuntime(
	requestScope *types.KnowledgeScopeRequest,
	scope *types.KnowledgeScope,
	executionScopeHash string,
) (knowledgeQARuntimeProjection, error) {
	if scope == nil {
		if executionScopeHash != "" {
			return knowledgeQARuntimeProjection{},
				apperrors.NewBadRequestError("invalid knowledge scope")
		}
		return knowledgeQARuntimeProjection{}, nil
	}
	if executionScopeHash == "" {
		return knowledgeQARuntimeProjection{},
			apperrors.NewBadRequestError("invalid knowledge scope")
	}

	projection := knowledgeQARuntimeProjection{
		prepared:      true,
		searchTargets: types.ProjectKnowledgeScopeToSearchTargets(scope, executionScopeHash),
	}
	projection.hasLocalKnowledge = len(projection.searchTargets) > 0
	folderFilterEnabled := requestScope != nil &&
		requestScope.FolderScopes != nil
	folderFilterEnabled = folderFilterEnabled ||
		scope.HasEnabledNonEmptyFolderFilter()
	for _, target := range projection.searchTargets {
		if target == nil {
			continue
		}
		projection.knowledgeBaseIDs = append(
			projection.knowledgeBaseIDs,
			target.KnowledgeBaseID,
		)
		projection.knowledgeIDs = append(
			projection.knowledgeIDs,
			target.KnowledgeIDs...,
		)
	}
	projection.retrievalExplicitlyEmpty =
		folderFilterEnabled && !projection.hasLocalKnowledge
	return projection, nil
}

func buildKnowledgeQAPipeline(
	hasLocalKnowledge bool,
	webSearchEnabled bool,
	retrievalExplicitlyEmpty bool,
	hasHistory bool,
	dataAnalysisEnabled bool,
) []types.EventType {
	if retrievalExplicitlyEmpty ||
		(!hasLocalKnowledge && !webSearchEnabled) {
		return types.NewPipelineBuilder().
			AddIf(hasHistory, types.LOAD_HISTORY).
			Add(types.CHAT_COMPLETION_STREAM).
			Build()
	}
	return types.NewPipelineBuilder().
		AddIf(hasHistory, types.LOAD_HISTORY).
		Add(types.QUERY_UNDERSTAND).
		Add(types.CHUNK_SEARCH_PARALLEL).
		Add(types.CHUNK_RERANK).
		AddIf(webSearchEnabled, types.WEB_FETCH).
		Add(types.CHUNK_MERGE).
		Add(types.FILTER_TOP_K).
		AddIf(dataAnalysisEnabled, types.DATA_ANALYSIS).
		Add(types.INTO_CHAT_MESSAGE).
		Add(types.CHAT_COMPLETION_STREAM).
		Build()
}

func applyKnowledgeQARuntimeProjection(
	request *types.PipelineRequest,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
	searchTargets types.SearchTargets,
	executionScope *types.KnowledgeScope,
	executionScopeHash string,
	retrievalExplicitlyEmpty bool,
) {
	if request == nil {
		return
	}
	request.KnowledgeBaseIDs = append([]string(nil), knowledgeBaseIDs...)
	request.KnowledgeIDs = append([]string(nil), knowledgeIDs...)
	request.SearchTargets = make(types.SearchTargets, len(searchTargets))
	for index, target := range searchTargets {
		request.SearchTargets[index] = target.Clone()
	}
	request.ExecutionScope = executionScope.Clone()
	request.ExecutionScopeHash = executionScopeHash
	request.RetrievalExplicitlyEmpty = retrievalExplicitlyEmpty
}

// KnowledgeQA performs knowledge base question answering with LLM summarization
// Events are emitted through eventBus (references, answer chunks, completion)
// customAgent is optional - if provided, uses custom agent configuration for multiTurnEnabled and historyTurns
func (s *sessionService) KnowledgeQA(
	ctx context.Context,
	req *types.QARequest,
	eventBus *event.EventBus,
) error {
	if req.ExecutionScopeHash != "" {
		logger.Infof(
			ctx,
			"Prepared knowledge QA request, query length: %d, web enabled: %t, scope hash: %s",
			len(req.Query),
			req.WebSearchEnabled,
			knowledgeScopeHashPrefix(req.ExecutionScopeHash),
		)
	} else {
		logger.Infof(
			ctx,
			"Knowledge base question answering parameters, session ID: %s, query: %s, webSearchEnabled: %v",
			req.Session.ID,
			req.Query,
			req.WebSearchEnabled,
		)
	}

	// Span the request setup (KB / model resolution, search target building,
	// agent override application). This covers the visible gap between trace
	// start and the first stage observation in the Langfuse timeline.
	setupMetadata := map[string]interface{}{
		"session_id": req.Session.ID,
	}
	if req.ExecutionScopeHash != "" {
		setupMetadata = map[string]interface{}{
			"query_length": len(req.Query),
			"web_enabled":  req.WebSearchEnabled,
			"scope_hash_prefix": knowledgeScopeHashPrefix(
				req.ExecutionScopeHash,
			),
		}
	}
	setupCtx, setupSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name:     "qa.setup",
		Metadata: setupMetadata,
	})
	ctx = setupCtx

	var (
		knowledgeBaseIDs []string
		knowledgeIDs     []string
		searchTargets    types.SearchTargets
		err              error
	)
	runtimeProjection, err := projectKnowledgeQARuntime(
		req.RequestScope,
		req.ExecutionScope,
		req.ExecutionScopeHash,
	)
	if err != nil {
		return err
	}
	if runtimeProjection.prepared {
		knowledgeBaseIDs = append(
			knowledgeBaseIDs,
			runtimeProjection.knowledgeBaseIDs...,
		)
		knowledgeIDs = append(
			knowledgeIDs,
			runtimeProjection.knowledgeIDs...,
		)
		searchTargets = runtimeProjection.searchTargets
	} else {
		// Legacy callers remain unchanged until their Phase 4B2 integration.
		knowledgeBaseIDs, knowledgeIDs, err = s.resolveKnowledgeBases(ctx, req)
		if err != nil {
			return err
		}
	}

	// Resolve chat model ID using shared helper
	chatModelID, err := s.resolveChatModelID(ctx, req, knowledgeBaseIDs, knowledgeIDs)
	if err != nil {
		return err
	}

	// Initialize ChatManage defaults from config.yaml
	summaryConfig := types.SummaryConfig{
		Prompt:              s.cfg.Conversation.Summary.Prompt,
		ContextTemplate:     s.cfg.Conversation.Summary.ContextTemplate,
		Temperature:         s.cfg.Conversation.Summary.Temperature,
		NoMatchPrefix:       s.cfg.Conversation.Summary.NoMatchPrefix,
		MaxCompletionTokens: s.cfg.Conversation.Summary.MaxCompletionTokens,
		Thinking:            s.cfg.Conversation.Summary.Thinking,
	}
	fallbackStrategy := types.FallbackStrategy(s.cfg.Conversation.FallbackStrategy)
	if fallbackStrategy == "" {
		fallbackStrategy = types.FallbackStrategyFixed
		logger.Infof(ctx, "Fallback strategy not set, using default: %v", fallbackStrategy)
	}

	// Resolve chat model vision capability and VLM model ID for image routing
	var chatModelSupportsVision bool
	var vlmModelID string
	if chatModelID != "" {
		if chatModelInfo, err := s.modelService.GetModelByID(ctx, chatModelID); err == nil && chatModelInfo != nil {
			chatModelSupportsVision = chatModelInfo.Parameters.SupportsVision
		}
	}
	if req.CustomAgent != nil {
		vlmModelID = req.CustomAgent.Config.VLMModelID
	}

	// Resolve retrieval tenant scope using shared helper
	retrievalTenantID := s.resolveRetrievalTenantID(ctx, req)

	if !runtimeProjection.prepared {
		// Build unified search targets for legacy Agent/IM callers only.
		searchTargets, err = s.buildSearchTargets(
			ctx,
			retrievalTenantID,
			knowledgeBaseIDs,
			knowledgeIDs,
			req.TagScopes,
		)
		if err != nil {
			return fmt.Errorf("build search targets: %w", err)
		}
	}

	// Create chat management object with session settings
	logger.Infof(
		ctx,
		"Creating chat manage object, knowledge bases: %d, knowledge: %d, chat model set: %t, search targets: %d, execution hash: %s",
		len(knowledgeBaseIDs),
		len(knowledgeIDs),
		chatModelID != "",
		len(searchTargets),
		knowledgeScopeHashPrefix(req.ExecutionScopeHash),
	)

	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:                   req.Query,
			SessionID:               req.Session.ID,
			UserID:                  types.SessionOwnerIDFromContext(ctx),
			MaxRounds:               s.cfg.Conversation.MaxRounds,
			VectorThreshold:         s.cfg.Conversation.VectorThreshold,
			KeywordThreshold:        s.cfg.Conversation.KeywordThreshold,
			EmbeddingTopK:           s.cfg.Conversation.EmbeddingTopK,
			RerankTopK:              s.cfg.Conversation.RerankTopK,
			RerankThreshold:         s.cfg.Conversation.RerankThreshold,
			ChatModelID:             chatModelID,
			SummaryConfig:           summaryConfig,
			FallbackStrategy:        fallbackStrategy,
			FallbackResponse:        s.cfg.Conversation.FallbackResponse,
			FallbackPrompt:          s.cfg.Conversation.FallbackPrompt,
			EnableRewrite:           s.cfg.Conversation.EnableRewrite,
			EnableQueryExpansion:    s.cfg.Conversation.EnableQueryExpansion,
			RewritePromptSystem:     s.cfg.Conversation.RewritePromptSystem,
			RewritePromptUser:       s.cfg.Conversation.RewritePromptUser,
			WebSearchEnabled:        req.WebSearchEnabled,
			WebSearchProviderID:     s.resolveWebSearchProviderID(ctx, req, retrievalTenantID),
			WebSearchMaxResults:     s.resolveWebSearchMaxResults(ctx, req),
			WebFetchEnabled:         s.resolveWebFetchEnabled(req),
			WebFetchTopN:            s.resolveWebFetchTopN(req),
			TenantID:                retrievalTenantID,
			Images:                  req.ImageURLs,
			VLMModelID:              vlmModelID,
			ChatModelSupportsVision: chatModelSupportsVision,
			Attachments:             req.Attachments,
			Language:                types.LanguageNameFromContext(ctx),
		},
		PipelineState: types.PipelineState{
			RewriteQuery:     req.Query,
			ImageDescription: req.ImageDescription,
			QuotedContext:    req.QuotedContext,
		},
		PipelineContext: types.PipelineContext{
			EventBus:      eventBus.AsEventBusInterface(),
			MessageID:     req.AssistantMessageID,
			UserMessageID: req.UserMessageID,
		},
	}
	applyKnowledgeQARuntimeProjection(
		&chatManage.PipelineRequest,
		knowledgeBaseIDs,
		knowledgeIDs,
		searchTargets,
		req.ExecutionScope,
		req.ExecutionScopeHash,
		runtimeProjection.retrievalExplicitlyEmpty,
	)

	// Apply custom agent overrides (system prompt, temperature, retrieval params,
	// rewrite, fallback, FAQ strategy, history turns)
	s.applyAgentOverridesToChatManage(ctx, req.CustomAgent, chatManage)

	// Determine pipeline based on the effective knowledge retrieval scope and
	// web search setting. Tag-only mentions leave the raw KB/knowledge ID slices
	// empty but produce SearchTargets, so the unified targets must participate in
	// this decision or the request is incorrectly downgraded to pure chat.
	hasKB := types.HasKnowledgeRetrievalScope(
		searchTargets,
		knowledgeBaseIDs,
		knowledgeIDs,
	)
	if runtimeProjection.prepared {
		hasKB = runtimeProjection.hasLocalKnowledge
	}
	needsRAG := hasKB || req.WebSearchEnabled
	if runtimeProjection.retrievalExplicitlyEmpty {
		needsRAG = false
	}
	hasHistory := chatManage.MaxRounds > 0

	if !needsRAG {
		// Pure chat — no retrieval needed.
		userContent := req.Query
		if req.ImageDescription != "" && !chatModelSupportsVision {
			userContent += "\n\n[用户上传图片内容]\n" + req.ImageDescription
		}
		if req.QuotedContext != "" {
			userContent += "\n\n" + req.QuotedContext
		}
		// Inject attachment content for pure-chat path (RAG path handles this in INTO_CHAT_MESSAGE).
		if len(req.Attachments) > 0 {
			userContent += req.Attachments.BuildPrompt()
		}
		chatManage.UserContent = userContent

	}
	pipeline := buildKnowledgeQAPipeline(
		hasKB,
		req.WebSearchEnabled,
		runtimeProjection.retrievalExplicitlyEmpty,
		hasHistory,
		chatManage.DataAnalysisEnabled,
	)

	logger.Infof(ctx, "Assembled pipeline (%d stages), hasKB=%v, webSearch=%v, history=%v",
		len(pipeline), hasKB, req.WebSearchEnabled, hasHistory)

	// Start knowledge QA event processing (set session tenant so pipeline session/message lookups use session owner)
	ctx = context.WithValue(ctx, types.SessionTenantIDContextKey, req.Session.TenantID)
	logger.Info(ctx, "Triggering question answering event")
	setupSpan.Finish(map[string]interface{}{
		"stages":               len(pipeline),
		"knowledge_base_count": len(knowledgeBaseIDs),
		"search_targets":       len(searchTargets),
		"scope_hash_prefix":    knowledgeScopeHashPrefix(req.ExecutionScopeHash),
	}, nil, nil)
	err = s.KnowledgeQAByEvent(ctx, chatManage, pipeline)
	if err != nil {
		if req.ExecutionScopeHash != "" {
			logger.WarnWithFields(ctx, logger.Fields{
				"scope_hash_prefix": knowledgeScopeHashPrefix(
					req.ExecutionScopeHash,
				),
			}, "prepared knowledge QA failed")
		} else {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"session_id": req.Session.ID,
			})
		}
		return err
	}

	// Note: Answer events are now emitted directly by chat_completion_stream plugin
	// Completion event will be emitted when the last answer event has Done=true
	// We can optionally add a completion watcher here if needed, but for now
	// the frontend can detect completion from the Done flag

	logger.Info(ctx, "Knowledge base question answering initiated")
	return nil
}

// selectChatModelID selects the appropriate chat model ID with priority for Remote models
// Priority order:
// 1. Session's SummaryModelID if it's a Remote model
// 2. First knowledge base with a Remote model (from knowledgeBaseIDs or derived from knowledgeIDs)
// 3. Session's SummaryModelID (if not Remote)
// 4. First knowledge base's SummaryModelID
func (s *sessionService) selectChatModelID(
	ctx context.Context,
	session *types.Session,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
) (string, error) {
	// If no knowledge base IDs but have knowledge IDs, derive KB IDs from knowledge IDs (include shared KB files)
	if len(knowledgeBaseIDs) == 0 && len(knowledgeIDs) > 0 {
		tenantID := types.MustTenantIDFromContext(ctx)
		knowledgeList, err := s.knowledgeService.GetKnowledgeBatchWithSharedAccess(ctx, tenantID, knowledgeIDs)
		if err != nil {
			logger.Warn(ctx, "Failed to get knowledge batch for model selection")
		} else {
			// Collect unique KB IDs from knowledge items
			kbIDSet := make(map[string]bool)
			for _, k := range knowledgeList {
				if k != nil && k.KnowledgeBaseID != "" {
					kbIDSet[k.KnowledgeBaseID] = true
				}
			}
			for kbID := range kbIDSet {
				knowledgeBaseIDs = append(knowledgeBaseIDs, kbID)
			}
			logger.Infof(ctx, "Derived %d knowledge base IDs from %d knowledge IDs for model selection",
				len(knowledgeBaseIDs), len(knowledgeIDs))
		}
	}
	// Check knowledge bases for models
	if len(knowledgeBaseIDs) > 0 {
		// Try to find a knowledge base with Remote model
		for _, kbID := range knowledgeBaseIDs {
			kb, err := s.knowledgeBaseService.GetKnowledgeBaseByID(ctx, kbID)
			if err != nil {
				logger.Warn(ctx, "Failed to get knowledge base for model selection")
				continue
			}
			if kb != nil && kb.SummaryModelID != "" {
				model, err := s.modelService.GetModelByID(ctx, kb.SummaryModelID)
				if err == nil && model != nil && model.Source == types.ModelSourceRemote {
					logger.Info(ctx, "Using Remote summary model from knowledge base")
					return kb.SummaryModelID, nil
				}
			}
		}

		// If no Remote model found, use first knowledge base's model
		kb, err := s.knowledgeBaseService.GetKnowledgeBaseByID(ctx, knowledgeBaseIDs[0])
		if err != nil {
			logger.Error(ctx, "Failed to get knowledge base for model selection")
			return "", fmt.Errorf("failed to get knowledge base: %w", err)
		}
		if kb != nil && kb.SummaryModelID != "" {
			logger.Info(ctx, "Using summary model from first knowledge base")
			return kb.SummaryModelID, nil
		}
	}

	// No knowledge bases - try to find any available chat model
	models, err := s.modelService.ListModels(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list models")
		return "", fmt.Errorf("failed to list models: %w", err)
	}
	for _, model := range models {
		if model != nil && model.Type == types.ModelTypeKnowledgeQA {
			logger.Info(ctx, "Using first available KnowledgeQA model")
			return model.ID, nil
		}
	}

	logger.Error(ctx, "No chat model ID available")
	return "", fmt.Errorf("no chat model ID available: no knowledge bases configured and no available models")
}

// resolveKnowledgeBasesFromAgent resolves knowledge base IDs based on agent's KBSelectionMode.
// sessionTenantID is the tenant of the current session (caller); it is compared with
// customAgent.TenantID to detect the shared-agent scenario and avoid leaking the
// current user's personal shared KBs into the agent's retrieval scope.
//
// Returns the resolved knowledge base IDs based on the selection mode:
//   - "all": fetches all knowledge bases for the tenant
//   - "selected": uses the explicitly configured knowledge bases
//   - "none": returns empty slice
//   - default: falls back to configured knowledge bases for backward compatibility
func (s *sessionService) resolveKnowledgeBasesFromAgent(
	ctx context.Context,
	customAgent *types.CustomAgent,
	sessionTenantID uint64,
) []string {
	knowledgeBaseIDs, err := s.resolveKnowledgeBasesFromAgentStrict(
		ctx,
		customAgent,
		sessionTenantID,
	)
	if err != nil {
		logger.Warn(ctx, "Failed to resolve agent knowledge bases")
		return nil
	}
	return knowledgeBaseIDs
}

func (s *sessionService) resolveKnowledgeBasesFromAgentStrict(
	ctx context.Context,
	customAgent *types.CustomAgent,
	sessionTenantID uint64,
) ([]string, error) {
	if customAgent == nil {
		return nil, nil
	}

	switch customAgent.Config.KBSelectionMode {
	case "all":
		// Authoritative capability filter for the runtime path. The frontend
		// editor and @mention dropdown apply the same filter, but we don't
		// trust the client here: a stale session payload or API caller could
		// still ask us to retrieve against an incompatible KB and we'd rather
		// just drop it (and log) than feed it to tools that would no-op.
		capFilter := tools.DeriveKBFilterForAgent(customAgent.Config.AgentMode, customAgent.Config.AllowedTools)
		accept := func(kb *types.KnowledgeBase) bool {
			if kb == nil {
				return false
			}
			if capFilter.IsEmpty() {
				return true
			}
			return tools.KBSatisfiesAgentRequirements(kb.Capabilities(), customAgent.Config.AgentMode, customAgent.Config.AllowedTools)
		}

		// Get own knowledge bases (uses ctx TenantID = agent's tenant)
		allKBs, err := s.knowledgeBaseService.ListKnowledgeBases(ctx)
		if err != nil {
			return nil, err
		}
		kbIDSet := make(map[string]bool)
		kbIDs := make([]string, 0, len(allKBs))
		ownSkipped := 0
		for _, kb := range allKBs {
			if !accept(kb) {
				ownSkipped++
				continue
			}
			kbIDs = append(kbIDs, kb.ID)
			kbIDSet[kb.ID] = true
		}

		// For shared agents (session tenant != agent tenant), only use the agent
		// tenant's own KBs. Including the current user's shared KBs would leak
		// unrelated KBs from other organisations into the agent's retrieval scope.
		isSharedAgent := sessionTenantID != 0 && sessionTenantID != customAgent.TenantID
		sharedSkipped := 0
		if !isSharedAgent {
			tenantID := types.MustTenantIDFromContext(ctx)
			userIDVal := ctx.Value(types.UserIDContextKey)
			if userIDVal != nil {
				if userID, ok := userIDVal.(string); ok && userID != "" && s.kbShareService != nil {
					callerTenantRole := types.TenantRoleFromContext(ctx)
					sharedList, err := s.kbShareService.ListSharedKnowledgeBases(ctx, tenantID, callerTenantRole)
					if err != nil {
						return nil, err
					}
					for _, info := range sharedList {
						if info == nil || info.KnowledgeBase == nil || kbIDSet[info.KnowledgeBase.ID] {
							continue
						}
						if !accept(info.KnowledgeBase) {
							sharedSkipped++
							continue
						}
						kbIDs = append(kbIDs, info.KnowledgeBase.ID)
						kbIDSet[info.KnowledgeBase.ID] = true
					}
				}
			}
		} else {
			logger.Info(ctx, "Shared agent detected: skipping caller-shared knowledge bases")
		}

		if ownSkipped+sharedSkipped > 0 {
			logger.Infof(ctx,
				"KBSelectionMode=all: tool-capability filter removed %d own + %d shared knowledge bases",
				ownSkipped, sharedSkipped)
		}
		logger.Infof(ctx, "KBSelectionMode=all: loaded %d knowledge bases (own + shared)", len(kbIDs))
		return kbIDs, nil
	case "selected":
		logger.Infof(ctx, "KBSelectionMode=selected: using %d configured knowledge bases", len(customAgent.Config.KnowledgeBases))
		return append([]string(nil), customAgent.Config.KnowledgeBases...), nil
	case "none":
		logger.Infof(ctx, "KBSelectionMode=none: no knowledge bases configured")
		return nil, nil
	default:
		// Default to "selected" behavior for backward compatibility
		if len(customAgent.Config.KnowledgeBases) > 0 {
			logger.Infof(ctx, "KBSelectionMode not set: using %d configured knowledge bases", len(customAgent.Config.KnowledgeBases))
		}
		return append([]string(nil), customAgent.Config.KnowledgeBases...), nil
	}
}

// buildSearchTargets computes the unified search targets from knowledgeBaseIDs and knowledgeIDs.
// tenantID is the retrieval scope: session.TenantID or effective tenant from shared agent (set by handler).
// This is called once at the request entry point to avoid repeated queries later in the pipeline.
// Logic:
//   - For each knowledgeBaseID: resolve actual TenantID (own, org-shared, or in retrieval-tenant scope for shared agent)
//   - For each knowledgeID: find its knowledgeBaseID; if the KB is already in the list, skip; otherwise add SearchTargetTypeKnowledge
func (s *sessionService) buildSearchTargets(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
	tagScopes []types.TagScope,
) (types.SearchTargets, error) {
	var targets types.SearchTargets
	tagIDsByKB := mergeTagScopesByKB(tagScopes)

	// Build a map from KB ID to TenantID for all KBs we need to process
	kbTenantMap := make(map[string]uint64)

	// Track which KBs are fully searched
	fullKBSet := make(map[string]bool)

	// First pass: batch-fetch KBs, then resolve tenant per ID (tenant scope already set by caller)
	callerTenantRole := types.TenantRoleFromContext(ctx)
	kbIDsToFetch := append([]string(nil), knowledgeBaseIDs...)
	for kbID := range tagIDsByKB {
		kbIDsToFetch = append(kbIDsToFetch, kbID)
	}
	kbIDsToFetch = uniqueNonEmptyStrings(kbIDsToFetch)

	kbByID := make(map[string]*types.KnowledgeBase)
	if len(kbIDsToFetch) > 0 {
		kbs, kbFetchErr := s.knowledgeBaseService.GetKnowledgeBasesByIDsOnly(ctx, kbIDsToFetch)
		if kbFetchErr != nil {
			logger.Warnf(ctx, "Failed to fetch knowledge bases for search targets: %v", kbFetchErr)
		}
		for _, kb := range kbs {
			if kb != nil {
				kbByID[kb.ID] = kb
			}
		}
	}
	userID, _ := types.UserIDFromContext(ctx)
	resolveKBTenant := func(kbID string) uint64 {
		if kbTenantMap[kbID] != 0 {
			return kbTenantMap[kbID]
		}
		kb := kbByID[kbID]
		if kb == nil {
			kbTenantMap[kbID] = tenantID
		} else if kb.TenantID == tenantID {
			kbTenantMap[kbID] = tenantID
		} else if s.kbShareService != nil && userID != "" {
			hasAccess, _ := s.kbShareService.HasTenantKBPermission(ctx, kbID, tenantID, callerTenantRole, types.OrgRoleViewer)
			if hasAccess {
				kbTenantMap[kbID] = kb.TenantID
			} else {
				kbTenantMap[kbID] = tenantID
			}
		} else {
			kbTenantMap[kbID] = tenantID
		}
		return kbTenantMap[kbID]
	}

	if len(knowledgeBaseIDs) > 0 {
		for _, kbID := range knowledgeBaseIDs {
			fullKBSet[kbID] = true
			kbTenant := resolveKBTenant(kbID)
			if len(tagIDsByKB[kbID]) > 0 {
				continue
			}
			targets = append(targets, &types.SearchTarget{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: kbID,
				TenantID:        kbTenant,
			})
		}
	}

	kbToKnowledgeIDs := make(map[string][]string)

	// Process individual knowledge IDs (include shared KB files the user has access to)
	if len(knowledgeIDs) > 0 {
		knowledgeList, err := s.knowledgeService.GetKnowledgeBatchWithSharedAccess(ctx, tenantID, knowledgeIDs)
		if err != nil {
			logger.Warnf(ctx, "Failed to get knowledge batch for search targets: %v", err)
			return targets, nil // Return what we have, don't fail
		}

		// Group knowledge IDs by their KB, excluding those already covered by full KB search
		// Also track KB tenant IDs from knowledge items
		for _, k := range knowledgeList {
			if k == nil || k.KnowledgeBaseID == "" {
				continue
			}
			// Track KB -> TenantID mapping from knowledge items
			if kbTenantMap[k.KnowledgeBaseID] == 0 {
				kbTenantMap[k.KnowledgeBaseID] = k.TenantID
			}
			// Skip if this KB is already fully searched without a tag scope.
			if fullKBSet[k.KnowledgeBaseID] && len(tagIDsByKB[k.KnowledgeBaseID]) == 0 {
				continue
			}
			kbToKnowledgeIDs[k.KnowledgeBaseID] = append(kbToKnowledgeIDs[k.KnowledgeBaseID], k.ID)
		}

		// Create SearchTargetTypeKnowledge targets for each KB with specific files
		for kbID, kidList := range kbToKnowledgeIDs {
			if len(tagIDsByKB[kbID]) > 0 {
				continue
			}
			kbTenant := kbTenantMap[kbID]
			if kbTenant == 0 {
				kbTenant = tenantID // fallback
			}
			targets = append(targets, &types.SearchTarget{
				Type:                    types.SearchTargetTypeKnowledge,
				KnowledgeBaseID:         kbID,
				TenantID:                kbTenant,
				KnowledgeIDs:            kidList,
				DisableRecallThresholds: true,
			})
		}
	}

	for kbID, tagIDs := range tagIDsByKB {
		if kbID == "" || len(tagIDs) == 0 {
			continue
		}
		kbTenant := resolveKBTenant(kbID)
		kb := kbByID[kbID]
		explicitKnowledgeIDs := uniqueNonEmptyStrings(kbToKnowledgeIDs[kbID])

		useDocumentTagResolution := kb == nil || kb.Type != types.KnowledgeBaseTypeFAQ
		if kb == nil {
			logger.Warnf(ctx, "Knowledge base metadata missing for tag scope, kb_id=%s, using document tag resolution", kbID)
		}
		if useDocumentTagResolution {
			tagKnowledgeIDs, err := s.knowledgeService.ListKnowledgeIDsByTagIDs(ctx, kbTenant, kbID, tagIDs)
			if err != nil {
				return nil, fmt.Errorf("resolve knowledge IDs for tag scope kb_id=%s: %w", kbID, err)
			}
			if len(explicitKnowledgeIDs) > 0 {
				tagKnowledgeIDs = intersectStrings(tagKnowledgeIDs, explicitKnowledgeIDs)
			}
			tagKnowledgeIDs = uniqueNonEmptyStrings(tagKnowledgeIDs)
			if len(tagKnowledgeIDs) == 0 {
				continue
			}
			targets = append(targets, &types.SearchTarget{
				Type:                    types.SearchTargetTypeKnowledge,
				KnowledgeBaseID:         kbID,
				TenantID:                kbTenant,
				KnowledgeIDs:            tagKnowledgeIDs,
				ScopeTagIDs:             append([]string(nil), tagIDs...),
				DisableRecallThresholds: true,
			})
			continue
		}

		target := &types.SearchTarget{
			Type:                    types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:         kbID,
			TenantID:                kbTenant,
			TagIDs:                  append([]string(nil), tagIDs...),
			ScopeTagIDs:             append([]string(nil), tagIDs...),
			DisableRecallThresholds: true,
		}
		if len(explicitKnowledgeIDs) > 0 {
			target.Type = types.SearchTargetTypeKnowledge
			target.KnowledgeIDs = explicitKnowledgeIDs
			target.DisableRecallThresholds = true
		}
		targets = append(targets, target)
	}

	logger.Infof(ctx, "Built %d search targets: %d full KB, %d partial/tag KB, kbTenantMap=%v",
		len(targets), len(knowledgeBaseIDs), len(targets)-len(knowledgeBaseIDs), kbTenantMap)

	return targets, nil
}

func mergeTagScopesByKB(scopes []types.TagScope) map[string][]string {
	byKB := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	for _, scope := range scopes {
		if scope.KnowledgeBaseID == "" {
			continue
		}
		if seen[scope.KnowledgeBaseID] == nil {
			seen[scope.KnowledgeBaseID] = make(map[string]bool)
		}
		for _, tagID := range scope.TagIDs {
			if tagID == "" || seen[scope.KnowledgeBaseID][tagID] {
				continue
			}
			seen[scope.KnowledgeBaseID][tagID] = true
			byKB[scope.KnowledgeBaseID] = append(byKB[scope.KnowledgeBaseID], tagID)
		}
	}
	return byKB
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func intersectStrings(left []string, right []string) []string {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	rightSet := make(map[string]bool, len(right))
	for _, value := range right {
		rightSet[value] = true
	}
	out := make([]string, 0)
	for _, value := range left {
		if rightSet[value] {
			out = append(out, value)
		}
	}
	return out
}

// KnowledgeQAByEvent processes knowledge QA through a series of events in the pipeline
func (s *sessionService) KnowledgeQAByEvent(ctx context.Context,
	chatManage *types.ChatManage, eventList []types.EventType,
) error {
	logger.Info(ctx, "Start processing knowledge base question answering through events")
	if chatManage.ExecutionScopeHash != "" {
		logger.Infof(
			ctx,
			"Prepared knowledge QA parameters, query length: %d, scope hash: %s",
			len(chatManage.Query),
			knowledgeScopeHashPrefix(chatManage.ExecutionScopeHash),
		)
	} else {
		logger.Infof(ctx, "Knowledge base question answering parameters, session ID: %s, query: %s",
			chatManage.SessionID, chatManage.Query)
	}

	if chatManage.ExecutionScopeHash != "" {
		logger.Infof(ctx, "Trigger prepared event count: %d", len(eventList))
	} else {
		methods := make([]string, len(eventList))
		for i, event := range eventList {
			methods[i] = string(event)
		}
		logger.Infof(ctx, "Trigger event list: %v", methods)
	}

	pipelineStart := time.Now()
	lastRetrievalStage := chatpipeline.LastConsolidatedRetrievalStage(eventList, chatManage)
	var retrievalProgress *chatpipeline.StageProgress
	var retrievalStart time.Time
	var understandProgress *chatpipeline.StageProgress
	var understandStart time.Time
	for _, eventType := range eventList {
		stageStart := time.Now()
		// Wrap each pipeline stage in a Langfuse span so the trace timeline
		// shows the gaps between LLM/embedding/rerank generations (the work
		// that happens between them — vector DB search, merge, filter, prompt
		// assembly — was previously invisible). Generations created inside
		// the stage automatically nest under this span.
		//
		// CHAT_COMPLETION_STREAM is intentionally skipped: its OnEvent kicks
		// off a streaming goroutine and returns immediately, so a span would
		// finish well before the chat.completion.stream generation does. The
		// generation already captures the full stream duration; adding a
		// stage span here would just produce a child observation that
		// visually exceeds its parent.
		stageCtx := ctx
		var stageSpan *langfuse.Span
		if eventType != types.CHAT_COMPLETION_STREAM {
			stageMetadata := map[string]interface{}{
				"event_type": string(eventType),
				"session_id": chatManage.SessionID,
			}
			if chatManage.ExecutionScopeHash != "" {
				stageMetadata = map[string]interface{}{
					"event_type": string(eventType),
					"scope_hash_prefix": knowledgeScopeHashPrefix(
						chatManage.ExecutionScopeHash,
					),
				}
			}
			stageCtx, stageSpan = langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
				Name:     "pipeline." + string(eventType),
				Metadata: stageMetadata,
			})
		}
		if eventType == types.QUERY_UNDERSTAND && chatpipeline.ShouldEmitQueryUnderstandProgress(chatManage) {
			understandStart = stageStart
			understandProgress = chatpipeline.BeginQueryUnderstandProgress(stageCtx, chatManage)
		}
		if chatpipeline.IsConsolidatedRetrievalStage(eventType, chatManage) && retrievalProgress == nil {
			retrievalStart = stageStart
			retrievalProgress = chatpipeline.BeginRetrievalProgress(stageCtx, chatManage)
		}
		// Emit references before answer streaming so the SSE client receives
		// them while the connection is still open. Previously references were
		// emitted after the pipeline returned — by then the `complete` event had
		// already closed the stream, so the frontend only saw citations on refresh.
		if eventType == types.CHAT_COMPLETION_STREAM {
			emitKnowledgeReferencesEvent(ctx, chatManage)
		}
		err := s.eventManager.Trigger(stageCtx, eventType, chatManage)
		if understandProgress != nil && eventType == types.QUERY_UNDERSTAND {
			chatpipeline.EndQueryUnderstandProgress(stageCtx, chatManage, understandProgress, understandStart, err)
			understandProgress = nil
		}
		// Close the consolidated retrieval progress window as soon as retrieval
		// is done: either the planned last retrieval stage completed, or a
		// retrieval stage short-circuited the pipeline (ErrSearchNothing or a
		// hard error). The early returns below (fallback / stage_failed) would
		// otherwise skip EndRetrievalProgress, leaving the "knowledge_search"
		// tool_call pending — so the frontend keeps spinning on "正在检索知识库"
		// forever even though the fallback answer has already streamed.
		if retrievalProgress != nil && chatpipeline.ShouldCloseRetrievalProgress(eventType, lastRetrievalStage, err) {
			chatpipeline.EndRetrievalProgress(stageCtx, chatManage, retrievalProgress, retrievalStart, err)
			retrievalProgress = nil
		}
		stageDuration := time.Since(stageStart)
		var spanErr error
		if err != nil && err != chatpipeline.ErrSearchNothing {
			if chatManage.ExecutionScopeHash != "" {
				spanErr = stderrors.New("prepared pipeline stage failed")
			} else {
				spanErr = err.Err
			}
		}
		if stageSpan != nil {
			stageSpan.Finish(map[string]interface{}{
				"duration_ms": stageDuration.Milliseconds(),
			}, nil, spanErr)
		}

		// If the user stopped generation, the context is cancelled. A cancelled
		// retrieval stage surfaces as ErrSearchNothing (the search goroutines
		// return no results when their embedding/vector calls are aborted), so
		// this check MUST come before the ErrSearchNothing handling below.
		// Otherwise we would persist the fixed fallback response ("Sorry, I am
		// unable to answer this question.") over the intentionally-empty stopped
		// message, and the user would see the fallback text after refreshing.
		// This is not single-machine specific: the stop arrives via the shared
		// StreamManager and cancels asyncCtx on whichever node is generating.
		if ctxErr := ctx.Err(); ctxErr != nil {
			common.PipelineWarn(ctx, "Pipeline", "stage_cancelled", map[string]interface{}{
				"event":       string(eventType),
				"duration_ms": stageDuration.Milliseconds(),
				"reason":      ctxErr.Error(),
			})
			return ctxErr
		}

		if err == chatpipeline.ErrSearchNothing {
			common.PipelineWarn(ctx, "Pipeline", "stage_fallback", map[string]interface{}{
				"event":       string(eventType),
				"duration_ms": stageDuration.Milliseconds(),
				"reason":      "search_nothing",
				"strategy":    string(chatManage.FallbackStrategy),
			})
			s.handleFallbackResponse(ctx, chatManage)
			return nil
		}

		if err != nil {
			common.PipelineError(ctx, "Pipeline", "stage_failed", map[string]interface{}{
				"event":       string(eventType),
				"duration_ms": stageDuration.Milliseconds(),
				"error_type":  err.ErrorType,
				"description": err.Description,
			})
			return err.Err
		}

		common.PipelineInfo(ctx, "Pipeline", "stage_complete", map[string]interface{}{
			"event":       string(eventType),
			"duration_ms": stageDuration.Milliseconds(),
		})
	}

	common.PipelineInfo(ctx, "Pipeline", "all_stages_complete", map[string]interface{}{
		"session_id":        chatManage.SessionID,
		"total_stages":      len(eventList),
		"total_duration_ms": time.Since(pipelineStart).Milliseconds(),
	})
	return nil
}

// SearchKnowledge performs knowledge base search without LLM summarization
// knowledgeBaseIDs: list of knowledge base IDs to search (supports multi-KB)
// knowledgeIDs: list of specific knowledge (file) IDs to search
func (s *sessionService) SearchKnowledge(ctx context.Context,
	knowledgeBaseIDs []string, knowledgeIDs []string, tagScopes []types.TagScope, query string,
) ([]*types.SearchResult, error) {
	logger.Info(ctx, "Start knowledge base search without LLM summary")
	logger.Infof(ctx, "Knowledge base search parameters, knowledge base IDs: %v, knowledge IDs: %v, tag scopes: %d, query: %s",
		knowledgeBaseIDs, knowledgeIDs, len(tagScopes), query)

	// Get tenant ID from context
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		logger.Error(ctx, "Failed to get tenant ID from context")
		return nil, fmt.Errorf("workspace ID not found in context")
	}

	// Build unified search targets (computed once, used throughout pipeline)
	searchTargets, err := s.buildSearchTargets(ctx, tenantID, knowledgeBaseIDs, knowledgeIDs, tagScopes)
	if err != nil {
		return nil, fmt.Errorf("build search targets: %w", err)
	}
	results, err := s.searchKnowledgeByTargets(
		ctx,
		query,
		knowledgeBaseIDs,
		knowledgeIDs,
		searchTargets,
		nil,
		"",
	)
	return results, err
}

// SearchKnowledgeWithScope executes HTTP search from one prepared scope.
func (s *sessionService) SearchKnowledgeWithScope(
	ctx context.Context,
	query string,
	preparation *types.KnowledgeScopePreparation,
) ([]*types.SearchResult, error) {
	if ctx == nil {
		return nil, apperrors.NewBadRequestError("invalid knowledge scope")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if preparation == nil {
		return nil, apperrors.NewBadRequestError("invalid knowledge scope")
	}
	execution := preparation.Execution()
	if execution == nil {
		return nil, apperrors.NewBadRequestError("invalid knowledge scope")
	}
	if execution.HasEnabledNonEmptyFolderFilter() {
		return nil, apperrors.NewServiceUnavailableError(
			knowledgeScopeUnavailableMessage,
		)
	}
	if !execution.HasLocalKnowledge() {
		return []*types.SearchResult{}, nil
	}
	var knowledgeBaseIDs []string
	var knowledgeIDs []string
	for _, target := range execution.Targets() {
		if target.FolderFilter().Empty() {
			continue
		}
		knowledgeBaseIDs = append(knowledgeBaseIDs, target.KnowledgeBaseID())
		knowledgeIDs = append(knowledgeIDs, target.KnowledgeIDs()...)
	}
	results, err := s.searchKnowledgeByTargets(
		ctx,
		query,
		knowledgeBaseIDs,
		knowledgeIDs,
		types.ProjectKnowledgeScopeToSearchTargets(
			execution,
			preparation.ExecutionScopeHash(),
		),
		execution,
		preparation.ExecutionScopeHash(),
	)
	if err != nil {
		return nil, mapPreparedKnowledgeRuntimeError(ctx, err)
	}
	return results, nil
}

func mapPreparedKnowledgeRuntimeError(
	ctx context.Context,
	err error,
) error {
	if err == nil {
		return nil
	}
	if stderrors.Is(err, context.Canceled) ||
		stderrors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	var appError *apperrors.AppError
	if stderrors.As(err, &appError) &&
		appError.HTTPCode == 503 &&
		appError.Message == knowledgeScopeUnavailableMessage {
		return apperrors.NewServiceUnavailableError(
			knowledgeScopeUnavailableMessage,
		)
	}
	return apperrors.NewInternalServerError("knowledge search failed")
}

func (s *sessionService) searchKnowledgeByTargets(
	ctx context.Context,
	query string,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
	searchTargets types.SearchTargets,
	executionScope *types.KnowledgeScope,
	executionScopeHash string,
) ([]*types.SearchResult, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("workspace ID not found in context")
	}
	if len(searchTargets) == 0 {
		logger.Warn(ctx, "No search targets available, returning empty results")
		return []*types.SearchResult{}, nil
	}

	// Create default retrieval parameters — prefer tenant RetrievalConfig, fallback to built-in defaults
	userID := types.SessionOwnerIDFromContext(ctx)

	// Load tenant-level retrieval config (nil is safe — GetEffective* methods handle nil receiver)
	var rc *types.RetrievalConfig
	if tenant, err2 := s.tenantService.GetTenantByID(ctx, tenantID); err2 == nil {
		rc = tenant.RetrievalConfig
	}

	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:              query,
			UserID:             userID,
			KnowledgeBaseIDs:   knowledgeBaseIDs,
			KnowledgeIDs:       knowledgeIDs,
			SearchTargets:      searchTargets,
			ExecutionScope:     executionScope.Clone(),
			ExecutionScopeHash: executionScopeHash,
			MaxRounds:          s.cfg.Conversation.MaxRounds,
			EmbeddingTopK:      rc.GetEffectiveEmbeddingTopK(),
			VectorThreshold:    rc.GetEffectiveVectorThreshold(),
			KeywordThreshold:   rc.GetEffectiveKeywordThreshold(),
			RerankTopK:         rc.GetEffectiveRerankTopK(),
			RerankThreshold:    rc.GetEffectiveRerankThreshold(),
		},
		PipelineState: types.PipelineState{
			RewriteQuery: query,
		},
	}

	// Get default models
	models, err := s.modelService.ListModels(ctx)
	if err != nil {
		if executionScopeHash != "" {
			logger.Error(ctx, "Failed to get models for prepared search")
		} else {
			logger.Errorf(ctx, "Failed to get models: %v", err)
		}
		return nil, err
	}

	// Use rerank model from RetrievalConfig if set, otherwise auto-select the first available
	if rc != nil && rc.RerankModelID != "" {
		chatManage.RerankModelID = rc.RerankModelID
	} else {
		for _, model := range models {
			if model == nil {
				continue
			}
			if model.Type == types.ModelTypeRerank {
				chatManage.RerankModelID = model.ID
				break
			}
		}
	}

	// Use specific event list, only including retrieval-related events, not LLM summarization
	searchEvents := []types.EventType{
		types.CHUNK_SEARCH, // Vector search
		types.CHUNK_RERANK, // Rerank search results
		types.CHUNK_MERGE,  // Merge search results
		types.FILTER_TOP_K, // Filter top K results
	}

	logger.Infof(ctx, "Trigger search event list: %v", searchEvents)

	for _, event := range searchEvents {
		logger.Infof(ctx, "Starting to trigger search event: %v", event)
		stageCtx, stageSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
			Name: "pipeline." + string(event),
			Metadata: map[string]interface{}{
				"event_type": string(event),
				"flow":       "search_knowledge",
			},
		})
		err := s.eventManager.Trigger(stageCtx, event, chatManage)
		var spanErr error
		if err != nil && err != chatpipeline.ErrSearchNothing {
			if executionScopeHash != "" {
				spanErr = stderrors.New("prepared search event failed")
			} else {
				spanErr = err.Err
			}
		}
		stageSpan.Finish(nil, nil, spanErr)

		if err == chatpipeline.ErrSearchNothing {
			logger.Warnf(ctx, "Event %v triggered, search result is empty", event)
			return []*types.SearchResult{}, nil
		}

		if err != nil {
			if executionScopeHash != "" {
				logger.Errorf(
					ctx,
					"Prepared search event failed, event: %v, scope hash: %s",
					event,
					knowledgeScopeHashPrefix(executionScopeHash),
				)
			} else {
				logger.Errorf(ctx, "Event triggering failed, event: %v, error type: %s, description: %s, error: %v",
					event, err.ErrorType, err.Description, err.Err)
			}
			return nil, err.Err
		}
		logger.Infof(ctx, "Event %v triggered successfully", event)
	}

	logger.Infof(ctx, "Knowledge base search completed, found %d results", len(chatManage.MergeResult))
	return chatManage.MergeResult, nil
}

// handleFallbackResponse handles fallback response based on strategy
func (s *sessionService) handleFallbackResponse(ctx context.Context, chatManage *types.ChatManage) {
	if chatManage.FallbackStrategy == types.FallbackStrategyModel {
		s.handleModelFallback(ctx, chatManage)
	} else {
		s.handleFixedFallback(ctx, chatManage)
	}
}

// handleFixedFallback handles fixed fallback response
func (s *sessionService) handleFixedFallback(ctx context.Context, chatManage *types.ChatManage) {
	fallbackContent := chatManage.FallbackResponse
	chatManage.ChatResponse = &types.ChatResponse{Content: fallbackContent}
	s.emitFallbackAnswer(ctx, chatManage, fallbackContent)
}

// handleModelFallback handles model-based fallback response using streaming
func (s *sessionService) handleModelFallback(ctx context.Context, chatManage *types.ChatManage) {
	// Check if FallbackPrompt is available
	if chatManage.FallbackPrompt == "" {
		logger.Warnf(ctx, "Fallback strategy is 'model' but FallbackPrompt is empty, falling back to fixed response")
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	// Render template with Query variable
	promptContent, err := s.renderFallbackPrompt(ctx, chatManage)
	if err != nil {
		logger.Errorf(ctx, "Failed to render fallback prompt: %v, falling back to fixed response", err)
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	// Check if EventBus is available for streaming
	if chatManage.EventBus == nil {
		logger.Warnf(ctx, "EventBus not available for streaming fallback, falling back to fixed response")
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	// Get chat model
	chatModel, err := s.modelService.GetChatModel(ctx, chatManage.ChatModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chat model for fallback: %v, falling back to fixed response", err)
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	// Prepare chat options
	thinking := false
	opt := &chat.ChatOptions{
		Temperature:         chatManage.SummaryConfig.Temperature,
		MaxCompletionTokens: chatManage.SummaryConfig.MaxCompletionTokens,
		Thinking:            &thinking,
	}

	// Start streaming response
	fallbackMessages, sourceRefs := prepareFallbackMessages(chatManage, promptContent)
	responseChan, err := chatModel.ChatStream(ctx, fallbackMessages, opt)
	if err != nil {
		logger.Errorf(ctx, "Failed to start streaming fallback response: %v, falling back to fixed response", err)
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	if responseChan == nil {
		logger.Errorf(ctx, "Chat stream returned nil channel, falling back to fixed response")
		s.handleFixedFallback(ctx, chatManage)
		return
	}

	// Start goroutine to consume stream and emit events
	go s.consumeFallbackStream(ctx, chatManage, responseChan, sourceRefs)
}

func prepareFallbackMessages(
	chatManage *types.ChatManage,
	promptContent string,
) ([]chat.Message, *llmreference.Registry) {
	messages := buildFallbackMessages(chatManage, promptContent)
	citationsEnabled := chatManage == nil || chatManage.CitationsEnabled()
	refs := llmreference.NewRegistry(citationsEnabled)
	if len(messages) > 0 && messages[0].Role == "system" {
		messages[0].Content = strings.TrimRight(messages[0].Content, " \t\r\n") + llmreference.ProtocolPrompt(citationsEnabled)
	} else {
		messages = append([]chat.Message{{Role: "system", Content: strings.TrimSpace(llmreference.ProtocolPrompt(citationsEnabled))}}, messages...)
	}
	return refs.EncodeMessages(messages), refs
}

func buildFallbackMessages(chatManage *types.ChatManage, promptContent string) []chat.Message {
	messages := make([]chat.Message, 0, len(chatManage.History)*2+2)

	// The model-fallback prompt is a system-style instruction (KB document
	// listing + "use general knowledge when nothing matched" guidance). Carry
	// it in the system role so the LLM input keeps a proper system message
	// instead of starting with a bare user turn. We deliberately do NOT reuse
	// the RAG summary system prompt (SummaryConfig.Prompt) here: that template
	// forbids prior knowledge ("reply ONLY based on retrieved information"),
	// which directly contradicts the fallback's purpose.
	if strings.TrimSpace(promptContent) != "" {
		messages = append(messages, chat.Message{Role: "system", Content: promptContent})
	}

	messages = chatpipeline.AppendHistoryMessages(messages, chatManage.History)

	// End on the user's actual question so generation is prompted by a user
	// turn (the query is also embedded in the system instruction above, but a
	// trailing user message keeps the chat shape valid for all providers).
	query := chatManage.Query
	if rq := strings.TrimSpace(chatManage.RewriteQuery); rq != "" {
		query = rq
	}
	userMsg := chat.Message{Role: "user", Content: query}
	if chatManage.ChatModelSupportsVision && len(chatManage.Images) > 0 {
		userMsg.Images = chatManage.Images
	}

	return append(messages, userMsg)
}

// renderFallbackPrompt renders the fallback prompt template with query and image context.
func (s *sessionService) renderFallbackPrompt(ctx context.Context, chatManage *types.ChatManage) (string, error) {
	query := chatManage.Query
	if rq := strings.TrimSpace(chatManage.RewriteQuery); rq != "" {
		query = rq
	}

	kbDocuments := s.buildKBDocumentListing(ctx, chatManage)

	result := types.RenderPromptPlaceholders(chatManage.FallbackPrompt, types.PlaceholderValues{
		"query":        query,
		"language":     chatManage.Language,
		"kb_documents": kbDocuments,
	})

	if chatManage.ImageDescription != "" && !chatManage.ChatModelSupportsVision {
		result += "\n\n[用户上传图片内容]\n" + chatManage.ImageDescription
	}
	if chatManage.QuotedContext != "" {
		result += "\n\n" + chatManage.QuotedContext
	}
	return result, nil
}

// buildKBDocumentListing returns a concise listing of documents in the knowledge bases
// associated with the current pipeline. This gives the LLM visibility into KB contents
// when vector/keyword search returns empty (e.g., broad browse queries).
func (s *sessionService) buildKBDocumentListing(ctx context.Context, chatManage *types.ChatManage) string {
	// Collect unique KB IDs from search targets
	kbIDs := make(map[string]struct{})
	for _, t := range chatManage.SearchTargets {
		if t == nil || t.FolderFilter.Enabled() {
			continue
		}
		if chatManage.ExecutionScope != nil &&
			(len(t.KnowledgeIDs) > 0 ||
				len(t.TagIDs) > 0 ||
				len(t.ScopeTagIDs) > 0) {
			continue
		}
		kbIDs[t.KnowledgeBaseID] = struct{}{}
	}
	if chatManage.ExecutionScope == nil {
		for _, id := range chatManage.KnowledgeBaseIDs {
			kbIDs[id] = struct{}{}
		}
	}
	if len(kbIDs) == 0 {
		return ""
	}

	const maxDocuments = 50
	var b strings.Builder
	total := 0

	for kbID := range kbIDs {
		if total >= maxDocuments {
			break
		}
		knowledges, err := s.knowledgeService.ListKnowledgeByKnowledgeBaseID(ctx, kbID)
		if err != nil {
			if chatManage.ExecutionScopeHash != "" {
				logger.Warn(
					ctx,
					"Prepared document listing failed",
				)
			} else {
				logger.Warnf(ctx, "buildKBDocumentListing: failed to list knowledge for KB %s: %v", kbID, err)
			}
			continue
		}
		for _, k := range knowledges {
			if total >= maxDocuments {
				break
			}
			if k.EnableStatus != "enabled" {
				continue
			}
			title := k.Title
			if title == "" {
				title = k.FileName
			}
			if title == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s", title)
			if k.FileType != "" {
				fmt.Fprintf(&b, " (%s)", k.FileType)
			}
			if k.Description != "" {
				desc := k.Description
				if len([]rune(desc)) > 100 {
					desc = string([]rune(desc)[:100]) + "..."
				}
				fmt.Fprintf(&b, ": %s", desc)
			}
			b.WriteString("\n")
			total++
		}
	}

	if b.Len() == 0 {
		return ""
	}

	if total >= maxDocuments {
		fmt.Fprintf(&b, "... (showing first %d documents)\n", maxDocuments)
	}

	return b.String()
}

// consumeFallbackStream consumes the streaming response and emits events
func (s *sessionService) consumeFallbackStream(
	ctx context.Context,
	chatManage *types.ChatManage,
	responseChan <-chan types.StreamResponse,
	sourceRefs *llmreference.Registry,
) {
	fallbackID := generateEventID("fallback")
	eventBus := chatManage.EventBus
	var finalContent string
	streamCompleted := false
	refExpander := llmreference.NewStreamExpander(sourceRefs)

	for response := range responseChan {
		// Emit event for each answer chunk
		if response.ResponseType == types.ResponseTypeAnswer {
			response.Content = refExpander.Feed(response.Content)
			if response.Done {
				response.Content += refExpander.Flush()
			}
			finalContent += response.Content
			if err := eventBus.Emit(ctx, types.Event{
				ID:        fallbackID,
				Type:      types.EventType(event.EventAgentFinalAnswer),
				SessionID: chatManage.SessionID,
				Data: event.AgentFinalAnswerData{
					Content:    response.Content,
					Done:       response.Done,
					IsFallback: true,
				},
			}); err != nil {
				logger.Errorf(ctx, "Failed to emit fallback answer chunk event: %v", err)
			}

			// Update ChatResponse with final content when done
			if response.Done {
				chatManage.ChatResponse = &types.ChatResponse{Content: finalContent}
				streamCompleted = true
				logger.Infof(ctx, "Fallback streaming response completed")
				break
			}
		}
	}

	// If channel closed without Done=true, emit final event with fixed response
	if !streamCompleted {
		logger.Warnf(ctx, "Fallback stream closed without completion, emitting final event with fixed response")
		s.emitFallbackAnswer(ctx, chatManage, chatManage.FallbackResponse)
	}
}

// emitKnowledgeReferencesEvent streams retrieved chunks to the client as a
// `references` SSE event. These references drive the retrieval-results UI and
// remain available even when inline citations in the model answer are disabled.
// Must run before CHAT_COMPLETION_STREAM so the event arrives while the
// connection is still open (complete closes the stream).
func emitKnowledgeReferencesEvent(ctx context.Context, chatManage *types.ChatManage) {
	if chatManage == nil || chatManage.EventBus == nil || len(chatManage.MergeResult) == 0 {
		return
	}
	logger.Infof(ctx, "Emitting references event with %d results (pre-answer)", len(chatManage.MergeResult))
	if err := chatManage.EventBus.Emit(ctx, types.Event{
		ID:        generateEventID("references"),
		Type:      types.EventType(event.EventAgentReferences),
		SessionID: chatManage.SessionID,
		Data: event.AgentReferencesData{
			References: chatManage.MergeResult,
		},
	}); err != nil {
		logger.Errorf(ctx, "Failed to emit references event: %v", err)
	}
}

// emitFallbackAnswer emits fallback answer event
func (s *sessionService) emitFallbackAnswer(ctx context.Context, chatManage *types.ChatManage, content string) {
	if chatManage.EventBus == nil {
		return
	}
	if !chatManage.CitationsEnabled() {
		content = llmreference.NewRegistry(false).ExpandText(content)
	}

	fallbackID := generateEventID("fallback")
	if err := chatManage.EventBus.Emit(ctx, types.Event{
		ID:        fallbackID,
		Type:      types.EventType(event.EventAgentFinalAnswer),
		SessionID: chatManage.SessionID,
		Data: event.AgentFinalAnswerData{
			Content:    content,
			Done:       true,
			IsFallback: true,
		},
	}); err != nil {
		logger.Errorf(ctx, "Failed to emit fallback answer event: %v", err)
	} else {
		logger.Infof(ctx, "Fallback answer event emitted successfully")
	}
}

// resolveWebSearchProviderID returns the web search provider ID to use for a pipeline request.
// Priority: agent config > tenant default (is_default=true)
func (s *sessionService) resolveWebSearchProviderID(ctx context.Context, req *types.QARequest, tenantID uint64) string {
	// 1. Agent-level override
	if req.CustomAgent != nil && req.CustomAgent.Config.WebSearchProviderID != "" {
		return req.CustomAgent.Config.WebSearchProviderID
	}
	// 2. Tenant default
	if s.webSearchProviderRepo != nil {
		if defaultProvider, err := s.webSearchProviderRepo.GetDefault(ctx, tenantID); err == nil && defaultProvider != nil {
			return defaultProvider.ID
		}
	}
	return ""
}

// resolveWebFetchEnabled returns whether auto web fetch is enabled for this request.
func (s *sessionService) resolveWebFetchEnabled(req *types.QARequest) bool {
	if req.CustomAgent != nil {
		return req.CustomAgent.Config.WebFetchEnabled
	}
	return false
}

// resolveWebFetchTopN returns how many pages to fetch after rerank.
func (s *sessionService) resolveWebFetchTopN(req *types.QARequest) int {
	if req.CustomAgent != nil && req.CustomAgent.Config.WebFetchTopN > 0 {
		return req.CustomAgent.Config.WebFetchTopN
	}
	return 3
}

// resolveWebSearchMaxResults returns the max results for web search.
// Priority: agent config > tenant default > default (10)
func (s *sessionService) resolveWebSearchMaxResults(ctx context.Context, req *types.QARequest) int {
	if req.CustomAgent != nil && req.CustomAgent.Config.WebSearchMaxResults > 0 {
		return req.CustomAgent.Config.WebSearchMaxResults
	}
	tenantInfo, _ := types.TenantInfoFromContext(ctx)
	if tenantInfo != nil {
		return types.EffectiveWebSearchConfig(tenantInfo.WebSearchConfig).MaxResults
	}
	return types.DefaultWebSearchMaxResults
}
