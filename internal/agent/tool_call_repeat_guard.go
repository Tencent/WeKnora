package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// JSONToolArgumentsSemanticallyEqual compares two JSON argument strings after parsing.
// Whitespace, key order, and number formatting differences do not affect equality.
// Invalid JSON on either side returns false.
func JSONToolArgumentsSemanticallyEqual(a, b string) bool {
	var va, vb any
	if err := json.Unmarshal([]byte(a), &va); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &vb); err != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// CanonicalJSONArgs returns a stable string form for logging and fingerprinting:
// parsed then re-marshaled so map key order is deterministic (Go json.Marshal sorts map keys).
func CanonicalJSONArgs(argsJSON string) (string, error) {
	var v any
	if err := json.Unmarshal([]byte(argsJSON), &v); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BuildDuplicateToolCallWarningCN returns a warning with first seen indexes.
func BuildDuplicateToolCallWarningCN(toolName string, historyMessageIndex, toolCallIndex int) string {
	if historyMessageIndex < 0 {
		return fmt.Sprintf(
			"[System Notice] Duplicate tool call detected: `%s` has arguments identical to an earlier call in the current response (ignoring JSON formatting differences), at current_response.tool_calls[%d]. Do not repeat the same call; adjust parameters or continue reasoning based on existing results.",
			toolName, toolCallIndex,
		)
	}
	return fmt.Sprintf(
		"[System Notice] Duplicate tool call detected: `%s` has arguments identical to a previous historical call (ignoring JSON formatting differences). First seen at messages[%d].tool_calls[%d]. Do not repeat the same call; adjust parameters or continue reasoning based on existing results.",
		toolName, historyMessageIndex, toolCallIndex,
	)
}

// FindDuplicateToolCallInMessages scans assistant messages to find whether
// (tool name + semantically equal JSON args) has appeared before.
// Returns first hit indexes and true when duplicated.
func FindDuplicateToolCallInMessages(
	messages []chat.Message,
	toolName string,
	argsJSON string,
) (historyMessageIndex int, toolCallIndex int, duplicated bool) {
	target, err := CanonicalJSONArgs(argsJSON)
	if err != nil {
		return -1, -1, false
	}
	for mi, m := range messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		for ti, tc := range m.ToolCalls {
			if !strings.EqualFold(tc.Function.Name, toolName) {
				continue
			}
			canon, err := CanonicalJSONArgs(tc.Function.Arguments)
			if err != nil {
				continue
			}
			if canon == target {
				return mi, ti, true
			}
		}
	}
	return -1, -1, false
}

// FindDuplicateToolCallInCurrentResponseBeforeIndex checks tool calls that
// already appeared earlier in the same LLM response.
func FindDuplicateToolCallInCurrentResponseBeforeIndex(
	toolCalls []types.LLMToolCall,
	currentIndex int,
) (prevIndex int, duplicated bool) {
	if currentIndex <= 0 || currentIndex >= len(toolCalls) {
		return -1, false
	}
	curr := toolCalls[currentIndex]
	currCanon, err := CanonicalJSONArgs(curr.Function.Arguments)
	if err != nil {
		return -1, false
	}
	for i := 0; i < currentIndex; i++ {
		prev := toolCalls[i]
		if !strings.EqualFold(prev.Function.Name, curr.Function.Name) {
			continue
		}
		prevCanon, err := CanonicalJSONArgs(prev.Function.Arguments)
		if err != nil {
			continue
		}
		if prevCanon == currCanon {
			return i, true
		}
	}
	return -1, false
}
