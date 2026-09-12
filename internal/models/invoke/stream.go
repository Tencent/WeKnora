package invoke

// stream.go ships the openai-shape stream bridging DEFAULT (design §6.2/§6.7):
// OpenAI-compatible adapters embed OpenAIStreamBridge and inherit
// TranslateStreamEvent; anthropic/ollama adapters write their own. The
// entry-level StreamEvent→types.StreamResponse mapping is in invoke.go — this
// file only knows the wire chunk → StreamEvent half.

import (
	"encoding/json"
	"strings"
)

const (
	stateFinishReason = "openai.finish_reason"
	stateToolCalls    = "openai.tool_calls"
)

// OpenAIStreamBridge is the exported default TranslateStreamEvent for
// OpenAI-compatible wire format (seam ⑤, P1c): SSE data frames carrying
// chat.completion.chunk JSON, terminated by the [DONE] sentinel (which the
// executor's Demuxer reports as Event="done"). OpenAI-compatible adapters
// embed it instead of porting a private copy.
type OpenAIStreamBridge struct{}

// openAIChunk mirrors the subset of the chat chunk payload the bridge needs.
type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// TranslateStreamEvent implements the openai-shape bridge.
func (OpenAIStreamBridge) TranslateStreamEvent(state *StreamBridgeState, chunk StreamChunk) (*StreamEvent, error) {
	if chunk.Event == "done" || isDoneSentinel(chunk.Data) {
		finish, _ := state.Get(stateFinishReason)
		reason, _ := finish.(string)
		var calls []ToolCall
		if a := toolAssembler(state); a != nil {
			calls = a.Calls()
		}
		return &StreamEvent{Kind: StreamKindAnswer, Done: &FinishInfo{FinishReason: reason, ToolCalls: calls}}, nil
	}
	var c openAIChunk
	if err := decodeJSON(chunk.Data, &c); err != nil {
		return nil, err
	}
	// Usage-only frames (choices empty) arrive near the end on several vendors.
	if len(c.Choices) == 0 {
		if c.Usage != nil {
			// Native cache counters the generic shape drops (seam ③): deepseek
			// hit/miss, anthropic-style read/creation, openai details.
			ApplyRawPromptCacheUsage(chunk.Data, c.Usage)
			return &StreamEvent{Kind: StreamKindUsage, Usage: c.Usage}, nil
		}
		return nil, nil
	}
	choice := c.Choices[0]
	if choice.FinishReason != "" {
		state.Set(stateFinishReason, choice.FinishReason)
		// The Done event is emitted on the [DONE] sentinel; a chunk that only
		// carries finish_reason yields no user-visible event.
		return nil, nil
	}
	d := choice.Delta
	if d.ReasoningContent != "" {
		return &StreamEvent{Kind: StreamKindThinking, Delta: &ContentDelta{Text: d.ReasoningContent}}, nil
	}
	if len(d.ToolCalls) > 0 {
		a := toolAssembler(state)
		if a == nil {
			a = &ToolCallAssembler{byIndex: make(map[int]*ToolCall)}
			state.Set(stateToolCalls, a)
		}
		out := make([]ToolCallDelta, 0, len(d.ToolCalls))
		for _, tc := range d.ToolCalls {
			delta := ToolCallDelta{
				Index: tc.Index, ID: tc.ID, Type: tc.Type,
				Name: tc.Function.Name, Arguments: tc.Function.Arguments,
			}
			a.Add(delta)
			out = append(out, delta)
		}
		// Multiple deltas in one frame collapse to the first for the event;
		// assembly state keeps every fragment for the final Calls().
		return &StreamEvent{Kind: StreamKindToolCall, ToolCallDelta: &out[0]}, nil
	}
	if d.Content != "" {
		return &StreamEvent{Kind: StreamKindAnswer, Delta: &ContentDelta{Text: d.Content}}, nil
	}
	return nil, nil
}

func toolAssembler(state *StreamBridgeState) *ToolCallAssembler {
	v, ok := state.Get(stateToolCalls)
	if !ok {
		return nil
	}
	a, _ := v.(*ToolCallAssembler)
	return a
}

// ToolCallAssembler merges streamed tool-call deltas (fragments arrive
// index-keyed, with name on the first frame and argument text spread across
// frames) into complete ToolCalls. Shared by openai-shape adapters — and read
// by the entry on stream interruption to deliver the partial calls (v1
// processStream semantics).
type ToolCallAssembler struct {
	order   []int
	byIndex map[int]*ToolCall
}

// NewToolCallAssembler creates an assembler for adapters outside this
// package (the DashScope native bridge assembles tool_call frames too).
func NewToolCallAssembler() *ToolCallAssembler {
	return &ToolCallAssembler{byIndex: make(map[int]*ToolCall)}
}

// Add merges one delta into the assembly.
func (a *ToolCallAssembler) Add(d ToolCallDelta) {
	tc, ok := a.byIndex[d.Index]
	if !ok {
		tc = &ToolCall{ID: d.ID, Type: d.Type}
		a.byIndex[d.Index] = tc
		a.order = append(a.order, d.Index)
	}
	if d.ID != "" {
		tc.ID = d.ID
	}
	if d.Type != "" {
		tc.Type = d.Type
	}
	if d.Name != "" {
		tc.Function.Name += d.Name
	}
	tc.Function.Arguments += d.Arguments
}

// Calls returns the assembled tool calls in first-appearance order.
func (a *ToolCallAssembler) Calls() []ToolCall {
	if a == nil || len(a.order) == 0 {
		return nil
	}
	calls := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		calls = append(calls, *a.byIndex[idx])
	}
	return calls
}

// isDoneSentinel reports a raw [DONE] payload (defensive: some vendors emit it
// without the exact SSE framing the Demuxer matches).
func isDoneSentinel(data []byte) bool {
	return strings.TrimSpace(string(data)) == "[DONE]"
}

// ApplyRawPromptCacheUsage ports v1 applyRawPromptCacheUsage
// (chat/prompt_cache.go:113-140): native prompt-cache counters that the
// generic OpenAI usage shape drops — deepseek hit/miss, anthropic-style
// read/creation, openai prompt_tokens_details — are captured into the cache
// detail fields (seam ③). Reported=true marks that the vendor actually
// reported cache counters on this frame.
func ApplyRawPromptCacheUsage(data []byte, usage *Usage) {
	if usage == nil || len(data) == 0 {
		return
	}
	var raw struct {
		Usage struct {
			PromptCacheHit      *int `json:"prompt_cache_hit_tokens"`
			PromptCacheMiss     *int `json:"prompt_cache_miss_tokens"`
			CacheReadInput      *int `json:"cache_read_input_tokens"`
			CacheCreationInput  *int `json:"cache_creation_input_tokens"`
			PromptTokensDetails *struct {
				CachedTokens     *int `json:"cached_tokens"`
				CacheWriteTokens *int `json:"cache_write_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	valueOrZero := func(v *int) int {
		if v == nil {
			return 0
		}
		return *v
	}
	switch {
	case raw.Usage.PromptCacheHit != nil || raw.Usage.PromptCacheMiss != nil:
		usage.CacheReadTokens = valueOrZero(raw.Usage.PromptCacheHit)
		usage.CacheWriteTokens = 0
		usage.CacheMissTokens = valueOrZero(raw.Usage.PromptCacheMiss)
		usage.CacheReported = true
	case raw.Usage.CacheReadInput != nil || raw.Usage.CacheCreationInput != nil:
		read := valueOrZero(raw.Usage.CacheReadInput)
		usage.CacheReadTokens = read
		usage.CacheWriteTokens = valueOrZero(raw.Usage.CacheCreationInput)
		usage.CacheMissTokens = max(0, usage.PromptTokens-read)
		usage.CacheReported = true
	case raw.Usage.PromptTokensDetails != nil:
		read := valueOrZero(raw.Usage.PromptTokensDetails.CachedTokens)
		usage.CacheReadTokens = read
		usage.CacheWriteTokens = valueOrZero(raw.Usage.PromptTokensDetails.CacheWriteTokens)
		usage.CacheMissTokens = max(0, usage.PromptTokens-read)
		usage.CacheReported = true
	}
}
