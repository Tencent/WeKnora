package session

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// convertMentionedItems converts MentionedItemRequest slice to types.MentionedItems
func convertMentionedItems(items []MentionedItemRequest) types.MentionedItems {
	if len(items) == 0 {
		return nil
	}
	result := make(types.MentionedItems, len(items))
	for i, item := range items {
		result[i] = types.MentionedItem{
			ID:     item.ID,
			Name:   item.Name,
			Type:   item.Type,
			KBType: item.KBType,
		}
	}
	return result
}

// setSSEHeaders sets the standard Server-Sent Events headers
func setSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}

// buildStreamResponse constructs a StreamResponse from a StreamEvent
func buildStreamResponse(evt interfaces.StreamEvent, requestID string) *types.StreamResponse {
	response := &types.StreamResponse{
		ID:           requestID,
		ResponseType: evt.Type,
		Content:      evt.Content,
		Done:         evt.Done,
		Data:         evt.Data,
	}

	// Extract session_id and assistant_message_id for agent_query events
	if evt.Type == types.ResponseTypeAgentQuery {
		if sid, ok := evt.Data["session_id"].(string); ok {
			response.SessionID = sid
		}
		if amid, ok := evt.Data["assistant_message_id"].(string); ok {
			response.AssistantMessageID = amid
		}
	}

	// Special handling for references event
	if evt.Type == types.ResponseTypeReferences {
		refsData := evt.Data["references"]
		if refs, ok := refsData.(types.References); ok {
			response.KnowledgeReferences = refs
		} else if refs, ok := refsData.([]*types.SearchResult); ok {
			response.KnowledgeReferences = types.References(refs)
		} else if refs, ok := refsData.([]interface{}); ok {
			// Handle case where data was serialized/deserialized (e.g., from Redis)
			searchResults := make([]*types.SearchResult, 0, len(refs))
			for _, ref := range refs {
				if refMap, ok := ref.(map[string]interface{}); ok {
					sr := &types.SearchResult{
						ID:                getString(refMap, "id"),
						Content:           getString(refMap, "content"),
						KnowledgeID:       getString(refMap, "knowledge_id"),
						ChunkIndex:        int(getFloat64(refMap, "chunk_index")),
						KnowledgeTitle:    getString(refMap, "knowledge_title"),
						StartAt:           int(getFloat64(refMap, "start_at")),
						EndAt:             int(getFloat64(refMap, "end_at")),
						Seq:               int(getFloat64(refMap, "seq")),
						Score:             getFloat64(refMap, "score"),
						ChunkType:         getString(refMap, "chunk_type"),
						ParentChunkID:     getString(refMap, "parent_chunk_id"),
						ImageInfo:         getString(refMap, "image_info"),
						KnowledgeFilename: getString(refMap, "knowledge_filename"),
						KnowledgeSource:   getString(refMap, "knowledge_source"),
					}
					searchResults = append(searchResults, sr)
				}
			}
			response.KnowledgeReferences = types.References(searchResults)
		}
	}

	return response
}

// sendCompletionEvent sends a final completion event to the client
// NOTE: This is now a no-op because:
// 1. The 'complete' event from handleComplete already signals stream completion
// 2. Sending an extra empty 'answer' event with done:true causes frontend issues
//    (multiple done events can confuse state management)
// The frontend should use 'complete' response_type to detect stream completion
func sendCompletionEvent(c *gin.Context, requestID string) {
	// Intentionally empty - completion is signaled by the 'complete' event
	// which is already sent before this function is called
}

// createAgentQueryEvent creates a standard agent query event
func createAgentQueryEvent(sessionID, assistantMessageID string) interfaces.StreamEvent {
	return interfaces.StreamEvent{
		ID:        fmt.Sprintf("query-%d", time.Now().UnixNano()),
		Type:      types.ResponseTypeAgentQuery,
		Content:   "",
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"session_id":           sessionID,
			"assistant_message_id": assistantMessageID,
		},
	}
}

// createUserMessage creates a user message
func (h *Handler) createUserMessage(ctx context.Context, sessionID, query, requestID string, mentionedItems types.MentionedItems) error {
	_, err := h.messageService.CreateMessage(ctx, &types.Message{
		SessionID:      sessionID,
		Role:           "user",
		Content:        query,
		RequestID:      requestID,
		CreatedAt:      time.Now(),
		IsCompleted:    true,
		MentionedItems: mentionedItems,
	})
	return err
}

// createAssistantMessage creates an assistant message
func (h *Handler) createAssistantMessage(ctx context.Context, assistantMessage *types.Message) (*types.Message, error) {
	assistantMessage.CreatedAt = time.Now()
	return h.messageService.CreateMessage(ctx, assistantMessage)
}

// setupStreamHandler creates and subscribes a stream handler
func (h *Handler) setupStreamHandler(
	ctx context.Context,
	sessionID, assistantMessageID, requestID string,
	assistantMessage *types.Message,
	eventBus *event.EventBus,
) *AgentStreamHandler {
	streamHandler := NewAgentStreamHandler(
		ctx, sessionID, assistantMessageID, requestID,
		assistantMessage, h.streamManager, eventBus,
	)
	streamHandler.Subscribe()
	return streamHandler
}

// setupStopEventHandler registers a stop event handler
func (h *Handler) setupStopEventHandler(
	eventBus *event.EventBus,
	sessionID string,
	assistantMessage *types.Message,
	cancel context.CancelFunc,
) {
	eventBus.On(event.EventStop, func(ctx context.Context, evt event.Event) error {
		logger.Infof(ctx, "Received stop event, cancelling async operations for session: %s", sessionID)
		cancel()
		assistantMessage.Content = "用户停止了本次对话"
		h.completeAssistantMessage(ctx, assistantMessage)
		return nil
	})
}

// writeAgentQueryEvent writes an agent query event to the stream manager
func (h *Handler) writeAgentQueryEvent(ctx context.Context, sessionID, assistantMessageID string) {
	agentQueryEvent := createAgentQueryEvent(sessionID, assistantMessageID)
	if err := h.streamManager.AppendEvent(ctx, sessionID, assistantMessageID, agentQueryEvent); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": assistantMessageID,
		})
		// Non-fatal error, continue
	}
}

// getRequestID gets the request ID from gin context
func getRequestID(c *gin.Context) string {
	return c.GetString(types.RequestIDContextKey.String())
}

// Helper function for type assertion with default value
func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getFloat64(m map[string]interface{}, key string) float64 {
	if val, ok := m[key].(float64); ok {
		return val
	}
	if val, ok := m[key].(int); ok {
		return float64(val)
	}
	return 0.0
}

// createDefaultSummaryConfig creates a default summary configuration from config
// It prioritizes tenant-level ConversationConfig, then falls back to config.yaml defaults
func (h *Handler) createDefaultSummaryConfig(ctx context.Context) *types.SummaryConfig {
	// Try to get tenant from context
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// Initialize with config.yaml defaults
	cfg := &types.SummaryConfig{
		MaxTokens:           h.config.Conversation.Summary.MaxTokens,
		TopP:                h.config.Conversation.Summary.TopP,
		TopK:                h.config.Conversation.Summary.TopK,
		FrequencyPenalty:    h.config.Conversation.Summary.FrequencyPenalty,
		PresencePenalty:     h.config.Conversation.Summary.PresencePenalty,
		RepeatPenalty:       h.config.Conversation.Summary.RepeatPenalty,
		Prompt:              h.config.Conversation.Summary.Prompt,
		ContextTemplate:     h.config.Conversation.Summary.ContextTemplate,
		NoMatchPrefix:       h.config.Conversation.Summary.NoMatchPrefix,
		Temperature:         h.config.Conversation.Summary.Temperature,
		Seed:                h.config.Conversation.Summary.Seed,
		MaxCompletionTokens: h.config.Conversation.Summary.MaxCompletionTokens,
	}

	// Override with tenant-level conversation config if available
	if tenant != nil && tenant.ConversationConfig != nil {
		// Use custom prompt if provided
		if tenant.ConversationConfig.Prompt != "" {
			cfg.Prompt = tenant.ConversationConfig.Prompt
		}

		// Use custom context template if provided
		if tenant.ConversationConfig.ContextTemplate != "" {
			cfg.ContextTemplate = tenant.ConversationConfig.ContextTemplate
		}
		if tenant.ConversationConfig.Temperature > 0 {
			cfg.Temperature = tenant.ConversationConfig.Temperature
		}
		if tenant.ConversationConfig.MaxCompletionTokens > 0 {
			cfg.MaxCompletionTokens = tenant.ConversationConfig.MaxCompletionTokens
		}
	}

	return cfg
}

// fillSummaryConfigDefaults fills missing fields in summary config with defaults
// It prioritizes tenant-level ConversationConfig, then falls back to config.yaml defaults
func (h *Handler) fillSummaryConfigDefaults(ctx context.Context, config *types.SummaryConfig) {
	// Try to get tenant from context
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// Determine default values: tenant config first, then config.yaml
	var defaultPrompt, defaultContextTemplate, defaultNoMatchPrefix string
	var defaultTemperature float64
	var defaultMaxCompletionTokens int

	if tenant != nil && tenant.ConversationConfig != nil {
		// Use custom prompt if provided
		if tenant.ConversationConfig.Prompt != "" {
			defaultPrompt = tenant.ConversationConfig.Prompt
		}

		// Use custom context template if provided
		if tenant.ConversationConfig.ContextTemplate != "" {
			defaultContextTemplate = tenant.ConversationConfig.ContextTemplate
		}
		defaultTemperature = tenant.ConversationConfig.Temperature
		defaultMaxCompletionTokens = tenant.ConversationConfig.MaxCompletionTokens
	}

	// Fall back to config.yaml if tenant config is empty
	if defaultPrompt == "" {
		defaultPrompt = h.config.Conversation.Summary.Prompt
	}
	if defaultContextTemplate == "" {
		defaultContextTemplate = h.config.Conversation.Summary.ContextTemplate
	}
	if defaultTemperature == 0 {
		defaultTemperature = h.config.Conversation.Summary.Temperature
	}
	if defaultMaxCompletionTokens == 0 {
		defaultMaxCompletionTokens = h.config.Conversation.Summary.MaxCompletionTokens
	}
	defaultNoMatchPrefix = h.config.Conversation.Summary.NoMatchPrefix

	// Fill missing fields
	if config.Prompt == "" {
		config.Prompt = defaultPrompt
	}
	if config.ContextTemplate == "" {
		config.ContextTemplate = defaultContextTemplate
	}
	if config.Temperature == 0 {
		config.Temperature = defaultTemperature
	}
	if config.MaxCompletionTokens == 0 {
		config.MaxCompletionTokens = defaultMaxCompletionTokens
	}
	if config.NoMatchPrefix == "" {
		config.NoMatchPrefix = defaultNoMatchPrefix
	}
}
