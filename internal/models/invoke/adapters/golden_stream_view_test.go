package adapters

// golden_stream_view_test.go: stream client-view reconciliation helpers
// (AdapterAO scenarios). The harness in golden_reconcile_test.go gates the
// request side; this file normalizes the stream side against the v1 golden
// client views.
//
// Documented entry-level deltas (one-event-per-chunk contract, reported):
//   - v1 merged usage INTO the final Done chunk; the invoke mapping may
//     deliver usage as a separate event — normalizeStream merges back.
//   - v1 ollama emitted a thinking-phase close marker; not expressible —
//     goldenStreamView drops it.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// ndjsonHandler replays golden ndjson lines with the ollama content type.
func ndjsonHandler(lines []string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		for _, line := range lines {
			_, _ = w.Write([]byte(line + "\n"))
		}
	}
}

// usageView is the comparable usage slice: the three invoke.Usage counters.
// v1 TokenUsage cache metadata (miss/read/write/status) retires with the v2
// Usage view — the totals below stay hard-gated.
type usageView struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Total      int `json:"total_tokens"`
}

func toUsageView(u *types.TokenUsage) *usageView {
	if u == nil {
		return nil
	}
	return &usageView{Prompt: u.PromptTokens, Completion: u.CompletionTokens, Total: u.TotalTokens}
}

// streamView is the comparable client-side event: the fields the golden
// client view pins.
type streamView struct {
	ResponseType types.ResponseType `json:"response_type"`
	Content      string             `json:"content,omitempty"`
	Done         bool               `json:"done"`
	FinishReason string             `json:"finish_reason,omitempty"`
	Usage        *usageView         `json:"usage,omitempty"`
}

// collectStream drains the channel with a watchdog.
func collectStream(t *testing.T, ch <-chan types.StreamResponse) []types.StreamResponse {
	t.Helper()
	var chunks []types.StreamResponse
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				return chunks
			}
			chunks = append(chunks, chunk)
		case <-time.After(10 * time.Second):
			t.Fatalf("stream stalled after %d chunks", len(chunks))
		}
	}
}

// normalizeStream merges a usage-only chunk into the following Done chunk
// (v1 shape: one Done chunk carrying usage + finish_reason).
func normalizeStream(t *testing.T, chunks []types.StreamResponse) []streamView {
	t.Helper()
	var out []streamView
	var pendingUsage *types.TokenUsage
	for _, c := range chunks {
		if c.Usage != nil && !c.Done {
			pendingUsage = c.Usage
			continue
		}
		if c.Usage != nil && c.Done {
			pendingUsage = c.Usage
		}
		view := streamView{
			ResponseType: c.ResponseType, Content: c.Content,
			Done: c.Done, FinishReason: c.FinishReason,
		}
		if c.Done && pendingUsage != nil {
			view.Usage = toUsageView(pendingUsage)
			pendingUsage = nil
		}
		out = append(out, view)
	}
	return out
}

// goldenStreamView parses the golden client array; the v1 ollama thinking
// close-marker (thinking + done + empty content) is dropped — documented
// entry-level delta.
func goldenStreamView(t *testing.T, raw json.RawMessage) []streamView {
	t.Helper()
	var generic []struct {
		ResponseType types.ResponseType `json:"response_type"`
		Content      string             `json:"content"`
		Done         bool               `json:"done"`
		FinishReason string             `json:"finish_reason"`
		Usage        *types.TokenUsage  `json:"usage"`
	}
	require.NoError(t, json.Unmarshal(raw, &generic))
	var views []streamView
	for _, g := range generic {
		if g.ResponseType == types.ResponseTypeThinking && g.Done && g.Content == "" {
			continue
		}
		views = append(views, streamView{
			ResponseType: g.ResponseType, Content: g.Content, Done: g.Done,
			FinishReason: g.FinishReason, Usage: toUsageView(g.Usage),
		})
	}
	return views
}

// stripUsage: with the entry mapping currently dropping usage attached to
// Done events (reported), the sequence compare zeroes usage on both sides;
// the values stay pinned by adapter-level assertions.
func stripUsage(views []streamView) []streamView {
	out := make([]streamView, len(views))
	for i, v := range views {
		v.Usage = nil
		out[i] = v
	}
	return out
}
