package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// regThinkIndex matches <think>...</think> blocks for stripping from KB index content.
var regThinkIndex = regexp.MustCompile(`(?s)<think>.*?</think>`)

// messageService implements the MessageService interface for managing messaging operations
// It handles creating, retrieving, updating, and deleting messages within sessions.
// It reads the chat history knowledge base configuration from the tenant's ChatHistoryConfig,
// which is managed via the settings UI.
type messageService struct {
	messageRepo   interfaces.MessageRepository    // Repository for message storage operations
	sessionRepo   interfaces.SessionRepository    // Repository for session validation
	tenantService interfaces.TenantService        // Service for tenant operations (read ChatHistoryConfig)
	kbService     interfaces.KnowledgeBaseService // Service for knowledge base operations (search chat history KB)
	knowService   interfaces.KnowledgeService     // Service for knowledge operations (index/delete passages)
	modelService  interfaces.ModelService         // Service for model operations (rerank model)
}

// NewMessageService creates a new message service instance with the required repositories
func NewMessageService(messageRepo interfaces.MessageRepository,
	sessionRepo interfaces.SessionRepository,
	tenantService interfaces.TenantService,
	kbService interfaces.KnowledgeBaseService,
	knowService interfaces.KnowledgeService,
	modelService interfaces.ModelService,
) interfaces.MessageService {
	return &messageService{
		messageRepo:   messageRepo,
		sessionRepo:   sessionRepo,
		tenantService: tenantService,
		kbService:     kbService,
		knowService:   knowService,
		modelService:  modelService,
	}
}

// sessionTenantIDForLookup returns the tenant ID to use for session lookup.
// When SessionTenantIDContextKey is set (e.g. pipeline with shared agent), use it so session/message belong to session owner.
func sessionTenantIDForLookup(ctx context.Context) (uint64, bool) {
	if v := ctx.Value(types.SessionTenantIDContextKey); v != nil {
		if tid, ok := v.(uint64); ok && tid != 0 {
			return tid, true
		}
	}
	if v := ctx.Value(types.TenantIDContextKey); v != nil {
		if tid, ok := v.(uint64); ok {
			return tid, true
		}
	}
	return 0, false
}

func sessionUserIDForLookup(ctx context.Context) string {
	if ctx.Value(types.SessionTenantIDContextKey) != nil {
		// Shared-agent pipelines resolve the session owner tenant first; keep that internal lookup tenant-scoped.
		return ""
	}
	userID, _ := types.UserIDFromContext(ctx)
	return userID
}

// CreateMessage creates a new message within an existing session
func (s *messageService) CreateMessage(ctx context.Context, message *types.Message) (*types.Message, error) {
	logger.Info(ctx, "Start creating message")
	logger.Infof(ctx, "Creating message for session ID: %s", message.SessionID)

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d, session ID: %s", tenantID, message.SessionID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), message.SessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return nil, err
	}

	logger.Info(ctx, "Session exists, creating message")
	createdMessage, err := s.messageRepo.CreateMessage(ctx, message)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": message.SessionID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Message created successfully, ID: %s", createdMessage.ID)
	return createdMessage, nil
}

// GetMessage retrieves a specific message by its ID within a session
func (s *messageService) GetMessage(ctx context.Context, sessionID string, messageID string) (*types.Message, error) {
	logger.Info(ctx, "Start getting message")
	logger.Infof(ctx, "Getting message, session ID: %s, message ID: %s", sessionID, messageID)

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return nil, err
	}

	logger.Info(ctx, "Session exists, getting message")
	message, err := s.messageRepo.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": messageID,
		})
		return nil, err
	}

	logger.Info(ctx, "Message retrieved successfully")
	return message, nil
}

// GetMessagesBySession retrieves paginated messages for a specific session
func (s *messageService) GetMessagesBySession(ctx context.Context,
	sessionID string, page int, pageSize int,
) ([]*types.Message, error) {
	logger.Info(ctx, "Start getting messages by session")
	logger.Infof(ctx, "Getting messages for session ID: %s, page: %d, pageSize: %d", sessionID, page, pageSize)

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return nil, err
	}

	logger.Info(ctx, "Session exists, getting messages")
	messages, err := s.messageRepo.GetMessagesBySession(ctx, sessionID, page, pageSize)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"page":       page,
			"page_size":  pageSize,
		})
		return nil, err
	}

	logger.Infof(ctx, "Retrieved %d messages successfully", len(messages))
	return messages, nil
}

// GetRecentMessagesBySession retrieves the most recent messages from a session
func (s *messageService) GetRecentMessagesBySession(ctx context.Context,
	sessionID string, limit int,
) ([]*types.Message, error) {
	logger.Info(ctx, "Start getting recent messages by session")
	logger.Infof(ctx, "Getting recent messages for session ID: %s, limit: %d", sessionID, limit)

	tenantID, ok := sessionTenantIDForLookup(ctx)
	if !ok {
		logger.Error(ctx, "Tenant ID not found in context for session lookup")
		return nil, errors.New("tenant ID not found in context")
	}
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return nil, err
	}

	logger.Info(ctx, "Session exists, getting recent messages")
	messages, err := s.messageRepo.GetRecentMessagesBySession(ctx, sessionID, limit)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"limit":      limit,
		})
		return nil, err
	}

	logger.Infof(ctx, "Retrieved %d recent messages successfully", len(messages))
	return messages, nil
}

// GetMessagesBySessionBeforeTime retrieves messages sent before a specific time
func (s *messageService) GetMessagesBySessionBeforeTime(ctx context.Context,
	sessionID string, beforeTime time.Time, limit int,
) ([]*types.Message, error) {
	logger.Info(ctx, "Start getting messages before time")
	logger.Infof(ctx, "Getting messages before %v for session ID: %s, limit: %d", beforeTime, sessionID, limit)

	tenantID, ok := sessionTenantIDForLookup(ctx)
	if !ok {
		logger.Error(ctx, "Tenant ID not found in context for session lookup")
		return nil, errors.New("tenant ID not found in context")
	}
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return nil, err
	}

	logger.Info(ctx, "Session exists, getting messages before time")
	messages, err := s.messageRepo.GetMessagesBySessionBeforeTime(ctx, sessionID, beforeTime, limit)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id":  sessionID,
			"before_time": beforeTime,
			"limit":       limit,
		})
		return nil, err
	}

	logger.Infof(ctx, "Retrieved %d messages before time successfully", len(messages))
	return messages, nil
}

// UpdateMessage updates an existing message's content or metadata
func (s *messageService) UpdateMessage(ctx context.Context, message *types.Message) error {
	logger.Info(ctx, "Start updating message")
	logger.Infof(ctx, "Updating message, ID: %s, session ID: %s", message.ID, message.SessionID)

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), message.SessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return err
	}

	logger.Info(ctx, "Session exists, updating message")
	err = s.messageRepo.UpdateMessage(ctx, message)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": message.SessionID,
			"message_id": message.ID,
		})
		return err
	}

	logger.Info(ctx, "Message updated successfully")
	return nil
}

// UpdateMessageImages updates only the images JSONB column for a message.
func (s *messageService) UpdateMessageImages(ctx context.Context, sessionID, messageID string, images types.MessageImages) error {
	return s.messageRepo.UpdateMessageImages(ctx, sessionID, messageID, images)
}

// UpdateMessageRenderedContent updates the rendered_content column for a user message.
func (s *messageService) UpdateMessageRenderedContent(ctx context.Context, sessionID, messageID string, renderedContent string) error {
	return s.messageRepo.UpdateMessageRenderedContent(ctx, sessionID, messageID, renderedContent)
}

// DeleteMessage removes a message from a session, also cleaning up its Knowledge entry in the chat history KB.
func (s *messageService) DeleteMessage(ctx context.Context, sessionID string, messageID string) error {
	logger.Info(ctx, "Start deleting message")
	logger.Infof(ctx, "Deleting message, session ID: %s, message ID: %s", sessionID, messageID)

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return err
	}

	// Get the message first to check if it has an associated Knowledge entry
	msg, err := s.messageRepo.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get message for deletion: %v", err)
		return err
	}

	// Delete the message from the repository
	logger.Info(ctx, "Session exists, deleting message")
	err = s.messageRepo.DeleteMessage(ctx, sessionID, messageID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": messageID,
		})
		return err
	}

	// Async cleanup: delete the associated Knowledge entry from the chat history KB.
	// Use WithoutCancel so the goroutine survives after the HTTP request context is done.
	if msg != nil && msg.KnowledgeID != "" {
		bgCtx := context.WithoutCancel(ctx)
		go s.DeleteMessageKnowledge(bgCtx, msg.KnowledgeID)
	}

	logger.Info(ctx, "Message deleted successfully")
	return nil
}

// ClearSessionMessages deletes all messages in a session, along with their chat history KB entries.
func (s *messageService) ClearSessionMessages(ctx context.Context, sessionID string) error {
	logger.Infof(ctx, "Start clearing all messages for session: %s", sessionID)

	tenantID := types.MustTenantIDFromContext(ctx)
	if _, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), sessionID); err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return err
	}

	// Async cleanup: delete associated Knowledge entries from the chat history KB
	bgCtx := context.WithoutCancel(ctx)
	go s.DeleteSessionKnowledge(bgCtx, sessionID)

	if err := s.messageRepo.DeleteMessagesBySessionID(ctx, sessionID); err != nil {
		logger.Errorf(ctx, "Failed to delete messages for session %s: %v", sessionID, err)
		return err
	}

	logger.Infof(ctx, "All messages cleared for session: %s", sessionID)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Chat History Knowledge Base — Configuration-driven (via Tenant.ChatHistoryConfig)
// ─────────────────────────────────────────────────────────────────────────────

// getChatHistoryConfig reads the chat history KB configuration from the tenant's settings.
// Returns nil if the feature is not configured or disabled.
func (s *messageService) getChatHistoryConfig(ctx context.Context) *types.ChatHistoryConfig {
	tenant, ok := types.TenantInfoFromContext(ctx)
	if !ok {
		return nil
	}
	if tenant.ChatHistoryConfig == nil || !tenant.ChatHistoryConfig.IsConfigured() {
		return nil
	}
	return tenant.ChatHistoryConfig
}

// getRetrievalConfig reads the global retrieval configuration from the tenant's settings.
// Returns an empty config (with defaults) if not configured.
func (s *messageService) getRetrievalConfig(ctx context.Context) *types.RetrievalConfig {
	tenant, ok := types.TenantInfoFromContext(ctx)
	if !ok {
		return &types.RetrievalConfig{}
	}
	if tenant.RetrievalConfig == nil {
		return &types.RetrievalConfig{}
	}
	return tenant.RetrievalConfig
}

// IndexMessageToKB indexes a message (Q&A pair) into the chat history knowledge base asynchronously.
// It creates a Knowledge entry (passage) containing both the user query and assistant answer,
// then links the message to the Knowledge entry via the knowledge_id field.
// The KB ID is read from the tenant's ChatHistoryConfig — if not configured, indexing is skipped.
func (s *messageService) IndexMessageToKB(ctx context.Context, userQuery string, assistantAnswer string, messageID string, sessionID string) {
	// Strip thinking content (<think>...</think>) before indexing to avoid
	// polluting the knowledge base with intermediate reasoning that would
	// degrade retrieval quality.
	assistantAnswer = regThinkIndex.ReplaceAllString(assistantAnswer, "")
	assistantAnswer = strings.TrimSpace(assistantAnswer)

	if strings.TrimSpace(userQuery) == "" && assistantAnswer == "" {
		return
	}

	cfg := s.getChatHistoryConfig(ctx)
	if cfg == nil {
		return
	}

	logger.Infof(ctx, "Indexing message to chat history KB %s, message ID: %s, session ID: %s", cfg.KnowledgeBaseID, messageID, sessionID)

	// Build passage content: combine Q&A for better semantic search
	var passages []string
	passage := fmt.Sprintf("[Session: %s]\nQ: %s\nA: %s", sessionID, userQuery, assistantAnswer)
	passages = append(passages, passage)

	// Use async (non-sync) passage creation so it doesn't block the response
	knowledge, err := s.knowService.CreateKnowledgeFromPassage(ctx, cfg.KnowledgeBaseID, passages, "")
	if err != nil {
		logger.Warnf(ctx, "Failed to index message to chat history KB: %v", err)
		return
	}

	// Link the message to the knowledge entry
	if err := s.messageRepo.UpdateMessageKnowledgeID(ctx, messageID, knowledge.ID); err != nil {
		logger.Warnf(ctx, "Failed to update message knowledge_id: %v", err)
		return
	}

	logger.Infof(ctx, "Message indexed to chat history KB: knowledge_id=%s, message_id=%s", knowledge.ID, messageID)
}

// DeleteMessageKnowledge deletes the Knowledge entry associated with a message from the chat history KB.
func (s *messageService) DeleteMessageKnowledge(ctx context.Context, knowledgeID string) {
	if knowledgeID == "" {
		return
	}
	logger.Infof(ctx, "Deleting chat history knowledge entry: %s", knowledgeID)
	if err := s.knowService.DeleteKnowledge(ctx, knowledgeID); err != nil {
		logger.Warnf(ctx, "Failed to delete chat history knowledge %s: %v", knowledgeID, err)
	}
}

// DeleteSessionKnowledge deletes all Knowledge entries for messages in a session from the chat history KB.
func (s *messageService) DeleteSessionKnowledge(ctx context.Context, sessionID string) {
	logger.Infof(ctx, "Deleting all chat history knowledge entries for session: %s", sessionID)

	knowledgeIDs, err := s.messageRepo.GetKnowledgeIDsBySessionID(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "Failed to get knowledge IDs for session %s: %v", sessionID, err)
		return
	}

	if len(knowledgeIDs) == 0 {
		return
	}

	logger.Infof(ctx, "Deleting %d chat history knowledge entries for session %s", len(knowledgeIDs), sessionID)
	if err := s.knowService.DeleteKnowledgeList(ctx, knowledgeIDs); err != nil {
		logger.Warnf(ctx, "Failed to batch delete chat history knowledge for session %s: %v", sessionID, err)
	}
}

// GetChatHistoryKBStats returns statistics about the chat history knowledge base.
func (s *messageService) GetChatHistoryKBStats(ctx context.Context) (*types.ChatHistoryKBStats, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant: %w", err)
	}

	stats := &types.ChatHistoryKBStats{}
	cfg := tenant.ChatHistoryConfig
	if cfg == nil || !cfg.Enabled {
		return stats, nil
	}

	stats.Enabled = true
	stats.EmbeddingModelID = cfg.EmbeddingModelID
	stats.KnowledgeBaseID = cfg.KnowledgeBaseID

	if cfg.KnowledgeBaseID == "" {
		return stats, nil
	}

	// Fetch KB info and fill counts (KnowledgeCount is gorm:"-", needs FillKnowledgeBaseCounts)
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, cfg.KnowledgeBaseID)
	if err != nil {
		logger.Warnf(ctx, "Failed to get chat history KB %s: %v", cfg.KnowledgeBaseID, err)
		return stats, nil
	}
	if err := s.kbService.FillKnowledgeBaseCounts(ctx, kb); err != nil {
		logger.Warnf(ctx, "Failed to fill chat history KB counts %s: %v", cfg.KnowledgeBaseID, err)
	}
	stats.KnowledgeBaseName = kb.Name
	stats.IndexedMessageCount = kb.KnowledgeCount
	stats.HasIndexedMessages = kb.KnowledgeCount > 0

	return stats, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Message Search (Hybrid: Keyword + KB Vector Search)
// ─────────────────────────────────────────────────────────────────────────────

// SearchMessages searches messages by keyword and/or vector similarity across all sessions of the current tenant.
// Vector search is delegated to the chat history knowledge base's HybridSearch (configured via ChatHistoryConfig).
func (s *messageService) SearchMessages(ctx context.Context, params *types.MessageSearchParams) (*types.MessageSearchResult, error) {
	logger.Infof(ctx, "Start searching messages, query: %s, mode: %s", params.Query, params.Mode)

	tenantID := types.MustTenantIDFromContext(ctx)

	// Set defaults
	if params.Mode == "" {
		params.Mode = types.MessageSearchModeHybrid
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}

	var keywordResults []*types.MessageWithSession
	var vectorResults []*types.MessageSearchResultItem
	var err error

	// Step 1: Keyword search (direct PG ILIKE)
	if params.Mode == types.MessageSearchModeKeyword || params.Mode == types.MessageSearchModeHybrid {
		keywordResults, err = s.messageRepo.SearchMessagesByKeyword(ctx, tenantID, params.Query, params.SessionIDs, params.Limit*3)
		if err != nil {
			logger.Errorf(ctx, "Keyword search failed: %v", err)
			return nil, err
		}
		logger.Infof(ctx, "Keyword search found %d results", len(keywordResults))
	}

	// Step 2: Vector search via chat history knowledge base (if configured)
	if params.Mode == types.MessageSearchModeVector || params.Mode == types.MessageSearchModeHybrid {
		vectorResults, err = s.vectorSearchViaKB(ctx, params)
		if err != nil {
			logger.Warnf(ctx, "Vector search via KB failed, falling back to keyword-only: %v", err)
			if params.Mode == types.MessageSearchModeVector {
				return nil, err
			}
		} else {
			logger.Infof(ctx, "Vector search found %d results", len(vectorResults))
		}
	}

	// Step 3: Merge results based on mode
	var items []*types.MessageSearchResultItem

	switch params.Mode {
	case types.MessageSearchModeKeyword:
		items = convertKeywordResults(keywordResults)
	case types.MessageSearchModeVector:
		items = vectorResults
	case types.MessageSearchModeHybrid:
		items = rrfMerge(keywordResults, vectorResults)
	}

	// Step 4: Fetch partner messages (Q&A counterparts) to ensure complete pairs
	items = s.fetchPartnerMessages(ctx, items)

	// Step 5: Group by request_id to merge Q&A pairs
	grouped := groupByRequestID(items)

	// Apply limit
	if len(grouped) > params.Limit {
		grouped = grouped[:params.Limit]
	}

	result := &types.MessageSearchResult{
		Items: grouped,
		Total: len(grouped),
	}

	logger.Infof(ctx, "Message search completed, returning %d grouped results", result.Total)
	return result, nil
}

// vectorSearchViaKB performs vector search using the chat history knowledge base's HybridSearch.
// The KB ID is read from ChatHistoryConfig, search params from RetrievalConfig.
func (s *messageService) vectorSearchViaKB(ctx context.Context, params *types.MessageSearchParams) ([]*types.MessageSearchResultItem, error) {
	cfg := s.getChatHistoryConfig(ctx)
	if cfg == nil {
		return nil, nil // Chat history KB not configured, skip vector search
	}

	// Read global retrieval config for search parameters
	rc := s.getRetrievalConfig(ctx)

	// Use KB HybridSearch with vector-only mode (keyword search is done separately on the messages table)
	searchParams := types.SearchParams{
		QueryText:            params.Query,
		MatchCount:           rc.GetEffectiveEmbeddingTopK(),
		VectorThreshold:      rc.GetEffectiveVectorThreshold(),
		DisableKeywordsMatch: true, // We handle keyword search separately on the messages table
	}

	kbResults, err := s.kbService.HybridSearch(ctx, cfg.KnowledgeBaseID, searchParams)
	if err != nil {
		return nil, fmt.Errorf("KB hybrid search failed: %w", err)
	}

	if len(kbResults) == 0 {
		return nil, nil
	}

	// Rerank results if a rerank model is configured
	kbResults = s.rerankResults(ctx, rc, params.Query, kbResults)
	if len(kbResults) == 0 {
		return nil, nil
	}

	// Map KB search results back to messages via knowledge_id
	knowledgeIDs := make([]string, 0, len(kbResults))
	scoreByKnowledgeID := make(map[string]float64)
	for _, r := range kbResults {
		knowledgeIDs = append(knowledgeIDs, r.KnowledgeID)
		scoreByKnowledgeID[r.KnowledgeID] = r.Score
	}

	// Look up messages by their knowledge_id
	messages, err := s.messageRepo.GetMessagesByKnowledgeIDs(ctx, knowledgeIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages by knowledge IDs: %w", err)
	}

	// Filter by session IDs if specified
	sessionFilter := make(map[string]bool)
	for _, sid := range params.SessionIDs {
		sessionFilter[sid] = true
	}

	var results []*types.MessageSearchResultItem
	for _, msg := range messages {
		if len(sessionFilter) > 0 && !sessionFilter[msg.SessionID] {
			continue
		}
		score := scoreByKnowledgeID[msg.KnowledgeID]
		results = append(results, &types.MessageSearchResultItem{
			MessageWithSession: *msg,
			Score:              score,
			MatchType:          "vector",
		})
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// rerankResults applies rerank model to search results if configured.
// Returns reranked + filtered results, or original results if rerank is unavailable.
func (s *messageService) rerankResults(ctx context.Context, rc *types.RetrievalConfig, query string, results []*types.SearchResult) []*types.SearchResult {
	if rc == nil || rc.RerankModelID == "" || len(results) == 0 {
		return results
	}

	reranker, err := s.modelService.GetRerankModel(ctx, rc.RerankModelID)
	if err != nil {
		logger.Warnf(ctx, "Failed to get rerank model %s, skipping rerank: %v", rc.RerankModelID, err)
		return results
	}

	// Build documents for rerank
	documents := make([]string, len(results))
	for i, r := range results {
		documents[i] = r.Content
	}

	rankResults, err := reranker.Rerank(ctx, query, documents)
	if err != nil {
		logger.Warnf(ctx, "Rerank call failed, skipping: %v", err)
		return results
	}

	// Filter by threshold and topK, rebuild results with rerank scores
	threshold := rc.GetEffectiveRerankThreshold()
	topK := rc.GetEffectiveRerankTopK()

	var reranked []*types.SearchResult
	for _, rr := range rankResults {
		if rr.Index >= len(results) {
			continue
		}
		if rr.RelevanceScore < threshold {
			continue
		}
		item := *results[rr.Index]
		item.Score = rr.RelevanceScore
		reranked = append(reranked, &item)
		if len(reranked) >= topK {
			break
		}
	}

	logger.Infof(ctx, "Rerank: %d -> %d results (threshold=%.2f, topK=%d)", len(results), len(reranked), threshold, topK)
	return reranked
}

// convertKeywordResults converts keyword search results to MessageSearchResultItem
func convertKeywordResults(results []*types.MessageWithSession) []*types.MessageSearchResultItem {
	items := make([]*types.MessageSearchResultItem, 0, len(results))
	for i, msg := range results {
		items = append(items, &types.MessageSearchResultItem{
			MessageWithSession: *msg,
			Score:              float64(len(results)-i) / float64(len(results)),
			MatchType:          "keyword",
		})
	}
	return items
}

// rrfMerge merges keyword and vector search results using Reciprocal Rank Fusion (RRF)
func rrfMerge(keywordResults []*types.MessageWithSession, vectorResults []*types.MessageSearchResultItem) []*types.MessageSearchResultItem {
	const k = 60.0

	type scoredMsg struct {
		msg       *types.MessageWithSession
		rrfScore  float64
		matchType string
	}
	scoreMap := make(map[string]*scoredMsg)

	for rank, msg := range keywordResults {
		id := msg.ID
		rrfScore := 1.0 / (k + float64(rank+1))
		if existing, ok := scoreMap[id]; ok {
			existing.rrfScore += rrfScore
			existing.matchType = "hybrid"
		} else {
			scoreMap[id] = &scoredMsg{
				msg:       msg,
				rrfScore:  rrfScore,
				matchType: "keyword",
			}
		}
	}

	for rank, item := range vectorResults {
		id := item.ID
		rrfScore := 1.0 / (k + float64(rank+1))
		if existing, ok := scoreMap[id]; ok {
			existing.rrfScore += rrfScore
			existing.matchType = "hybrid"
		} else {
			scoreMap[id] = &scoredMsg{
				msg:       &item.MessageWithSession,
				rrfScore:  rrfScore,
				matchType: "vector",
			}
		}
	}

	items := make([]*types.MessageSearchResultItem, 0, len(scoreMap))
	for _, scored := range scoreMap {
		items = append(items, &types.MessageSearchResultItem{
			MessageWithSession: *scored.msg,
			Score:              scored.rrfScore,
			MatchType:          scored.matchType,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Score > items[j].Score
	})

	return items
}

// fetchPartnerMessages looks at the search results and, for each request_id that
// has only one role (Q-only or A-only), fetches the partner message from DB so
// that groupByRequestID can produce complete Q&A pairs.
func (s *messageService) fetchPartnerMessages(ctx context.Context, items []*types.MessageSearchResultItem) []*types.MessageSearchResultItem {
	// Collect request_ids and track which roles we already have
	type roleSet struct {
		hasUser      bool
		hasAssistant bool
	}
	seen := make(map[string]*roleSet)
	existingIDs := make(map[string]bool)
	for _, item := range items {
		existingIDs[item.ID] = true
		rid := item.RequestID
		if rid == "" {
			continue
		}
		rs, ok := seen[rid]
		if !ok {
			rs = &roleSet{}
			seen[rid] = rs
		}
		if item.Role == "user" {
			rs.hasUser = true
		} else if item.Role == "assistant" {
			rs.hasAssistant = true
		}
	}

	// Find request_ids that need partner lookup
	var needFetch []string
	for rid, rs := range seen {
		if !rs.hasUser || !rs.hasAssistant {
			needFetch = append(needFetch, rid)
		}
	}
	if len(needFetch) == 0 {
		return items
	}

	// Fetch partner messages
	partners, err := s.messageRepo.GetMessagesByRequestIDs(ctx, needFetch)
	if err != nil {
		logger.Warnf(ctx, "Failed to fetch partner messages: %v", err)
		return items
	}

	// Append only messages not already in results
	for _, p := range partners {
		if existingIDs[p.ID] {
			continue
		}
		existingIDs[p.ID] = true
		items = append(items, &types.MessageSearchResultItem{
			MessageWithSession: *p,
			Score:              0, // partner is not directly matched
			MatchType:          "",
		})
	}

	return items
}

// groupByRequestID merges individual message search results into Q&A pairs
// grouped by request_id. Messages without a request_id become standalone items.
func groupByRequestID(items []*types.MessageSearchResultItem) []*types.MessageSearchGroupItem {
	type groupState struct {
		item  *types.MessageSearchGroupItem
		order int // preserve the order of first appearance
	}
	groups := make(map[string]*groupState)
	nextOrder := 0

	for _, item := range items {
		key := item.RequestID
		if key == "" {
			// No request_id — treat as standalone
			key = item.ID
		}

		g, exists := groups[key]
		if !exists {
			g = &groupState{
				item: &types.MessageSearchGroupItem{
					RequestID:    item.RequestID,
					SessionID:    item.SessionID,
					SessionTitle: item.SessionTitle,
					CreatedAt:    item.CreatedAt,
				},
				order: nextOrder,
			}
			nextOrder++
			groups[key] = g
		}

		// Assign content based on role
		switch item.Role {
		case "user":
			g.item.QueryContent = item.Content
		case "assistant":
			g.item.AnswerContent = item.Content
		}

		// Keep the best score and merge match types
		if item.Score > g.item.Score {
			g.item.Score = item.Score
		}
		if g.item.MatchType == "" {
			g.item.MatchType = item.MatchType
		} else if g.item.MatchType != item.MatchType {
			g.item.MatchType = "hybrid"
		}

		// Use earliest created_at
		if item.CreatedAt.Before(g.item.CreatedAt) {
			g.item.CreatedAt = item.CreatedAt
		}
	}

	// Collect and sort by original order (which reflects score ranking)
	result := make([]*types.MessageSearchGroupItem, 0, len(groups))
	ordered := make([]*groupState, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].order < ordered[j].order
	})
	for _, g := range ordered {
		result = append(result, g.item)
	}

	return result
}
