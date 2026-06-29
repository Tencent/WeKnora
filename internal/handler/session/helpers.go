package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// convertImageAttachments converts ImageAttachment slice to types.MessageImages
func convertImageAttachments(items []ImageAttachment) types.MessageImages {
	if len(items) == 0 {
		return nil
	}
	result := make(types.MessageImages, len(items))
	for i, item := range items {
		result[i] = types.MessageImage{
			URL:     item.URL,
			Caption: item.Caption,
		}
	}
	return result
}

// extractImageURLsAndOCRText extracts image references and concatenated analysis text.
// For LLM consumption it prefers the raw Data (data URI) when available so that
// image_resolve can skip the disk round-trip; falls back to the storage URL otherwise.
func extractImageURLsAndOCRText(images []ImageAttachment) (urls []string, ocrText string) {
	if len(images) == 0 {
		return nil, ""
	}
	urls = make([]string, 0, len(images))
	var parts []string
	for _, img := range images {
		switch {
		case img.Data != "":
			urls = append(urls, img.Data)
		case img.URL != "":
			urls = append(urls, img.URL)
		}
		if img.Caption != "" {
			parts = append(parts, img.Caption)
		}
	}
	if len(parts) > 0 {
		ocrText = strings.Join(parts, "\n")
	}
	return
}

// convertMentionedItems converts MentionedItemRequest slice to types.MentionedItems
func convertMentionedItems(items []MentionedItemRequest) types.MentionedItems {
	if len(items) == 0 {
		return nil
	}
	result := make(types.MentionedItems, len(items))
	for i, item := range items {
		result[i] = types.MentionedItem{
			ID:        item.ID,
			Name:      item.Name,
			Type:      item.Type,
			KBType:    item.KBType,
			KBID:      item.KBID,
			KBName:    item.KBName,
			ServiceID: item.ServiceID,
			SkillName: item.SkillName,
		}
	}
	return result
}

func tagScopesFromMentionedItems(items []MentionedItemRequest) []types.TagScope {
	byKB := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	for _, item := range items {
		if item.Type != "tag" || item.ID == "" || item.KBID == "" {
			continue
		}
		if seen[item.KBID] == nil {
			seen[item.KBID] = make(map[string]bool)
		}
		if seen[item.KBID][item.ID] {
			continue
		}
		seen[item.KBID][item.ID] = true
		byKB[item.KBID] = append(byKB[item.KBID], item.ID)
	}
	scopes := make([]types.TagScope, 0, len(byKB))
	for kbID, tagIDs := range byKB {
		scopes = append(scopes, types.TagScope{KnowledgeBaseID: kbID, TagIDs: tagIDs})
	}
	return scopes
}

func mentionedIDsByType(items []MentionedItemRequest, itemType string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, item := range items {
		if item.Type != itemType || item.ID == "" || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		result = append(result, item.ID)
	}
	return result
}

func dedupRequestStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
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
		if refsData == nil {
			return response
		}
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
						ID:                   getString(refMap, "id"),
						Content:              getString(refMap, "content"),
						KnowledgeID:          getString(refMap, "knowledge_id"),
						ChunkIndex:           int(getFloat64(refMap, "chunk_index")),
						KnowledgeTitle:       getString(refMap, "knowledge_title"),
						StartAt:              int(getFloat64(refMap, "start_at")),
						EndAt:                int(getFloat64(refMap, "end_at")),
						Seq:                  int(getFloat64(refMap, "seq")),
						Score:                getFloat64(refMap, "score"),
						ChunkType:            getString(refMap, "chunk_type"),
						ParentChunkID:        getString(refMap, "parent_chunk_id"),
						ImageInfo:            getString(refMap, "image_info"),
						KnowledgeFilename:    getString(refMap, "knowledge_filename"),
						KnowledgeSource:      getString(refMap, "knowledge_source"),
						KnowledgeDescription: getString(refMap, "knowledge_description"),
						KnowledgeBaseID:      getString(refMap, "knowledge_base_id"),
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
//  1. The 'complete' event from handleComplete already signals stream completion
//  2. Sending an extra empty 'answer' event with done:true causes frontend issues
//     (multiple done events can confuse state management)
//
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

// createUserMessage creates a user message and returns the created message.
func (h *Handler) createUserMessage(ctx context.Context, sessionID, query, requestID string, mentionedItems types.MentionedItems, images types.MessageImages, attachments types.MessageAttachments, channel string) (*types.Message, error) {
	return h.messageService.CreateMessage(ctx, &types.Message{
		SessionID:      sessionID,
		Role:           "user",
		Content:        query,
		RequestID:      requestID,
		CreatedAt:      time.Now(),
		IsCompleted:    true,
		MentionedItems: mentionedItems,
		Images:         images,
		Attachments:    attachments,
		Channel:        channel,
	})
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
	receivedAt time.Time,
	assistantMessage *types.Message,
	eventBus *event.EventBus,
) *AgentStreamHandler {
	streamHandler := NewAgentStreamHandler(
		ctx, sessionID, assistantMessageID, requestID, receivedAt,
		assistantMessage, h.streamManager, eventBus,
	)
	streamHandler.Subscribe()
	return streamHandler
}

// setupStopEventHandler registers a stop event handler
func (h *Handler) setupStopEventHandler(
	eventBus *event.EventBus,
	sessionID string,
	sessionTenantID uint64,
	assistantMessage *types.Message,
	cancel context.CancelFunc,
) {
	eventBus.On(event.EventStop, func(ctx context.Context, evt event.Event) error {
		logger.Infof(ctx, "Received stop event, cancelling async operations for session: %s", sessionID)
		cancel()
		// Preserve whatever has been streamed so far; do not overwrite Content.
		// Use session's tenant for message update (ctx may have effectiveTenantID when using shared agent).
		// Use WithoutCancel so the GORM UPDATE survives the upcoming ctx.Done triggered by cancel()/client disconnect.
		updateCtx := context.WithValue(
			context.WithoutCancel(ctx),
			types.TenantIDContextKey, sessionTenantID,
		)
		h.completeAssistantMessage(updateCtx, assistantMessage, "") // empty query: stopped conversations are not indexed
		return nil
	})
}

// stopWatcherMaxDuration bounds the lifetime of a stop watcher as an
// anti-leak backstop. Normally the watcher exits well before this on a
// terminal stream event; this only guards pathological streams that never
// emit a terminal marker.
const stopWatcherMaxDuration = 2 * time.Hour

// startStopWatcher polls the stream for a user-requested stop event
// independently of the client's SSE connection.
//
// Background: the original design only detected the stop marker inside
// handleAgentEventsForSSE, which is bound to the request context. Once the
// client closes the SSE stream (common for API-Key / programmatic callers that
// close the stream before POSTing /stop), that loop returns and nothing
// converts the stop marker (written to the shared StreamManager by
// StopSession) into a context cancellation — so generation keeps running to
// completion even though /stop returned success.
//
// The watcher is intentionally self-terminating rather than tied to the QA
// service call returning: KnowledgeQA (quick answer) returns immediately while
// the actual token stream runs in a background goroutine, whereas AgentQA
// (smart reasoning) blocks until done. Keying teardown off the call return
// would therefore tear the watcher down before quick-answer streaming even
// starts. Instead it exits when it observes a terminal stream event
// (complete, or a stream-level error), on stop, or after a safety timeout.
func (h *Handler) startStopWatcher(
	ctx context.Context,
	sessionID, assistantMessageID string,
	eventBus *event.EventBus,
) {
	go func() {
		watchCtx, cancel := context.WithTimeout(ctx, stopWatcherMaxDuration)
		defer cancel()

		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()

		offset := 0
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				events, newOffset, err := h.streamManager.GetEvents(watchCtx, sessionID, assistantMessageID, offset)
				if err != nil {
					// Transient read error (e.g. Redis blip); retry next tick.
					continue
				}
				offset = newOffset
				for _, evt := range events {
					switch {
					case evt.Type == types.ResponseType(event.EventStop):
						logger.Infof(watchCtx,
							"Stop watcher detected stop event, cancelling generation for session=%s, message=%s",
							sessionID, assistantMessageID)
						eventBus.Emit(watchCtx, event.Event{
							Type:      event.EventStop,
							SessionID: sessionID,
							Data: event.StopData{
								SessionID: sessionID,
								MessageID: assistantMessageID,
								Reason:    "user_requested",
							},
						})
						return
					case evt.Type == types.ResponseTypeComplete:
						// Generation finished normally; nothing left to stop.
						return
					case evt.Type == types.ResponseTypeError && evt.Done:
						// Stream-level (terminal) error; generation has ended.
						return
					}
				}
			}
		}
	}()
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

// createDefaultSummaryConfig and fillSummaryConfigDefaults used to build
// per-session SummaryConfig from tenant-level ConversationConfig + config.yaml
// defaults. Both helpers became unreachable when the chat pipeline moved to
// CustomAgent (builtin-quick-answer / smart-reasoning) and the tenant-level
// ConversationConfig field was removed; deleting them avoids the only
// remaining references to that defunct path.
