package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

type toolResultLookupKey struct{}

// ToolResultLookup resolves a completed tool call ID to the JSON value that
// run_javascript may expose through its inputs object. The lookup is scoped to
// the current run; callers must not expose another session's results.
type ToolResultLookup func(callID string) (json.RawMessage, bool)

// WithToolResultLookup attaches the run's prior-result lookup to ctx.
func WithToolResultLookup(ctx context.Context, lookup ToolResultLookup) context.Context {
	if lookup == nil {
		return ctx
	}
	return context.WithValue(ctx, toolResultLookupKey{}, lookup)
}

// ToolResultLookupFromContext returns the lookup attached by the agent engine.
func ToolResultLookupFromContext(ctx context.Context) (ToolResultLookup, bool) {
	if ctx == nil {
		return nil, false
	}
	lookup, ok := ctx.Value(toolResultLookupKey{}).(ToolResultLookup)
	return lookup, ok && lookup != nil
}

// ToolResultInputValueForTool is ToolResultInputValue with tool-specific
// unwrapping. A prior run_javascript result exposes its envelope's result, not
// its calls/failed_calls summary, so a later script can consume the value.
func ToolResultInputValueForTool(toolName string, result *types.ToolResult) json.RawMessage {
	if toolName == ToolRunJavaScript && result != nil {
		output := strings.TrimSpace(result.Output)
		if output != "" && json.Valid([]byte(output)) {
			var envelope struct {
				Result json.RawMessage `json:"result"`
				Error  string          `json:"error"`
			}
			if json.Unmarshal([]byte(output), &envelope) == nil {
				if len(envelope.Result) > 0 {
					return envelope.Result
				}
				if envelope.Error != "" {
					if raw, err := json.Marshal(map[string]interface{}{
						"success": false,
						"error":   envelope.Error,
					}); err == nil {
						return raw
					}
				}
			}
		}
	}
	return ToolResultInputValue(result)
}

// ToolResultInputValue snapshots one completed tool result for scripts. The
// structured Data field wins; otherwise a JSON-looking Output is passed through,
// and plain text is encoded as a JSON string. nil means the call has no usable
// value (for example a failure with no output).
func ToolResultInputValue(result *types.ToolResult) json.RawMessage {
	if result == nil {
		return nil
	}
	if result.Data != nil {
		if raw, err := json.Marshal(result.Data); err == nil {
			return raw
		}
	}
	output := strings.TrimSpace(result.Output)
	if output != "" {
		if json.Valid([]byte(output)) {
			return json.RawMessage(output)
		}
		if raw, err := json.Marshal(output); err == nil {
			return raw
		}
	}
	if result.Error != "" {
		if raw, err := json.Marshal(map[string]interface{}{
			"success": false,
			"error":   result.Error,
			"output":  result.Output,
		}); err == nil {
			return raw
		}
	}
	return nil
}
