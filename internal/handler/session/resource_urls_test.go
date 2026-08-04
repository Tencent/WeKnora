package session

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const testResourceHandle = "resource://xifDo7NTSL300Lp1goVutw"

// stubResourceFileService resolves any storage reference to one fixed public URL.
type stubResourceFileService struct {
	interfaces.FileService
}

func (s *stubResourceFileService) GetFileURL(context.Context, string) (string, error) {
	return "https://cdn.example.com/signed.png", nil
}

func (s *stubResourceFileService) SaveFile(
	context.Context, *multipart.FileHeader, uint64, string,
) (string, error) {
	return "", nil
}

func (s *stubResourceFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return nil, nil
}

func publicStreamRewriter() *storageurl.StreamRewriter {
	return storageurl.NewStreamRewriter(storageurl.NewRequestRewriter(
		context.Background(), storageurl.ModePublic, &stubResourceFileService{}, nil))
}

func newTestGinContext(t *testing.T, query string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/knowledge-chat/sess1"+query, nil)
	return c, recorder
}

// The default mode must leave the stream byte-identical and unbuffered.
func TestResolveStreamRewriter_DefaultIsDisabled(t *testing.T) {
	h := &Handler{fileService: &stubResourceFileService{}}
	c, _ := newTestGinContext(t, "")

	rewriter, err := h.resolveStreamRewriter(c)
	require.NoError(t, err)
	assert.False(t, rewriter.Enabled())
}

func TestResolveStreamRewriter_RejectsInvalidValue(t *testing.T) {
	h := &Handler{fileService: &stubResourceFileService{}}
	c, _ := newTestGinContext(t, "?resource_urls=signed")

	_, err := h.resolveStreamRewriter(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource_urls")
}

// The default mode must not alter a payload at all.
func TestBuildStreamResponseFor_DisabledPassesThrough(t *testing.T) {
	rewriter := storageurl.NewStreamRewriter(storageurl.NewRewriter(nil, "TEST"))
	evt := interfaces.StreamEvent{
		ID:      "answer-1",
		Type:    types.ResponseTypeAnswer,
		Content: "see ![fig](" + testResourceHandle + ")",
	}

	response := buildStreamResponseFor(context.Background(), evt, "req-1", rewriter)
	assert.Equal(t, evt.Content, response.Content)
}

// Answer deltas are chunks the client accumulates, so a handle split across two
// events must be held back and rewritten once complete rather than emitted broken.
func TestBuildStreamResponseFor_HoldsReferenceSplitAcrossDeltas(t *testing.T) {
	rewriter := publicStreamRewriter()
	ctx := context.Background()

	first := buildStreamResponseFor(ctx, interfaces.StreamEvent{
		ID:      "answer-1",
		Type:    types.ResponseTypeAnswer,
		Content: "see ![fig](resource://xifDo7",
	}, "req-1", rewriter)
	assert.Equal(t, "see ", first.Content, "the incomplete reference must be held back")

	second := buildStreamResponseFor(ctx, interfaces.StreamEvent{
		ID:      "answer-1",
		Type:    types.ResponseTypeAnswer,
		Content: "NTSL300Lp1goVutw) done",
	}, "req-1", rewriter)
	assert.Equal(t, "![fig](https://cdn.example.com/signed.png) done", second.Content)
}

// Interleaved answer and thinking streams must not corrupt each other's buffers.
func TestBuildStreamResponseFor_DeltaStreamsAreIndependent(t *testing.T) {
	rewriter := publicStreamRewriter()
	ctx := context.Background()

	answer := buildStreamResponseFor(ctx, interfaces.StreamEvent{
		ID: "answer-1", Type: types.ResponseTypeAnswer, Content: "![a](resource://xifDo7NTSL",
	}, "req-1", rewriter)
	assert.Empty(t, answer.Content)

	thinking := buildStreamResponseFor(ctx, interfaces.StreamEvent{
		ID: "think-1", Type: types.ResponseTypeThinking, Content: "reasoning text",
	}, "req-1", rewriter)
	assert.Equal(t, "reasoning text", thinking.Content)

	answer = buildStreamResponseFor(ctx, interfaces.StreamEvent{
		ID: "answer-1", Type: types.ResponseTypeAnswer, Content: "300Lp1goVutw)",
	}, "req-1", rewriter)
	assert.Equal(t, "![a](https://cdn.example.com/signed.png)", answer.Content)
}

// Non-delta events carry a complete value, so they must be rewritten immediately
// rather than waiting for a chunk that will never arrive.
func TestBuildStreamResponseFor_NonDeltaContentIsRewrittenImmediately(t *testing.T) {
	response := buildStreamResponseFor(context.Background(), interfaces.StreamEvent{
		ID:      "tool-1",
		Type:    types.ResponseTypeToolResult,
		Content: "chart ![c](" + testResourceHandle + ")",
	}, "req-1", publicStreamRewriter())
	assert.Equal(t, "chart ![c](https://cdn.example.com/signed.png)", response.Content)
}

// The references payload and tool metadata share pointers and maps with the
// stream replay buffer, so rewriting must not mutate the source event.
func TestBuildStreamResponseFor_DoesNotMutateSourceEvent(t *testing.T) {
	reference := &types.SearchResult{Content: "chunk ![c](" + testResourceHandle + ")"}
	evt := interfaces.StreamEvent{
		ID:   "refs-1",
		Type: types.ResponseTypeReferences,
		Data: map[string]interface{}{
			"references": types.References{reference},
			"output":     "chart ![o](" + testResourceHandle + ")",
		},
	}

	response := buildStreamResponseFor(context.Background(), evt, "req-1", publicStreamRewriter())

	require.Len(t, response.KnowledgeReferences, 1)
	assert.Equal(t, "chunk ![c](https://cdn.example.com/signed.png)",
		response.KnowledgeReferences[0].Content)
	assert.Equal(t, "chunk ![c]("+testResourceHandle+")", reference.Content,
		"the replay buffer's SearchResult must be untouched")
	assert.Equal(t, "chart ![o]("+testResourceHandle+")", evt.Data["output"],
		"the replay buffer's metadata map must be untouched")
	assert.Equal(t, "chart ![o](https://cdn.example.com/signed.png)", response.Data["output"])
}

// A trailing reference held back when the stream ends must still be delivered,
// as the event type it came from, before the completion marker.
func TestEmitStreamEvent_FlushesHeldContentBeforeCompletion(t *testing.T) {
	rewriter := publicStreamRewriter()
	ctx := context.Background()
	c, recorder := newTestGinContext(t, "?resource_urls=public")

	held := buildStreamResponseFor(ctx, interfaces.StreamEvent{
		ID: "answer-1", Type: types.ResponseTypeAnswer, Content: "tail ![fig](resource://xifDo7",
	}, "req-1", rewriter)
	require.Equal(t, "tail ", held.Content)

	emitStreamEvent(ctx, c, interfaces.StreamEvent{
		ID: "complete-1", Type: types.ResponseTypeComplete, Done: true,
	}, "req-1", rewriter)

	body := recorder.Body.String()
	assert.Contains(t, body, `"response_type":"answer"`)
	assert.Contains(t, body, `![fig](resource://xifDo7`,
		"an incomplete reference cannot be resolved, but must not be swallowed")
	assert.Less(t,
		indexOf(body, `"response_type":"answer"`),
		indexOf(body, `"response_type":"complete"`),
		"held content must precede the completion marker",
	)
}

func TestHoldbackKeyRoundTrip(t *testing.T) {
	responseType, eventID := parseHoldbackKey(holdbackKey(types.ResponseTypeThinking, "think-1"))
	assert.Equal(t, types.ResponseTypeThinking, responseType)
	assert.Equal(t, "think-1", eventID)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
