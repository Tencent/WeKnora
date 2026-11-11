package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// titleBatchStream separates events into poll batches, including empty polls.
// This reproduces titles arriving well before the terminal answer event.
type titleBatchStream struct {
	interfaces.StreamManager
	batches [][]interfaces.StreamEvent
	reads   int
	onRead  func(int)
}

func (s *titleBatchStream) GetEvents(
	ctx context.Context, _, _ string, fromOffset int,
) ([]interfaces.StreamEvent, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, fromOffset, err
	}
	s.reads++
	if s.onRead != nil {
		s.onRead(s.reads)
	}
	if s.reads > len(s.batches) {
		return nil, fromOffset, nil
	}
	batch := s.batches[s.reads-1]
	return batch, fromOffset + len(batch), nil
}

func TestHandleAgentEventsForSSE_TitleAcrossPolls(t *testing.T) {
	title := interfaces.StreamEvent{
		ID: "title-1", Type: types.ResponseTypeSessionTitle, Content: "Synthetic title", Done: true,
	}
	complete := interfaces.StreamEvent{
		ID: "complete-1", Type: types.ResponseTypeComplete, Done: true,
	}
	stop := interfaces.StreamEvent{ID: "stop-1", Type: "stop", Done: true}
	tests := []struct {
		name         string
		batches      [][]interfaces.StreamEvent
		waitForTitle bool
		wantTitle    bool
		wantStop     bool
		wantTimeout  bool
	}{
		{"title_before_complete", [][]interfaces.StreamEvent{{title}, {complete}}, true, true, false, false},
		{"title_before_empty_poll_and_complete", [][]interfaces.StreamEvent{{title}, nil, {complete}}, true, true, false, false},
		{"title_with_complete", [][]interfaces.StreamEvent{{title, complete}}, true, true, false, false},
		{"title_after_complete", [][]interfaces.StreamEvent{{complete}, nil, {title}}, true, true, false, false},
		{"no_title_requested", [][]interfaces.StreamEvent{{complete}}, false, false, false, false},
		{"missing_title_has_bounded_wait", [][]interfaces.StreamEvent{{complete}}, true, false, false, true},
		{"stop_does_not_wait_for_title", [][]interfaces.StreamEvent{{title}, {stop}}, true, true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &titleBatchStream{batches: tt.batches}
			h := &Handler{streamManager: stream}
			c, recorder := newTestGinContext(t, "")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)

			start := time.Now()
			h.handleAgentEventsForSSE(ctx, c, "sess1", "msg1", "req-1", nil, tt.waitForTitle, publicStreamRewriter())
			elapsed := time.Since(start)
			t.Logf("stream duration: %s; polls: %d", elapsed, stream.reads)
			require.NoError(t, ctx.Err(), "stream did not finish before the test deadline")
			if tt.wantTimeout {
				assert.GreaterOrEqual(t, elapsed, 3*time.Second, "a genuinely missing title should retain the bounded wait")
			} else {
				assert.Less(t, elapsed, 2*time.Second, "an already delivered title must not trigger the 3-second timeout")
			}
			body := recorder.Body.String()
			if tt.wantTitle {
				assert.Equal(t, 1, strings.Count(body, `"response_type":"session_title"`))
			}
			if tt.wantStop {
				assert.Contains(t, body, `"response_type":"stop"`)
				assert.NotContains(t, body, `"response_type":"complete"`)
			} else {
				assert.Contains(t, body, `"response_type":"complete"`)
			}
		})
	}
}

func TestHandleAgentEventsForSSE_DisconnectDuringTitleWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &titleBatchStream{
		batches: [][]interfaces.StreamEvent{{{ID: "complete-1", Type: types.ResponseTypeComplete, Done: true}}},
		onRead: func(read int) {
			if read == 2 {
				cancel()
			}
		},
	}
	h := &Handler{streamManager: stream}
	c, _ := newTestGinContext(t, "")
	c.Request = c.Request.WithContext(ctx)
	start := time.Now()
	h.handleAgentEventsForSSE(ctx, c, "sess1", "msg1", "req-1", nil, true, publicStreamRewriter())
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}
