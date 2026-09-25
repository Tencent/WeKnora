package session

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func chunk(id string, kind types.ResponseType, content string) interfaces.StreamEvent {
	return interfaces.StreamEvent{ID: id, Type: kind, Content: content, Data: map[string]interface{}{"event_id": id}}
}

func doneChunk(id string, kind types.ResponseType, content string) interfaces.StreamEvent {
	evt := chunk(id, kind, content)
	evt.Done = true
	evt.Data["duration_ms"] = int64(1200)
	return evt
}

func TestCoalesceReplayEvents_MergesTheChunksOfOneSegmentIntoOneFrame(t *testing.T) {
	t1, t2, t3 := time.Unix(1, 0), time.Unix(2, 0), time.Unix(3, 0)
	a := chunk("think-1", types.ResponseTypeThinking, "Let ")
	b := chunk("think-1", types.ResponseTypeThinking, "me ")
	c := chunk("think-1", types.ResponseTypeThinking, "see.")
	a.Timestamp, b.Timestamp, c.Timestamp = t1, t2, t3
	b.Data["is_fallback"] = true

	got := coalesceReplayEvents([]interfaces.StreamEvent{a, b, c})

	require.Len(t, got, 1)
	assert.Equal(t, "Let me see.", got[0].Content)
	assert.Equal(t, "think-1", got[0].ID)
	assert.False(t, got[0].Done)
	assert.Equal(t, t3, got[0].Timestamp)
	assert.Equal(t, map[string]interface{}{"event_id": "think-1", "is_fallback": true}, got[0].Data)
	// The stored events keep their own content and data.
	assert.Equal(t, "Let ", a.Content)
	assert.Equal(t, map[string]interface{}{"event_id": "think-1"}, a.Data)
	assert.Equal(t, map[string]interface{}{"event_id": "think-1"}, c.Data)
}

func TestCoalesceReplayEvents_ADoneChunkKeepsItsOwnFrame(t *testing.T) {
	// The client marks a segment finished on its done chunk without appending
	// that chunk's content, so a done chunk must never absorb earlier text.
	got := coalesceReplayEvents([]interfaces.StreamEvent{
		chunk("ans-1", types.ResponseTypeAnswer, "The answer "),
		chunk("ans-1", types.ResponseTypeAnswer, "is 42."),
		doneChunk("ans-1", types.ResponseTypeAnswer, ""),
		chunk("ans-2", types.ResponseTypeAnswer, "Next."),
	})

	require.Len(t, got, 3)
	assert.Equal(t, "The answer is 42.", got[0].Content)
	assert.False(t, got[0].Done)
	assert.True(t, got[1].Done)
	assert.Equal(t, int64(1200), got[1].Data["duration_ms"])
	assert.Equal(t, "Next.", got[2].Content)
}

func TestCoalesceReplayEvents_AnotherSegmentOrTypeEndsTheRun(t *testing.T) {
	got := coalesceReplayEvents([]interfaces.StreamEvent{
		chunk("think-1", types.ResponseTypeThinking, "a"),
		chunk("think-2", types.ResponseTypeThinking, "b"),
		chunk("think-1", types.ResponseTypeThinking, "c"),
		chunk("ans-1", types.ResponseTypeAnswer, "d"),
		{
			ID: "tool-1", Type: types.ResponseTypeToolCall, Content: "Calling tool: search",
			Data: map[string]interface{}{"tool": "search"},
		},
		chunk("ans-1", types.ResponseTypeAnswer, "e"),
		// Same event ID, different type: two segments as far as the client
		// is concerned, so two frames.
		chunk("ans-1", types.ResponseTypeThinking, "f"),
		{ID: "complete-1", Type: types.ResponseTypeComplete, Done: true},
	})

	require.Len(t, got, 8)
	for i, want := range []string{"a", "b", "c", "d", "Calling tool: search", "e", "f", ""} {
		assert.Equal(t, want, got[i].Content, "frame %d", i)
	}
	assert.Equal(t, types.ResponseTypeToolCall, got[4].Type)
	assert.Equal(t, types.ResponseTypeThinking, got[6].Type)
	assert.Equal(t, types.ResponseTypeComplete, got[7].Type)
}

func TestCoalesceReplayEvents_MergesReflectionChunksToo(t *testing.T) {
	got := coalesceReplayEvents([]interfaces.StreamEvent{
		chunk("refl-1", types.ResponseTypeReflection, "Looks "),
		chunk("refl-1", types.ResponseTypeReflection, "right."),
	})

	require.Len(t, got, 1)
	assert.Equal(t, "Looks right.", got[0].Content)
}

func TestCoalesceReplayEvents_LeavesShortInputsAlone(t *testing.T) {
	assert.Empty(t, coalesceReplayEvents(nil))
	one := []interfaces.StreamEvent{chunk("ans-1", types.ResponseTypeAnswer, "x")}
	assert.Equal(t, one, coalesceReplayEvents(one))
}

// A long turn stored chunk by chunk replays as a handful of frames, and the
// client still receives every character in order.
func TestContinueStream_ReplaysALongAnswerInAFewFrames(t *testing.T) {
	var events []interfaces.StreamEvent
	var thought, answer strings.Builder
	for i := 0; i < 2000; i++ {
		piece := fmt.Sprintf("t%d ", i)
		thought.WriteString(piece)
		events = append(events, chunk("think-1", types.ResponseTypeThinking, piece))
	}
	events = append(events, doneChunk("think-1", types.ResponseTypeThinking, ""))
	for i := 0; i < 3000; i++ {
		piece := fmt.Sprintf("a%d ", i)
		answer.WriteString(piece)
		events = append(events, chunk("ans-1", types.ResponseTypeAnswer, piece))
	}
	events = append(events,
		doneChunk("ans-1", types.ResponseTypeAnswer, ""),
		interfaces.StreamEvent{ID: "complete-1", Type: types.ResponseTypeComplete, Done: true})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &Handler{
		sessionService: &stubSessionService{},
		messageService: &stubMessageServiceForStream{},
		streamManager:  &stubStreamManager{events: events},
		fileService:    &stubResourceFileService{},
	}
	r.GET("/sessions/continue-stream/:session_id", h.ContinueStream)
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sessions/continue-stream/sess1?message_id=msg1", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	// thinking run, thinking done, answer run, answer done, complete.
	assert.Equal(t, 5, strings.Count(body, "event:message"), body)
	assert.Contains(t, body, thought.String())
	assert.Contains(t, body, answer.String())
	assert.Less(t, strings.Index(body, thought.String()), strings.Index(body, answer.String()))
}

// growingStreamManager shows `visible` events on the first read and the whole
// log afterwards, so the handler replays a prefix and polls for the rest.
type growingStreamManager struct {
	stubStreamManager
	mu      sync.Mutex
	visible int
	reads   int
}

func (g *growingStreamManager) GetEvents(
	_ context.Context, _, _ string, from int,
) ([]interfaces.StreamEvent, int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reads++
	limit := g.visible
	if g.reads > 1 {
		limit = len(g.events)
	}
	if from >= limit {
		return nil, from, nil
	}
	return g.events[from:limit], limit, nil
}

// A storage reference cut between the replayed prefix and the chunks that
// arrive through the poll loop is still reassembled: the merged replay frame
// goes through the same holdback buffer as the live chunks after it.
func TestContinueStream_ReplayHoldbackSurvivesToThePollLoop(t *testing.T) {
	events := []interfaces.StreamEvent{
		chunk("answer-1", types.ResponseTypeAnswer, "The diagram "),
		chunk("answer-1", types.ResponseTypeAnswer, "![fig](resource://xifDo7"),
		// replay ends here (visible: 2); the rest arrives through the poll loop
		chunk("answer-1", types.ResponseTypeAnswer, "NTSL300Lp1goVutw) shows the flow."),
		doneChunk("answer-1", types.ResponseTypeAnswer, ""),
		{ID: "complete-1", Type: types.ResponseTypeComplete, Done: true},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &Handler{
		sessionService: &stubSessionService{},
		messageService: &stubMessageServiceForStream{},
		streamManager:  &growingStreamManager{stubStreamManager: stubStreamManager{events: events}, visible: 2},
		fileService:    &stubResourceFileService{},
	}
	r.GET("/sessions/continue-stream/:session_id", h.ContinueStream)
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/sessions/continue-stream/sess1?message_id=msg1&resource_urls=public", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.NotContains(t, body, "resource://")
	assert.Contains(t, body, `![fig](https://cdn.example.com/signed.png) shows the flow.`)
	assert.Less(t, strings.LastIndex(body, `"response_type":"answer"`),
		strings.Index(body, `"response_type":"complete"`))
}
