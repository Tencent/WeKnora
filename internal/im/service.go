package im

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

const (
	// dedupTTL is how long processed message IDs are retained.
	dedupTTL = 5 * time.Minute
	// dedupCleanupInterval is how often the dedup map is cleaned.
	dedupCleanupInterval = 1 * time.Minute
	// maxContentLength is the maximum allowed message content length.
	maxContentLength = 4096
	// maxQuoteContentLength is the max runes to include from a quoted message.
	maxQuoteContentLength = 500
	// streamFlushInterval is how often buffered stream content is flushed to the IM platform.
	// This prevents API rate-limiting while keeping perceived latency low.
	streamFlushInterval = 300 * time.Millisecond
)

// imCitationTagRe matches inline citation tags produced by the agent pipeline.
// These tags are rendered as interactive UI in the web frontend but are meaningless
// in IM platforms, so they must be stripped before sending.
var imCitationTagRe = regexp.MustCompile(`<(?:kb|web)\b[^>]*/?>`)

// stripIMCitationTags removes <kb .../> and <web .../> inline citation tags from s.
func stripIMCitationTags(s string) string {
	return imCitationTagRe.ReplaceAllString(s, "")
}

// imageXMLBlockRe matches <image ...>...</image> blocks produced by
// EnrichContentWithImageInfo in the RAG context pipeline. These blocks contain
// metadata for the LLM and must be stripped before sending to IM platforms.
var imageXMLBlockRe = regexp.MustCompile(`(?s)<image\b[^>]*>.*?</image>`)

// imageOriginalRe extracts the original markdown image syntax from <image_original> tags.
var imageOriginalRe = regexp.MustCompile(`<image_original>(.*?)</image_original>`)

// stripImageXMLTags collapses <image> blocks back to plain markdown.
// Extracts the original ![alt](url) from <image_original> when present,
// otherwise drops the block entirely.
func stripImageXMLTags(s string) string {
	return imageXMLBlockRe.ReplaceAllStringFunc(s, func(block string) string {
		if m := imageOriginalRe.FindStringSubmatch(block); len(m) > 1 {
			return m[1]
		}
		return ""
	})
}

// storageSchemeRe matches provider:// URLs used by file storage backends.
var storageSchemeRe = regexp.MustCompile(`\b(local|minio|s3|cos|tos|oss)://[^\s)\]>"]+`)

// rewriteStorageURLs replaces all provider:// URLs in content with HTTP URLs
// obtained from fileService.GetFileURL. URLs that are already HTTP or cannot
// be resolved are left unchanged.
func rewriteStorageURLs(ctx context.Context, content string, fileSvc interfaces.FileService) string {
	if fileSvc == nil {
		return content
	}
	return storageSchemeRe.ReplaceAllStringFunc(content, func(match string) string {
		httpURL, err := fileSvc.GetFileURL(ctx, match)
		if err != nil || httpURL == match {
			return match
		}
		return httpURL
	})
}

// ── Streaming holdback helpers ──
// During streaming, content is flushed in 300ms batches. A provider:// URL or
// an XML tag may be split across two batches. These helpers detect incomplete
// patterns at the end of a chunk so the caller can hold them back until the
// next flush completes them.

// incompleteURLSuffixRe matches a provider:// URL that reaches the end of the
// string — it may continue in the next chunk.
var incompleteURLSuffixRe = regexp.MustCompile(
	`\b(?:local|minio|s3|cos|tos|oss)://[^\s)\]>"]*$`,
)

// findIncompleteStorageURL returns the byte offset of a potentially truncated
// provider:// URL at the tail of s, or -1 if none.
func findIncompleteStorageURL(s string) int {
	loc := incompleteURLSuffixRe.FindStringIndex(s)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// incompleteXMLTagRe matches the opening of an <image…>, <kb…>, or <web…> tag
// that reaches the end of the string without a closing '>'.
var incompleteXMLTagRe = regexp.MustCompile(
	`<(?:image|image_original|image_caption|image_ocr|kb|web)[^>]*$`,
)

// findIncompleteXMLTag returns the byte offset of a potentially truncated XML
// tag at the tail of s, or -1 if none.
func findIncompleteXMLTag(s string) int {
	loc := incompleteXMLTagRe.FindStringIndex(s)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// holdbackCutoff returns the earliest incomplete-pattern offset at the tail of
// chunk, or len(chunk) if the chunk is safe to flush entirely.
func holdbackCutoff(chunk string) int {
	cutoff := len(chunk)
	if idx := findIncompleteStorageURL(chunk); idx >= 0 && idx < cutoff {
		cutoff = idx
	}
	if idx := findIncompleteXMLTag(chunk); idx >= 0 && idx < cutoff {
		cutoff = idx
	}
	return cutoff
}

// cleanIMContent applies all IM-specific content transformations:
//  1. Collapse <image> XML blocks back to plain markdown
//  2. Strip <kb/> and <web/> citation tags
//  3. Rewrite provider:// URLs to HTTP URLs (if fileSvc is available)
func cleanIMContent(ctx context.Context, content string, fileSvc interfaces.FileService) string {
	content = stripImageXMLTags(content)
	content = stripIMCitationTags(content)
	content = rewriteStorageURLs(ctx, content, fileSvc)
	return content
}

// buildTenantFileService creates a FileService for the given tenant's storage config.
// Returns nil if the tenant has no storage config or if creation fails.
func buildTenantFileService(tenant *types.Tenant) interfaces.FileService {
	if tenant == nil {
		return nil
	}
	baseDir := os.Getenv("LOCAL_STORAGE_BASE_DIR")
	if baseDir == "" {
		baseDir = "/data/files"
	}
	fileSvc, _, err := filesvc.NewFileServiceFromStorageConfig("", tenant.StorageEngineConfig, baseDir)
	if err != nil {
		return nil
	}
	return fileSvc
}

const (
	// wsLeaderTTL is the TTL for the Redis key used for WebSocket leader election.
	wsLeaderTTL = 15 * time.Second
	// wsLeaderRenewInterval is how often the leader renews its lock.
	wsLeaderRenewInterval = 5 * time.Second
	// wsLeaderRetryInterval is how often non-leader instances try to acquire the lock.
	wsLeaderRetryInterval = 10 * time.Second
	// stopMarkerTTL is the TTL for cross-instance /stop markers in Redis.
	stopMarkerTTL = 30 * time.Second
	// stopPollInterval is how often in-flight workers check for remote /stop signals.
	stopPollInterval = 500 * time.Millisecond
)

// ── Redis key prefixes ──────────────────────────────────────────────────────
// All IM-related Redis keys are defined here for discoverability and to avoid
// scattered string literals across multiple files.
const (
	RedisKeyLeader     = "im:ws:leader:"    // + channelID — WebSocket leader election
	RedisKeyDedup      = "im:dedup:"        // + messageID — message deduplication
	RedisKeyStop       = "im:stop:"         // + userKey   — cross-instance /stop marker (pre-execution)
	RedisKeyInflight   = "im:inflight:"     // + userKey   — maps userKey → sessionID:messageID for cross-instance /stop
	RedisKeyQueueUser  = "im:queue:user:"   // + userKey   — global per-user queue counter
	RedisKeyRateLimit  = "im:ratelimit:"    // + key       — sliding-window rate limiting
	RedisKeyGlobalGate = "im:global:active" // global concurrent worker counter
)

// channelState holds runtime state for a running IM channel.
type channelState struct {
	Channel      *IMChannel
	Adapter      Adapter
	Cancel       context.CancelFunc // for stopping websocket goroutines
	leaderCancel context.CancelFunc // stops the leader renewal goroutine (nil if not leader)
}

// AdapterFactory creates an Adapter from an IMChannel configuration.
// The second return value is an optional cleanup function (e.g., for stopping websocket connections).
type AdapterFactory func(ctx context.Context, channel *IMChannel, msgHandler func(ctx context.Context, msg *IncomingMessage) error) (Adapter, context.CancelFunc, error)

// inflightEntry tracks a running QA request, keyed by userKey in the inflight map.
type inflightEntry struct {
	cancel             context.CancelFunc
	sessionID          string // set after assistant message is created
	assistantMessageID string // set after assistant message is created
}

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

	// streamManager writes/reads QA events for distributed stop detection,
	// consistent with the web StopSession mechanism. May be nil in Lite mode
	// (but NewStreamManager always returns at least a memory implementation).
	streamManager interfaces.StreamManager

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
	// Uses Redis ZSET when available, falls back to local sliding window.
	rateLimiter *distributedLimiter

	// inflight tracks in-progress QA requests, keyed by userKey
	// ("channelID:userID:chatID"). Allows /stop to abort a running request
	// on this instance and look up (sessionID, messageID) for StreamManager.
	inflight sync.Map // userKey -> *inflightEntry

	// qaQueue manages bounded queuing and worker-pool execution of QA requests,
	// providing backpressure to protect downstream LLM resources.
	qaQueue *qaQueue

	// redis is the optional Redis client for distributed state (dedup, rate
	// limiting, leader election, cross-instance /stop). When nil the service
	// falls back to local in-memory state (single-instance / Lite mode).
	redis *redis.Client

	// instanceID uniquely identifies this service instance for leader election.
	instanceID string

	stopCh chan struct{}
}

// makeUserKey builds the canonical key used to identify a user's request
// across the queue, inflight map, and /stop command.
// threadID should only be non-empty when channel.SessionMode == "thread";
// callers must guard this to avoid leaking thread scope into user-mode keys.
func makeUserKey(channelID, userID, chatID, threadID string) string {
	if threadID != "" {
		return fmt.Sprintf("%s:%s:%s:%s", channelID, userID, chatID, threadID)
	}
	return fmt.Sprintf("%s:%s:%s", channelID, userID, chatID)
}

// nonTextTypeLabel maps a message type to a Chinese label for LLM instructions.
var nonTextTypeLabel = map[string]string{
	"image": "图片",
	"file":  "文件",
	"video": "视频",
	"voice": "语音",
}

// formatQuotedContext formats a QuotedMessage into a labeled string for LLM context.
// Returns empty string if quote is nil.
// For non-text quotes, generates an instruction telling the LLM to acknowledge
// the unprocessable content instead of a placeholder that causes hallucination.
func formatQuotedContext(quote *QuotedMessage) string {
	if quote == nil {
		return ""
	}
	// Non-text quote: generate instruction, not content placeholder.
	if quote.NonTextType != "" {
		label := nonTextTypeLabel[quote.NonTextType]
		if label == "" {
			label = "该类型的"
		}
		return "用户引用了一条" + label + "消息，但你无法查看该内容。请直接告知用户你目前无法处理" + label + "消息，建议用户用文字描述问题。不要猜测该消息的内容。"
	}
	if quote.Content == "" {
		return ""
	}
	content := quote.Content
	runes := []rune(content)
	if len(runes) > maxQuoteContentLength {
		content = string(runes[:maxQuoteContentLength]) + "..."
	}
	// Prevent quoted content from escaping the XML tag boundary.
	content = strings.ReplaceAll(content, "</quoted_message>", "")
	label := "以下是用户引用的一条历史消息，仅作为上下文参考："
	if quote.IsBotMessage {
		label = "以下是用户引用的你（机器人）之前的回复，仅作为上下文参考："
	}
	return label + "\n<quoted_message>\n" + content + "\n</quoted_message>"
}

func buildIMQARequest(
	session *types.Session,
	query string,
	assistantMessageID string,
	userMessageID string,
	customAgent *types.CustomAgent,
	kbIDs []string,
	quote *QuotedMessage,
) *types.QARequest {
	// WebSearchEnabled: the web handler passes this per-request from the
	// frontend toggle; for IM channels the user has no per-message toggle,
	// so we derive it from the agent config (the single source of truth).
	webSearchEnabled := customAgent != nil && customAgent.Config.WebSearchEnabled
	quotedContext := formatQuotedContext(quote)
	return &types.QARequest{
		Session:            session,
		Query:              query,
		AssistantMessageID: assistantMessageID,
		CustomAgent:        customAgent,
		KnowledgeBaseIDs:   kbIDs,
		UserMessageID:      userMessageID,
		WebSearchEnabled:   webSearchEnabled,
		QuotedContext:      quotedContext,
	}
}

// resolveIMConfig extracts IM tuning parameters from the application config,
// falling back to built-in defaults for any zero/nil values.
func resolveIMConfig(appCfg *config.Config) (workers, maxQueue, maxPerUser, globalMaxWorkers int, rlWindow time.Duration, rlMax int) {
	workers = defaultWorkers
	maxQueue = defaultMaxQueueSize
	maxPerUser = defaultMaxPerUser
	rlWindow = rateLimitWindow
	rlMax = rateLimitMaxRequests

	if appCfg == nil || appCfg.IM == nil {
		return
	}
	im := appCfg.IM
	if im.Workers > 0 {
		workers = im.Workers
	}
	if im.MaxQueueSize > 0 {
		maxQueue = im.MaxQueueSize
	}
	if im.MaxPerUser > 0 {
		maxPerUser = im.MaxPerUser
	}
	if im.GlobalMaxWorkers > 0 {
		globalMaxWorkers = im.GlobalMaxWorkers
	}
	if im.RateLimitWindow > 0 {
		rlWindow = im.RateLimitWindow
	}
	if im.RateLimitMax > 0 {
		rlMax = im.RateLimitMax
	}
	return
}

// NewService creates a new IM service.
// redisClient may be nil — in that case the service falls back to local
// in-memory state (Lite / single-instance mode).
// cfg may be nil — in that case built-in defaults are used.
func NewService(
	db *gorm.DB,
	sessionService interfaces.SessionService,
	messageService interfaces.MessageService,
	tenantService interfaces.TenantService,
	agentService interfaces.CustomAgentService,
	knowledgeService interfaces.KnowledgeService,
	kbService interfaces.KnowledgeBaseService,
	modelService interfaces.ModelService,
	streamManager interfaces.StreamManager,
	redisClient *redis.Client,
	appCfg *config.Config,
) *Service {
	// Resolve IM configuration with defaults.
	workers, maxQueue, maxPerUser, globalMaxWorkers, rlWindow, rlMax := resolveIMConfig(appCfg)

	// Build command registry.
	registry := NewCommandRegistry()
	registry.Register(newHelpCommand(registry))
	registry.Register(newInfoCommand(kbService))
	registry.Register(newSearchCommand(sessionService, kbService))
	registry.Register(newStopCommand())
	registry.Register(newClearCommand())

	instanceID := uuid.New().String()
	s := &Service{
		db:               db,
		sessionService:   sessionService,
		messageService:   messageService,
		tenantService:    tenantService,
		agentService:     agentService,
		knowledgeService: knowledgeService,
		kbService:        kbService,
		modelService:     modelService,
		streamManager:    streamManager,
		cmdRegistry:      registry,
		channels:         make(map[string]*channelState),
		adapterFactories: make(map[string]AdapterFactory),
		rateLimiter:      newDistributedLimiter(redisClient, rlWindow, rlMax, instanceID),
		redis:            redisClient,
		instanceID:       instanceID,
		stopCh:           make(chan struct{}),
	}

	// Initialize the QA worker pool and bounded queue.
	s.qaQueue = newQAQueue(workers, maxQueue, maxPerUser, globalMaxWorkers, s.executeQARequest, redisClient)
	s.qaQueue.Start(s.stopCh)

	// Start periodic cleanup loops.
	// Dedup cleanup is only needed in single-instance mode (local sync.Map);
	// when Redis handles dedup, the TTL on Redis keys handles expiry automatically.
	if redisClient == nil {
		go s.dedupCleanupLoop()
	}
	go s.rateLimiter.cleanupLoop(s.stopCh)

	if redisClient != nil {
		globalInfo := "unlimited"
		if globalMaxWorkers > 0 {
			globalInfo = fmt.Sprintf("%d", globalMaxWorkers)
		}
		logger.Infof(context.Background(), "[IM] Multi-instance mode enabled (instance=%s, workers=%d, queue=%d, global_max=%s)",
			s.instanceID[:8], workers, maxQueue, globalInfo)
	} else {
		logger.Infof(context.Background(), "[IM] Single-instance mode (no Redis, workers=%d, queue=%d)",
			workers, maxQueue)
	}

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
		s.stopChannelLocked(id, cs)
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
// For WebSocket channels with Redis available, only one instance acquires
// the leader lock and opens the connection; other instances periodically
// retry so they can take over if the leader dies.
func (s *Service) StartChannel(channel *IMChannel) error {
	_, span := tracing.ContextWithSpan(context.Background(), "im.StartChannel")
	defer span.End()
	span.SetAttributes(
		attribute.String("im.channel_id", channel.ID),
		attribute.String("im.platform", channel.Platform),
		attribute.String("im.mode", channel.Mode),
	)

	s.mu.Lock()
	factory, ok := s.adapterFactories[channel.Platform]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("no adapter factory for platform: %s", channel.Platform)
	}
	// Stop existing channel if running
	if existing, ok := s.channels[channel.ID]; ok {
		s.stopChannelLocked(channel.ID, existing)
	}
	s.mu.Unlock()

	// For WebSocket / long-poll channels, try leader election to avoid
	// duplicate connections. Only one instance should actively poll or
	// maintain a persistent connection for each channel.
	if (channel.Mode == "websocket" || channel.Mode == "longpoll") && s.redis != nil {
		acquired := s.tryAcquireWSLeader(channel.ID)
		if !acquired {
			logger.Infof(context.Background(),
				"[IM] Channel %s %s owned by another instance, will retry", channel.ID, channel.Mode)
			go s.wsLeaderRetryLoop(channel)
			return nil
		}
	}

	return s.startChannelInternal(channel, factory)
}

// startChannelInternal does the actual adapter creation and registration.
func (s *Service) startChannelInternal(channel *IMChannel, factory AdapterFactory) error {
	// Build the message handler that delegates to HandleMessage with this channel's config
	msgHandler := func(msgCtx context.Context, msg *IncomingMessage) error {
		return s.HandleMessage(msgCtx, msg, channel.ID)
	}

	ctx := context.Background()
	adapter, cancelFn, err := factory(ctx, channel, msgHandler)
	if err != nil {
		s.releaseWSLeader(channel.ID) // release lock on failure
		return fmt.Errorf("create adapter: %w", err)
	}

	// Start leader renewal goroutine for WebSocket / long-poll channels.
	var leaderCancel context.CancelFunc
	if (channel.Mode == "websocket" || channel.Mode == "longpoll") && s.redis != nil {
		leaderCtx, lCancel := context.WithCancel(context.Background())
		leaderCancel = lCancel
		go s.wsLeaderRenewLoop(leaderCtx, channel.ID)
	}

	s.mu.Lock()
	s.channels[channel.ID] = &channelState{
		Channel:      channel,
		Adapter:      adapter,
		Cancel:       cancelFn,
		leaderCancel: leaderCancel,
	}
	s.mu.Unlock()

	return nil
}

// StopChannel stops and removes a running channel.
func (s *Service) StopChannel(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cs, ok := s.channels[channelID]; ok {
		s.stopChannelLocked(channelID, cs)
	}
}

// stopChannelLocked stops a channel and removes it from the map.
// Caller must hold s.mu.
func (s *Service) stopChannelLocked(channelID string, cs *channelState) {
	if cs.leaderCancel != nil {
		cs.leaderCancel()
	}
	if cs.Cancel != nil {
		cs.Cancel()
	}
	delete(s.channels, channelID)
	// For long-poll channels, do NOT release the leader lock immediately.
	// Let it expire naturally via TTL so the old poll goroutine has time to
	// fully drain before another instance takes over. This prevents a brief
	// dual-writer window where both old and new instances process messages.
	// For websocket channels, the connection closes synchronously, so
	// immediate release is safe.
	if cs.Channel != nil && cs.Channel.Mode == "longpoll" {
		logger.Infof(context.Background(), "[IM] Stopped longpoll channel: id=%s (leader lock will expire via TTL)", channelID)
	} else {
		s.releaseWSLeader(channelID)
		logger.Infof(context.Background(), "[IM] Stopped channel: id=%s", channelID)
	}
}

// ── WebSocket leader election ───────────────────────────────────────────────

// tryAcquireWSLeader attempts to acquire the Redis lock for a WebSocket channel.
// Returns true if this instance is now the leader.
func (s *Service) tryAcquireWSLeader(channelID string) bool {
	if s.redis == nil {
		return true // single-instance mode: always leader
	}
	key := RedisKeyLeader + channelID
	ok, err := s.redis.SetNX(context.Background(), key, s.instanceID, wsLeaderTTL).Result()
	if err != nil {
		logger.Warnf(context.Background(), "[IM] Redis leader election failed for %s: %v, assuming leader", channelID, err)
		return true // Redis error: proceed anyway to avoid channel getting stuck
	}
	return ok
}

// releaseWSLeader releases the Redis leader lock for a WebSocket channel,
// but only if this instance owns it.
func (s *Service) releaseWSLeader(channelID string) {
	if s.redis == nil {
		return
	}
	key := RedisKeyLeader + channelID
	// Only delete if we own it (compare-and-delete via Lua).
	script := redis.NewScript(`
		if redis.call('GET', KEYS[1]) == ARGV[1] then
			return redis.call('DEL', KEYS[1])
		end
		return 0
	`)
	script.Run(context.Background(), s.redis, []string{key}, s.instanceID)
}

// wsLeaderRenewLoop periodically refreshes the leader lock TTL.
// Stops when ctx is cancelled (channel stopped) or if the lock is lost.
func (s *Service) wsLeaderRenewLoop(ctx context.Context, channelID string) {
	key := RedisKeyLeader + channelID
	ticker := time.NewTicker(wsLeaderRenewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Only renew if we still own the lock.
			script := redis.NewScript(`
				if redis.call('GET', KEYS[1]) == ARGV[1] then
					redis.call('PEXPIRE', KEYS[1], ARGV[2])
					return 1
				end
				return 0
			`)
			result, err := script.Run(ctx, s.redis, []string{key}, s.instanceID, wsLeaderTTL.Milliseconds()).Int64()
			if err != nil || result == 0 {
				logger.Warnf(context.Background(),
					"[IM] Lost leadership for channel %s, stopping adapter", channelID)
				s.StopChannel(channelID)
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// wsLeaderRetryLoop periodically tries to acquire the WebSocket leader lock.
// When it succeeds, it starts the channel adapter.
func (s *Service) wsLeaderRetryLoop(channel *IMChannel) {
	ticker := time.NewTicker(wsLeaderRetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check if channel is already running (another goroutine may have started it).
			if _, _, ok := s.GetChannelAdapter(channel.ID); ok {
				return
			}
			if s.tryAcquireWSLeader(channel.ID) {
				logger.Infof(context.Background(),
					"[IM] Acquired leadership for channel %s, starting adapter", channel.ID)
				s.mu.RLock()
				factory, ok := s.adapterFactories[channel.Platform]
				s.mu.RUnlock()
				if !ok {
					return
				}
				if err := s.startChannelInternal(channel, factory); err != nil {
					logger.Warnf(context.Background(),
						"[IM] Failed to start channel %s after acquiring leadership: %v", channel.ID, err)
				}
				return
			}
		case <-s.stopCh:
			return
		}
	}
}

// ── Cross-instance /stop via StreamManager ───────────────────────────────────
//
// The mechanism mirrors the web StopSession flow:
//   1. /stop writes a stop StreamEvent to StreamManager (keyed by sessionID + messageID)
//   2. A per-request watcher polls StreamManager and cancels the context on detection
//
// A Redis marker (im:stop:{userKey}) is kept as a lightweight pre-execution
// check for requests that haven't created an assistant message yet.

// checkAndClearStopMarker checks if a pre-execution /stop marker exists for
// the given userKey. If found, it deletes the marker and returns true.
func (s *Service) checkAndClearStopMarker(ctx context.Context, userKey string) bool {
	if s.redis == nil {
		return false
	}
	stopKey := RedisKeyStop + userKey
	deleted, err := s.redis.Del(ctx, stopKey).Result()
	if err != nil {
		return false
	}
	return deleted > 0
}

// storeInflightMapping writes the (sessionID, assistantMessageID) to Redis so
// that /stop on any instance can look it up and write to StreamManager.
func (s *Service) storeInflightMapping(ctx context.Context, userKey, sessionID, messageID string) {
	if s.redis == nil {
		return
	}
	val := sessionID + ":" + messageID
	if err := s.redis.Set(ctx, RedisKeyInflight+userKey, val, 10*time.Minute).Err(); err != nil {
		logger.Warnf(ctx, "[IM] Failed to store inflight mapping: %v", err)
	}
}

// clearInflightMapping removes the inflight mapping from Redis.
func (s *Service) clearInflightMapping(ctx context.Context, userKey string) {
	if s.redis == nil {
		return
	}
	s.redis.Del(ctx, RedisKeyInflight+userKey)
}

// loadInflightMapping retrieves (sessionID, messageID) from Redis.
func (s *Service) loadInflightMapping(ctx context.Context, userKey string) (sessionID, messageID string, ok bool) {
	if s.redis == nil {
		return "", "", false
	}
	val, err := s.redis.Get(ctx, RedisKeyInflight+userKey).Result()
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(val, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// writeStopEvent writes a stop event to StreamManager, matching the web
// StopSession pattern. The QA watcher goroutine detects it and cancels.
func (s *Service) writeStopEvent(ctx context.Context, sessionID, messageID string) {
	stopEvt := interfaces.StreamEvent{
		ID:        fmt.Sprintf("stop-%d", time.Now().UnixNano()),
		Type:      types.ResponseType(event.EventStop),
		Content:   "",
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"session_id": sessionID,
			"message_id": messageID,
			"reason":     "user_requested",
			"source":     "im",
		},
	}
	if err := s.streamManager.AppendEvent(ctx, sessionID, messageID, stopEvt); err != nil {
		logger.Warnf(ctx, "[IM] Failed to write stop event to StreamManager: %v", err)
	}
}

// watchStreamManagerStop polls StreamManager for stop events and cancels the
// QA context when one is detected. This is the IM equivalent of the web SSE
// handler's stop detection loop. Exits when ctx is done.
func (s *Service) watchStreamManagerStop(ctx context.Context, sessionID, messageID string, cancel context.CancelFunc) {
	ticker := time.NewTicker(stopPollInterval)
	defer ticker.Stop()

	offset := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			events, newOffset, err := s.streamManager.GetEvents(ctx, sessionID, messageID, offset)
			if err != nil {
				continue
			}
			for _, evt := range events {
				if evt.Type == types.ResponseType(event.EventStop) {
					logger.Infof(ctx, "[IM] Stop event from StreamManager, cancelling: session=%s message=%s",
						sessionID, messageID)
					cancel()
					return
				}
			}
			offset = newOffset
		}
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

// isDuplicate checks if a message has already been processed.
//
// Multi-instance mode (Redis available): uses Redis SetNX for cross-instance
// deduplication. If Redis fails, returns true (fail-closed) to prevent
// duplicate processing across instances — a dropped message can be retried
// by the user, but a duplicate LLM response wastes resources and confuses.
//
// Single-instance mode (no Redis): uses a local sync.Map, which is sufficient
// when only one instance receives messages.
func (s *Service) isDuplicate(ctx context.Context, messageID string) bool {
	if s.redis != nil {
		key := RedisKeyDedup + messageID
		ok, err := s.redis.SetNX(ctx, key, "1", dedupTTL).Result()
		if err == nil {
			return !ok // SetNX returns true when key was newly set (not a duplicate)
		}
		// Redis is configured but failed — fail-closed to avoid cross-instance
		// duplicate processing. The user can simply resend the message.
		logger.Errorf(ctx, "[IM] Redis dedup failed (fail-closed, message dropped): %v", err)
		return true
	}
	// Single-instance mode: local dedup is sufficient.
	_, loaded := s.processedMsgs.LoadOrStore(messageID, time.Now())
	return loaded
}

// HandleMessage processes an incoming IM message end-to-end using channel config.
func (s *Service) HandleMessage(ctx context.Context, msg *IncomingMessage, channelID string) error {
	ctx, span := tracing.ContextWithSpan(ctx, "im.HandleMessage")
	defer span.End()
	span.SetAttributes(
		attribute.String("im.channel_id", channelID),
		attribute.String("im.platform", string(msg.Platform)),
		attribute.String("im.user_id", msg.UserID),
		attribute.String("im.chat_id", msg.ChatID),
		attribute.String("im.thread_id", msg.ThreadID),
		attribute.String("im.message_type", string(msg.MessageType)),
		attribute.Bool("im.has_quote", msg.Quote != nil),
	)

	// Dedup: skip if this message was already processed (IM platforms may retry)
	if msg.MessageID != "" {
		if s.isDuplicate(ctx, msg.MessageID) {
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

	span.SetAttributes(attribute.String("im.session_mode", channel.SessionMode))

	// Resolve threadID for key building — only include in thread mode to avoid
	// leaking thread scope into user-mode rate limit / inflight keys.
	threadID := ""
	if channel.SessionMode == string(SessionModeThread) {
		threadID = msg.ThreadID
	}

	// Rate limit: enforce per-user sliding window to prevent abuse.
	// Slash-commands (/stop, /clear, etc.) bypass rate limiting so the user
	// always retains control over the bot even under heavy messaging.
	isCommand := s.cmdRegistry.IsRegistered(msg.Content)
	if !isCommand {
		rateLimitKey := makeUserKey(channelID, msg.UserID, msg.ChatID, threadID)
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

	// ── Non-text message without text content ──
	// If the message is an image/file/video but has no text content, the QA pipeline
	// cannot do anything useful (no vision support in IM yet). Sending an empty query
	// to KB retrieval would return irrelevant results and cause hallucination.
	if msg.Content == "" && (msg.MessageType == MessageTypeImage || msg.MessageType == MessageTypeFile) {
		logger.Infof(ctx, "[IM] Skipping QA for non-text message without content: type=%s", msg.MessageType)
		if err := adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: "当前渠道未配置文件知识库，无法处理图片/文件消息。请在渠道设置中配置文件知识库后再发送，或直接用文字描述您的问题。",
			IsFinal: true,
		}); err != nil {
			logger.Warnf(ctx, "[IM] Failed to send non-text hint reply: %v", err)
		}
		return nil
	}

	// 1. Get tenant
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("get tenant: %w", err)
	}
	sessionCtx := context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	sessionCtx = context.WithValue(sessionCtx, types.TenantInfoContextKey, tenant)

	// 2. Resolve or create a WeKnora session
	channelSession, err := s.resolveSession(sessionCtx, msg, tenantID, agentID, channelID, channel.SessionMode)
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
		// The underlying session may have been deleted from the UI while the
		// ChannelSession mapping still exists (GORM soft-delete does not trigger
		// SQL ON DELETE CASCADE). Recover by soft-deleting the stale mapping and
		// re-creating a fresh session so the IM bot doesn't become permanently
		// unresponsive. (fixes #1046)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warnf(ctx, "[IM] Session %s not found (deleted?), recycling stale channel session %s",
				channelSession.SessionID, channelSession.ID)
			if delErr := s.db.Delete(&ChannelSession{}, "id = ?", channelSession.ID).Error; delErr != nil {
				logger.Warnf(ctx, "[IM] Failed to delete stale channel session %s: %v", channelSession.ID, delErr)
			}
			channelSession, err = s.resolveSession(sessionCtx, msg, tenantID, agentID, channelID, channel.SessionMode)
			if err != nil {
				return fmt.Errorf("resolve session (retry): %w", err)
			}
			session, err = s.sessionService.GetSession(sessionCtx, channelSession.SessionID)
			if err != nil {
				return fmt.Errorf("get session (retry): %w", err)
			}
		} else {
			return fmt.Errorf("get session: %w", err)
		}
	}

	// 5. Enqueue the QA request into the bounded worker pool.
	// The worker pool controls LLM concurrency and provides backpressure.
	qaCtx, qaCancel := context.WithCancel(sessionCtx)
	userKey := makeUserKey(channelID, msg.UserID, msg.ChatID, threadID)

	req := &qaRequest{
		ctx:       qaCtx,
		cancel:    qaCancel,
		msg:       msg,
		session:   session,
		agent:     customAgent,
		adapter:   adapter,
		channel:   channel,
		channelID: channelID,
		fileSvc:   buildTenantFileService(tenant),
		userKey:   userKey,
	}

	pos, enqueueErr := s.qaQueue.Enqueue(req)
	if enqueueErr != nil {
		qaCancel()
		span.AddEvent("queue rejected", trace.WithAttributes(attribute.String("reason", enqueueErr.Error())))
		logger.Warnf(ctx, "[IM] Queue rejected: user=%s reason=%v", msg.UserID, enqueueErr)
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: "当前排队人数较多，请稍后再试。",
			IsFinal: true,
		})
		return nil
	}

	if pos > 0 {
		logger.Infof(ctx, "[IM] Enqueued: user=%s pos=%d depth=%d", msg.UserID, pos, s.qaQueue.Metrics().Depth)
		// In multi-instance mode the local queue position does not reflect global
		// depth, so use a generic "queued" hint instead of an exact number.
		queueMsg := fmt.Sprintf("收到，前面还有 %d 条消息在处理，请稍候 ⏳", pos)
		if s.redis != nil {
			queueMsg = "收到，当前排队中，请稍候 ⏳"
		}
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: queueMsg,
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
	ctx, span := tracing.ContextWithSpan(req.ctx, "im.ExecuteQA")
	defer span.End()
	span.SetAttributes(
		attribute.String("im.channel_id", req.channelID),
		attribute.String("im.user_key", req.userKey),
		attribute.String("im.user_id", req.msg.UserID),
	)
	defer req.cancel()

	// Track in-flight request so /stop can cancel it.
	entry := &inflightEntry{cancel: req.cancel}
	s.inflight.Store(req.userKey, entry)
	defer s.inflight.Delete(req.userKey)

	// Check if a pre-execution /stop was issued while this request was queued.
	if s.checkAndClearStopMarker(ctx, req.userKey) {
		span.AddEvent("cancelled by remote /stop before execution")
		logger.Infof(ctx, "[IM] Request cancelled by remote /stop before execution: %s", req.userKey)
		return
	}

	// NOTE: StreamManager-based stop detection is started inside handleMessageStream /
	// runQA after the assistant message is created (that's when we have the
	// sessionID + messageID needed to poll StreamManager).

	// kbIDs is left empty so the QA pipeline resolves them from the agent config.
	var kbIDs []string

	// Determine output mode from channel config.
	streamDisabled := req.channel.OutputMode == "full"

	// If the adapter supports streaming and output is not "full", use streaming.
	if !streamDisabled {
		if streamer, ok := req.adapter.(StreamSender); ok {
			if err := s.handleMessageStream(ctx, req.msg, req.session, req.agent, kbIDs, streamer, req.adapter, req.userKey, req.fileSvc); err != nil {
				span.SetStatus(codes.Error, err.Error())
				logger.Errorf(ctx, "[IM] Stream QA failed: %v", err)
			}
			return
		}
	}

	// Non-streaming fallback: collect full answer then send.
	answer, err := s.runQA(ctx, req.session, req.msg.Content, req.agent, kbIDs, req.userKey, req.msg.Quote)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		logger.Errorf(ctx, "[IM] QA failed: %v, sending fallback reply", err)
		answer = "抱歉，处理您的问题时出现了异常，请稍后再试。"
	}

	reply := &ReplyMessage{
		Content: cleanIMContent(ctx, answer, req.fileSvc),
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
	ctx, span := tracing.ContextWithSpan(ctx, "im.HandleCommand")
	defer span.End()
	span.SetAttributes(
		attribute.String("im.command", cmd.Name()),
		attribute.String("im.channel_id", channel.ID),
		attribute.String("im.user_id", msg.UserID),
	)

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
		// Soft-delete the current ChannelSession so the next IM message
		// starts a completely fresh WeKnora session. Conversation history
		// is keyed by session ID and rebuilt from DB on demand, so no
		// separate cache invalidation step is needed.
		if err := s.db.Model(&ChannelSession{}).
			Where("id = ?", channelSession.ID).
			Update("deleted_at", time.Now()).Error; err != nil {
			logger.Warnf(ctx, "[IM] Failed to soft-delete channel session: %v", err)
		}
	case ActionStop:
		stopThreadID := ""
		if channel.SessionMode == string(SessionModeThread) {
			stopThreadID = msg.ThreadID
		}
		inflightKey := makeUserKey(channel.ID, msg.UserID, msg.ChatID, stopThreadID)

		// 1. Try local cancel: remove from queue or cancel in-flight.
		var localSessionID, localMessageID string
		localStopped := s.qaQueue.Remove(inflightKey)
		if localStopped {
			logger.Infof(ctx, "[IM] Cancelled queued QA: key=%s", inflightKey)
		} else if raw, loaded := s.inflight.LoadAndDelete(inflightKey); loaded {
			e := raw.(*inflightEntry)
			e.cancel()
			localStopped = true
			localSessionID = e.sessionID
			localMessageID = e.assistantMessageID
			logger.Infof(ctx, "[IM] Cancelled in-flight QA: key=%s", inflightKey)
		}

		// 2. Write stop event to StreamManager (same as web StopSession).
		//    For local stop with known IDs, write directly.
		//    For cross-instance, look up Redis inflight mapping to get IDs.
		sessionID, messageID := localSessionID, localMessageID
		if sessionID == "" || messageID == "" {
			// Try cross-instance lookup.
			sessionID, messageID, _ = s.loadInflightMapping(ctx, inflightKey)
		}
		if sessionID != "" && messageID != "" {
			s.writeStopEvent(ctx, sessionID, messageID)
			logger.Infof(ctx, "[IM] Wrote stop event to StreamManager: session=%s message=%s", sessionID, messageID)
		}

		// 3. Set Redis marker as fallback for requests not yet executing
		//    (no assistant message yet → no StreamManager entry to poll).
		if s.redis != nil {
			s.redis.Set(ctx, RedisKeyStop+inflightKey, "1", stopMarkerTTL)
		}

		if !localStopped && sessionID == "" {
			logger.Infof(ctx, "[IM] Set cross-instance stop marker (no inflight found): key=%s", inflightKey)
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

// resolveSession dispatches to the appropriate session resolution strategy
// based on the channel's session mode.
func (s *Service) resolveSession(ctx context.Context, msg *IncomingMessage, tenantID uint64, agentID string, imChannelID string, sessionMode string) (*ChannelSession, error) {
	switch SessionMode(sessionMode) {
	case SessionModeThread:
		return s.resolveThreadSession(ctx, msg, tenantID, agentID, imChannelID)
	default: // SessionModeUser
		return s.resolveUserSession(ctx, msg, tenantID, agentID, imChannelID)
	}
}

// buildUserSessionTitle produces a human-distinguishable title for a user-mode
// IM session. Platform adapters only surface ChatID, not a readable chat name,
// so we fall back to short ID suffixes to keep group/DM sessions visually distinct.
// Platform prefix is intentionally omitted — the UI renders a platform icon badge
// alongside the title, so the `[feishu]` prefix would be redundant clutter.
func buildUserSessionTitle(msg *IncomingMessage) string {
	var b strings.Builder
	if msg.UserName != "" {
		b.WriteString(msg.UserName)
	} else if msg.UserID != "" {
		b.WriteString("user ")
		b.WriteString(shortID(msg.UserID))
	} else {
		b.WriteString("user")
	}
	if msg.ChatType == ChatTypeGroup && msg.ChatID != "" {
		fmt.Fprintf(&b, " · group %s", shortID(msg.ChatID))
	} else if msg.ChatType == ChatTypeDirect {
		b.WriteString(" · dm")
	}
	return b.String()
}

// buildThreadSessionTitle produces a title for a thread-mode IM session.
// In thread mode different users can share one session, so the user name is
// omitted and chat/thread IDs carry the distinguishing information.
// Platform prefix is omitted for the same reason as buildUserSessionTitle.
func buildThreadSessionTitle(msg *IncomingMessage) string {
	var b strings.Builder
	if msg.ChatID != "" {
		fmt.Fprintf(&b, "chat %s · ", shortID(msg.ChatID))
	}
	b.WriteString("thread ")
	b.WriteString(shortID(msg.ThreadID))
	return b.String()
}

// shortID returns the last 8 characters of id, or id itself when shorter.
// Used to keep long platform IDs readable inside titles without losing uniqueness.
func shortID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

// resolveUserSession finds or creates a ChannelSession keyed by (platform, user_id, chat_id, tenant_id, agent_id).
// This is the original session resolution strategy.
func (s *Service) resolveUserSession(ctx context.Context, msg *IncomingMessage, tenantID uint64, agentID string, imChannelID string) (*ChannelSession, error) {
	var cs ChannelSession
	result := s.db.Where("platform = ? AND user_id = ? AND chat_id = ? AND tenant_id = ? AND agent_id = ? AND deleted_at IS NULL",
		string(msg.Platform), msg.UserID, msg.ChatID, tenantID, agentID).
		First(&cs)

	if result.Error == nil {
		return &cs, nil
	}

	if result.Error != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("query channel session: %w", result.Error)
	}

	// Create a new WeKnora session
	title := buildUserSessionTitle(msg)

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
		if delErr := s.db.Where("id = ?", createdSession.ID).Delete(createdSession).Error; delErr != nil {
			logger.Warnf(ctx, "[IM] Failed to clean up orphaned session %s: %v", createdSession.ID, delErr)
		}
		var existing ChannelSession
		if findErr := s.db.Where("platform = ? AND user_id = ? AND chat_id = ? AND tenant_id = ? AND agent_id = ? AND deleted_at IS NULL",
			string(msg.Platform), msg.UserID, msg.ChatID, tenantID, agentID).
			First(&existing).Error; findErr != nil {
			return nil, fmt.Errorf("create channel session: %w (lookup fallback: %v)", err, findErr)
		}
		return &existing, nil
	}

	logger.Infof(ctx, "[IM] Created new session mapping: channel=%s/%s/%s -> session=%s",
		msg.Platform, msg.UserID, msg.ChatID, createdSession.ID)

	return &cs, nil
}

// resolveThreadSession finds or creates a ChannelSession keyed by (platform, chat_id, thread_id, tenant_id, agent_id).
// In thread mode, each message thread gets its own session. Multiple users in the
// same thread share the same session. Top-level messages use their own ID as
// ThreadID, creating a new session per top-level message.
func (s *Service) resolveThreadSession(ctx context.Context, msg *IncomingMessage, tenantID uint64, agentID string, imChannelID string) (*ChannelSession, error) {
	threadID := msg.ThreadID
	if threadID == "" {
		// Defense-in-depth: frontend blocks thread mode for unsupported platforms,
		// but if ThreadID is somehow empty, fall back to user-mode resolution
		// to avoid creating a shared session for all empty-thread messages.
		logger.Warnf(ctx, "[IM] Thread mode but ThreadID is empty (platform=%s chat=%s), falling back to user session", msg.Platform, msg.ChatID)
		return s.resolveUserSession(ctx, msg, tenantID, agentID, imChannelID)
	}

	var cs ChannelSession
	result := s.db.Where(
		"platform = ? AND chat_id = ? AND thread_id = ? AND tenant_id = ? AND agent_id = ? AND deleted_at IS NULL",
		string(msg.Platform), msg.ChatID, threadID, tenantID, agentID,
	).First(&cs)

	if result.Error == nil {
		return &cs, nil
	}

	if result.Error != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("query thread session: %w", result.Error)
	}

	// Build a session title including chat + thread suffix for traceability.
	title := buildThreadSessionTitle(msg)

	newSession := &types.Session{
		TenantID:    tenantID,
		Title:       title,
		Description: fmt.Sprintf("Thread-based session from %s IM", msg.Platform),
	}

	createdSession, err := s.sessionService.CreateSession(ctx, newSession)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	cs = ChannelSession{
		Platform:    string(msg.Platform),
		UserID:      msg.UserID, // record the first creator
		ChatID:      msg.ChatID,
		ThreadID:    threadID,
		SessionID:   createdSession.ID,
		TenantID:    tenantID,
		AgentID:     agentID,
		IMChannelID: imChannelID,
	}

	if err := s.db.Create(&cs).Error; err != nil {
		// Unique constraint fallback for concurrent creation.
		if delErr := s.db.Where("id = ?", createdSession.ID).Delete(createdSession).Error; delErr != nil {
			logger.Warnf(ctx, "[IM] Failed to clean up orphaned session %s: %v", createdSession.ID, delErr)
		}
		var existing ChannelSession
		if findErr := s.db.Where(
			"platform = ? AND chat_id = ? AND thread_id = ? AND tenant_id = ? AND agent_id = ? AND deleted_at IS NULL",
			string(msg.Platform), msg.ChatID, threadID, tenantID, agentID,
		).First(&existing).Error; findErr != nil {
			return nil, fmt.Errorf("create thread session: %w (lookup fallback: %v)", err, findErr)
		}
		return &existing, nil
	}

	logger.Infof(ctx, "[IM] Created new thread session: platform=%s thread=%s chat=%s -> session=%s",
		msg.Platform, threadID, msg.ChatID, createdSession.ID)
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
func (s *Service) handleMessageStream(ctx context.Context, msg *IncomingMessage, session *types.Session, customAgent *types.CustomAgent, kbIDs []string, streamer StreamSender, adapter Adapter, userKey string, fileSvc interfaces.FileService) error {
	// Start the stream on the IM platform (e.g., create Feishu streaming card)
	streamID, err := streamer.StartStream(ctx, msg)
	if err != nil {
		logger.Warnf(ctx, "[IM] StartStream failed, falling back to non-streaming: %v", err)
		return s.fallbackNonStream(ctx, msg, session, customAgent, kbIDs, adapter, userKey, fileSvc)
	}

	// Prepare the QA pipeline
	// No total deadline: each agent round has its own LLMCallTimeout (default 120s).
	// A hard pipeline deadline would kill multi-round agent reasoning prematurely.
	qaCtx, qaCancel := context.WithCancel(ctx)
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
		Channel: "im",
	})
	if err != nil {
		return fmt.Errorf("create user message: %w", err)
	}

	// Create placeholder assistant message
	assistantMsg, err := s.messageService.CreateMessage(qaCtx, &types.Message{
		SessionID: session.ID, Role: "assistant",
		RequestID: requestID, CreatedAt: time.Now(), IsCompleted: false,
		Channel: "im",
	})
	if err != nil {
		return fmt.Errorf("create assistant message: %w", err)
	}

	// Register inflight mapping so cross-instance /stop can find this request
	// and write a stop event to StreamManager.
	if raw, ok := s.inflight.Load(userKey); ok {
		e := raw.(*inflightEntry)
		e.sessionID = session.ID
		e.assistantMessageID = assistantMsg.ID
	}
	s.storeInflightMapping(qaCtx, userKey, session.ID, assistantMsg.ID)
	defer s.clearInflightMapping(ctx, userKey)

	// Start StreamManager stop watcher — mirrors web's handleAgentEventsForSSE
	// stop detection. Cancels qaCtx if a stop event is written by any instance.
	go s.watchStreamManagerStop(qaCtx, session.ID, assistantMsg.ID, qaCancel)

	// Run QA async
	go func() {
		var err error
		req := buildIMQARequest(session, msg.Content, assistantMsg.ID, userMsg.ID, customAgent, kbIDs, msg.Quote)
		if req.QuotedContext != "" {
			logger.Debugf(qaCtx, "[IM] QuotedContext set: length=%d", len(req.QuotedContext))
		}
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

	// Flush loop: periodically send buffered content to the IM platform.
	// A holdback mechanism prevents flushing incomplete provider:// URLs or
	// XML tags that straddle a chunk boundary (see holdbackCutoff).
	ticker := time.NewTicker(streamFlushInterval)
	defer ticker.Stop()

	var holdback string // text held back from the previous flush

	flush := func(final bool) {
		bufMu.Lock()
		chunk := holdback + buf.String()
		buf.Reset()
		bufMu.Unlock()
		holdback = ""

		if chunk == "" {
			return
		}

		// On non-final flushes, check for incomplete patterns at the tail.
		if !final {
			if cut := holdbackCutoff(chunk); cut < len(chunk) {
				holdback = chunk[cut:]
				chunk = chunk[:cut]
			}
		}

		if chunk != "" {
			if err := streamer.SendStreamChunk(ctx, msg, streamID, cleanIMContent(ctx, chunk, fileSvc)); err != nil {
				logger.Warnf(ctx, "[IM] SendStreamChunk failed: %v", err)
			}
		}
	}

loop:
	for {
		select {
		case <-ticker.C:
			flush(false)
		case <-done:
			break loop
		case <-qaCtx.Done():
			break loop
		}
	}

	// Final flush of any remaining content (including holdback).
	flush(true)

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
func (s *Service) fallbackNonStream(ctx context.Context, msg *IncomingMessage, session *types.Session, customAgent *types.CustomAgent, kbIDs []string, adapter Adapter, userKey string, fileSvc interfaces.FileService) error {
	answer, err := s.runQA(ctx, session, msg.Content, customAgent, kbIDs, userKey, msg.Quote)
	if err != nil {
		logger.Errorf(ctx, "[IM] QA fallback failed: %v", err)
		answer = "抱歉，处理您的问题时出现了异常，请稍后再试。"
	}

	return adapter.SendReply(ctx, msg, &ReplyMessage{Content: cleanIMContent(ctx, answer, fileSvc), IsFinal: true})
}

// runQA executes the WeKnora QA pipeline and returns the full answer text.
func (s *Service) runQA(ctx context.Context, session *types.Session, query string, customAgent *types.CustomAgent, kbIDs []string, userKey string, quote *QuotedMessage) (string, error) {
	// Cancellable context (no hard deadline): each agent round has its own
	// LLMCallTimeout. The context can still be cancelled by /stop.
	ctx, cancel := context.WithCancel(ctx)
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
		Channel:     "im",
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
		Channel:     "im",
	})
	if err != nil {
		return "", fmt.Errorf("create assistant message: %w", err)
	}

	// Register inflight mapping for cross-instance /stop via StreamManager.
	if raw, ok := s.inflight.Load(userKey); ok {
		e := raw.(*inflightEntry)
		e.sessionID = session.ID
		e.assistantMessageID = assistantMsg.ID
	}
	s.storeInflightMapping(ctx, userKey, session.ID, assistantMsg.ID)
	defer s.clearInflightMapping(ctx, userKey)

	// Start StreamManager stop watcher.
	go s.watchStreamManagerStop(ctx, session.ID, assistantMsg.ID, cancel)

	// Run QA async
	go func() {
		var err error
		req := buildIMQARequest(session, query, assistantMsg.ID, userMsg.ID, customAgent, kbIDs, quote)
		if req.QuotedContext != "" {
			logger.Debugf(ctx, "[IM] QuotedContext set: length=%d", len(req.QuotedContext))
		}
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

	// Wait for completion or cancellation (e.g., /stop)
	select {
	case <-done:
	case <-ctx.Done():
		// Mark assistant message as completed to avoid dangling incomplete records
		assistantMsg.Content = "抱歉，回答已被取消。"
		assistantMsg.IsCompleted = true
		// Use a fresh context since the original is cancelled
		if updateErr := s.messageService.UpdateMessage(context.WithoutCancel(ctx), assistantMsg); updateErr != nil {
			logger.Warnf(ctx, "[IM] Failed to update cancelled assistant message: %v", updateErr)
		}
		return "", fmt.Errorf("QA cancelled: %w", ctx.Err())
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

	// Update assistant message with the full answer (including citation tags for web rendering).
	assistantMsg.Content = answer
	assistantMsg.IsCompleted = true
	if err := s.messageService.UpdateMessage(ctx, assistantMsg); err != nil {
		logger.Warnf(ctx, "[IM] Failed to update assistant message: %v", err)
	}

	// Return raw answer — callers apply cleanIMContent with the appropriate FileService.
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

// ChannelWithAgent augments an IMChannel summary with its owning agent's display name.
// Credentials are intentionally omitted so this type is safe to return from a
// tenant-scoped list endpoint; callers that need credentials must use the
// per-agent endpoint which enforces the same tenant scope anyway.
type ChannelWithAgent struct {
	ID          string    `json:"id"`
	TenantID    uint64    `json:"tenant_id"`
	AgentID     string    `json:"agent_id"`
	AgentName   string    `json:"agent_name"`
	Platform    string    `json:"platform"`
	Name        string    `json:"name"`
	Enabled     bool      `json:"enabled"`
	Mode        string    `json:"mode"`
	OutputMode  string    `json:"output_mode"`
	SessionMode string    `json:"session_mode"`
	BotIdentity string    `json:"bot_identity"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListChannelsByTenant returns all non-deleted IM channels in the given tenant,
// joined with custom_agents.name. Built-in agent IDs (whose rows may not exist
// in custom_agents) produce an empty AgentName — the frontend can substitute a
// localized "builtin agent" label in that case.
func (s *Service) ListChannelsByTenant(tenantID uint64) ([]ChannelWithAgent, error) {
	var rows []ChannelWithAgent
	err := s.db.Table("im_channels AS c").
		Select(`c.id, c.tenant_id, c.agent_id,
                COALESCE(a.name, '') AS agent_name,
                c.platform, c.name, c.enabled, c.mode, c.output_mode,
                c.session_mode, c.bot_identity, c.created_at, c.updated_at`).
		Joins(`LEFT JOIN custom_agents AS a
               ON a.id = c.agent_id AND a.tenant_id = c.tenant_id`).
		Where("c.tenant_id = ? AND c.deleted_at IS NULL", tenantID).
		Order("c.created_at DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
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
	knowledge, err := s.knowledgeService.CreateKnowledgeFromFile(kbCtx, kbID, fh, nil, nil, "", "", imPlatformToChannel(channel.Platform))
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
	typeName := fileTypeName(fileName)

	var fallback string
	if success {
		fallback = fmt.Sprintf("✅ %s已保存到知识库，正在解析中，完成后会通知你～", typeName)
	} else {
		fallback = fmt.Sprintf("❌ %s处理失败：%s", typeName, errDetail)
	}

	var situation string
	if success {
		situation = fmt.Sprintf("用户上传的%s已成功保存到知识库，但还需要后台解析文档内容（这需要一些时间）。请告知用户文件已收到，正在解析处理中，解析完成后会自动推送结果。", typeName)
	} else {
		situation = fmt.Sprintf("用户上传的%s处理失败，原因：%s。", typeName, errDetail)
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

			typeName := fileTypeName(fileName)

			switch knowledge.ParseStatus {
			case types.ParseStatusFailed:
				// Parsing failed — notify user and stop watching
				errMsg := knowledge.ErrorMessage
				if errMsg == "" {
					errMsg = "文档解析失败"
				}
				_ = s.sendSmartReply(ctx, adapter, msg, channel,
					fmt.Sprintf("用户之前上传的%s解析失败了，错误原因：%s。请安慰用户并建议重试。", typeName, errMsg),
					fmt.Sprintf("⚠️ %s解析失败：%s", typeName, errMsg))
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
							fmt.Sprintf("用户之前上传的%s已解析完成。以下是文件的完整摘要内容：\n%s\n\n请生成一条通知消息，包含：1) 告知文件已解析完成；2) 用 Markdown 格式（标题、列表、加粗等）结构化展示上述摘要内容，不要删减或概括；3) 提示用户可以针对该文件提问。", typeName, knowledge.Description),
							fmt.Sprintf("📄 %s已解析完成。\n\n**摘要：**\n\n%s\n\n---\n可以针对该文件进行提问。", typeName, knowledge.Description))
					} else {
						_ = s.sendSmartReply(ctx, adapter, msg, channel,
							fmt.Sprintf("用户之前上传的%s已解析完成，现在可以开始针对该文件进行提问了。", typeName),
							fmt.Sprintf("📄 %s已解析完成，可以开始提问了！", typeName))
					}
					return

				case types.SummaryStatusCompleted:
					// Summary is ready — send it
					s.sendSummaryNotification(ctx, adapter, msg, knowledge, fileName, channel)
					return

				case types.SummaryStatusFailed:
					_ = s.sendSmartReply(ctx, adapter, msg, channel,
						fmt.Sprintf("用户之前上传的%s已解析完成，但摘要生成失败了。不过文件已可用于提问。", typeName),
						fmt.Sprintf("📄 %s已解析完成，可以开始提问了！（摘要生成失败）", typeName))
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

	typeName := fileTypeName(fileName)
	var situation, fallback string
	if summary != "" && summary != fileName {
		situation = fmt.Sprintf("用户之前上传的%s已解析完成。以下是文件的完整摘要内容：\n%s\n\n请生成一条通知消息，包含：1) 告知文件已解析完成；2) 用 Markdown 格式（标题、列表、加粗等）结构化展示上述摘要内容，不要删减或概括；3) 提示用户可以针对该文件提问。", typeName, summary)
		fallback = fmt.Sprintf("📄 %s已解析完成。\n\n**摘要：**\n\n%s\n\n---\n可以针对该文件进行提问。", typeName, summary)
	} else {
		situation = fmt.Sprintf("用户之前上传的%s已解析完成，现在可以开始针对该文件进行提问了。", typeName)
		fallback = fmt.Sprintf("📄 %s已解析完成，可以开始提问了！", typeName)
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

// imPlatformToChannel maps an IM platform identifier to a Knowledge.Channel constant.
func imPlatformToChannel(platform string) string {
	switch strings.ToLower(platform) {
	case "wechat":
		return types.ChannelWechat
	case "wecom", "wxwork":
		return types.ChannelWecom
	case "feishu", "lark":
		return types.ChannelFeishu
	case "dingtalk":
		return types.ChannelDingtalk
	case "slack":
		return types.ChannelSlack
	default:
		return types.ChannelIM
	}
}

// fileTypeName returns a human-readable file type name based on the file extension.
func fileTypeName(filename string) string {
	switch fileExtension(filename) {
	case "pdf":
		return "PDF 文档"
	case "doc", "docx":
		return "Word 文档"
	case "txt":
		return "文本文件"
	case "md", "markdown":
		return "Markdown 文档"
	case "png", "jpg", "jpeg", "gif":
		return "图片"
	case "csv":
		return "CSV 表格"
	case "xls", "xlsx":
		return "Excel 表格"
	case "ppt", "pptx":
		return "PPT 演示文稿"
	default:
		return "文件"
	}
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
