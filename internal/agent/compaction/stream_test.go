package compaction

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pacedChat streams a summary as chunks spaced by gap, optionally stopping
// after a number of chunks without closing the stream: a provider that stalls.
type pacedChat struct {
	stubChat
	chunks    []types.StreamResponse
	gap       time.Duration
	stallFrom int // stop sending after this many chunks; 0 sends all
	opened    atomic.Int32
}

func (p *pacedChat) ChatStream(
	ctx context.Context, _ []chat.Message, _ *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	p.opened.Add(1)
	ch := make(chan types.StreamResponse)
	go func() {
		defer close(ch)
		for i, chunk := range p.chunks {
			if p.stallFrom > 0 && i >= p.stallFrom {
				<-ctx.Done() // stalled until cancelled
				return
			}
			select {
			case <-time.After(p.gap):
			case <-ctx.Done():
				return
			}
			select {
			case ch <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func pacedSettings() Settings {
	s := testSettings()
	s.StallTimeout = 80 * time.Millisecond
	return s
}

func answerChunks(n int) []types.StreamResponse {
	chunks := []types.StreamResponse{{ResponseType: types.ResponseTypeThinking, Content: "considering"}}
	for range n {
		chunks = append(chunks, types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "## Goal "})
	}
	return append(chunks,
		types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"})
}

// A summarization that keeps producing output is left to finish, however long
// it takes in total: the old 60-second total budget cut exactly the large
// requests compaction exists for. Reasoning counts as progress but is not
// part of the summary.
func TestSummarizationRunsAsLongAsItProgresses(t *testing.T) {
	chatModel := &pacedChat{chunks: answerChunks(10), gap: 30 * time.Millisecond}
	c := New(chatModel, newEstimator(t), pacedSettings())

	start := time.Now()
	result, err := c.Compact(context.Background(), reactTurn(12), ReasonThreshold)
	require.NoError(t, err)

	require.Greater(t, time.Since(start), 4*80*time.Millisecond, "longer in total than the stall timeout")
	require.False(t, result.Degraded)
	assert.Contains(t, result.Summary, "## Goal")
	assert.NotContains(t, result.Summary, "considering")
}

// A stream that stops producing output is cancelled after the stall timeout,
// retried once, and then degrades to the archive rather than hanging the turn.
func TestSummarizationIsCancelledWhenItStalls(t *testing.T) {
	chatModel := &pacedChat{chunks: answerChunks(10), gap: 10 * time.Millisecond, stallFrom: 2}
	c := New(chatModel, newEstimator(t), pacedSettings())

	result, err := c.Compact(context.Background(), reactTurn(12), ReasonThreshold)
	require.NoError(t, err)
	require.True(t, result.Degraded)
	assert.Contains(t, result.Summary, "Raw conversation archive")
	assert.EqualValues(t, maxSummarizationAttempts, chatModel.opened.Load(),
		"a single live turn has only its prefix to summarize, attempted twice")
}

// An error reported inside the stream is a failed attempt, not a summary.
func TestSummarizationStreamErrorIsAFailure(t *testing.T) {
	chatModel := &pacedChat{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "## Goal half"},
		{ResponseType: types.ResponseTypeError, Content: "upstream reset", Done: true},
	}}
	c := New(chatModel, newEstimator(t), pacedSettings())

	result, err := c.Compact(context.Background(), reactTurn(12), ReasonThreshold)
	require.NoError(t, err)
	require.True(t, result.Degraded)
	assert.NotContains(t, result.Summary, "## Goal half")
}

// A stopped turn is not retried: another attempt cannot outlive the turn.
func TestSummarizationIsNotRetriedOnceTheTurnIsCancelled(t *testing.T) {
	chatModel := &pacedChat{chunks: answerChunks(10), gap: 10 * time.Millisecond, stallFrom: 1}
	c := New(chatModel, newEstimator(t), pacedSettings())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := c.streamSummary(ctx, nil, &chat.ChatOptions{})
	require.True(t, errors.Is(err, context.Canceled))

	_, err = c.summarize(ctx, reactTurn(2), 0, "", initialSummarizationInstructions, 1024)
	require.Error(t, err)
	assert.EqualValues(t, 2, chatModel.opened.Load(), "one open for streamSummary, one for summarize")
}
