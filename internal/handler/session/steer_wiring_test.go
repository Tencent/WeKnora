package session

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSteerWiringGinContext builds the minimal gin context setupSSEStream
// needs (setSSEHeaders writes to it immediately).
func newSteerWiringGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)
	return c
}

// TestSetupSSEStreamMirrorsSteerSinkOntoRequestContext is the regression guard
// for the wiring bug where the sink was created on sseStreamContext but
// buildQARequest read qaRequestContext.steerSink — which nobody ever set — so
// queued steer messages were never handed to the engine and sat in the
// sub-list forever. The sink must be reachable from BOTH holders.
func TestSetupSSEStreamMirrorsSteerSinkOntoRequestContext(t *testing.T) {
	streams := stream.NewMemoryStreamManager()
	h := &Handler{streamManager: streams}

	assistantMessage := &types.Message{
		ID:        "assist-1",
		SessionID: "sess-1",
		Role:      "assistant",
		CreatedAt: time.Now(),
	}
	reqCtx := &qaRequestContext{
		c:                newSteerWiringGinContext(),
		ctx:              t.Context(),
		sessionID:        "sess-1",
		requestID:        "req-1",
		session:          &types.Session{ID: "sess-1"},
		customAgent:      &types.CustomAgent{ID: "agent-1"},
		assistantMessage: assistantMessage,
	}

	streamCtx := h.setupSSEStream(reqCtx, false, qaModeAgent)

	require.NotNil(t, streamCtx, "setupSSEStream returned nil")
	require.NotNil(t, streamCtx.steerSink, "streamCtx.steerSink must be created for agent runs")
	assert.NotNil(t, reqCtx.steerSink,
		"reqCtx.steerSink must mirror the streamCtx sink — buildQARequest reads it; "+
			"if this is nil the engine never receives a SteerSink and queued messages are silently dropped")
	assert.Same(t, streamCtx.steerSink, reqCtx.steerSink,
		"both holders must point at the same sink instance")

	// The built QA request must carry the sink through to the service layer.
	qaReq := reqCtx.buildQARequest()
	assert.NotNil(t, qaReq.SteerSink,
		"QARequest.SteerSink must be set for agent runs — nil here means the engine runs without mid-run injection")

	// The live-run marker must be published through the StreamManager, not a
	// process-local map: a steer request that lands on another replica has
	// to find this run instead of reporting "no run is live" and starting a
	// duplicate turn.
	liveID, liveReq, err := streams.GetLiveRun(t.Context(), "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "assist-1", liveID, "agent run must be published as the session's live run")
	assert.Equal(t, "req-1", liveReq)
}

// TestSetupSSEStreamNoSinkWithoutCustomAgent pins the negative case: runs
// without a custom agent (quick-answer) get no sink, matching the previous
// behaviour.
func TestSetupSSEStreamNoSinkWithoutCustomAgent(t *testing.T) {
	streams := stream.NewMemoryStreamManager()
	h := &Handler{streamManager: streams}

	reqCtx := &qaRequestContext{
		ctx:              t.Context(),
		c:                newSteerWiringGinContext(),
		sessionID:        "sess-2",
		requestID:        "req-2",
		session:          &types.Session{ID: "sess-2"},
		customAgent:      nil,
		assistantMessage: &types.Message{ID: "assist-2", SessionID: "sess-2", Role: "assistant", CreatedAt: time.Now()},
	}

	streamCtx := h.setupSSEStream(reqCtx, false, qaModeNormal)
	require.NotNil(t, streamCtx)
	assert.Nil(t, streamCtx.steerSink)
	assert.Nil(t, reqCtx.steerSink)
	qaReq := reqCtx.buildQARequest()
	assert.Nil(t, qaReq.SteerSink)

	// Quick-answer turns must not claim the session either: there is no
	// engine loop to drain a queue, so a steer request pointed here would
	// park messages that never run.
	liveID, _, err := streams.GetLiveRun(t.Context(), "sess-2")
	require.NoError(t, err)
	assert.Empty(t, liveID, "non-agent run must not be published as the live run")
}

// A custom agent configured as quick-answer still has customAgent != nil, but
// executeQA runs KnowledgeQA (qaModeNormal). Publishing a steer sink / live
// marker there parks /steer messages on a turn with no engine loop and no
// teardown ClearLiveRun.
func TestSetupSSEStreamNoSinkForQuickAnswerCustomAgent(t *testing.T) {
	streams := stream.NewMemoryStreamManager()
	h := &Handler{streamManager: streams}

	reqCtx := &qaRequestContext{
		ctx:       t.Context(),
		c:         newSteerWiringGinContext(),
		sessionID: "sess-3",
		requestID: "req-3",
		session:   &types.Session{ID: "sess-3"},
		customAgent: &types.CustomAgent{
			ID:     "custom-quick",
			Config: types.CustomAgentConfig{AgentMode: types.AgentModeQuickAnswer},
		},
		assistantMessage: &types.Message{
			ID: "assist-3", SessionID: "sess-3", Role: "assistant", CreatedAt: time.Now(),
		},
	}

	streamCtx := h.setupSSEStream(reqCtx, false, qaModeNormal)
	require.NotNil(t, streamCtx)
	assert.Nil(t, streamCtx.steerSink,
		"KnowledgeQA / quick-answer turns must not get a steer sink")
	assert.Nil(t, reqCtx.steerSink)
	assert.Nil(t, reqCtx.buildQARequest().SteerSink)

	liveID, _, err := streams.GetLiveRun(t.Context(), "sess-3")
	require.NoError(t, err)
	assert.Empty(t, liveID,
		"quick-answer custom-agent turns must not publish a live-run marker")
}

// KnowledgeQA can still resolve a smart-reasoning agent_id (API / stale
// client). The teardown that clears the live marker only runs for
// qaModeAgent, so a live marker here would never be released.
func TestSetupSSEStreamNoSinkForKnowledgeQAEvenWithAgentModeAgent(t *testing.T) {
	streams := stream.NewMemoryStreamManager()
	h := &Handler{streamManager: streams}

	reqCtx := &qaRequestContext{
		ctx:       t.Context(),
		c:         newSteerWiringGinContext(),
		sessionID: "sess-4",
		requestID: "req-4",
		session:   &types.Session{ID: "sess-4"},
		customAgent: &types.CustomAgent{
			ID:     "custom-react",
			Config: types.CustomAgentConfig{AgentMode: types.AgentModeSmartReasoning},
		},
		assistantMessage: &types.Message{
			ID: "assist-4", SessionID: "sess-4", Role: "assistant", CreatedAt: time.Now(),
		},
	}

	streamCtx := h.setupSSEStream(reqCtx, false, qaModeNormal)
	require.NotNil(t, streamCtx)
	assert.Nil(t, streamCtx.steerSink)
	liveID, _, err := streams.GetLiveRun(t.Context(), "sess-4")
	require.NoError(t, err)
	assert.Empty(t, liveID)
}

func TestSetupSSEStreamDoesNotWriteSSEWhenLiveRunFails(t *testing.T) {
	streams := stream.NewMemoryStreamManager()
	require.NoError(t, streams.SetLiveRun(t.Context(), "sess-5", "other-assist", "req-other"))
	h := &Handler{streamManager: streams}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)

	reqCtx := &qaRequestContext{
		c:           c,
		ctx:         t.Context(),
		sessionID:   "sess-5",
		requestID:   "req-5",
		session:     &types.Session{ID: "sess-5"},
		customAgent: &types.CustomAgent{ID: "agent-1"},
		assistantMessage: &types.Message{
			ID: "assist-5", SessionID: "sess-5", Role: "assistant", CreatedAt: time.Now(),
		},
	}
	streamCtx := h.setupSSEStream(reqCtx, false, qaModeAgent)
	require.True(t, streamCtx.liveRunFailed)
	assert.NotEqual(t, "text/event-stream", w.Header().Get("Content-Type"),
		"SetLiveRun failure must not start an SSE response the client cannot recover from")
}
