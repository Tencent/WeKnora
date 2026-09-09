package session

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Delivery modes for a mid-run user message. Chosen at send time.
const (
	// steerDeliveryInject is drained into the running turn at the next
	// round boundary (and again just before a natural stop, so the agent
	// continues instead of finishing).
	steerDeliveryInject = "inject"
	// steerDeliveryAfter stays in the queue until the current run exits,
	// then becomes the query (or carry-over) of a follow-up run.
	steerDeliveryAfter = "after"
)

// steerDataConsumed marks a steer event the running turn has already taken
// into its message list. It lives on the event itself (not in process memory)
// so every replica agrees on what is still pending: the overlay, the queue
// depth guard and the follow-up backlog all read the same flag.
const steerDataConsumed = "consumed"

// steerDataUserMessageID is the persisted user-row ID written alongside the
// consumed flag so a retried drain can reuse the row instead of inserting again.
const steerDataUserMessageID = "user_message_id"

// SteerMessageRequest is the payload of POST /sessions/:session_id/steer.
type SteerMessageRequest struct {
	Query          string                 `json:"query" binding:"required"`
	MentionedItems []MentionedItemRequest `json:"mentioned_items,omitempty"`
	Channel        string                 `json:"channel,omitempty"`
	// Delivery is "after" (default) or "inject". See the constants above.
	Delivery string `json:"delivery,omitempty"`
}

// Steer request limits. maxSteerQueueDepth bounds how many messages a single
// run can accumulate — past that the run is clearly ignoring its user and the
// honest answer is to refuse instead of queueing silently.
const (
	maxSteerQueueDepth   = 10
	maxSteerQueryLength  = 10000
	steerDrainBatchLimit = 20
)

// steerSink implements types.SteerSink on the handler side: it reads the
// steer sub-list through the shared StreamManager and persists accepted
// messages as user-role rows under the run's request ID. Constructed per run
// in setupSSEStream and handed to the engine via SetSteerSink.
type steerSink struct {
	ctx              context.Context
	sessionID        string
	requestID        string
	assistantMessage *types.Message
	messageService   interfaces.MessageService
	streamManager    interfaces.StreamManager

	mu                sync.Mutex
	lastUserMessageID string
	drainedOffset     int
	injectedIDs       map[string]struct{}
}

func newSteerSink(
	ctx context.Context,
	sessionID, requestID string,
	assistantMessage *types.Message,
	messageService interfaces.MessageService,
	streamManager interfaces.StreamManager,
) *steerSink {
	return &steerSink{
		ctx:              ctx,
		sessionID:        sessionID,
		requestID:        requestID,
		assistantMessage: assistantMessage,
		messageService:   messageService,
		streamManager:    streamManager,
		injectedIDs:      make(map[string]struct{}),
	}
}

// PollSteer drains the steer sub-list and returns plain maps so the shape
// matches types.SteerSink without the agent package importing interfaces.
// Satisfies types.SteerSink.
func (s *steerSink) PollSteer(
	ctx context.Context, sessionID, messageID string, lastOffset int,
) ([]map[string]interface{}, int, error) {
	// Always read from the start so an after→inject promote of an already
	// skipped event is visible on the next drain. Consumed injects are
	// filtered by injectedIDs rather than offset.
	_ = lastOffset
	events, total, err := s.streamManager.GetSteerEvents(ctx, sessionID, messageID, 0)
	if err != nil {
		return nil, lastOffset, err
	}
	out := make([]map[string]interface{}, 0)
	for _, evt := range events {
		if steerDeliveryOfEvent(evt) == steerDeliveryAfter {
			continue
		}
		if s.hasInjected(evt.ID) || steerEventConsumed(evt) {
			continue
		}
		if len(out) >= steerDrainBatchLimit {
			break
		}
		s.markInjected(evt.ID)
		out = append(out, steerEventToRaw(evt))
	}
	s.mu.Lock()
	if total > s.drainedOffset {
		s.drainedOffset = total
	}
	s.mu.Unlock()
	return out, total, nil
}

func (s *steerSink) hasInjected(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.injectedIDs[id]
	return ok
}

func (s *steerSink) markInjected(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.injectedIDs == nil {
		s.injectedIDs = make(map[string]struct{})
	}
	s.injectedIDs[id] = struct{}{}
}

func (s *steerSink) unmarkInjected(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.injectedIDs, id)
}

// InjectedIDs is a copy of steer event IDs the engine has already consumed.
func (s *steerSink) InjectedIDs() map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]struct{}, len(s.injectedIDs))
	for id := range s.injectedIDs {
		out[id] = struct{}{}
	}
	return out
}

// DrainedOffset is retained for tests. Production teardown uses consumed
// flags plus InjectedIDs, not a numeric offset.
func (s *steerSink) DrainedOffset() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drainedOffset
}

// steerEventToRaw flattens a StreamEvent into the map shape PollSteer returns.
func steerEventToRaw(evt interfaces.StreamEvent) map[string]interface{} {
	raw := map[string]interface{}{
		"id":      evt.ID,
		"content": evt.Content,
	}
	if m, ok := evt.Data["mentioned_items"].([]interface{}); ok {
		raw["mentioned_items"] = m
	}
	if ch, ok := evt.Data["channel"].(string); ok {
		raw["channel"] = ch
	}
	if d, ok := evt.Data["delivery"].(string); ok {
		raw["delivery"] = d
	}
	return raw
}

// PersistSteerMessage stores the accepted steer as a normal user message row
// carrying the run's request ID, so history replay (LoadAgentHistory) places
// it inside this turn and the next turn's LLM context includes it. Satisfies
// types.SteerSink.
//
// Mentions are stored for display and for the next turn only. The running
// turn's tool, KB and skill scope was resolved when it started and is not
// widened mid-flight — @-ing a knowledge base in a steered message does not
// hand the in-flight agent a new retriever.
func (s *steerSink) PersistSteerMessage(
	ctx context.Context, sessionID, messageID, steerID, content string,
	mentionedItems types.MentionedItems,
	channel string,
) string {
	if s.messageService == nil {
		s.unmarkInjected(steerID)
		return ""
	}
	if existing := s.persistedUserMessageID(ctx, sessionID, messageID, steerID); existing != "" {
		s.mu.Lock()
		s.lastUserMessageID = existing
		s.mu.Unlock()
		return existing
	}
	if strings.TrimSpace(channel) == "" {
		channel = "web"
	}
	msg, err := s.messageService.CreateMessage(ctx, &types.Message{
		SessionID:      sessionID,
		Role:           "user",
		Content:        content,
		RequestID:      s.requestID,
		MentionedItems: mentionedItems,
		CreatedAt:      time.Now(),
		IsCompleted:    true,
		Channel:        channel,
	})
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"steer_id":   steerID,
		})
		s.unmarkInjected(steerID)
		return ""
	}
	updated, err := s.streamManager.UpdateSteerEventData(ctx, sessionID, messageID, steerID,
		map[string]interface{}{
			steerDataConsumed:      true,
			steerDataUserMessageID: msg.ID,
		})
	if err != nil {
		logger.Warnf(ctx, "steer consume flag failed for session %s steer %s: %v",
			sessionID, steerID, err)
	}
	if !updated {
		// Deleted concurrently, or the CAS gave up. The user row must not
		// stay around for a retry to insert a second copy of the same steer.
		if delErr := s.messageService.DeleteMessage(ctx, sessionID, msg.ID); delErr != nil {
			logger.Warnf(ctx, "steer persist rollback failed for session %s message %s: %v",
				sessionID, msg.ID, delErr)
		}
		s.unmarkInjected(steerID)
		if existing := s.persistedUserMessageID(ctx, sessionID, messageID, steerID); existing != "" {
			s.mu.Lock()
			s.lastUserMessageID = existing
			s.mu.Unlock()
			return existing
		}
		return ""
	}
	s.mu.Lock()
	s.lastUserMessageID = msg.ID
	s.mu.Unlock()
	return msg.ID
}

func (s *steerSink) persistedUserMessageID(ctx context.Context, sessionID, messageID, steerID string) string {
	if s.streamManager == nil || steerID == "" {
		return ""
	}
	events, _, err := s.streamManager.GetSteerEvents(ctx, sessionID, messageID, 0)
	if err != nil {
		return ""
	}
	for _, evt := range events {
		if evt.ID == steerID {
			return getString(evt.Data, steerDataUserMessageID)
		}
	}
	return ""
}

// LastPersistedUserMessageID exposes the ID of the most recently persisted
// steer row so the injected event can carry it for frontend correlation.
func (s *steerSink) LastPersistedUserMessageID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUserMessageID
}

// liveAgentRun resolves which assistant message is currently generating for a
// session, or "" when none is. The marker lives in the shared StreamManager
// rather than in process memory, mirroring how stop events are coordinated:
// a mid-run send that lands on a different replica must find the same run,
// otherwise it would report "no run is live" and the client would start a
// second turn on top of the first.
//
// Because the marker can outlive its process (a TTL'd key, not a map that
// dies with the goroutine), it is verified against the assistant row before
// use. A completed message means the run is over and the caller should start
// a new turn rather than queue into a list nobody will drain.
//
// Lookup failures are returned as errors, not as "". Collapsing a Redis or
// database blip into "no live run" is exactly the duplicate-turn path above:
// SteerMessage would answer new_run and the client would POST a second AgentQA
// while the first is still generating.
func (h *Handler) liveAgentRun(ctx context.Context, sessionID string) (string, error) {
	assistantID, _, err := h.streamManager.GetLiveRun(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if assistantID == "" {
		return "", nil
	}

	msg, err := h.messageService.GetMessage(ctx, sessionID, assistantID)
	if err != nil {
		return "", err
	}
	if msg == nil || msg.IsCompleted {
		if err := h.streamManager.ClearLiveRun(ctx, sessionID, assistantID); err != nil {
			logger.Warnf(ctx, "stale live run cleanup failed for session %s: %v", sessionID, err)
		}
		return "", nil
	}
	return assistantID, nil
}

// resolveLiveAgentRun is the HTTP wrapper around liveAgentRun: a lookup
// failure becomes a retryable 503 so the client toasts instead of starting
// a second turn. ok is false when the handler has already written the error.
func (h *Handler) resolveLiveAgentRun(ctx context.Context, c *gin.Context, sessionID string) (string, bool) {
	assistantID, err := h.liveAgentRun(ctx, sessionID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewServiceUnavailableError("Failed to look up running turn"))
		return "", false
	}
	return assistantID, true
}

// steerEvent is one queued user message as stored in the StreamManager
// sub-list. Data keys mirror what the injected user_message_injected event
// carries so consumers can correlate queue → injection.
func steerEvent(id, query string, mentionedItems types.MentionedItems, channel string) interfaces.StreamEvent {
	return interfaces.StreamEvent{
		ID:      id,
		Type:    types.ResponseTypeSteer,
		Content: query,
		Done:    true,
		Data: map[string]interface{}{
			"steer_id":        id,
			"channel":         channel,
			"delivery":        steerDeliveryInject,
			"mentioned_items": mentionedItemsToRaw(mentionedItems),
		},
	}
}

// parseSteerDelivery defaults to "after". Queueing until the current turn
// finishes is the conservative reading of "the user sent another message":
// interrupting a running agent is the explicit opt-in, so a client that omits
// the field never lands mid-turn by accident.
func parseSteerDelivery(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", steerDeliveryAfter:
		return steerDeliveryAfter, nil
	case steerDeliveryInject:
		return steerDeliveryInject, nil
	default:
		return "", fmt.Errorf("invalid delivery %q (want inject or after)", s)
	}
}

func steerDeliveryOfEvent(evt interfaces.StreamEvent) string {
	if getString(evt.Data, "delivery") == steerDeliveryAfter {
		return steerDeliveryAfter
	}
	return steerDeliveryInject
}

// steerEventConsumed reports whether the running turn already took this event
// into its message list.
func steerEventConsumed(evt interfaces.StreamEvent) bool {
	consumed, _ := evt.Data[steerDataConsumed].(bool)
	return consumed
}

// selectSteerBacklog is the teardown drain: every event the engine has not
// yet consumed as an inject. After events (never consumed) and inject events
// that arrived too late both belong here; consumed events do not.
//
// injectedIDs is the in-flight run's own view, which can be a beat ahead of
// the durable flag; both are checked so a message is never injected twice.
func selectSteerBacklog(events []interfaces.StreamEvent, injectedIDs map[string]struct{}) []interfaces.StreamEvent {
	out := make([]interfaces.StreamEvent, 0)
	for _, evt := range events {
		if steerEventConsumed(evt) {
			continue
		}
		if _, ok := injectedIDs[evt.ID]; ok {
			continue
		}
		out = append(out, evt)
	}
	return out
}

// pendingSteerQueueItems is the overlay restore payload: every steer event
// the live run has not yet consumed as an inject. After events always appear;
// inject events drop out once PollSteer marked them.
func pendingSteerQueueItems(events []interfaces.StreamEvent, injectedIDs map[string]struct{}) []map[string]interface{} {
	pending := selectSteerBacklog(events, injectedIDs)
	out := make([]map[string]interface{}, 0, len(pending))
	for _, evt := range pending {
		item := map[string]interface{}{
			"steer_id": evt.ID,
			"content":  evt.Content,
			"delivery": steerDeliveryOfEvent(evt),
		}
		if m, ok := evt.Data["mentioned_items"]; ok && m != nil {
			item["mentioned_items"] = m
		}
		out = append(out, item)
	}
	return out
}

// mentionedItemsToRaw converts typed mentions into plain values that survive
// Redis JSON round-trips without a second unmarshal type on the read side.
func mentionedItemsToRaw(items types.MentionedItems) []interface{} {
	return types.MentionedItemsToRaw(items)
}

// SteerMessage godoc
// @Summary      向运行中的对话追加消息
// @Description  向运行中的 agent turn 追加用户消息（after 排队 / inject 注入）。无活 turn 时返回 new_run。
// @Tags         问答
// @Accept       json
// @Produce      json
// @Param        session_id  path  string  true  "会话 ID"
// @Param        request     body  SteerMessageRequest  true  "追加消息"
// @Success      200  {object}  map[string]interface{}  "queued | new_run"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "会话不存在"
// @Failure      503  {object}  errors.AppError         "活 turn 查询失败，可重试"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/steer [post]
func (h *Handler) SteerMessage(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	if sessionID == "" {
		_ = c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	var req SteerMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		_ = c.Error(errors.NewBadRequestError("query must not be empty"))
		return
	}
	if len([]rune(query)) > maxSteerQueryLength {
		_ = c.Error(errors.NewBadRequestError("query too long"))
		return
	}
	delivery, err := parseSteerDelivery(req.Delivery)
	if err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Same ownership scope as StopSession: steer mutates an in-flight turn, so
	// use the strict owner scope and reject cross-tenant access.
	if _, err := h.sessionService.GetOwnedSession(ctx, sessionID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewNotFoundError("Session not found"))
		return
	}

	assistantID, ok := h.resolveLiveAgentRun(ctx, c, sessionID)
	if !ok {
		return
	}
	if assistantID == "" {
		// No run is live: the message starts a brand-new run via the normal
		// AgentQA path. The request returns immediately and the SSE stream
		// for the new turn is opened by the client's next agent-chat call.
		c.JSON(200, gin.H{"success": true, "status": "new_run"})
		return
	}

	// Queue depth guard: refuse rather than silently accumulate. Only pending
	// messages count — a turn that already absorbed ten injects still has an
	// empty overlay, so charging the user for them would refuse a send with
	// nothing on screen to explain it.
	existing, _, err := h.streamManager.GetSteerEvents(ctx, sessionID, assistantID, 0)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewInternalServerError("Failed to check steer queue"))
		return
	}
	pending := len(selectSteerBacklog(existing, nil))
	if pending >= maxSteerQueueDepth {
		_ = c.Error(errors.NewBadRequestError("too many queued messages for the running turn"))
		return
	}

	steerID := uuid.New().String()
	evt := steerEvent(steerID, query, convertMentionedItems(req.MentionedItems), req.Channel)
	evt.Data["delivery"] = delivery
	if err := h.streamManager.AppendSteerEvents(ctx, sessionID, assistantID,
		[]interfaces.StreamEvent{evt}); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewInternalServerError("Failed to queue message"))
		return
	}

	queuedOn, status, err := h.rebindSteerIfLiveRunMoved(ctx, sessionID, assistantID, evt)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewInternalServerError("Failed to queue message"))
		return
	}
	if status == "new_run" {
		c.JSON(200, gin.H{"success": true, "status": "new_run"})
		return
	}

	logger.Infof(ctx, "Steer message queued for session=%s run=%s steer_id=%s delivery=%s queue_len=%d",
		sessionID, queuedOn, steerID, delivery, pending+1)

	c.JSON(200, gin.H{
		"success":              true,
		"status":               "queued",
		"steer_id":             steerID,
		"delivery":             delivery,
		"assistant_message_id": queuedOn,
	})
}

// PromoteSteerMessage godoc
// @Summary      将排队消息改为立即注入
// @Description  把一条 delivery=after 的排队消息改为 inject，运行中的 agent 会在下一轮边界读到它。
// @Tags         问答
// @Produce      json
// @Param        session_id  path  string  true  "会话 ID"
// @Param        steer_id    path  string  true  "排队消息 ID"
// @Success      200  {object}  map[string]interface{}  "queued | new_run"
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      503  {object}  errors.AppError         "活 turn 查询失败，可重试"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/steer/{steer_id}/inject [post]
func (h *Handler) PromoteSteerMessage(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	steerID := c.Param("steer_id")
	if sessionID == "" || steerID == "" {
		_ = c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	if _, err := h.sessionService.GetOwnedSession(ctx, sessionID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewNotFoundError("Session not found"))
		return
	}

	assistantID, ok := h.resolveLiveAgentRun(ctx, c, sessionID)
	if !ok {
		return
	}
	if assistantID == "" {
		c.JSON(200, gin.H{"success": true, "status": "new_run"})
		return
	}

	events, _, err := h.streamManager.GetSteerEvents(ctx, sessionID, assistantID, 0)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewInternalServerError("Failed to update queued message"))
		return
	}
	for _, evt := range events {
		if evt.ID == steerID && steerEventConsumed(evt) {
			c.JSON(200, gin.H{
				"success":  true,
				"status":   "already_injected",
				"steer_id": steerID,
			})
			return
		}
	}

	updated, err := h.streamManager.UpdateSteerEventData(ctx, sessionID, assistantID, steerID,
		map[string]interface{}{"delivery": steerDeliveryInject})
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"steer_id":   steerID,
		})
		_ = c.Error(errors.NewInternalServerError("Failed to update queued message"))
		return
	}
	if !updated {
		_ = c.Error(errors.NewNotFoundError("Queued message not found"))
		return
	}

	logger.Infof(ctx, "Steer message promoted to inject session=%s run=%s steer_id=%s",
		sessionID, assistantID, steerID)
	c.JSON(200, gin.H{
		"success":              true,
		"status":               "queued",
		"steer_id":             steerID,
		"delivery":             steerDeliveryInject,
		"assistant_message_id": assistantID,
	})
}

// ListSteerMessages godoc
// @Summary      列出当前运行中尚未消费的排队消息
// @Description  刷新页面后用来恢复输入框上方的队列。没有正在运行的 turn 时返回空列表。
// @Tags         问答
// @Produce      json
// @Param        id  path  string  true  "会话 ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  errors.AppError
// @Failure      503  {object}  errors.AppError         "活 turn 查询失败，可重试"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id}/steer [get]
func (h *Handler) ListSteerMessages(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	sessionID := secutils.SanitizeForLog(c.Param("id"))
	if sessionID == "" {
		sessionID = secutils.SanitizeForLog(c.Param("session_id"))
	}
	if sessionID == "" {
		_ = c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	if _, err := h.sessionService.GetOwnedSession(ctx, sessionID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewNotFoundError("Session not found"))
		return
	}

	assistantID, ok := h.resolveLiveAgentRun(ctx, c, sessionID)
	if !ok {
		return
	}
	if assistantID == "" {
		c.JSON(200, gin.H{"success": true, "items": []map[string]interface{}{}})
		return
	}

	events, _, err := h.streamManager.GetSteerEvents(ctx, sessionID, assistantID, 0)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewInternalServerError("Failed to load queued messages"))
		return
	}

	c.JSON(200, gin.H{
		"success":              true,
		"assistant_message_id": assistantID,
		"items":                pendingSteerQueueItems(events, nil),
	})
}

// DeleteSteerMessage godoc
// @Summary      删除一条排队中的消息
// @Description  从当前运行的排队列表里去掉一条，不再注入也不再作为 follow-up 发出。
// @Tags         问答
// @Produce      json
// @Param        id        path  string  true  "会话 ID"
// @Param        steer_id  path  string  true  "排队消息 ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  errors.AppError
// @Failure      503  {object}  errors.AppError         "活 turn 查询失败，可重试"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id}/steer/{steer_id} [delete]
func (h *Handler) DeleteSteerMessage(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	sessionID := secutils.SanitizeForLog(c.Param("id"))
	if sessionID == "" {
		sessionID = secutils.SanitizeForLog(c.Param("session_id"))
	}
	steerID := c.Param("steer_id")
	if sessionID == "" || steerID == "" {
		_ = c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	if _, err := h.sessionService.GetOwnedSession(ctx, sessionID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewNotFoundError("Session not found"))
		return
	}

	assistantID, ok := h.resolveLiveAgentRun(ctx, c, sessionID)
	if !ok {
		return
	}
	if assistantID == "" {
		c.JSON(200, gin.H{"success": true, "status": "gone"})
		return
	}

	// A message the engine already took cannot be unsent — it is in the
	// model's context. Say so instead of reporting a delete that did not
	// change what the agent saw.
	events, _, err := h.streamManager.GetSteerEvents(ctx, sessionID, assistantID, 0)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewInternalServerError("Failed to delete queued message"))
		return
	}
	for _, evt := range events {
		if evt.ID == steerID && steerEventConsumed(evt) {
			c.JSON(200, gin.H{
				"success":  true,
				"status":   "already_injected",
				"removed":  false,
				"steer_id": steerID,
			})
			return
		}
	}

	ok, err = h.streamManager.DeleteSteerEvent(ctx, sessionID, assistantID, steerID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"steer_id":   steerID,
		})
		_ = c.Error(errors.NewInternalServerError("Failed to delete queued message"))
		return
	}

	c.JSON(200, gin.H{
		"success":  true,
		"status":   "deleted",
		"removed":  ok,
		"steer_id": steerID,
	})
}

// discardSteerBacklog marks every still-pending steer message consumed
// without running any of it. This is the stop path: the user asked the agent
// to stop, and quietly starting a fresh run with the text they had queued is
// the opposite of stopping. Marking (rather than deleting) keeps the events
// around for the run's own audit trail while taking them out of the overlay,
// the depth budget and any follow-up handoff.
func (h *Handler) discardSteerBacklog(
	ctx context.Context,
	sessionID, assistantMessageID string,
	injected map[string]struct{},
) {
	all, _, err := h.streamManager.GetSteerEvents(ctx, sessionID, assistantMessageID, 0)
	if err != nil {
		logger.Warnf(ctx, "steer backlog read failed for session %s: %v", sessionID, err)
		return
	}
	backlog := selectSteerBacklog(all, injected)
	if len(backlog) == 0 {
		return
	}
	h.markSteerEventsConsumed(ctx, sessionID, assistantMessageID, backlog)
	if lateAll, _, err := h.streamManager.GetSteerEvents(ctx, sessionID, assistantMessageID, 0); err != nil {
		logger.Warnf(ctx, "steer discard sweep failed for session %s: %v", sessionID, err)
	} else if late := selectSteerBacklog(lateAll, injected); len(late) > 0 {
		h.markSteerEventsConsumed(ctx, sessionID, assistantMessageID, late)
		backlog = append(backlog, late...)
	}
	logger.Infof(ctx, "Discarded %d queued steer message(s) after stop, session=%s",
		len(backlog), sessionID)
}

// kickNextRunFromSteerBacklog drains steer messages that arrived after the
// loop could no longer inject them (final answer already streaming, max
// iterations reached, or a user stop) and starts a follow-up run with the
// first message as its query. Remaining messages stay queued on the NEW run's
// steer sub-list, so the follow-up run injects them at its own round
// boundaries.
//
// The follow-up is published as the session's live run before this function
// returns, so the finished run's subsequent ClearLiveRun cannot open a
// window where POST /steer answers new_run. executeQA is started afterwards
// and skips message creation that claimNextSteerFollowUp already did.
//
// Runs on the executeQA teardown path with a WithoutCancel context, so a
// user-initiated stop doesn't cancel the follow-up run.
func (h *Handler) kickNextRunFromSteerBacklog(
	ctx context.Context,
	prevReqCtx *qaRequestContext,
	prevStreamCtx *sseStreamContext,
) bool {
	followUp, ok := h.claimNextSteerFollowUp(ctx, prevReqCtx, prevStreamCtx)
	if !ok {
		followUp, ok = h.claimNextSteerFollowUp(ctx, prevReqCtx, prevStreamCtx)
	}
	if !ok {
		return false
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(ctx, "steer follow-up run panicked: %v", r)
			}
		}()
		h.executeQA(followUp, qaModeAgent, false)
	}()
	return true
}

// claimNextSteerFollowUp persists the follow-up turn and SetLiveRun's it so
// the session is never unmarked between the previous run exiting and the
// next engine loop starting. Returns false when there is nothing to hand off.
func (h *Handler) claimNextSteerFollowUp(
	ctx context.Context,
	prevReqCtx *qaRequestContext,
	prevStreamCtx *sseStreamContext,
) (*qaRequestContext, bool) {
	prevMessageID := prevStreamCtx.assistantMessage.ID

	all, _, err := h.streamManager.GetSteerEvents(ctx, prevReqCtx.sessionID, prevMessageID, 0)
	if err != nil {
		logger.Warnf(ctx, "steer backlog read failed for session %s: %v", prevReqCtx.sessionID, err)
		return nil, false
	}
	injected := map[string]struct{}{}
	if prevStreamCtx.steerSink != nil {
		injected = prevStreamCtx.steerSink.InjectedIDs()
	}
	backlog := selectSteerBacklog(all, injected)
	if len(backlog) == 0 {
		if lateAll, _, lateErr := h.streamManager.GetSteerEvents(
			ctx, prevReqCtx.sessionID, prevMessageID, 0,
		); lateErr == nil {
			backlog = selectSteerBacklog(lateAll, injected)
		}
		if len(backlog) == 0 {
			return nil, false
		}
	}

	first := backlog[0]
	rest := backlog[1:]

	logger.Infof(ctx, "Steer backlog detected after run completion, session=%s, count=%d, injected=%d",
		prevReqCtx.sessionID, len(backlog), len(injected))

	followUp := *prevReqCtx
	followUp.ctx = ctx
	followUp.query = first.Content
	followUp.requestID = uuid.New().String()
	followUp.channel = getString(first.Data, "channel")
	if followUp.channel == "" {
		followUp.channel = "web"
	}
	h.applyFollowUpMentions(ctx, &followUp, first.Data["mentioned_items"])
	followUp.assistantMessage = &types.Message{
		SessionID:   prevReqCtx.sessionID,
		Role:        "assistant",
		IsCompleted: false,
		RequestID:   followUp.requestID,
		CreatedAt:   time.Now(),
	}
	followUp.suggestionAttribution = nil
	followUp.userMessageID = ""
	followUp.steerSink = nil
	followUp.images = nil
	followUp.attachments = nil
	followUp.attachmentIDs = nil
	followUp.attachmentMetas = nil
	followUp.skipSSE = true
	followUp.steerCarryOver = rest

	if err := h.persistTurnMessages(ctx, &followUp); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": prevReqCtx.sessionID,
		})
		h.rollbackTurnMessages(ctx, &followUp, true, true)
		return nil, false
	}
	if followUp.assistantMessage == nil || followUp.assistantMessage.ID == "" {
		h.rollbackTurnMessages(ctx, &followUp, true, true)
		return nil, false
	}
	if err := h.streamManager.ClaimLiveRun(
		ctx, followUp.sessionID, followUp.assistantMessage.ID, followUp.requestID,
	); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": followUp.sessionID,
		})
		h.rollbackTurnMessages(ctx, &followUp, true, true)
		return nil, false
	}

	h.markSteerEventsConsumed(ctx, prevReqCtx.sessionID, prevMessageID, backlog)

	// A send that landed on A while we persisted still sits on A's list.
	// Sweep it onto B now that B is the live run.
	if lateAll, _, err := h.streamManager.GetSteerEvents(ctx, prevReqCtx.sessionID, prevMessageID, 0); err != nil {
		logger.Warnf(ctx, "steer late-handoff read failed for session %s: %v", prevReqCtx.sessionID, err)
	} else if late := selectSteerBacklog(lateAll, injected); len(late) > 0 {
		h.markSteerEventsConsumed(ctx, prevReqCtx.sessionID, prevMessageID, late)
		followUp.steerCarryOver = append(followUp.steerCarryOver, late...)
	}

	if len(followUp.steerCarryOver) > 0 {
		if err := h.streamManager.AppendSteerEvents(
			ctx, followUp.sessionID, followUp.assistantMessage.ID, followUp.steerCarryOver,
		); err != nil {
			logger.Warnf(ctx, "steer carry-over append failed for session %s: %v", followUp.sessionID, err)
		} else {
			followUp.steerCarryOver = nil
		}
	}
	return &followUp, true
}

func (h *Handler) markSteerEventsConsumed(
	ctx context.Context, sessionID, assistantID string, events []interfaces.StreamEvent,
) {
	for _, evt := range events {
		if _, err := h.streamManager.UpdateSteerEventData(ctx, sessionID, assistantID, evt.ID,
			map[string]interface{}{steerDataConsumed: true}); err != nil {
			logger.Warnf(ctx, "steer consume flag failed for session %s steer %s: %v",
				sessionID, evt.ID, err)
		}
	}
}

// rebindSteerIfLiveRunMoved moves an event that landed on a run that has
// already handed off. Lookup-then-append is not atomic with SetLiveRun.
func (h *Handler) rebindSteerIfLiveRunMoved(
	ctx context.Context, sessionID, appendedOn string, evt interfaces.StreamEvent,
) (string, string, error) {
	current, err := h.liveAgentRun(ctx, sessionID)
	if err != nil {
		return appendedOn, "queued", err
	}
	if current == appendedOn {
		return appendedOn, "queued", nil
	}
	if current == "" {
		// Leave the event on the finished run. Deleting it here would drop
		// the user's text when the client cannot (and must not) abort a
		// still-open SSE to start a new AgentQA.
		return "", "new_run", nil
	}
	if err := h.streamManager.AppendSteerEvents(ctx, sessionID, current, []interfaces.StreamEvent{evt}); err != nil {
		return "", "", err
	}
	_, _ = h.streamManager.DeleteSteerEvent(ctx, sessionID, appendedOn, evt.ID)
	return current, "queued", nil
}

func mentionedItemsToRequests(items types.MentionedItems) []MentionedItemRequest {
	out := make([]MentionedItemRequest, 0, len(items))
	for _, item := range items {
		out = append(out, MentionedItemRequest{
			ID:        item.ID,
			Name:      item.Name,
			Type:      item.Type,
			KBType:    item.KBType,
			KBID:      item.KBID,
			KBName:    item.KBName,
			ServiceID: item.ServiceID,
			SkillName: item.SkillName,
		})
	}
	return out
}

func (h *Handler) applyFollowUpMentions(ctx context.Context, followUp *qaRequestContext, raw interface{}) {
	snapshot := followUpMentionSnapshot{
		mentionedItems:   followUp.mentionedItems,
		knowledgeBaseIDs: append([]string(nil), followUp.knowledgeBaseIDs...),
		knowledgeIDs:     append([]string(nil), followUp.knowledgeIDs...),
		mcpServiceIDs:    append([]string(nil), followUp.mcpServiceIDs...),
		skillNames:       append([]string(nil), followUp.skillNames...),
		tagIDs:           append([]string(nil), followUp.tagIDs...),
		tagScopes:        append([]types.TagScope(nil), followUp.tagScopes...),
	}
	items := rawToMentionedItems(raw)
	followUp.mentionedItems = items
	reqs := mentionedItemsToRequests(items)
	kbs, files := mergeKnowledgeTargets(followUp.knowledgeBaseIDs, followUp.knowledgeIDs, reqs)
	followUp.knowledgeBaseIDs = kbs
	followUp.knowledgeIDs = files
	followUp.mcpServiceIDs = dedupRequestStrings(append(followUp.mcpServiceIDs, mentionedIDsByType(reqs, "mcp")...))
	followUp.skillNames = dedupRequestStrings(append(followUp.skillNames, mentionedIDsByType(reqs, "skill")...))
	followUp.tagIDs = dedupRequestStrings(append(followUp.tagIDs, mentionedIDsByType(reqs, "tag")...))
	followUp.tagScopes = mergeTagScopesFromRequestIDs(
		tagScopesFromMentionedItems(reqs), followUp.tagIDs, followUp.knowledgeBaseIDs)

	if err := types.AuthorizeTenantAPIKeyKnowledgeTargets(
		ctx, followUp.knowledgeBaseIDs, followUp.knowledgeIDs,
	); err != nil {
		logger.Warnf(ctx, "dropping follow-up mentions outside API key KB scope: %v", err)
		restoreFollowUpMentions(followUp, snapshot)
		return
	}
	if err := validateUnscopedTagIDs(
		orphanTagIDsForScope(followUp.tagIDs, tagScopesFromMentionedItems(reqs)),
		followUp.knowledgeBaseIDs,
	); err != nil {
		logger.Warnf(ctx, "dropping follow-up mentions with unscoped tags: %v", err)
		restoreFollowUpMentions(followUp, snapshot)
	}
}

type followUpMentionSnapshot struct {
	mentionedItems   types.MentionedItems
	knowledgeBaseIDs []string
	knowledgeIDs     []string
	mcpServiceIDs    []string
	skillNames       []string
	tagIDs           []string
	tagScopes        []types.TagScope
}

func restoreFollowUpMentions(followUp *qaRequestContext, snapshot followUpMentionSnapshot) {
	followUp.mentionedItems = snapshot.mentionedItems
	followUp.knowledgeBaseIDs = snapshot.knowledgeBaseIDs
	followUp.knowledgeIDs = snapshot.knowledgeIDs
	followUp.mcpServiceIDs = snapshot.mcpServiceIDs
	followUp.skillNames = snapshot.skillNames
	followUp.tagIDs = snapshot.tagIDs
	followUp.tagScopes = snapshot.tagScopes
}

// rawToMentionedItems rebuilds typed mentions from the JSON-safe raw shape
// stored in steer events (inverse of mentionedItemsToRaw).
func rawToMentionedItems(raw interface{}) types.MentionedItems {
	return types.MentionedItemsFromRaw(raw)
}
