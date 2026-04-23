package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeQA performs knowledge base question answering with LLM summarization
// Events are emitted through eventBus (references, answer chunks, completion)
// customAgent is optional - if provided, uses custom agent configuration for multiTurnEnabled and historyTurns
func (s *sessionService) KnowledgeQA(
	ctx context.Context,
	req *types.QARequest,
	eventBus *event.EventBus,
) error {
	logger.Infof(
		ctx,
		"Knowledge base question answering parameters, session ID: %s, query: %s, webSearchEnabled: %v, enableMemory: %v",
		req.Session.ID,
		req.Query,
		req.WebSearchEnabled,
		req.EnableMemory,
	)

	// Resolve knowledge bases using shared helper
	knowledgeBaseIDs, knowledgeIDs := s.resolveKnowledgeBases(ctx, req)

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

	// Build unified search targets (computed once, used throughout pipeline)
	searchTargets, err := s.buildSearchTargets(ctx, retrievalTenantID, knowledgeBaseIDs, knowledgeIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to build search targets: %v", err)
	}

	// Create chat management object with session settings
	logger.Infof(
		ctx,
		"Creating chat manage object, knowledge base IDs: %v, knowledge IDs: %v, chat model ID: %s, search targets: %d",
		knowledgeBaseIDs,
		knowledgeIDs,
		chatModelID,
		len(searchTargets),
	)

	// Get UserID from context
	userID, _ := types.UserIDFromContext(ctx)

	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:                   req.Query,
			SessionID:               req.Session.ID,
			UserID:                  userID,
			EnableMemory:            req.EnableMemory,
			MaxRounds:               s.cfg.Conversation.MaxRounds,
			KnowledgeBaseIDs:        knowledgeBaseIDs,
			KnowledgeIDs:            knowledgeIDs,
			SearchTargets:           searchTargets,
			VectorThreshold:         s.cfg.Conversation.VectorThreshold,
			KeywordThreshold:        s.cfg.Conversation.KeywordThreshold,
			EmbeddingTopK:           s.cfg.Conversation.EmbeddingTopK,
			RerankTopK:              s.cfg.Conversation.RerankTopK,
			RerankThreshold:         s.cfg.Conversation.RerankThreshold,
			ChatModelID:             chatModelID,
			QueryUnderstandModelID:  s.cfg.Conversation.QueryUnderstandModelID,
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

	// Apply custom agent overrides (system prompt, temperature, retrieval params,
	// rewrite, fallback, FAQ strategy, history turns)
	s.applyAgentOverridesToChatManage(ctx, req.CustomAgent, chatManage)

	// Determine pipeline based on knowledge bases availability and web search setting
	hasKB := len(knowledgeBaseIDs) > 0 || len(knowledgeIDs) > 0
	needsRAG := hasKB || req.WebSearchEnabled
	hasHistory := chatManage.MaxRounds > 0

	var pipeline []types.EventType
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

		pipeline = types.NewPipelineBuilder().
			AddIf(hasHistory, types.LOAD_HISTORY).
			AddIf(chatManage.EnableMemory, types.MEMORY_RETRIEVAL).
			Add(types.CHAT_COMPLETION_STREAM).
			AddIf(chatManage.EnableMemory, types.MEMORY_STORAGE).
			Build()
	} else {
		// RAG — dynamically assemble based on feature flags.
		pipeline = types.NewPipelineBuilder().
			Add(types.LOAD_HISTORY).
			Add(types.QUERY_UNDERSTAND).
			Add(types.CHUNK_SEARCH_PARALLEL).
			Add(types.CHUNK_RERANK).
			AddIf(req.WebSearchEnabled, types.WEB_FETCH).
			Add(types.CHUNK_MERGE).
			Add(types.FILTER_TOP_K).
			Add(types.DATA_ANALYSIS).
			Add(types.INTO_CHAT_MESSAGE).
			Add(types.CHAT_COMPLETION_STREAM).
			Build()
	}

	logger.Infof(ctx, "Assembled pipeline (%d stages), hasKB=%v, webSearch=%v, history=%v",
		len(pipeline), hasKB, req.WebSearchEnabled, hasHistory)

	// Start knowledge QA event processing (set session tenant so pipeline session/message lookups use session owner)
	ctx = context.WithValue(ctx, types.SessionTenantIDContextKey, req.Session.TenantID)
	logger.Info(ctx, "Triggering question answering event")
	err = s.KnowledgeQAByEvent(ctx, chatManage, pipeline)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": req.Session.ID,
		})
		return err
	}

	// Emit references event if we have search results
	if len(chatManage.MergeResult) > 0 {
		logger.Infof(ctx, "Emitting references event with %d results", len(chatManage.MergeResult))
		if err := eventBus.Emit(ctx, event.Event{
			ID:        generateEventID("references"),
			Type:      event.EventAgentReferences,
			SessionID: req.Session.ID,
			Data: event.AgentReferencesData{
				References: chatManage.MergeResult,
			},
		}); err != nil {
			logger.Errorf(ctx, "Failed to emit references event: %v", err)
		}
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
			logger.Warnf(ctx, "Failed to get knowledge batch for model selection: %v", err)
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
				logger.Warnf(ctx, "Failed to get knowledge base: %v", err)
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
			logger.Errorf(ctx, "Failed to get knowledge base for model ID: %v", err)
			return "", fmt.Errorf("failed to get knowledge base %s: %w", knowledgeBaseIDs[0], err)
		}
		if kb != nil && kb.SummaryModelID != "" {
			logger.Infof(
				ctx,
				"Using summary model from first knowledge base %s: %s",
				knowledgeBaseIDs[0],
				kb.SummaryModelID,
			)
			return kb.SummaryModelID, nil
		}
	}

	// No knowledge bases - try to find any available chat model
	models, err := s.modelService.ListModels(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to list models: %v", err)
		return "", fmt.Errorf("failed to list models: %w", err)
	}
	for _, model := range models {
		if model != nil && model.Type == types.ModelTypeKnowledgeQA {
			logger.Infof(ctx, "Using first available KnowledgeQA model: %s", model.ID)
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
	if customAgent == nil {
		return nil
	}

	switch customAgent.Config.KBSelectionMode {
	case "all":
		// Authoritative capability filter for the runtime path. The frontend
		// editor and @mention dropdown apply the same filter, but we don't
		// trust the client here: a stale session payload or API caller could
		// still ask us to retrieve against an incompatible KB and we'd rather
		// just drop it (and log) than feed it to tools that would no-op.
		capFilter := tools.DeriveKBFilterFromTools(customAgent.Config.AllowedTools)
		accept := func(kb *types.KnowledgeBase) bool {
			if kb == nil {
				return false
			}
			if capFilter.IsEmpty() {
				return true
			}
			return tools.KBSatisfiesToolRequirements(kb.Capabilities(), customAgent.Config.AllowedTools)
		}

		// Get own knowledge bases (uses ctx TenantID = agent's tenant)
		allKBs, err := s.knowledgeBaseService.ListKnowledgeBases(ctx)
		if err != nil {
			logger.Warnf(ctx, "Failed to list all knowledge bases: %v", err)
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
					sharedList, err := s.kbShareService.ListSharedKnowledgeBases(ctx, userID, tenantID)
					if err != nil {
						logger.Warnf(ctx, "Failed to list shared knowledge bases: %v", err)
					} else {
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
			}
		} else {
			logger.Infof(ctx, "Shared agent detected (session tenant %d != agent tenant %d): skipping user's shared KBs",
				sessionTenantID, customAgent.TenantID)
		}

		if ownSkipped+sharedSkipped > 0 {
			logger.Infof(ctx,
				"KBSelectionMode=all: tool-capability filter removed %d own + %d shared KBs (agent=%s, tools=%v)",
				ownSkipped, sharedSkipped, customAgent.ID, customAgent.Config.AllowedTools)
		}
		logger.Infof(ctx, "KBSelectionMode=all: loaded %d knowledge bases (own + shared)", len(kbIDs))
		return kbIDs
	case "selected":
		logger.Infof(ctx, "KBSelectionMode=selected: using %d configured knowledge bases", len(customAgent.Config.KnowledgeBases))
		return customAgent.Config.KnowledgeBases
	case "none":
		logger.Infof(ctx, "KBSelectionMode=none: no knowledge bases configured")
		return nil
	default:
		// Default to "selected" behavior for backward compatibility
		if len(customAgent.Config.KnowledgeBases) > 0 {
			logger.Infof(ctx, "KBSelectionMode not set: using %d configured knowledge bases", len(customAgent.Config.KnowledgeBases))
		}
		return customAgent.Config.KnowledgeBases
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
) (types.SearchTargets, error) {
	var targets types.SearchTargets

	// Build a map from KB ID to TenantID for all KBs we need to process
	kbTenantMap := make(map[string]uint64)

	// Track which KBs are fully searched
	fullKBSet := make(map[string]bool)

	// First pass: batch-fetch KBs, then resolve tenant per ID (tenant scope already set by caller)
	if len(knowledgeBaseIDs) > 0 {
		kbs, _ := s.knowledgeBaseService.GetKnowledgeBasesByIDsOnly(ctx, knowledgeBaseIDs)
		kbByID := make(map[string]*types.KnowledgeBase, len(kbs))
		for _, kb := range kbs {
			if kb != nil {
				kbByID[kb.ID] = kb
			}
		}
		userID, _ := types.UserIDFromContext(ctx)
		for _, kbID := range knowledgeBaseIDs {
			fullKBSet[kbID] = true
			kb := kbByID[kbID]
			if kb == nil {
				kbTenantMap[kbID] = tenantID
			} else if kb.TenantID == tenantID {
				kbTenantMap[kbID] = tenantID
			} else if s.kbShareService != nil && userID != "" {
				hasAccess, _ := s.kbShareService.HasKBPermission(ctx, kbID, userID, types.OrgRoleViewer)
				if hasAccess {
					kbTenantMap[kbID] = kb.TenantID
				} else {
					kbTenantMap[kbID] = tenantID
				}
			} else {
				kbTenantMap[kbID] = tenantID
			}
			targets = append(targets, &types.SearchTarget{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: kbID,
				TenantID:        kbTenantMap[kbID],
			})
		}
	}

	// Process individual knowledge IDs (include shared KB files the user has access to)
	if len(knowledgeIDs) > 0 {
		knowledgeList, err := s.knowledgeService.GetKnowledgeBatchWithSharedAccess(ctx, tenantID, knowledgeIDs)
		if err != nil {
			logger.Warnf(ctx, "Failed to get knowledge batch for search targets: %v", err)
			return targets, nil // Return what we have, don't fail
		}

		// Group knowledge IDs by their KB, excluding those already covered by full KB search
		// Also track KB tenant IDs from knowledge items
		kbToKnowledgeIDs := make(map[string][]string)
		for _, k := range knowledgeList {
			if k == nil || k.KnowledgeBaseID == "" {
				continue
			}
			// Track KB -> TenantID mapping from knowledge items
			if kbTenantMap[k.KnowledgeBaseID] == 0 {
				kbTenantMap[k.KnowledgeBaseID] = k.TenantID
			}
			// Skip if this KB is already fully searched
			if fullKBSet[k.KnowledgeBaseID] {
				continue
			}
			kbToKnowledgeIDs[k.KnowledgeBaseID] = append(kbToKnowledgeIDs[k.KnowledgeBaseID], k.ID)
		}

		// Create SearchTargetTypeKnowledge targets for each KB with specific files
		for kbID, kidList := range kbToKnowledgeIDs {
			kbTenant := kbTenantMap[kbID]
			if kbTenant == 0 {
				kbTenant = tenantID // fallback
			}
			targets = append(targets, &types.SearchTarget{
				Type:            types.SearchTargetTypeKnowledge,
				KnowledgeBaseID: kbID,
				TenantID:        kbTenant,
				KnowledgeIDs:    kidList,
			})
		}
	}

	logger.Infof(ctx, "Built %d search targets: %d full KB, %d partial KB, kbTenantMap=%v",
		len(targets), len(knowledgeBaseIDs), len(targets)-len(knowledgeBaseIDs), kbTenantMap)

	return targets, nil
}

// KnowledgeQAByEvent processes knowledge QA through a series of events in the pipeline
func (s *sessionService) KnowledgeQAByEvent(ctx context.Context,
	chatManage *types.ChatManage, eventList []types.EventType,
) error {
	logger.Info(ctx, "Start processing knowledge base question answering through events")
	logger.Infof(ctx, "Knowledge base question answering parameters, session ID: %s, query: %s",
		chatManage.SessionID, chatManage.Query)

	methods := make([]string, len(eventList))
	for i, event := range eventList {
		methods[i] = string(event)
	}
	logger.Infof(ctx, "Trigger event list: %v", methods)

	pipelineStart := time.Now()
	for _, eventType := range eventList {
		stageStart := time.Now()
		err := s.eventManager.Trigger(ctx, eventType, chatManage)
		stageDuration := time.Since(stageStart)

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
	knowledgeBaseIDs []string, knowledgeIDs []string, query string,
) ([]*types.SearchResult, error) {
	logger.Info(ctx, "Start knowledge base search without LLM summary")
	logger.Infof(ctx, "Knowledge base search parameters, knowledge base IDs: %v, knowledge IDs: %v, query: %s",
		knowledgeBaseIDs, knowledgeIDs, query)

	// Get tenant ID from context
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		logger.Error(ctx, "Failed to get tenant ID from context")
		return nil, fmt.Errorf("tenant ID not found in context")
	}

	// Build unified search targets (computed once, used throughout pipeline)
	searchTargets, err := s.buildSearchTargets(ctx, tenantID, knowledgeBaseIDs, knowledgeIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to build search targets: %v", err)
	}

	if len(searchTargets) == 0 {
		logger.Warn(ctx, "No search targets available, returning empty results")
		return []*types.SearchResult{}, nil
	}

	// Create default retrieval parameters — prefer tenant RetrievalConfig, fallback to built-in defaults
	userID, _ := types.UserIDFromContext(ctx)

	// Load tenant-level retrieval config (nil is safe — GetEffective* methods handle nil receiver)
	var rc *types.RetrievalConfig
	if tenant, err2 := s.tenantService.GetTenantByID(ctx, tenantID); err2 == nil {
		rc = tenant.RetrievalConfig
	}

	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:            query,
			UserID:           userID,
			KnowledgeBaseIDs: knowledgeBaseIDs,
			KnowledgeIDs:     knowledgeIDs,
			SearchTargets:    searchTargets,
			MaxRounds:        s.cfg.Conversation.MaxRounds,
			EmbeddingTopK:    rc.GetEffectiveEmbeddingTopK(),
			VectorThreshold:  rc.GetEffectiveVectorThreshold(),
			KeywordThreshold: rc.GetEffectiveKeywordThreshold(),
			RerankTopK:       rc.GetEffectiveRerankTopK(),
			RerankThreshold:  rc.GetEffectiveRerankThreshold(),
		},
		PipelineState: types.PipelineState{
			RewriteQuery: query,
		},
	}

	// Get default models
	models, err := s.modelService.ListModels(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get models: %v", err)
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
		err := s.eventManager.Trigger(ctx, event, chatManage)

		if err == chatpipeline.ErrSearchNothing {
			logger.Warnf(ctx, "Event %v triggered, search result is empty", event)
			return []*types.SearchResult{}, nil
		}

		if err != nil {
			logger.Errorf(ctx, "Event triggering failed, event: %v, error type: %s, description: %s, error: %v",
				event, err.ErrorType, err.Description, err.Err)
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
	userMsg := chat.Message{Role: "user", Content: promptContent}
	if chatManage.ChatModelSupportsVision && len(chatManage.Images) > 0 {
		userMsg.Images = chatManage.Images
	}
	responseChan, err := chatModel.ChatStream(ctx, []chat.Message{userMsg}, opt)
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
	go s.consumeFallbackStream(ctx, chatManage, responseChan)
}

// renderFallbackPrompt renders the fallback prompt template with query and image context.
func (s *sessionService) renderFallbackPrompt(ctx context.Context, chatManage *types.ChatManage) (string, error) {
	query := chatManage.Query
	if rq := strings.TrimSpace(chatManage.RewriteQuery); rq != "" {
		query = rq
	}
	result := types.RenderPromptPlaceholders(chatManage.FallbackPrompt, types.PlaceholderValues{
		"query":    query,
		"language": chatManage.Language,
	})

	if chatManage.ImageDescription != "" && !chatManage.ChatModelSupportsVision {
		result += "\n\n[用户上传图片内容]\n" + chatManage.ImageDescription
	}
	if chatManage.QuotedContext != "" {
		result += "\n\n" + chatManage.QuotedContext
	}
	return result, nil
}

// consumeFallbackStream consumes the streaming response and emits events
func (s *sessionService) consumeFallbackStream(
	ctx context.Context,
	chatManage *types.ChatManage,
	responseChan <-chan types.StreamResponse,
) {
	fallbackID := generateEventID("fallback")
	eventBus := chatManage.EventBus
	var finalContent string
	streamCompleted := false

	for response := range responseChan {
		// Emit event for each answer chunk
		if response.ResponseType == types.ResponseTypeAnswer {
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

// emitFallbackAnswer emits fallback answer event
func (s *sessionService) emitFallbackAnswer(ctx context.Context, chatManage *types.ChatManage, content string) {
	if chatManage.EventBus == nil {
		return
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
	if tenantInfo != nil && tenantInfo.WebSearchConfig != nil && tenantInfo.WebSearchConfig.MaxResults > 0 {
		return tenantInfo.WebSearchConfig.MaxResults
	}
	return 10
}
