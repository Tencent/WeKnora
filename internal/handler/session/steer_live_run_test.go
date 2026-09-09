package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Redis/DB blip on the live-run lookup used to collapse into "no run is
// live". SteerMessage then answered status=new_run, and the client started a
// second AgentQA on top of the still-generating one. These tests pin the
// contract: lookup failure is retryable (503); only a genuine empty or
// completed marker may say new_run.

type steerOwnedSessionStub struct {
	interfaces.SessionService
}

func (s *steerOwnedSessionStub) GetOwnedSession(_ context.Context, id string) (*types.Session, error) {
	return &types.Session{ID: id}, nil
}

type steerMessageLookupStub struct {
	interfaces.MessageService
	msg *types.Message
	err error
}

func (s *steerMessageLookupStub) GetMessage(_ context.Context, _, _ string) (*types.Message, error) {
	return s.msg, s.err
}

type steerLiveRunLookupStub struct {
	interfaces.StreamManager
	assistantID string
	err         error
	cleared     string
}

func (s *steerLiveRunLookupStub) GetLiveRun(context.Context, string) (string, string, error) {
	return s.assistantID, "req-1", s.err
}

func (s *steerLiveRunLookupStub) ClearLiveRun(_ context.Context, _, assistantID string) error {
	s.cleared = assistantID
	return nil
}

func newSteerLiveRunRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/sessions/:session_id/steer", h.SteerMessage)
	r.POST("/sessions/:session_id/steer/:steer_id/inject", h.PromoteSteerMessage)
	r.GET("/sessions/:id/steer", h.ListSteerMessages)
	r.DELETE("/sessions/:id/steer/:steer_id", h.DeleteSteerMessage)
	return r
}

func postSteer(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess-1/steer", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSteerMessageLiveRunLookupFailureReturns503(t *testing.T) {
	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: &steerMessageLookupStub{},
		streamManager:  &steerLiveRunLookupStub{err: errors.New("redis timeout")},
	}
	w := postSteer(t, newSteerLiveRunRouter(h), `{"query":"nudge the agent"}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), `"new_run"`)
}

func TestSteerMessageMessageLookupFailureReturns503(t *testing.T) {
	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: &steerMessageLookupStub{err: errors.New("db timeout")},
		streamManager:  &steerLiveRunLookupStub{assistantID: "assist-1"},
	}
	w := postSteer(t, newSteerLiveRunRouter(h), `{"query":"nudge the agent"}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), `"new_run"`)
}

func TestSteerMessageNoLiveRunStillReturnsNewRun(t *testing.T) {
	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: &steerMessageLookupStub{},
		streamManager:  &steerLiveRunLookupStub{},
	}
	w := postSteer(t, newSteerLiveRunRouter(h), `{"query":"hello"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "new_run", body["status"])
}

func TestSteerMessageCompletedLiveRunStillReturnsNewRun(t *testing.T) {
	streams := &steerLiveRunLookupStub{assistantID: "assist-1"}
	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: &steerMessageLookupStub{
			msg: &types.Message{ID: "assist-1", IsCompleted: true},
		},
		streamManager: streams,
	}
	w := postSteer(t, newSteerLiveRunRouter(h), `{"query":"hello"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "new_run", body["status"])
	assert.Equal(t, "assist-1", streams.cleared)
}

func TestSteerMessageQueuesWhenLiveRunIsVerified(t *testing.T) {
	mgr := stream.NewMemoryStreamManager()
	require.NoError(t, mgr.SetLiveRun(t.Context(), "sess-1", "assist-1", "req-1"))
	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: &steerMessageLookupStub{
			msg: &types.Message{ID: "assist-1", SessionID: "sess-1", IsCompleted: false},
		},
		streamManager: mgr,
	}
	w := postSteer(t, newSteerLiveRunRouter(h), `{"query":"keep going","delivery":"after"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "queued", body["status"])
	assert.Equal(t, "assist-1", body["assistant_message_id"])
}

func TestPromoteAndListLiveRunLookupFailureReturns503(t *testing.T) {
	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: &steerMessageLookupStub{},
		streamManager:  &steerLiveRunLookupStub{err: errors.New("redis timeout")},
	}
	r := newSteerLiveRunRouter(h)

	promote := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess-1/steer/s1/inject", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(promote, req)
	require.Equal(t, http.StatusServiceUnavailable, promote.Code, promote.Body.String())
	assert.NotContains(t, promote.Body.String(), `"new_run"`)

	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/sessions/sess-1/steer", nil))
	require.Equal(t, http.StatusServiceUnavailable, list.Code, list.Body.String())
	assert.NotContains(t, list.Body.String(), `"items"`)

	del := httptest.NewRecorder()
	r.ServeHTTP(del, httptest.NewRequest(http.MethodDelete, "/sessions/sess-1/steer/s1", nil))
	require.Equal(t, http.StatusServiceUnavailable, del.Code, del.Body.String())
	assert.NotContains(t, del.Body.String(), `"gone"`)
}

// steerPersistingMessageStub assigns IDs on create so a follow-up can claim
// the live-run marker without a database.
type steerPersistingMessageStub struct {
	interfaces.MessageService
	n    int
	byID map[string]*types.Message
}

func (s *steerPersistingMessageStub) CreateMessage(_ context.Context, msg *types.Message) (*types.Message, error) {
	s.n++
	out := *msg
	if out.ID == "" {
		out.ID = fmt.Sprintf("msg-%d", s.n)
	}
	if s.byID == nil {
		s.byID = map[string]*types.Message{}
	}
	stored := out
	s.byID[out.ID] = &stored
	return &out, nil
}

func (s *steerPersistingMessageStub) DeleteMessage(_ context.Context, _, id string) error {
	if s.byID != nil {
		delete(s.byID, id)
	}
	return nil
}

func (s *steerPersistingMessageStub) GetMessage(_ context.Context, _, id string) (*types.Message, error) {
	if s.byID == nil {
		return nil, errors.New("not found")
	}
	msg, ok := s.byID[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cloned := *msg
	return &cloned, nil
}

// The previous run's ClearLiveRun used to run before the follow-up had an
// assistant row or a live marker. POST /steer then answered new_run and the
// client started a second AgentQA alongside kick's executeQA. Claiming the
// session (persist + SetLiveRun) must finish before that Clear, and the CAS
// Clear of the old id must leave the new marker in place.
func TestSteerFollowUpHandoffKeepsSessionLiveAcrossPreviousClear(t *testing.T) {
	ctx := t.Context()
	mgr := stream.NewMemoryStreamManager()
	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-A", "req-A"))
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess-1", "assist-A", []interfaces.StreamEvent{
		steerEventWithDelivery("after-1", "do this next", steerDeliveryAfter),
		steerEventWithDelivery("after-2", "then this", steerDeliveryAfter),
	}))

	msgs := &steerPersistingMessageStub{}
	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: msgs,
		streamManager:  mgr,
	}
	prev := &qaRequestContext{
		sessionID: "sess-1",
		session:   &types.Session{ID: "sess-1"},
	}
	streamCtx := &sseStreamContext{assistantMessage: &types.Message{ID: "assist-A"}}

	followUp, ok := h.claimNextSteerFollowUp(ctx, prev, streamCtx)
	require.True(t, ok)
	require.NotNil(t, followUp)
	assert.Equal(t, "do this next", followUp.query)
	require.NotEmpty(t, followUp.assistantMessage.ID)
	assert.NotEqual(t, "assist-A", followUp.assistantMessage.ID)
	assert.Empty(t, followUp.steerCarryOver, "carry-over must already sit on the new run's list")

	liveID, liveReq, err := mgr.GetLiveRun(ctx, "sess-1")
	require.NoError(t, err)
	assert.Equal(t, followUp.assistantMessage.ID, liveID)
	assert.Equal(t, followUp.requestID, liveReq)

	require.NoError(t, mgr.ClearLiveRun(ctx, "sess-1", "assist-A"))
	liveID, _, err = mgr.GetLiveRun(ctx, "sess-1")
	require.NoError(t, err)
	assert.Equal(t, followUp.assistantMessage.ID, liveID,
		"ClearLiveRun of the finished run must not drop the follow-up marker")

	events, _, err := mgr.GetSteerEvents(ctx, "sess-1", followUp.assistantMessage.ID, 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "after-2", events[0].ID)

	old, _, err := mgr.GetSteerEvents(ctx, "sess-1", "assist-A", 0)
	require.NoError(t, err)
	require.Len(t, old, 2)
	assert.True(t, steerEventConsumed(old[0]))
	assert.True(t, steerEventConsumed(old[1]))

	w := postSteer(t, newSteerLiveRunRouter(h), `{"query":"and this too","delivery":"after"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "queued", body["status"],
		"a send during handoff must queue on the follow-up, not start a third run")
	assert.Equal(t, followUp.assistantMessage.ID, body["assistant_message_id"])
}

func TestClaimNextSteerFollowUpNoBacklogLeavesMarkerUntouched(t *testing.T) {
	ctx := t.Context()
	mgr := stream.NewMemoryStreamManager()
	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-A", "req-A"))

	h := &Handler{streamManager: mgr}
	followUp, ok := h.claimNextSteerFollowUp(ctx, &qaRequestContext{sessionID: "sess-1"},
		&sseStreamContext{assistantMessage: &types.Message{ID: "assist-A"}})
	assert.False(t, ok)
	assert.Nil(t, followUp)

	liveID, _, err := mgr.GetLiveRun(ctx, "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "assist-A", liveID)
}

func TestClaimNextSteerFollowUpPersistFailureLeavesBacklog(t *testing.T) {
	ctx := t.Context()
	mgr := stream.NewMemoryStreamManager()
	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-A", "req-A"))
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess-1", "assist-A", []interfaces.StreamEvent{
		steerEventWithDelivery("after-1", "do this next", steerDeliveryAfter),
	}))

	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: &steerFailingCreateStub{},
		streamManager:  mgr,
	}
	followUp, ok := h.claimNextSteerFollowUp(ctx, &qaRequestContext{sessionID: "sess-1"},
		&sseStreamContext{assistantMessage: &types.Message{ID: "assist-A"}})
	assert.False(t, ok)
	assert.Nil(t, followUp)

	old, _, err := mgr.GetSteerEvents(ctx, "sess-1", "assist-A", 0)
	require.NoError(t, err)
	require.Len(t, old, 1)
	assert.False(t, steerEventConsumed(old[0]))
}

func TestRebindSteerMovesEventOntoNewLiveRun(t *testing.T) {
	ctx := t.Context()
	mgr := stream.NewMemoryStreamManager()
	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-A", "req-A"))
	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: &steerMessageLookupStub{
			msg: &types.Message{ID: "assist-B", SessionID: "sess-1", IsCompleted: false},
		},
		streamManager: mgr,
	}
	evt := steerEventWithDelivery("late-1", "landed on A", steerDeliveryAfter)
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess-1", "assist-A", []interfaces.StreamEvent{evt}))
	require.NoError(t, mgr.ClaimLiveRun(ctx, "sess-1", "assist-B", "req-B"))

	queuedOn, status, err := h.rebindSteerIfLiveRunMoved(ctx, "sess-1", "assist-A", evt)
	require.NoError(t, err)
	assert.Equal(t, "queued", status)
	assert.Equal(t, "assist-B", queuedOn)

	old, _, err := mgr.GetSteerEvents(ctx, "sess-1", "assist-A", 0)
	require.NoError(t, err)
	assert.Empty(t, old)
	moved, _, err := mgr.GetSteerEvents(ctx, "sess-1", "assist-B", 0)
	require.NoError(t, err)
	require.Len(t, moved, 1)
	assert.Equal(t, "late-1", moved[0].ID)
}

func TestApplyFollowUpMentionsMergesKBAndMCP(t *testing.T) {
	h := &Handler{}
	followUp := &qaRequestContext{
		knowledgeBaseIDs: []string{"kb-old"},
		mcpServiceIDs:    []string{"mcp-old"},
	}
	h.applyFollowUpMentions(t.Context(), followUp, []interface{}{
		map[string]interface{}{"id": "kb-new", "type": "kb", "name": "New KB"},
		map[string]interface{}{"id": "mcp-new", "type": "mcp", "name": "MCP"},
	})
	assert.Equal(t, []string{"kb-old", "kb-new"}, followUp.knowledgeBaseIDs)
	assert.Equal(t, []string{"mcp-old", "mcp-new"}, followUp.mcpServiceIDs)
}

type steerFailingCreateStub struct {
	interfaces.MessageService
}

func (s *steerFailingCreateStub) CreateMessage(context.Context, *types.Message) (*types.Message, error) {
	return nil, errors.New("db down")
}

func TestPersistTurnMessagesSkipsWhenAlreadyClaimed(t *testing.T) {
	msgs := &steerPersistingMessageStub{}
	h := &Handler{messageService: msgs}
	reqCtx := &qaRequestContext{
		sessionID:     "sess-1",
		query:         "already persisted",
		requestID:     "req-B",
		userMessageID: "user-1",
		assistantMessage: &types.Message{
			ID:        "assist-B",
			SessionID: "sess-1",
			Role:      "assistant",
		},
	}
	require.NoError(t, h.persistTurnMessages(t.Context(), reqCtx))
	assert.Equal(t, 0, msgs.n)
}

type steerClaimFailingManager struct {
	interfaces.StreamManager
}

func (s *steerClaimFailingManager) ClaimLiveRun(context.Context, string, string, string) error {
	return errors.New("redis down")
}

type steerAppendFailingManager struct {
	interfaces.StreamManager
}

func (s *steerAppendFailingManager) AppendSteerEvents(
	context.Context, string, string, []interfaces.StreamEvent,
) error {
	return errors.New("redis down")
}

func TestClaimNextSteerFollowUpClaimFailureRollsBackMessages(t *testing.T) {
	ctx := t.Context()
	inner := stream.NewMemoryStreamManager()
	require.NoError(t, inner.SetLiveRun(ctx, "sess-1", "assist-A", "req-A"))
	require.NoError(t, inner.AppendSteerEvents(ctx, "sess-1", "assist-A", []interfaces.StreamEvent{
		steerEventWithDelivery("after-1", "do this next", steerDeliveryAfter),
	}))

	msgs := &steerPersistingMessageStub{}
	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: msgs,
		streamManager:  &steerClaimFailingManager{StreamManager: inner},
	}
	followUp, ok := h.claimNextSteerFollowUp(ctx, &qaRequestContext{sessionID: "sess-1"},
		&sseStreamContext{assistantMessage: &types.Message{ID: "assist-A"}})
	assert.False(t, ok)
	assert.Nil(t, followUp)
	assert.Empty(t, msgs.byID, "ClaimLiveRun failure must not leave a follow-up turn in the database")

	old, _, err := inner.GetSteerEvents(ctx, "sess-1", "assist-A", 0)
	require.NoError(t, err)
	require.Len(t, old, 1)
	assert.False(t, steerEventConsumed(old[0]), "backlog must stay pending so a retry can claim it")
}

func TestRebindSteerKeepsEventWhenAppendFails(t *testing.T) {
	ctx := t.Context()
	inner := stream.NewMemoryStreamManager()
	require.NoError(t, inner.SetLiveRun(ctx, "sess-1", "assist-A", "req-A"))
	h := &Handler{
		sessionService: &steerOwnedSessionStub{},
		messageService: &steerMessageLookupStub{
			msg: &types.Message{ID: "assist-B", SessionID: "sess-1", IsCompleted: false},
		},
		streamManager: &steerAppendFailingManager{StreamManager: inner},
	}
	evt := steerEventWithDelivery("late-1", "landed on A", steerDeliveryAfter)
	require.NoError(t, inner.AppendSteerEvents(ctx, "sess-1", "assist-A", []interfaces.StreamEvent{evt}))
	require.NoError(t, inner.ClaimLiveRun(ctx, "sess-1", "assist-B", "req-B"))

	_, _, err := h.rebindSteerIfLiveRunMoved(ctx, "sess-1", "assist-A", evt)
	require.Error(t, err)

	old, _, err := inner.GetSteerEvents(ctx, "sess-1", "assist-A", 0)
	require.NoError(t, err)
	require.Len(t, old, 1)
	assert.Equal(t, "late-1", old[0].ID)
}
