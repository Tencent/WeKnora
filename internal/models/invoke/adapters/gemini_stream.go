// gemini_stream.go — the streamGenerateContent?alt=sse bridge (P5-1). Each SSE
// data frame is a COMPLETE GenerateContentResponse: text deltas arrive as
// plain parts, function calls arrive whole (no openai-style index deltas), and
// the final frame carries finishReason + cumulative usageMetadata. The
// vendor stream has no [DONE] sentinel — the frame bearing finishReason IS
// the terminator, so the bridge emits its Done event right there (the entry
// loop ends at EOF without calling the bridge again).

package adapters

import (
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

// geminiBridgeAccum is the cross-chunk scratch: the running usage (every frame
// repeats the cumulative usageMetadata) and the tool calls seen so far (they
// must survive into Done — a mid-stream break still delivers partial calls,
// v1 streamState semantics).
type geminiBridgeAccum struct {
	usage    *geminiUsageMetadata
	toolIdx  int
	toolCopy []invoke.ToolCall
}

// geminiIdentifiedCall is one functionCall part resolved onto the platform
// tool-call shape.
type geminiIdentifiedCall struct {
	index int
	id    string
	name  string
	args  string
	call  invoke.ToolCall
}

// addCall folds a wire functionCall into the accumulator and returns the
// identified view (synthesizing the id when the vendor omits it).
func (a *geminiBridgeAccum) addCall(fc *struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
},
) geminiIdentifiedCall {
	args := string(fc.Args)
	if args == "" {
		args = "{}"
	}
	id := fc.ID
	if id == "" {
		id = fmt.Sprintf("gemini_call_%d", a.toolIdx+1)
	}
	index := a.toolIdx
	a.toolIdx++
	call := invoke.ToolCall{
		ID:   id,
		Type: "function",
		Function: invoke.FunctionCall{
			Name:      fc.Name,
			Arguments: args,
		},
	}
	a.toolCopy = append(a.toolCopy, call)
	return geminiIdentifiedCall{index: index, id: id, name: fc.Name, args: args, call: call}
}

func geminiState(state *invoke.StreamBridgeState) *geminiBridgeAccum {
	if v, ok := state.Get("geminiAccum"); ok {
		return v.(*geminiBridgeAccum)
	}
	acc := &geminiBridgeAccum{}
	state.Set("geminiAccum", acc)
	return acc
}

// TranslateStreamEvent bridges one demuxed SSE frame.
func (a *GeminiAdapter) TranslateStreamEvent(
	state *invoke.StreamBridgeState, chunk invoke.StreamChunk,
) (*invoke.StreamEvent, error) {
	if chunk.Event == "done" {
		// Defensive: gemini frames no [DONE] sentinel; treat it as a bare
		// close (the real terminator is the finishReason frame below).
		return geminiFinalEvent(geminiState(state), state, ""), nil
	}
	var ev geminiGenerateResponse
	if err := decodeChunk(chunk.Data, &ev); err != nil {
		return nil, err
	}

	// doc §PromptFeedback: blocked prompts yield feedback and zero candidates.
	if ev.PromptFeedback != nil && ev.PromptFeedback.BlockReason != "" && len(ev.Candidates) == 0 {
		msg := fmt.Sprintf("gemini: prompt blocked (%s)", ev.PromptFeedback.BlockReason)
		return &invoke.StreamEvent{
			Kind:  invoke.StreamKindError,
			Delta: &invoke.ContentDelta{Text: msg},
			Done:  &invoke.FinishInfo{},
		}, nil
	}

	acc := geminiState(state)
	if ev.UsageMetadata != nil {
		acc.usage = ev.UsageMetadata
	}

	if len(ev.Candidates) == 0 {
		return nil, nil
	}
	cand := ev.Candidates[0]
	// The frame bearing finishReason TERMINATES the vendor stream (no [DONE]
	// sentinel follows) — this is the only chance to emit Done, so a
	// terminating frame folds ALL of its parts into the final event: text
	// rides Done.Delta (mapStreamEvent emits it before Done) and function
	// calls ride Done.ToolCalls in full. Non-terminating frames emit their
	// fragments as immediate delta events.
	terminates := cand.FinishReason != ""
	var finalText string
	if terminates {
		state.Set("geminiFinish", cand.FinishReason)
	}
	if cand.Content != nil {
		for _, part := range cand.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				call := acc.addCall(part.FunctionCall)
				if terminates {
					continue // rides Done.ToolCalls in full
				}
				// Whole-call fragment: one delta carries name + full args.
				return &invoke.StreamEvent{
					Kind: invoke.StreamKindToolCall,
					ToolCallDelta: &invoke.ToolCallDelta{
						Index: call.index, ID: call.id, Type: "function",
						Name: call.name, Arguments: call.args,
					},
				}, nil
			case part.Thought:
				if part.Text == "" {
					continue
				}
				if terminates {
					continue // thought summaries after the answer are dropped
				}
				return &invoke.StreamEvent{
					Kind:  invoke.StreamKindThinking,
					Delta: &invoke.ContentDelta{Text: part.Text},
				}, nil
			case part.Text != "":
				if terminates {
					finalText += part.Text
					continue
				}
				return &invoke.StreamEvent{
					Kind:  invoke.StreamKindAnswer,
					Delta: &invoke.ContentDelta{Text: part.Text},
				}, nil
			}
		}
	}

	if terminates {
		return geminiFinalEvent(acc, state, finalText), nil
	}
	return nil, nil
}

// geminiFinalEvent closes the stream: answer Done with the mapped finish
// reason, the tool calls seen so far, and the latest cumulative usage. Any
// text the terminating frame itself carried rides Done.Delta (mapStreamEvent
// emits it before the Done chunk, v1 content+done shape).
func geminiFinalEvent(acc *geminiBridgeAccum, state *invoke.StreamBridgeState, finalText string) *invoke.StreamEvent {
	event := &invoke.StreamEvent{
		Kind: invoke.StreamKindAnswer,
		Done: &invoke.FinishInfo{
			FinishReason: geminiFinishReason(stateFinishValue(state), len(acc.toolCopy) > 0),
			ToolCalls:    acc.toolCopy,
		},
	}
	if finalText != "" {
		event.Delta = &invoke.ContentDelta{Text: finalText}
	}
	if acc.usage != nil {
		u := acc.usage.usage()
		event.Usage = &u
	}
	return event
}

func stateFinishValue(state *invoke.StreamBridgeState) string {
	if v, ok := state.Get("geminiFinish"); ok {
		return v.(string)
	}
	return ""
}
