package im

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	// qaTimeout is the maximum time to wait for the QA pipeline to complete.
	qaTimeout = 120 * time.Second
	// dedupTTL is how long processed message IDs are retained.
	dedupTTL = 5 * time.Minute
	// dedupCleanupInterval is how often the dedup map is cleaned.
	dedupCleanupInterval = 1 * time.Minute
	// maxContentLength is the maximum allowed message content length.
	maxContentLength = 4096
)

// Service orchestrates IM message handling:
// 1. Receives a unified IncomingMessage from an Adapter
// 2. Resolves or creates a WeKnora session for the IM channel
// 3. Calls the WeKnora QA pipeline
// 4. Collects the streaming answer and sends it back via the Adapter
type Service struct {
	db             *gorm.DB
	sessionService interfaces.SessionService
	messageService interfaces.MessageService
	tenantService  interfaces.TenantService
	agentService   interfaces.CustomAgentService

	adapters map[Platform]Adapter
	mu       sync.RWMutex

	// processedMsgs tracks recently processed message IDs to prevent duplicate handling.
	processedMsgs sync.Map

	stopCh chan struct{}
}

// NewService creates a new IM service.
func NewService(
	db *gorm.DB,
	sessionService interfaces.SessionService,
	messageService interfaces.MessageService,
	tenantService interfaces.TenantService,
	agentService interfaces.CustomAgentService,
) *Service {
	s := &Service{
		db:             db,
		sessionService: sessionService,
		messageService: messageService,
		tenantService:  tenantService,
		agentService:   agentService,
		adapters:       make(map[Platform]Adapter),
		stopCh:         make(chan struct{}),
	}

	// Start periodic dedup cleanup instead of per-message goroutines
	go s.dedupCleanupLoop()

	return s
}

// Stop gracefully shuts down the service, stopping background goroutines.
func (s *Service) Stop() {
	close(s.stopCh)
}

// dedupCleanupLoop periodically cleans up expired entries from the dedup map.
func (s *Service) dedupCleanupLoop() {
	ticker := time.NewTicker(dedupCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-dedupTTL)
			s.processedMsgs.Range(func(key, value interface{}) bool {
				if t, ok := value.(time.Time); ok && t.Before(cutoff) {
					s.processedMsgs.Delete(key)
				}
				return true
			})
		case <-s.stopCh:
			return
		}
	}
}

// RegisterAdapter registers an IM platform adapter.
func (s *Service) RegisterAdapter(adapter Adapter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapters[adapter.Platform()] = adapter
}

// GetAdapter returns the adapter for a given platform.
func (s *Service) GetAdapter(platform Platform) (Adapter, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.adapters[platform]
	return a, ok
}

// HandleMessage processes an incoming IM message end-to-end:
// resolves session, runs QA, sends reply.
func (s *Service) HandleMessage(ctx context.Context, msg *IncomingMessage, tenantID uint64, agentID string, kbIDs []string) error {
	// Dedup: skip if this message was already processed (IM platforms may retry)
	if msg.MessageID != "" {
		if _, loaded := s.processedMsgs.LoadOrStore(msg.MessageID, time.Now()); loaded {
			logger.Infof(ctx, "[IM] Skipping duplicate message: %s", msg.MessageID)
			return nil
		}
	}

	// Reject overly long messages to protect the QA pipeline
	contentRunes := []rune(msg.Content)
	if len(contentRunes) > maxContentLength {
		logger.Warnf(ctx, "[IM] Message too long (%d runes), truncating to %d", len(contentRunes), maxContentLength)
		msg.Content = string(contentRunes[:maxContentLength])
	}

	logger.Infof(ctx, "[IM] HandleMessage: platform=%s user=%s chat=%s content_len=%d",
		msg.Platform, msg.UserID, msg.ChatID, len(msg.Content))

	// 1. Get tenant (once, shared across resolve + QA)
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("get tenant: %w", err)
	}
	sessionCtx := context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	sessionCtx = context.WithValue(sessionCtx, types.TenantInfoContextKey, tenant)

	// 2. Resolve or create a WeKnora session
	channelSession, err := s.resolveSession(sessionCtx, msg, tenantID, agentID)
	if err != nil {
		return fmt.Errorf("resolve session: %w", err)
	}

	// 3. Get the WeKnora session
	session, err := s.sessionService.GetSession(sessionCtx, channelSession.SessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	// 4. Resolve custom agent (optional)
	var customAgent *types.CustomAgent
	if agentID != "" {
		agent, err := s.agentService.GetAgentByID(sessionCtx, agentID)
		if err != nil {
			logger.Warnf(ctx, "[IM] Failed to get agent %s: %v, using default", agentID, err)
		} else {
			customAgent = agent
		}
	}

	// 5. Run the QA pipeline and collect the full answer
	answer, err := s.runQA(sessionCtx, session, msg.Content, customAgent, kbIDs)
	if err != nil {
		logger.Errorf(ctx, "[IM] QA failed: %v, sending fallback reply", err)
		answer = "抱歉，处理您的问题时出现了异常，请稍后再试。"
	}

	// 6. Send the reply back via the platform adapter
	adapter, ok := s.GetAdapter(msg.Platform)
	if !ok {
		return fmt.Errorf("no adapter for platform: %s", msg.Platform)
	}

	reply := &ReplyMessage{
		Content: answer,
		IsFinal: true,
	}
	if err := adapter.SendReply(ctx, msg, reply); err != nil {
		return fmt.Errorf("send reply: %w", err)
	}

	logger.Infof(ctx, "[IM] Reply sent: platform=%s user=%s answer_len=%d", msg.Platform, msg.UserID, len(answer))
	return nil
}

// resolveSession finds or creates a ChannelSession for the given IM message.
// ctx must already carry TenantIDContextKey and TenantInfoContextKey.
func (s *Service) resolveSession(ctx context.Context, msg *IncomingMessage, tenantID uint64, agentID string) (*ChannelSession, error) {
	var cs ChannelSession
	result := s.db.Where("platform = ? AND user_id = ? AND chat_id = ? AND tenant_id = ? AND deleted_at IS NULL",
		string(msg.Platform), msg.UserID, msg.ChatID, tenantID).
		First(&cs)

	if result.Error == nil {
		return &cs, nil
	}

	if result.Error != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("query channel session: %w", result.Error)
	}

	// Create a new WeKnora session
	title := fmt.Sprintf("IM-%s", msg.Platform)
	if msg.UserName != "" {
		title = fmt.Sprintf("IM-%s-%s", msg.Platform, msg.UserName)
	}

	newSession := &types.Session{
		TenantID:    tenantID,
		Title:       title,
		Description: fmt.Sprintf("Auto-created from %s IM integration", msg.Platform),
	}

	createdSession, err := s.sessionService.CreateSession(ctx, newSession)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Create the channel-session mapping; use a unique constraint fallback
	// to handle concurrent creation attempts for the same channel.
	cs = ChannelSession{
		Platform:  string(msg.Platform),
		UserID:    msg.UserID,
		ChatID:    msg.ChatID,
		SessionID: createdSession.ID,
		TenantID:  tenantID,
		AgentID:   agentID,
	}
	if err := s.db.Create(&cs).Error; err != nil {
		// If the insert failed due to unique constraint (concurrent request),
		// fetch the existing record.
		var existing ChannelSession
		if findErr := s.db.Where("platform = ? AND user_id = ? AND chat_id = ? AND tenant_id = ? AND deleted_at IS NULL",
			string(msg.Platform), msg.UserID, msg.ChatID, tenantID).
			First(&existing).Error; findErr != nil {
			return nil, fmt.Errorf("create channel session: %w (lookup fallback: %v)", err, findErr)
		}
		return &existing, nil
	}

	logger.Infof(ctx, "[IM] Created new session mapping: channel=%s/%s/%s -> session=%s",
		msg.Platform, msg.UserID, msg.ChatID, createdSession.ID)

	return &cs, nil
}

// runQA executes the WeKnora QA pipeline and returns the full answer text.
func (s *Service) runQA(ctx context.Context, session *types.Session, query string, customAgent *types.CustomAgent, kbIDs []string) (string, error) {
	// Add timeout to prevent indefinite blocking
	ctx, cancel := context.WithTimeout(ctx, qaTimeout)
	defer cancel()

	eventBus := event.NewEventBus()

	// Thread-safe answer collection
	var answerMu sync.Mutex
	var answerBuilder strings.Builder
	var qaErr error
	done := make(chan struct{})
	var closeOnce sync.Once
	closeDone := func() { closeOnce.Do(func() { close(done) }) }

	eventBus.On(event.EventAgentFinalAnswer, func(ctx context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		answerMu.Lock()
		answerBuilder.WriteString(data.Content)
		answerMu.Unlock()
		if data.Done {
			closeDone()
		}
		return nil
	})

	eventBus.On(event.EventError, func(ctx context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			return nil
		}
		logger.Errorf(ctx, "[IM] QA error: %s", data.Error)
		answerMu.Lock()
		qaErr = fmt.Errorf("QA pipeline error: %s", data.Error)
		answerMu.Unlock()
		closeDone()
		return nil
	})

	// Determine whether to use agent mode
	useAgent := customAgent != nil && customAgent.IsAgentMode()

	// Generate a shared RequestID to pair user and assistant messages for history
	requestID := uuid.New().String()

	// Create user message so it appears in conversation history
	userMsg, err := s.messageService.CreateMessage(ctx, &types.Message{
		SessionID:   session.ID,
		Role:        "user",
		Content:     query,
		RequestID:   requestID,
		CreatedAt:   time.Now(),
		IsCompleted: true,
	})
	if err != nil {
		return "", fmt.Errorf("create user message: %w", err)
	}
	_ = userMsg

	// Create a placeholder assistant message
	assistantMsg, err := s.messageService.CreateMessage(ctx, &types.Message{
		SessionID:   session.ID,
		Role:        "assistant",
		RequestID:   requestID,
		CreatedAt:   time.Now(),
		IsCompleted: false,
	})
	if err != nil {
		return "", fmt.Errorf("create assistant message: %w", err)
	}

	// Run QA async
	go func() {
		var err error
		if useAgent {
			err = s.sessionService.AgentQA(ctx, session, query, assistantMsg.ID, "", eventBus, customAgent, kbIDs, nil)
		} else {
			err = s.sessionService.KnowledgeQA(ctx, session, query, kbIDs, nil, assistantMsg.ID, "", false, eventBus, customAgent, false)
		}
		if err != nil {
			logger.Errorf(ctx, "[IM] QA execution error: %v", err)
			answerMu.Lock()
			qaErr = fmt.Errorf("QA execution error: %w", err)
			answerMu.Unlock()
			closeDone()
		}
	}()

	// Wait for completion or timeout
	select {
	case <-done:
	case <-ctx.Done():
		// Mark assistant message as completed to avoid dangling incomplete records
		assistantMsg.Content = "抱歉，回答超时，请稍后再试。"
		assistantMsg.IsCompleted = true
		// Use a fresh context since the original is cancelled
		if updateErr := s.messageService.UpdateMessage(context.WithoutCancel(ctx), assistantMsg); updateErr != nil {
			logger.Warnf(ctx, "[IM] Failed to update timed-out assistant message: %v", updateErr)
		}
		return "", fmt.Errorf("QA timed out after %v", qaTimeout)
	}

	answerMu.Lock()
	answer := answerBuilder.String()
	qaError := qaErr
	answerMu.Unlock()

	if answer == "" && qaError != nil {
		return "", qaError
	}
	if answer == "" {
		answer = "抱歉，我暂时无法回答这个问题。"
	}

	// Update assistant message with the final answer content
	assistantMsg.Content = answer
	assistantMsg.IsCompleted = true
	if err := s.messageService.UpdateMessage(ctx, assistantMsg); err != nil {
		logger.Warnf(ctx, "[IM] Failed to update assistant message: %v", err)
	}

	return answer, nil
}
