package im

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
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
	// streamFlushInterval is how often buffered stream content is flushed to the IM platform.
	// This prevents API rate-limiting while keeping perceived latency low.
	streamFlushInterval = 300 * time.Millisecond
)

// channelState holds runtime state for a running IM channel.
type channelState struct {
	Channel *IMChannel
	Adapter Adapter
	Cancel  context.CancelFunc // for stopping websocket goroutines
}

// AdapterFactory creates an Adapter from an IMChannel configuration.
// The second return value is an optional cleanup function (e.g., for stopping websocket connections).
type AdapterFactory func(ctx context.Context, channel *IMChannel, msgHandler func(ctx context.Context, msg *IncomingMessage) error) (Adapter, context.CancelFunc, error)

// Service orchestrates IM message handling:
// 1. Receives a unified IncomingMessage from an Adapter
// 2. Resolves or creates a WeKnora session for the IM channel
// 3. Dispatches slash-commands (/help, /kb, /clear, etc.) without entering QA
// 4. Calls the WeKnora QA pipeline for normal messages
// 5. Collects the streaming answer and sends it back via the Adapter
type Service struct {
	db             *gorm.DB
	sessionService interfaces.SessionService
	messageService interfaces.MessageService
	tenantService  interfaces.TenantService
	agentService   interfaces.CustomAgentService

	// knowledgeService is used for saving IM file messages to knowledge bases.
	knowledgeService interfaces.KnowledgeService

	// kbService is used by slash-commands (/info) to list and inspect knowledge bases.
	kbService interfaces.KnowledgeBaseService

	// modelService is used to obtain the chat model for generating smart notification replies.
	modelService interfaces.ModelService

	// cmdRegistry holds all registered slash-commands.
	cmdRegistry *CommandRegistry

	// channels maps channel ID -> running channel state
	channels map[string]*channelState
	mu       sync.RWMutex

	// adapterFactories maps platform name -> factory function
	adapterFactories map[string]AdapterFactory

	// processedMsgs tracks recently processed message IDs to prevent duplicate handling.
	processedMsgs sync.Map

	// rateLimiter enforces per-user sliding window rate limiting.
	rateLimiter *slidingWindowLimiter

	// inflight tracks cancel functions for in-progress QA requests, keyed by
	// "channelID:userID:chatID". This allows /stop to abort a running request.
	inflight sync.Map // key -> context.CancelFunc

	// qaQueue manages bounded queuing and worker-pool execution of QA requests,
	// providing backpressure to protect downstream LLM resources.
	qaQueue *qaQueue

	stopCh chan struct{}
}

// makeUserKey builds the canonical key used to identify a user's request
// across the queue, inflight map, and /stop command.
func makeUserKey(channelID, userID, chatID string) string {
	return fmt.Sprintf("%s:%s:%s", channelID, userID, chatID)
}

func buildIMQARequest(
	session *types.Session,
	query string,
	assistantMessageID string,
	userMessageID string,
	customAgent *types.CustomAgent,
	kbIDs []string,
) *types.QARequest {
	// WebSearchEnabled: the web handler passes this per-request from the
	// frontend toggle; for IM channels the user has no per-message toggle,
	// so we derive it from the agent config (the single source of truth).
	webSearchEnabled := customAgent != nil && customAgent.Config.WebSearchEnabled
	return &types.QARequest{
		Session:            session,
		Query:              query,
		AssistantMessageID: assistantMessageID,
		CustomAgent:        customAgent,
		KnowledgeBaseIDs:   kbIDs,
		UserMessageID:      userMessageID,
		WebSearchEnabled:   webSearchEnabled,
	}
}

// NewService creates a new IM service.
func NewService(
	db *gorm.DB,
	sessionService interfaces.SessionService,
	messageService interfaces.MessageService,
	tenantService interfaces.TenantService,
	agentService interfaces.CustomAgentService,
	knowledgeService interfaces.KnowledgeService,
	kbService interfaces.KnowledgeBaseService,
	modelService interfaces.ModelService,
) *Service {
	// Build command registry.
	registry := NewCommandRegistry()
	registry.Register(newHelpCommand(registry))
	registry.Register(newInfoCommand(kbService))
	registry.Register(newSearchCommand(sessionService, kbService))
	registry.Register(newStopCommand())
	registry.Register(newClearCommand())

	s := &Service{
		db:               db,
		sessionService:   sessionService,
		messageService:   messageService,
		tenantService:    tenantService,
		agentService:     agentService,
		knowledgeService: knowledgeService,
		kbService:        kbService,
		modelService:     modelService,
		cmdRegistry:      registry,
		channels:         make(map[string]*channelState),
		adapterFactories: make(map[string]AdapterFactory),
		rateLimiter:      newSlidingWindowLimiter(rateLimitWindow, rateLimitMaxRequests),
		stopCh:           make(chan struct{}),
	}

	// Initialize the QA worker pool and bounded queue.
	s.qaQueue = newQAQueue(defaultWorkers, defaultMaxQueueSize, defaultMaxPerUser, s.executeQARequest)
	s.qaQueue.Start(s.stopCh)

	// Start periodic dedup cleanup instead of per-message goroutines
	go s.dedupCleanupLoop()
	go s.rateLimiter.cleanupLoop(s.stopCh)

	return s
}

// RegisterAdapterFactory registers a factory for creating adapters for a given platform.
func (s *Service) RegisterAdapterFactory(platform string, factory AdapterFactory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapterFactories[platform] = factory
}

// Stop gracefully shuts down the service, stopping all channels and background goroutines.
func (s *Service) Stop() {
	close(s.stopCh)
	s.qaQueue.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cs := range s.channels {
		if cs.Cancel != nil {
			cs.Cancel()
		}
		delete(s.channels, id)
	}
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

// LoadAndStartChannels loads all enabled channels from the database and starts them.
func (s *Service) LoadAndStartChannels() error {
	ctx := context.Background()
	var channels []IMChannel
	if err := s.db.Where("enabled = ? AND deleted_at IS NULL", true).Find(&channels).Error; err != nil {
		return fmt.Errorf("load im channels: %w", err)
	}

	for i := range channels {
		ch := channels[i]
		if err := s.StartChannel(&ch); err != nil {
			logger.Warnf(ctx, "[IM] Failed to start channel %s (%s/%s): %v", ch.ID, ch.Platform, ch.Name, err)
		} else {
			logger.Infof(ctx, "[IM] Started channel: id=%s platform=%s name=%s mode=%s agent=%s",
				ch.ID, ch.Platform, ch.Name, ch.Mode, ch.AgentID)
		}
	}

	logger.Infof(ctx, "[IM] Loaded %d enabled channels", len(channels))
	return nil
}

// StartChannel creates and registers an adapter for the given channel.
func (s *Service) StartChannel(channel *IMChannel) error {
	s.mu.Lock()
	factory, ok := s.adapterFactories[channel.Platform]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("no adapter factory for platform: %s", channel.Platform)
	}
	// Stop existing channel if running
	if existing, ok := s.channels[channel.ID]; ok {
		if existing.Cancel != nil {
			existing.Cancel()
		}
		delete(s.channels, channel.ID)
	}
	s.mu.Unlock()

	// Build the message handler that delegates to HandleMessage with this channel's config
	msgHandler := func(msgCtx context.Context, msg *IncomingMessage) error {
		return s.HandleMessage(msgCtx, msg, channel.ID)
	}

	ctx := context.Background()
	adapter, cancelFn, err := factory(ctx, channel, msgHandler)
	if err != nil {
		return fmt.Errorf("create adapter: %w", err)
	}

	s.mu.Lock()
	s.channels[channel.ID] = &channelState{
		Channel: channel,
		Adapter: adapter,
		Cancel:  cancelFn,
	}
	s.mu.Unlock()

	return nil
}

// StopChannel stops and removes a running channel.
func (s *Service) StopChannel(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cs, ok := s.channels[channelID]; ok {
		if cs.Cancel != nil {
			cs.Cancel()
		}
		delete(s.channels, channelID)
		logger.Infof(context.Background(), "[IM] Stopped channel: id=%s", channelID)
	}
}

// GetChannelAdapter returns the adapter and channel config for a given channel ID.
func (s *Service) GetChannelAdapter(channelID string) (Adapter, *IMChannel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cs, ok := s.channels[channelID]
	if !ok {
		return nil, nil, false
	}
	return cs.Adapter, cs.Channel, true
}

// GetChannelByID loads a channel from the database.
func (s *Service) GetChannelByID(channelID string) (*IMChannel, error) {
	var ch IMChannel
	if err := s.db.Where("id = ? AND deleted_at IS NULL", channelID).First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

// GetChannelByIDAndTenant loads a channel from the database, scoped to a specific tenant.
func (s *Service) GetChannelByIDAndTenant(channelID string, tenantID uint64) (*IMChannel, error) {
	var ch IMChannel
	if err := s.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", channelID, tenantID).First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

// HandleMessage processes an incoming IM message end-to-end using channel config.
func (s *Service) HandleMessage(ctx context.Context, msg *IncomingMessage, channelID string) error {
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

	// Get channel config (moved before rate limit so we can reply to the user)
	adapter, channel, ok := s.GetChannelAdapter(channelID)
	if !ok {
		// Try loading from DB (channel might have been created after service start)
		ch, err := s.GetChannelByID(channelID)
		if err != nil {
			return fmt.Errorf("channel not found: %s", channelID)
		}
		// Start it dynamically
		if err := s.StartChannel(ch); err != nil {
			return fmt.Errorf("start channel %s: %w", channelID, err)
		}
		adapter, channel, ok = s.GetChannelAdapter(channelID)
		if !ok {
			return fmt.Errorf("channel adapter not available after start: %s", channelID)
		}
	}

	// Rate limit: enforce per-user sliding window to prevent abuse.
	// Slash-commands (/stop, /clear, etc.) bypass rate limiting so the user
	// always retains control over the bot even under heavy messaging.
	isCommand := s.cmdRegistry.IsRegistered(msg.Content)
	if !isCommand {
		rateLimitKey := makeUserKey(channelID, msg.UserID, msg.ChatID)
		if !s.rateLimiter.Allow(rateLimitKey) {
			logger.Warnf(ctx, "[IM] Rate limited: channel=%s user=%s chat=%s", channelID, msg.UserID, msg.ChatID)
			_ = adapter.SendReply(ctx, msg, &ReplyMessage{
				Content: "您的消息发送过于频繁，请稍后再试。",
				IsFinal: true,
			})
			return nil
		}
	}

	tenantID := channel.TenantID
	agentID := channel.AgentID

	logger.Infof(ctx, "[IM] HandleMessage: channel=%s platform=%s user=%s chat=%s msgtype=%s content_len=%d",
		channelID, msg.Platform, msg.UserID, msg.ChatID, msg.MessageType, len(msg.Content))
	logger.Debugf(ctx, "[IM] HandleMessage detail: msgid=%s filekey=%s filename=%s",
		msg.MessageID, msg.FileKey, msg.FileName)

	// ── File/Image message shortcut ──
	// If the message is a file or image and the channel has a knowledge_base_id configured,
	// handle it separately without entering the QA pipeline.
	if (msg.MessageType == MessageTypeFile || msg.MessageType == MessageTypeImage) && channel.KnowledgeBaseID != "" {
		return s.handleFileMessage(ctx, msg, adapter, channel)
	}

	// 1. Get tenant
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("get tenant: %w", err)
	}
	sessionCtx := context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	sessionCtx = context.WithValue(sessionCtx, types.TenantInfoContextKey, tenant)

	// 2. Resolve or create a WeKnora session
	channelSession, err := s.resolveSession(sessionCtx, msg, tenantID, agentID, channelID)
	if err != nil {
		return fmt.Errorf("resolve session: %w", err)
	}

	// 3. Resolve custom agent (optional)
	var customAgent *types.CustomAgent
	if agentID != "" {
		agent, err := s.agentService.GetAgentByID(sessionCtx, agentID)
		if err != nil {
			logger.Warnf(ctx, "[IM] Failed to get agent %s: %v, using default", agentID, err)
		} else {
			customAgent = agent
		}
	}

	// ── Slash-command dispatch ──
	// Commands are handled before the QA pipeline so they respond instantly.
	if cmd, args, ok := s.cmdRegistry.Parse(msg.Content); ok {
		return s.handleCommand(sessionCtx, cmd, args, msg, adapter, channel, channelSession, customAgent)
	}
	// Unrecognised slash-word: show help hint instead of sending to QA.
	if LooksLikeCommand(msg.Content) {
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: "未知指令，发送 `/help` 查看所有可用指令。",
			IsFinal: true,
		})
		return nil
	}

	// 4. Get the WeKnora session
	session, err := s.sessionService.GetSession(sessionCtx, channelSession.SessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	// 5. Enqueue the QA request into the bounded worker pool.
	// The worker pool controls LLM concurrency and provides backpressure.
	qaCtx, qaCancel := context.WithCancel(sessionCtx)
	userKey := makeUserKey(channelID, msg.UserID, msg.ChatID)

	req := &qaRequest{
		ctx:       qaCtx,
		cancel:    qaCancel,
		msg:       msg,
		session:   session,
		agent:     customAgent,
		adapter:   adapter,
		channel:   channel,
		channelID: channelID,
		userKey:   userKey,
	}

	pos, enqueueErr := s.qaQueue.Enqueue(req)
	if enqueueErr != nil {
		qaCancel()
		logger.Warnf(ctx, "[IM] Queue rejected: user=%s reason=%v", msg.UserID, enqueueErr)
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: "当前排队人数较多，请稍后再试。",
			IsFinal: true,
		})
		return nil
	}

	if pos > 0 {
		logger.Infof(ctx, "[IM] Enqueued: user=%s pos=%d depth=%d", msg.UserID, pos, s.qaQueue.Metrics().Depth)
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: fmt.Sprintf("收到，前面还有 %d 条消息在处理，请稍候 ⏳", pos),
			IsFinal: true,
		})
	} else {
		logger.Infof(ctx, "[IM] Enqueued: user=%s pos=0 (immediate)", msg.UserID)
	}

	return nil
}

// executeQARequest is the worker handler that runs the QA pipeline for a queued request.
// It is called by qaQueue workers and must not block indefinitely.
func (s *Service) executeQARequest(req *qaRequest) {
	ctx := req.ctx
	defer req.cancel()

	// Track in-flight request so /stop can cancel it.
	s.inflight.Store(req.userKey, req.cancel)
	defer s.inflight.Delete(req.userKey)

	// kbIDs is left empty so the QA pipeline resolves them from the agent config.
	var kbIDs []string

	// Determine output mode from channel config.
	streamDisabled := req.channel.OutputMode == "full"

	// If the adapter supports streaming and output is not "full", use streaming.
	if !streamDisabled {
		if streamer, ok := req.adapter.(StreamSender); ok {
			if err := s.handleMessageStream(ctx, req.msg, req.session, req.agent, kbIDs, streamer, req.adapter); err != nil {
				logger.Errorf(ctx, "[IM] Stream QA failed: %v", err)
			}
			return
		}
	}

	// Non-streaming fallback: collect full answer then send.
	answer, err := s.runQA(ctx, req.session, req.msg.Content, req.agent, kbIDs)
	if err != nil {
		logger.Errorf(ctx, "[IM] QA failed: %v, sending fallback reply", err)
		answer = "抱歉，处理您的问题时出现了异常，请稍后再试。"
	}

	reply := &ReplyMessage{
		Content: answer,
		IsFinal: true,
	}
	if err := req.adapter.SendReply(ctx, req.msg, reply); err != nil {
		logger.Errorf(ctx, "[IM] Send reply failed: %v", err)
		return
	}

	logger.Infof(ctx, "[IM] Reply sent: channel=%s platform=%s user=%s answer_len=%d",
		req.channelID, req.msg.Platform, req.msg.UserID, len(answer))
}

// handleCommand executes a slash-command and sends the result back to the user.
// It also handles side effects (ActionClear, ActionStop).
func (s *Service) handleCommand(
	ctx context.Context,
	cmd Command,
	args []string,
	msg *IncomingMessage,
	adapter Adapter,
	channel *IMChannel,
	channelSession *ChannelSession,
	customAgent *types.CustomAgent,
) error {
	agentName := ""
	if customAgent != nil {
		agentName = customAgent.Name
	}

	cmdCtx := &CommandContext{
		Incoming:          msg,
		Session:           channelSession,
		TenantID:          channel.TenantID,
		AgentName:         agentName,
		CustomAgent:       customAgent,
		ChannelOutputMode: channel.OutputMode,
	}

	result, err := cmd.Execute(ctx, cmdCtx, args)
	if err != nil {
		logger.Errorf(ctx, "[IM] Command /%s error: %v", cmd.Name(), err)
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: "抱歉，执行指令时出现了异常，请稍后再试。",
			IsFinal: true,
		})
		return err
	}

	// Handle service-level side effects.
	switch result.Action {
	case ActionClear:
		// Soft-delete the current ChannelSession and clear the LLM context
		// so the next message creates a completely fresh conversation.
		if err := s.db.Model(&ChannelSession{}).
			Where("id = ?", channelSession.ID).
			Update("deleted_at", time.Now()).Error; err != nil {
			logger.Warnf(ctx, "[IM] Failed to soft-delete channel session: %v", err)
		}
		if err := s.sessionService.ClearContext(ctx, channelSession.SessionID); err != nil {
			logger.Warnf(ctx, "[IM] Failed to clear session context: %v", err)
		}
	case ActionStop:
		// Cancel the request — first check if it's queued, then check if it's in-flight.
		inflightKey := makeUserKey(channel.ID, msg.UserID, msg.ChatID)
		if s.qaQueue.Remove(inflightKey) {
			logger.Infof(ctx, "[IM] Cancelled queued QA: key=%s", inflightKey)
		} else if cancelFn, loaded := s.inflight.LoadAndDelete(inflightKey); loaded {
			cancelFn.(context.CancelFunc)()
			logger.Infof(ctx, "[IM] Cancelled in-flight QA: key=%s", inflightKey)
		}
	}

	// Send the command reply, respecting the configured output mode.
	sent := false
	if channel.OutputMode != "full" {
		if streamer, ok := adapter.(StreamSender); ok {
			if err := s.sendStreamReply(ctx, msg, streamer, result.Content); err != nil {
				logger.Warnf(ctx, "[IM] Stream reply for command /%s failed, falling back: %v", cmd.Name(), err)
			} else {
				sent = true
			}
		}
	}
	if !sent {
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: result.Content,
			IsFinal: true,
		})
	}

	logger.Infof(ctx, "[IM] Command /%s executed: channel=%s user=%s action=%d",
		cmd.Name(), channel.ID, msg.UserID, result.Action)
	return nil
}

// sendStreamReply sends a complete content string via the streaming interface
// (StartStream → SendStreamChunk → EndStream). This is used for command replies
// when the output mode is set to "stream", so they visually match QA responses.
func (s *Service) sendStreamReply(ctx context.Context, msg *IncomingMessage, streamer StreamSender, content string) error {
	streamID, err := streamer.StartStream(ctx, msg)
	if err != nil {
		return fmt.Errorf("start stream: %w", err)
	}
	if err := streamer.SendStreamChunk(ctx, msg, streamID, content); err != nil {
		return fmt.Errorf("send stream chunk: %w", err)
	}
	if err := streamer.EndStream(ctx, msg, streamID); err != nil {
		return fmt.Errorf("end stream: %w", err)
	}
	return nil
}

// resolveSession finds or creates a ChannelSession for the given IM message.
// ctx must already carry TenantIDContextKey and TenantInfoContextKey.
func (s *Service) resolveSession(ctx context.Context, msg *IncomingMessage, tenantID uint64, agentID string, imChannelID string) (*ChannelSession, error) {
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
		Platform:    string(msg.Platform),
		UserID:      msg.UserID,
		ChatID:      msg.ChatID,
		SessionID:   createdSession.ID,
		TenantID:    tenantID,
		AgentID:     agentID,
		IMChannelID: imChannelID,
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

// ── Agent tool call progress formatting ──────────────────────────────
// These helpers format tool-call / tool-result events as Markdown text
// that is injected into the streaming reply so IM users can see the
// agent's reasoning process in real-time.
// ─────────────────────────────────────────────────────────────────────

// toolDisplayNames maps internal tool function names to user-friendly labels.
var toolDisplayNames = map[string]string{
	"thinking":              "深度思考",
	"todo_write":            "制定计划",
	"knowledge_search":      "知识库检索",
	"grep_chunks":           "关键词搜索",
	"list_knowledge_chunks": "查看文档分块",
	"query_knowledge_graph": "查询知识图谱",
	"get_document_info":     "获取文档信息",
	"database_query":        "查询数据库",
	"data_analysis":         "数据分析",
	"data_schema":           "查看数据元信息",
	"web_search":            "网络搜索",
	"web_fetch":             "网页阅读",
	"read_skill":            "读取技能",
	"execute_skill_script":  "执行技能脚本",
	"final_answer":          "生成回答",
}

// internalToolNames lists tools whose execution should NOT be displayed in IM
// messages because they are internal reasoning aids (thinking, planning) rather
// than user-facing actions.
var internalToolNames = map[string]bool{
	"thinking":   true,
	"todo_write": true,
}

// friendlyToolName returns a human-readable name for a tool.
func friendlyToolName(toolName string) string {
	if display, ok := toolDisplayNames[toolName]; ok {
		return display
	}
	return toolName
}

// isToolVisibleToUser returns true if the tool's execution progress should be
// displayed to the IM user. Internal reasoning tools (thinking, planning) and
// the final_answer pseudo-tool are hidden.
func isToolVisibleToUser(toolName string) bool {
	if toolName == "final_answer" {
		return false
	}
	return !internalToolNames[toolName]
}

// formatToolCallStart returns a plain-text line for a tool invocation (inside <think> block).
func formatToolCallStart(toolName string) string {
	return fmt.Sprintf("⏳ %s\n", friendlyToolName(toolName))
}

// formatToolCallResult returns a plain-text line for a tool result (inside <think> block).
func formatToolCallResult(toolName string, success bool, output string) string {
	friendly := friendlyToolName(toolName)
	if success {
		if summary := briefToolSummary(output); summary != "" {
			return fmt.Sprintf("✅ %s · %s\n", friendly, summary)
		}
		return fmt.Sprintf("✅ %s\n", friendly)
	}
	return fmt.Sprintf("⚠️ %s 失败\n", friendly)
}

// briefToolSummary extracts a short human-readable summary from tool output.
// Returns empty string if no suitable summary can be extracted.
func briefToolSummary(output string) string {
	const maxRunes = 40
	if output == "" {
		return ""
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	// Skip structured data (JSON, XML, etc.)
	if output[0] == '{' || output[0] == '[' || output[0] == '<' {
		return ""
	}
	// Take first non-empty line
	if idx := strings.IndexByte(output, '\n'); idx >= 0 {
		output = strings.TrimSpace(output[:idx])
	}
	if output == "" {
		return ""
	}
	runes := []rune(output)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return output
}

// handleMessageStream runs the QA pipeline and streams answer chunks to the IM platform
// in real-time via the StreamSender interface. Chunks are batched at streamFlushInterval
// to avoid API rate-limiting.
func (s *Service) handleMessageStream(ctx context.Context, msg *IncomingMessage, session *types.Session, customAgent *types.CustomAgent, kbIDs []string, streamer StreamSender, adapter Adapter) error {
	// Start the stream on the IM platform (e.g., create Feishu streaming card)
	streamID, err := streamer.StartStream(ctx, msg)
	if err != nil {
		logger.Warnf(ctx, "[IM] StartStream failed, falling back to non-streaming: %v", err)
		return s.fallbackNonStream(ctx, msg, session, customAgent, kbIDs, adapter)
	}

	// Prepare the QA pipeline
	qaCtx, qaCancel := context.WithTimeout(ctx, qaTimeout)
	defer qaCancel()

	eventBus := event.NewEventBus()

	var (
		bufMu          sync.Mutex
		buf            strings.Builder // buffered content awaiting flush
		answerBuilder  strings.Builder // full answer for DB persistence (includes <think>)
		qaErr          error
		done           = make(chan struct{})
		closeOnce      sync.Once
		thinkBlockOpen bool // whether we've opened a <think> block (agent pipeline)
		answerStarted  bool // whether the final answer stream has begun

		// seenToolCalls deduplicates EventAgentToolCall events.
		// The engine emits tool calls twice: once during streaming (pending)
		// and once at execution time. We only show the first occurrence.
		seenToolCalls = make(map[string]bool)

		// lastCharNewline tracks whether the most recently written character
		// (across flush boundaries) was '\n'. This lets ensureNewlineBefore
		// work correctly even after buf has been Reset by a flush.
		lastCharNewline = true
		streamedAny     bool // whether any user-visible content was written to buf
	)
	closeDone := func() { closeOnce.Do(func() { close(done) }) }

	// bufWrite appends s to buf and updates lastCharNewline. Must hold bufMu.
	bufWrite := func(s string) {
		if s == "" {
			return
		}
		buf.WriteString(s)
		lastCharNewline = s[len(s)-1] == '\n'
	}

	// ensureNewlineBefore guarantees a '\n' exists before the next write,
	// even if the previous content was already flushed. Must hold bufMu.
	ensureNewlineBefore := func() {
		if !lastCharNewline {
			buf.WriteByte('\n')
			lastCharNewline = true
		}
	}

	// ensureThinkOpen opens a <think> block if not already open.
	// Used for agent pipeline to wrap thinking + tool calls. Must hold bufMu.
	ensureThinkOpen := func() {
		if !thinkBlockOpen {
			thinkBlockOpen = true
			bufWrite("<think>\n")
		}
	}

	// Subscribe to answer chunks.
	// Non-agent pipeline: content may contain <think>...</think> from the model — pass through as-is.
	// Agent pipeline: we've already opened a <think> block via EventAgentThought/ToolCall,
	// so we close it before streaming the answer.
	eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}

		bufMu.Lock()
		answerBuilder.WriteString(data.Content)

		if thinkBlockOpen && !answerStarted {
			answerStarted = true
			bufWrite("\n</think>\n\n")
		}

		bufWrite(data.Content)
		streamedAny = true
		bufMu.Unlock()

		if data.Done {
			closeDone()
		}
		return nil
	})

	eventBus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			return nil
		}
		logger.Errorf(ctx, "[IM] QA stream error: %s", data.Error)
		bufMu.Lock()
		qaErr = fmt.Errorf("QA pipeline error: %s", data.Error)
		bufMu.Unlock()
		closeDone()
		return nil
	})

	// Subscribe to agent thought events — stream thinking content into <think> block
	eventBus.On(event.EventAgentThought, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentThoughtData)
		if !ok {
			return nil
		}
		bufMu.Lock()
		ensureThinkOpen()
		bufWrite(data.Content)
		bufMu.Unlock()
		return nil
	})

	// Subscribe to agent tool call events — write status line into <think> block.
	// The engine may emit this event twice per tool call (once during streaming,
	// once at execution), so we deduplicate by ToolCallID.
	eventBus.On(event.EventAgentToolCall, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentToolCallData)
		if !ok {
			return nil
		}
		if !isToolVisibleToUser(data.ToolName) {
			return nil
		}
		bufMu.Lock()
		if seenToolCalls[data.ToolCallID] {
			bufMu.Unlock()
			return nil
		}
		seenToolCalls[data.ToolCallID] = true
		ensureThinkOpen()
		ensureNewlineBefore()
		bufWrite(formatToolCallStart(data.ToolName))
		bufMu.Unlock()
		logger.Debugf(ctx, "[IM] Tool call streamed to IM: tool=%s id=%s", data.ToolName, data.ToolCallID)
		return nil
	})

	// Subscribe to agent tool result events — write result line into <think> block
	eventBus.On(event.EventAgentToolResult, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentToolResultData)
		if !ok {
			return nil
		}
		if !isToolVisibleToUser(data.ToolName) {
			return nil
		}
		bufMu.Lock()
		ensureNewlineBefore()
		bufWrite(formatToolCallResult(data.ToolName, data.Success, data.Output))
		bufMu.Unlock()
		logger.Debugf(ctx, "[IM] Tool result streamed to IM: tool=%s success=%v duration=%dms",
			data.ToolName, data.Success, data.Duration)
		return nil
	})

	// Determine whether to use agent mode
	useAgent := customAgent != nil && customAgent.IsAgentMode()
	requestID := uuid.New().String()

	// Create user message
	userMsg, err := s.messageService.CreateMessage(qaCtx, &types.Message{
		SessionID: session.ID, Role: "user", Content: msg.Content,
		RequestID: requestID, CreatedAt: time.Now(), IsCompleted: true,
	})
	if err != nil {
		return fmt.Errorf("create user message: %w", err)
	}

	// Create placeholder assistant message
	assistantMsg, err := s.messageService.CreateMessage(qaCtx, &types.Message{
		SessionID: session.ID, Role: "assistant",
		RequestID: requestID, CreatedAt: time.Now(), IsCompleted: false,
	})
	if err != nil {
		return fmt.Errorf("create assistant message: %w", err)
	}

	// Run QA async
	go func() {
		var err error
		req := buildIMQARequest(session, msg.Content, assistantMsg.ID, userMsg.ID, customAgent, kbIDs)
		if useAgent {
			err = s.sessionService.AgentQA(qaCtx, req, eventBus)
		} else {
			err = s.sessionService.KnowledgeQA(qaCtx, req, eventBus)
		}
		if err != nil {
			logger.Errorf(ctx, "[IM] QA stream execution error: %v", err)
			bufMu.Lock()
			qaErr = fmt.Errorf("QA execution error: %w", err)
			bufMu.Unlock()
			closeDone()
		}
	}()

	// Flush loop: periodically send buffered content to the IM platform
	ticker := time.NewTicker(streamFlushInterval)
	defer ticker.Stop()

	flush := func() {
		bufMu.Lock()
		chunk := buf.String()
		buf.Reset()
		bufMu.Unlock()

		if chunk != "" {
			if err := streamer.SendStreamChunk(ctx, msg, streamID, chunk); err != nil {
				logger.Warnf(ctx, "[IM] SendStreamChunk failed: %v", err)
			}
		}
	}

loop:
	for {
		select {
		case <-ticker.C:
			flush()
		case <-done:
			break loop
		case <-qaCtx.Done():
			break loop
		}
	}

	// Final flush of any remaining content
	flush()

	// If no user-visible content was streamed (e.g., the entire response was
	// in <think> blocks, or the QA pipeline errored), send a fallback message
	// as the last chunk so the Feishu card doesn't end up empty.
	bufMu.Lock()
	answer := answerBuilder.String()
	finalErr := qaErr
	noVisibleContent := !streamedAny
	bufMu.Unlock()

	if noVisibleContent {
		fallback := "抱歉，我暂时无法回答这个问题。"
		if finalErr != nil {
			fallback = "抱歉，处理您的问题时出现了异常，请稍后再试。"
		}
		if err := streamer.SendStreamChunk(ctx, msg, streamID, fallback); err != nil {
			logger.Warnf(ctx, "[IM] SendStreamChunk fallback failed: %v", err)
		}
		if answer == "" {
			answer = fallback
		}
	}

	// End the stream
	if err := streamer.EndStream(ctx, msg, streamID); err != nil {
		logger.Warnf(ctx, "[IM] EndStream failed: %v", err)
	}

	if answer == "" {
		answer = "抱歉，我暂时无法回答这个问题。"
	}

	assistantMsg.Content = answer
	assistantMsg.IsCompleted = true
	if err := s.messageService.UpdateMessage(ctx, assistantMsg); err != nil {
		logger.Warnf(ctx, "[IM] Failed to update assistant message: %v", err)
	}

	logger.Infof(ctx, "[IM] Stream reply sent: platform=%s user=%s answer_len=%d", msg.Platform, msg.UserID, len(answer))
	return nil
}

// fallbackNonStream is used when streaming initialization fails.
func (s *Service) fallbackNonStream(ctx context.Context, msg *IncomingMessage, session *types.Session, customAgent *types.CustomAgent, kbIDs []string, adapter Adapter) error {
	answer, err := s.runQA(ctx, session, msg.Content, customAgent, kbIDs)
	if err != nil {
		logger.Errorf(ctx, "[IM] QA fallback failed: %v", err)
		answer = "抱歉，处理您的问题时出现了异常，请稍后再试。"
	}

	return adapter.SendReply(ctx, msg, &ReplyMessage{Content: answer, IsFinal: true})
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
		req := buildIMQARequest(session, query, assistantMsg.ID, userMsg.ID, customAgent, kbIDs)
		if useAgent {
			err = s.sessionService.AgentQA(ctx, req, eventBus)
		} else {
			err = s.sessionService.KnowledgeQA(ctx, req, eventBus)
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

// ── CRUD operations for IM channels ──

// ListChannelsByAgent returns all channels for a given agent within a tenant.
func (s *Service) ListChannelsByAgent(agentID string, tenantID uint64) ([]IMChannel, error) {
	var channels []IMChannel
	if err := s.db.Where("agent_id = ? AND tenant_id = ? AND deleted_at IS NULL", agentID, tenantID).
		Order("created_at DESC").Find(&channels).Error; err != nil {
		return nil, err
	}
	return channels, nil
}

// CreateChannel creates a new IM channel and optionally starts it.
// Returns a duplicate_bot error if the bot identity is already used by another channel.
func (s *Service) CreateChannel(channel *IMChannel) error {
	if err := s.checkDuplicateBot(channel, ""); err != nil {
		return err
	}
	if err := s.db.Create(channel).Error; err != nil {
		return err
	}
	if channel.Enabled {
		if err := s.StartChannel(channel); err != nil {
			logger.Warnf(context.Background(), "[IM] Created channel %s but failed to start: %v", channel.ID, err)
		}
	}
	return nil
}

// UpdateChannel updates a channel and restarts it if needed.
// Returns a duplicate_bot error if the bot identity is already used by another channel.
func (s *Service) UpdateChannel(channel *IMChannel) error {
	if err := s.checkDuplicateBot(channel, channel.ID); err != nil {
		return err
	}
	if err := s.db.Save(channel).Error; err != nil {
		return err
	}
	// Restart channel: stop old, start new if enabled
	s.StopChannel(channel.ID)
	if channel.Enabled {
		if err := s.StartChannel(channel); err != nil {
			logger.Warnf(context.Background(), "[IM] Updated channel %s but failed to restart: %v", channel.ID, err)
		}
	}
	return nil
}

// DeleteChannel soft-deletes a channel and stops it. Only deletes if the channel belongs to the given tenant.
func (s *Service) DeleteChannel(channelID string, tenantID uint64) error {
	s.StopChannel(channelID)
	result := s.db.Where("id = ? AND tenant_id = ?", channelID, tenantID).Delete(&IMChannel{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("channel not found")
	}
	return nil
}

// ToggleChannel enables or disables a channel. Only toggles if the channel belongs to the given tenant.
func (s *Service) ToggleChannel(channelID string, tenantID uint64) (*IMChannel, error) {
	var ch IMChannel
	if err := s.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", channelID, tenantID).First(&ch).Error; err != nil {
		return nil, err
	}
	ch.Enabled = !ch.Enabled
	if err := s.db.Save(&ch).Error; err != nil {
		return nil, err
	}
	if ch.Enabled {
		if err := s.StartChannel(&ch); err != nil {
			logger.Warnf(context.Background(), "[IM] Failed to start channel %s after enable: %v", ch.ID, err)
		}
	} else {
		s.StopChannel(channelID)
	}
	return &ch, nil
}

// checkDuplicateBot queries the bot_identity index to see if another active channel
// already uses the same bot. This is an O(1) index lookup, not a full table scan.
// The DB unique index on bot_identity serves as an additional safety net.
// excludeID is the channel's own ID (for updates); pass "" for new channels.
func (s *Service) checkDuplicateBot(channel *IMChannel, excludeID string) error {
	// Compute bot_identity the same way the BeforeSave hook will
	botKey := channel.computeBotIdentity()
	if botKey == "" {
		return nil
	}

	var existing IMChannel
	query := s.db.Where("bot_identity = ? AND deleted_at IS NULL", botKey)
	if excludeID != "" {
		query = query.Where("id != ?", excludeID)
	}
	if err := query.First(&existing).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil // no conflict
		}
		return fmt.Errorf("check duplicate bot: %w", err)
	}
	return fmt.Errorf("duplicate_bot: this bot is already bound to channel %q (%s); each bot can only be connected to one channel", existing.Name, existing.ID)
}

// ── File message handling ──────────────────────────────────────────────
// These methods handle file messages received via IM platforms.
// Files are downloaded from the IM platform, validated, and saved to the
// configured knowledge base asynchronously. The user receives a notification
// at the start and end of processing.
// ────────────────────────────────────────────────────────────────────────

// supportedKBFileExts is the set of file extensions that can be saved to a knowledge base.
var supportedKBFileExts = map[string]bool{
	"pdf": true, "txt": true, "docx": true, "doc": true,
	"md": true, "markdown": true,
	"png": true, "jpg": true, "jpeg": true, "gif": true,
	"csv": true, "xlsx": true, "xls": true,
	"pptx": true, "ppt": true,
}

// handleFileMessage processes a file message by downloading it from the IM platform
// and saving it to the channel's configured knowledge base. Sends start/end
// notifications to the user via the adapter.
func (s *Service) handleFileMessage(ctx context.Context, msg *IncomingMessage, adapter Adapter, channel *IMChannel) error {
	// Check if the adapter supports file downloading
	downloader, ok := adapter.(FileDownloader)
	if !ok {
		logger.Infof(ctx, "[IM] Adapter for platform %s does not support file download, ignoring file message", msg.Platform)
		return s.sendSmartReply(ctx, adapter, msg, channel,
			"用户尝试发送文件，但当前平台暂不支持文件消息处理。",
			"❌ 当前平台暂不支持文件消息处理。")
	}

	// For image messages, ensure a proper file extension is present.
	// IM platforms may only provide a hash/key as filename without extension.
	if msg.MessageType == MessageTypeImage && fileExtension(msg.FileName) == "" {
		msg.FileName = msg.FileName + ".png"
	}

	// Validate file extension (pre-download).
	// Some platforms (e.g. WeCom aibot) do not provide original filenames in the
	// callback JSON — only a hash ID. For such cases we defer extension validation
	// to after the file is downloaded, where the real name may be obtained from
	// HTTP Content-Disposition or Content-Type headers.
	ext := fileExtension(msg.FileName)
	if ext != "" && !supportedKBFileExts[ext] {
		logger.Infof(ctx, "[IM] Unsupported file type: %s (file=%s)", ext, msg.FileName)
		return s.sendSmartReply(ctx, adapter, msg, channel,
			fmt.Sprintf("用户上传了一个不支持的文件类型「%s」。目前支持的类型包括：PDF、Word、TXT、Markdown、Excel、CSV、PPT、图片。", ext),
			fmt.Sprintf("❌ 不支持的文件类型「%s」。\n\n支持的类型：PDF、Word、TXT、Markdown、Excel、CSV、PPT、图片。", ext))
	}

	displayName := msg.FileName
	if ext == "" {
		displayName = "文件"
	}

	// Send "processing started" notification (streaming)
	if err := s.sendSmartReply(ctx, adapter, msg, channel,
		fmt.Sprintf("用户发送了一个文件「%s」，系统正在处理并保存到知识库中，需要告知用户请稍候。", displayName),
		fmt.Sprintf("📥 已收到%s，正在处理并保存到知识库，请稍候...", displayName)); err != nil {
		logger.Warnf(ctx, "[IM] Failed to send file processing start notification: %v", err)
	}

	// Process asynchronously to avoid blocking the message handler
	go s.processFileToKnowledgeBase(context.WithoutCancel(ctx), msg, downloader, adapter, channel)

	return nil
}

// processFileToKnowledgeBase is the async worker that downloads a file from the
// IM platform and creates a knowledge entry in the configured knowledge base.
func (s *Service) processFileToKnowledgeBase(ctx context.Context, msg *IncomingMessage, downloader FileDownloader, adapter Adapter, channel *IMChannel) {
	kbID := channel.KnowledgeBaseID
	tenantID := channel.TenantID

	// Build context with tenant info for the knowledge service
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil {
		logger.Errorf(ctx, "[IM] Failed to get tenant %d for file processing: %v", tenantID, err)
		s.sendFileResult(ctx, adapter, msg, msg.FileName, false, "获取租户信息失败", channel)
		return
	}
	kbCtx := context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	kbCtx = context.WithValue(kbCtx, types.TenantInfoContextKey, tenant)

	// Download file from IM platform
	reader, fileName, err := downloader.DownloadFile(ctx, msg)
	if err != nil {
		logger.Errorf(ctx, "[IM] Failed to download file from %s: %v", msg.Platform, err)
		s.sendFileResult(ctx, adapter, msg, msg.FileName, false, "下载文件失败", channel)
		return
	}
	defer reader.Close()

	logger.Debugf(ctx, "[IM] Downloaded file: original_name=%s resolved_name=%s", msg.FileName, fileName)

	// Post-download extension validation: if the pre-download name had no extension
	// (e.g. WeCom file messages only provide a hash), check the resolved name now.
	ext := fileExtension(fileName)
	if !supportedKBFileExts[ext] {
		logger.Infof(ctx, "[IM] Unsupported file type after download: %s (file=%s)", ext, fileName)
		s.sendFileResult(ctx, adapter, msg, fileName, false,
			fmt.Sprintf("不支持的文件类型「%s」。支持：PDF、Word、TXT、Markdown、Excel、CSV、PPT、图片", ext), channel)
		return
	}

	// Read file content into memory for multipart upload
	content, err := io.ReadAll(reader)
	if err != nil {
		logger.Errorf(ctx, "[IM] Failed to read file content: %v", err)
		s.sendFileResult(ctx, adapter, msg, fileName, false, "读取文件内容失败", channel)
		return
	}

	// Create a multipart.FileHeader compatible wrapper
	fh := newInMemoryFileHeader(fileName, content)

	// Create knowledge entry via the knowledge service
	knowledge, err := s.knowledgeService.CreateKnowledgeFromFile(kbCtx, kbID, fh, nil, nil, "", "")
	if err != nil {
		errMsg := err.Error()
		// Check for duplicate file
		if strings.Contains(errMsg, "duplicate") || strings.Contains(errMsg, "already exists") {
			logger.Infof(ctx, "[IM] File already exists in knowledge base: %s", fileName)
			s.sendFileResult(ctx, adapter, msg, fileName, false, "文件已存在于知识库中", channel)
			return
		}
		logger.Errorf(ctx, "[IM] Failed to create knowledge from file: %v", err)
		s.sendFileResult(ctx, adapter, msg, fileName, false, "保存到知识库失败", channel)
		return
	}

	logger.Infof(ctx, "[IM] File saved to knowledge base: kb=%s knowledge=%s file=%s", kbID, knowledge.ID, fileName)
	s.sendFileResult(ctx, adapter, msg, fileName, true, "", channel)

	// Start a background watcher to send the document summary once Asynq
	// finishes parsing + summary generation. This is intentionally decoupled
	// from the Asynq task pipeline to avoid modifying any existing logic.
	go s.watchAndSendSummary(ctx, kbCtx, adapter, msg, knowledge.ID, fileName, channel)
}

// sendFileResult sends a notification about the file processing result.
// It uses sendSmartReply to generate a friendly, streaming reply via the channel's LLM.
// Falls back to a static template if the LLM is unavailable.
func (s *Service) sendFileResult(ctx context.Context, adapter Adapter, msg *IncomingMessage, fileName string, success bool, errDetail string, channel *IMChannel) {
	var fallback string
	if success {
		fallback = fmt.Sprintf("✅ 文件「%s」已保存到知识库，正在解析中，完成后会通知你～", fileName)
	} else {
		fallback = fmt.Sprintf("❌ 文件「%s」处理失败：%s", fileName, errDetail)
	}

	var situation string
	if success {
		situation = fmt.Sprintf("用户上传的文件「%s」已成功保存到知识库，但还需要后台解析文档内容（这需要一些时间）。请告知用户文件已收到，正在解析处理中，解析完成后会自动推送结果。", fileName)
	} else {
		situation = fmt.Sprintf("用户上传的文件「%s」处理失败，原因：%s。", fileName, errDetail)
	}

	if err := s.sendSmartReply(ctx, adapter, msg, channel, situation, fallback); err != nil {
		logger.Warnf(ctx, "[IM] Failed to send file result notification: %v", err)
	}
}

// smartReplySystemPrompt is the system prompt used for generating smart notification replies.
const smartReplySystemPrompt = "你是一个专业的 IM 机器人助手。请根据以下事件情况，生成一条简洁、清晰的通知消息。" +
	"要求：1) 可适当使用 emoji 但不要过多；2) 语气专业平等，像同事之间对话，不要谄媚讨好，不要用「啦」「哦」「呢」「哟」等撒娇语气词；" +
	"3) 直接输出消息内容，不要加任何额外解释；" +
	"4) 如果事件中包含摘要或详细内容，请用 Markdown 格式结构化展示（使用标题、列表、加粗等），完整呈现，不要删减或概括；如果是简单通知，则控制在 2-3 句话以内。"

// sendSmartReply generates a notification message using the channel's LLM and sends it
// to the user. If the adapter supports streaming (StreamSender), it streams the reply
// in real-time for a better user experience. Otherwise, it falls back to non-streaming.
// If the LLM is unavailable or fails, it sends the provided fallback text.
func (s *Service) sendSmartReply(ctx context.Context, adapter Adapter, msg *IncomingMessage, channel *IMChannel, situation string, fallback string) error {
	chatModel := s.getChatModelForChannel(ctx, channel)
	if chatModel == nil {
		return adapter.SendReply(ctx, msg, &ReplyMessage{Content: fallback, IsFinal: true})
	}

	// If the adapter supports streaming, use stream mode
	if streamer, ok := adapter.(StreamSender); ok {
		if err := s.streamSmartReply(ctx, chatModel, streamer, msg, situation); err == nil {
			return nil
		}
		// Stream failed — fall through to non-streaming
		logger.Warnf(ctx, "[IM] Stream smart reply failed, falling back to non-streaming")
	}

	// Non-streaming fallback
	content := s.generateSmartReply(ctx, chatModel, situation, fallback)
	return adapter.SendReply(ctx, msg, &ReplyMessage{Content: content, IsFinal: true})
}

// streamSmartReply uses ChatStream to generate and stream a notification reply in real-time.
func (s *Service) streamSmartReply(ctx context.Context, chatModel chat.Chat, streamer StreamSender, msg *IncomingMessage, situation string) error {
	messages := []chat.Message{
		{Role: "system", Content: smartReplySystemPrompt},
		{Role: "user", Content: situation},
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	streamCh, err := chatModel.ChatStream(timeoutCtx, messages, &chat.ChatOptions{
		Temperature: 0.7,
		MaxTokens:   800,
	})
	if err != nil {
		logger.Warnf(ctx, "[IM] ChatStream failed for smart reply: %v", err)
		return err
	}

	// Start the stream on the IM platform
	streamID, err := streamer.StartStream(ctx, msg)
	if err != nil {
		logger.Warnf(ctx, "[IM] StartStream failed for smart reply: %v", err)
		return err
	}

	// Flush loop with batching (same pattern as handleMessageStream)
	var (
		bufMu sync.Mutex
		buf   strings.Builder
		done  = make(chan struct{})
	)

	go func() {
		defer close(done)
		for resp := range streamCh {
			if resp.Content != "" {
				bufMu.Lock()
				buf.WriteString(resp.Content)
				bufMu.Unlock()
			}
		}
	}()

	ticker := time.NewTicker(streamFlushInterval)
	defer ticker.Stop()

	flush := func() {
		bufMu.Lock()
		chunk := buf.String()
		buf.Reset()
		bufMu.Unlock()

		if chunk != "" {
			if err := streamer.SendStreamChunk(ctx, msg, streamID, chunk); err != nil {
				logger.Warnf(ctx, "[IM] SendStreamChunk failed for smart reply: %v", err)
			}
		}
	}

loop:
	for {
		select {
		case <-ticker.C:
			flush()
		case <-done:
			break loop
		case <-timeoutCtx.Done():
			break loop
		}
	}

	// Final flush
	flush()

	// End the stream
	if err := streamer.EndStream(ctx, msg, streamID); err != nil {
		logger.Warnf(ctx, "[IM] EndStream failed for smart reply: %v", err)
	}

	return nil
}

// generateSmartReply uses the channel's agent LLM to produce a natural-language
// notification message for the given situation (non-streaming).
// If the call fails, it returns the provided fallback text.
func (s *Service) generateSmartReply(ctx context.Context, chatModel chat.Chat, situation string, fallback string) string {
	messages := []chat.Message{
		{Role: "system", Content: smartReplySystemPrompt},
		{Role: "user", Content: situation},
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := chatModel.Chat(timeoutCtx, messages, &chat.ChatOptions{
		Temperature: 0.7,
		MaxTokens:   800,
	})
	if err != nil {
		logger.Warnf(ctx, "[IM] Smart reply generation failed, using fallback: %v", err)
		return fallback
	}

	reply := strings.TrimSpace(resp.Content)
	if reply == "" {
		return fallback
	}
	return reply
}

// getChatModelForChannel resolves the chat.Chat instance configured on the
// channel's agent. Returns nil if the model cannot be resolved.
func (s *Service) getChatModelForChannel(ctx context.Context, channel *IMChannel) chat.Chat {
	if channel == nil || channel.AgentID == "" {
		return nil
	}

	// Ensure the context carries tenant ID — some call sites (e.g. handleFileMessage)
	// may invoke this before the tenant has been injected into ctx.
	if _, ok := types.TenantIDFromContext(ctx); !ok && channel.TenantID != 0 {
		ctx = context.WithValue(ctx, types.TenantIDContextKey, channel.TenantID)
	}

	agent, err := s.agentService.GetAgentByID(ctx, channel.AgentID)
	if err != nil || agent == nil {
		logger.Debugf(ctx, "[IM] Cannot get agent %s for smart reply: %v", channel.AgentID, err)
		return nil
	}

	modelID := agent.Config.ModelID
	if modelID == "" {
		return nil
	}

	chatModel, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil {
		logger.Debugf(ctx, "[IM] Cannot get chat model %s for smart reply: %v", modelID, err)
		return nil
	}
	return chatModel
}

// watchAndSendSummary polls the knowledge record until document parsing (and
// optionally summary generation) completes, then sends the result back to the
// IM user. This runs as a fire-and-forget goroutine, completely decoupled from
// the Asynq worker pipeline.
func (s *Service) watchAndSendSummary(
	ctx context.Context,
	kbCtx context.Context,
	adapter Adapter,
	msg *IncomingMessage,
	knowledgeID string,
	fileName string,
	channel *IMChannel,
) {
	const (
		pollInterval = 5 * time.Second
		maxWait      = 10 * time.Minute // give up after 10 minutes
	)

	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Now().After(deadline) {
				logger.Infof(ctx, "[IM] Summary watcher timed out for knowledge %s", knowledgeID)
				return
			}

			knowledge, err := s.knowledgeService.GetKnowledgeByID(kbCtx, knowledgeID)
			if err != nil {
				logger.Warnf(ctx, "[IM] Summary watcher: failed to get knowledge %s: %v", knowledgeID, err)
				return
			}

			switch knowledge.ParseStatus {
			case types.ParseStatusFailed:
				// Parsing failed — notify user and stop watching
				errMsg := knowledge.ErrorMessage
				if errMsg == "" {
					errMsg = "文档解析失败"
				}
				_ = s.sendSmartReply(ctx, adapter, msg, channel,
					fmt.Sprintf("用户之前上传的文件「%s」解析失败了，错误原因：%s。请安慰用户并建议重试。", fileName, errMsg),
					fmt.Sprintf("⚠️ 文件「%s」解析失败：%s", fileName, errMsg))
				return

			case types.ParseStatusCompleted:
				// Parsing done. If summary generation is in progress, wait for it.
				switch knowledge.SummaryStatus {
				case types.SummaryStatusNone, "":
					// No summary task configured. For image files the VLM caption
					// is stored in Description by finalizeImageKnowledge, so we
					// still show it if present.
					if knowledge.Description != "" && knowledge.Description != fileName {
						_ = s.sendSmartReply(ctx, adapter, msg, channel,
							fmt.Sprintf("用户之前上传的文件「%s」已解析完成。以下是文件的完整摘要内容：\n%s\n\n请生成一条通知消息，包含：1) 告知文件已解析完成；2) 用 Markdown 格式（标题、列表、加粗等）结构化展示上述摘要内容，不要删减或概括；3) 提示用户可以针对该文件提问。", fileName, knowledge.Description),
							fmt.Sprintf("📄 文件「%s」已解析完成。\n\n**摘要：**\n\n%s\n\n---\n可以针对该文件进行提问。", fileName, knowledge.Description))
					} else {
						_ = s.sendSmartReply(ctx, adapter, msg, channel,
							fmt.Sprintf("用户之前上传的文件「%s」已解析完成，现在可以开始针对该文件进行提问了。", fileName),
							fmt.Sprintf("📄 文件「%s」已解析完成，可以开始提问了！", fileName))
					}
					return

				case types.SummaryStatusCompleted:
					// Summary is ready — send it
					s.sendSummaryNotification(ctx, adapter, msg, knowledge, fileName, channel)
					return

				case types.SummaryStatusFailed:
					_ = s.sendSmartReply(ctx, adapter, msg, channel,
						fmt.Sprintf("用户之前上传的文件「%s」已解析完成，但摘要生成失败了。不过文件已可用于提问。", fileName),
						fmt.Sprintf("📄 文件「%s」已解析完成，可以开始提问了！（摘要生成失败）", fileName))
					return

				default:
					// Still generating summary — keep polling
				}

			default:
				// Still parsing — keep polling
			}
		}
	}
}

// sendSummaryNotification retrieves the summary chunk for a knowledge entry
// and sends it as a message to the IM user.
func (s *Service) sendSummaryNotification(
	ctx context.Context,
	adapter Adapter,
	msg *IncomingMessage,
	knowledge *types.Knowledge,
	fileName string,
	channel *IMChannel,
) {
	// The summary is stored in the knowledge's Description field or as a
	// ChunkTypeSummary chunk. We use Description first (populated by the
	// summary generation task), falling back to a generic notice.
	summary := knowledge.Description
	if summary == "" {
		summary = knowledge.Title
	}

	var situation, fallback string
	if summary != "" && summary != fileName {
		situation = fmt.Sprintf("用户之前上传的文件「%s」已解析完成。以下是文件的完整摘要内容：\n%s\n\n请生成一条通知消息，包含：1) 告知文件已解析完成；2) 用 Markdown 格式（标题、列表、加粗等）结构化展示上述摘要内容，不要删减或概括；3) 提示用户可以针对该文件提问。", fileName, summary)
		fallback = fmt.Sprintf("📄 文件「%s」已解析完成。\n\n**摘要：**\n\n%s\n\n---\n可以针对该文件进行提问。", fileName, summary)
	} else {
		situation = fmt.Sprintf("用户之前上传的文件「%s」已解析完成，现在可以开始针对该文件进行提问了。", fileName)
		fallback = fmt.Sprintf("📄 文件「%s」已解析完成，可以开始提问了！", fileName)
	}

	if err := s.sendSmartReply(ctx, adapter, msg, channel, situation, fallback); err != nil {
		logger.Warnf(ctx, "[IM] Failed to send summary notification: %v", err)
	}
}

// fileExtension extracts the lowercase file extension from a filename.
func fileExtension(filename string) string {
	parts := strings.Split(filename, ".")
	if len(parts) < 2 {
		return ""
	}
	return strings.ToLower(parts[len(parts)-1])
}

// newInMemoryFileHeader wraps in-memory file content as a *multipart.FileHeader
// so it can be passed to CreateKnowledgeFromFile which expects a multipart upload.
func newInMemoryFileHeader(filename string, data []byte) *multipart.FileHeader {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	h.Set("Content-Type", "application/octet-stream")

	part, err := writer.CreatePart(h)
	if err != nil {
		// Fallback: return a minimal FileHeader
		return &multipart.FileHeader{Filename: filename, Size: int64(len(data))}
	}
	_, _ = part.Write(data)
	_ = writer.Close()

	// Parse the multipart body to extract the FileHeader
	reader := multipart.NewReader(body, writer.Boundary())
	form, err := reader.ReadForm(int64(len(data)) + 1024)
	if err != nil || form == nil {
		return &multipart.FileHeader{Filename: filename, Size: int64(len(data))}
	}
	files := form.File["file"]
	if len(files) == 0 {
		return &multipart.FileHeader{Filename: filename, Size: int64(len(data))}
	}
	return files[0]
}
