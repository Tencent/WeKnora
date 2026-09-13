package invoke

// stream_meta.go — the entry-side stream Data producers (2026-09-13 裁定 A4,
// design §6.6 migration). v1 openai_stream.go carried three feature payloads
// inside the model stream that the v2 port silently dropped, leaving the
// agent consumers in think.go permanently dead:
//
//   - the tool-call PENDING notification, emitted once a call's name has
//     stabilized and argument text keeps arriving (agent → tool_call_pending);
//   - live sandbox write/edit progress while the model is still emitting the
//     tool-call JSON (agent → tool_call_progress), path first, then running
//     +N/-M line stats and a short preview — never the whole file;
//   - the "thinking" tool's streamed thought field, re-emitted as thinking
//     chunks tagged Data["source"]="thinking_tool" (agent → thought events).
//
// All three ride types.StreamResponse.Data. This is FEATURE assembly, not
// vendor knowledge — it lives on the entry (post-bridge) and keys off the
// neutral ToolCallDelta, so every openai-shape/native bridge gets it for
// free (v1 parity for whichever vendor streams a thinking tool or sandbox
// mutation call).

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// thinkingToolName is the built-in agent tool whose streamed "thought"
// argument field is echoed to the client as thinking chunks.
const thinkingToolName = "thinking"

type streamMetaState struct {
	accName map[int]string // accumulated tool-call name per delta index
	accID   map[int]string // accumulated tool-call id per delta index
	// lastName mirrors v1 state.lastFunctionName: the PREVIOUS fragment's
	// accumulated name — the pending notification fires on the first
	// args-continuing fragment after the name became known, not on the
	// fragment that introduced it.
	lastName        map[int]string
	nameNotified    map[int]bool
	fileProgress    map[int]*sandboxFileProgress
	fieldExtractors map[int]*jsonFieldExtractor
}

func newStreamMetaState() *streamMetaState {
	return &streamMetaState{
		accName:         map[int]string{},
		accID:           map[int]string{},
		lastName:        map[int]string{},
		nameNotified:    map[int]bool{},
		fileProgress:    map[int]*sandboxFileProgress{},
		fieldExtractors: map[int]*jsonFieldExtractor{},
	}
}

// feedToolCallDelta consumes one tool-call delta and returns the extra
// Data-carrying chunks it produces (the pending/progress notification and,
// for the thinking tool, the thought chunk).
func (s *streamMetaState) feedToolCallDelta(d *ToolCallDelta) []types.StreamResponse {
	if d == nil {
		return nil
	}
	if d.Name != "" {
		s.accName[d.Index] = d.Name
	}
	if d.ID != "" {
		s.accID[d.Index] = d.ID
	}
	currName := s.accName[d.Index]
	id := s.accID[d.Index]
	argsUpdated := d.Arguments != ""

	var out []types.StreamResponse
	var progressArgs map[string]any
	if isSandboxMutationTool(currName) && argsUpdated {
		prog := s.fileProgress[d.Index]
		if prog == nil {
			prog = newSandboxFileProgress(currName)
			s.fileProgress[d.Index] = prog
		}
		if payload, ok := prog.Feed(d.Arguments); ok {
			progressArgs = payload
		}
	}

	switch {
	case currName != "" && currName == s.lastName[d.Index] && argsUpdated &&
		!s.nameNotified[d.Index] && id != "":
		data := map[string]any{
			"tool_name":    currName,
			"tool_call_id": id,
		}
		if progressArgs != nil {
			data["arguments"] = progressArgs
		}
		out = append(out, types.StreamResponse{
			ResponseType: types.ResponseTypeToolCall,
			Data:         data,
		})
		s.nameNotified[d.Index] = true
	case progressArgs != nil && id != "" && currName != "":
		out = append(out, types.StreamResponse{
			ResponseType: types.ResponseTypeToolCall,
			Data: map[string]any{
				"tool_name":    currName,
				"tool_call_id": id,
				"arguments":    progressArgs,
			},
		})
	}
	s.lastName[d.Index] = currName

	// Stream thinking tool's thought field as thinking-type chunks (v1
	// parity — the thought rides Data["source"]="thinking_tool" so the agent
	// routes it to the thought event instead of a tool call).
	if currName == thinkingToolName && argsUpdated {
		extractor := s.fieldExtractors[d.Index]
		if extractor == nil {
			extractor = newJSONFieldExtractor("thought")
			s.fieldExtractors[d.Index] = extractor
		}
		if thought := extractor.Feed(d.Arguments); thought != "" {
			out = append(out, types.StreamResponse{
				ResponseType: types.ResponseTypeThinking,
				Content:      thought,
				Data: map[string]any{
					"source":       "thinking_tool",
					"tool_call_id": id,
				},
			})
		}
	}
	return out
}
