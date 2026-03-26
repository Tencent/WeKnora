package llmcontext

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// dbFallbackFetchCount is the number of raw DB messages to fetch when
// rebuilding context from persistent storage.  This should be generous
// because user+assistant messages are paired by RequestID and some
// incomplete pairs are discarded.
const dbFallbackFetchCount = 200

var regThinkTags = regexp.MustCompile(`(?s)<think>.*?</think>`)

// contextManager implements the ContextManager interface.
// It is a cache-backed storage layer: messages are persisted per session in
// a fast store (Redis / memory).  When the cache is empty (e.g. TTL expired),
// it falls back to the persistent messages table via MessageService to
// rebuild context.
//
// All LLM-aware compression (summarisation, tool-boundary-aware truncation)
// is handled by the Agent Engine's Consolidator before messages are sent to
// the model.
type contextManager struct {
	storage     ContextStorage
	messageRepo interfaces.MessageRepository // optional; enables DB fallback
}

// NewContextManager creates a context manager.
// messageRepo is optional — when provided, GetContext will reconstruct
// history from the DB if the cache is empty.
func NewContextManager(storage ContextStorage, messageRepo interfaces.MessageRepository) interfaces.ContextManager {
	return &contextManager{
		storage:     storage,
		messageRepo: messageRepo,
	}
}

// AddMessage appends a message to the session context and persists it.
func (cm *contextManager) AddMessage(ctx context.Context, sessionID string, message chat.Message) error {
	messages, err := cm.storage.Load(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to load context: %w", err)
	}

	messages = append(messages, message)

	if err := cm.storage.Save(ctx, sessionID, messages); err != nil {
		return fmt.Errorf("failed to save context: %w", err)
	}

	logger.Debugf(ctx, "[ContextManager][Session-%s] Message saved (total: %d)", sessionID, len(messages))
	return nil
}

// GetContext retrieves the stored context for a session.
// If the cache is empty and a MessageService is available, it rebuilds
// the context from the persistent messages table and warms the cache.
func (cm *contextManager) GetContext(ctx context.Context, sessionID string) ([]chat.Message, error) {
	messages, err := cm.storage.Load(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to load context: %w", err)
	}

	if len(messages) > 0 {
		logger.Debugf(ctx, "[ContextManager][Session-%s] Cache hit: %d messages", sessionID, len(messages))
		return messages, nil
	}

	if cm.messageRepo == nil {
		return messages, nil
	}

	// Cache miss — rebuild from DB
	rebuilt, err := cm.rebuildFromDB(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "[ContextManager][Session-%s] Failed to rebuild context from DB: %v", sessionID, err)
		return []chat.Message{}, nil
	}

	if len(rebuilt) > 0 {
		if saveErr := cm.storage.Save(ctx, sessionID, rebuilt); saveErr != nil {
			logger.Warnf(ctx, "[ContextManager][Session-%s] Failed to warm cache: %v", sessionID, saveErr)
		}
		logger.Infof(ctx, "[ContextManager][Session-%s] Rebuilt %d messages from DB", sessionID, len(rebuilt))
	}

	return rebuilt, nil
}

// rebuildFromDB loads recent messages from the persistent messages table
// and converts them into chat.Message pairs (user + assistant).
func (cm *contextManager) rebuildFromDB(ctx context.Context, sessionID string) ([]chat.Message, error) {
	dbMessages, err := cm.messageRepo.GetRecentMessagesBySession(ctx, sessionID, dbFallbackFetchCount)
	if err != nil {
		return nil, fmt.Errorf("failed to load messages from DB: %w", err)
	}
	if len(dbMessages) == 0 {
		return nil, nil
	}

	// Group by RequestID into Q&A pairs, same logic as chat_pipeline/common.go
	type pair struct {
		query     string
		answer    string
		createdAt time.Time
	}
	pairMap := make(map[string]*pair)
	for _, msg := range dbMessages {
		p, ok := pairMap[msg.RequestID]
		if !ok {
			p = &pair{}
			pairMap[msg.RequestID] = p
		}
		switch msg.Role {
		case "user":
			if msg.RenderedContent != "" {
				p.query = msg.RenderedContent
			} else {
				p.query = msg.Content
			}
			p.createdAt = msg.CreatedAt
			if desc := extractImageCaptions(msg.Images); desc != "" && msg.RenderedContent == "" {
				p.query += "\n\n[用户上传图片内容]\n" + desc
			}
		case "assistant":
			p.answer = regThinkTags.ReplaceAllString(msg.Content, "")
		}
	}

	pairs := make([]*pair, 0, len(pairMap))
	for _, p := range pairMap {
		if p.query != "" && p.answer != "" {
			pairs = append(pairs, p)
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].createdAt.Before(pairs[j].createdAt)
	})

	result := make([]chat.Message, 0, len(pairs)*2)
	for _, p := range pairs {
		result = append(result,
			chat.Message{Role: "user", Content: p.query},
			chat.Message{Role: "assistant", Content: p.answer},
		)
	}

	return result, nil
}

// extractImageCaptions concatenates non-empty Caption fields from message
// images so that previous turns' image descriptions are included in context.
func extractImageCaptions(images types.MessageImages) string {
	var parts []string
	for _, img := range images {
		if img.Caption != "" {
			parts = append(parts, img.Caption)
		}
	}
	return strings.Join(parts, "\n")
}

// ClearContext removes all context for a session.
func (cm *contextManager) ClearContext(ctx context.Context, sessionID string) error {
	if err := cm.storage.Delete(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to clear context: %w", err)
	}
	logger.Infof(ctx, "[ContextManager][Session-%s] Context cleared", sessionID)
	return nil
}

// GetContextStats returns statistics about the stored context.
func (cm *contextManager) GetContextStats(ctx context.Context, sessionID string) (*interfaces.ContextStats, error) {
	messages, err := cm.storage.Load(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to load context: %w", err)
	}

	return &interfaces.ContextStats{
		MessageCount:         len(messages),
		OriginalMessageCount: len(messages),
	}, nil
}

// SetSystemPrompt sets or updates the system prompt for a session.
func (cm *contextManager) SetSystemPrompt(ctx context.Context, sessionID string, systemPrompt string) error {
	messages, err := cm.storage.Load(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to load context: %w", err)
	}

	systemMessage := chat.Message{
		Role:    "system",
		Content: systemPrompt,
	}

	if len(messages) > 0 && messages[0].Role == "system" {
		messages[0] = systemMessage
	} else {
		messages = append([]chat.Message{systemMessage}, messages...)
	}

	if err := cm.storage.Save(ctx, sessionID, messages); err != nil {
		return fmt.Errorf("failed to save context: %w", err)
	}

	logger.Debugf(ctx, "[ContextManager][Session-%s] System prompt set (length=%d)", sessionID, len(systemPrompt))
	return nil
}
