package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var finalAnswerTool = BaseTool{
	name: ToolFinalAnswer,
	description: `Submit your final answer to the user's question.

## When to Use This Tool

You MUST call this tool as your LAST action when you are ready to deliver your final response to the user.
After gathering all necessary information through other tools (search, retrieval, analysis, etc.),
synthesize your findings and submit the complete answer through this tool.

## Important Rules

1. NEVER end your turn without calling this tool
2. The answer parameter must contain your complete, well-formatted response
3. Include all citations, structure, and formatting in the answer
4. This should always be the last tool you call

## Parameters

- **answer**: Your complete final answer in Markdown format, including all citations and formatting`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "answer": {
      "type": "string",
      "description": "Your complete final answer in Markdown format. Include all citations, structure, images, and formatting."
    }
  },
  "required": ["answer"]
}`),
}

// FinalAnswerInput defines the input parameters for the final answer tool
type FinalAnswerInput struct {
	Answer string `json:"answer"`
}

// FinalAnswerTool submits the agent's final answer to the user
type FinalAnswerTool struct {
	BaseTool
}

// NewFinalAnswerTool creates a new final answer tool instance
func NewFinalAnswerTool() *FinalAnswerTool {
	return &FinalAnswerTool{
		BaseTool: finalAnswerTool,
	}
}

// Execute executes the final answer tool
func (t *FinalAnswerTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][FinalAnswer] Execute started")

	answer, ok := ParseFinalAnswerArgs(string(args))
	if !ok {
		logger.Errorf(ctx, "[Tool][FinalAnswer] Failed to parse args (even with repair): %s", string(args))
		return &types.ToolResult{
			Success: false,
			Error:   "Failed to parse final_answer args: malformed JSON and no recoverable answer field",
		}, fmt.Errorf("malformed final_answer arguments")
	}

	if answer == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "answer must be a non-empty string",
		}, fmt.Errorf("answer must be a non-empty string")
	}

	logger.Infof(ctx, "[Tool][FinalAnswer] Answer length: %d characters", len(answer))

	return &types.ToolResult{
		Success: true,
		Output:  answer,
	}, nil
}

// answerRegex best-effort extracts the value of an "answer": "..." field from
// a malformed JSON string. Used as a last resort when both strict parsing and
// RepairJSON fail — this keeps the final_answer tool call terminal so the
// agent loop cannot re-enter and emit duplicate answers.
//
// The pattern handles escaped quotes inside the answer via the non-greedy
// body `(?:\\.|[^"\\])*` which consumes either an escape sequence or any
// non-quote, non-backslash char.
var answerRegex = regexp.MustCompile(`"answer"\s*:\s*"((?:\\.|[^"\\])*)"`)

// ParseFinalAnswerArgs extracts the `answer` field from the final_answer
// tool's raw arguments. It is intentionally tolerant of malformed JSON that
// LLMs sometimes emit (unescaped quotes inside the answer, trailing commas,
// truncated closing braces, etc.), applying three fallbacks in order:
//
//  1. Strict json.Unmarshal on the raw string.
//  2. RepairJSON (trailing commas, invalid escapes, bracket balance) + Unmarshal.
//  3. Regex best-effort extraction of the `"answer": "..."` field.
//
// Returns the answer string and a bool indicating whether any path succeeded
// with a non-empty answer. Callers should treat ok=false as "unrecoverable"
// and surface a fallback message to the user, but must still treat the
// tool call as terminal to avoid the agent loop re-emitting final_answer.
func ParseFinalAnswerArgs(raw string) (string, bool) {
	var input FinalAnswerInput
	if err := json.Unmarshal([]byte(raw), &input); err == nil && input.Answer != "" {
		return input.Answer, true
	}

	repaired := RepairJSON(raw)
	if repaired != raw {
		if err := json.Unmarshal([]byte(repaired), &input); err == nil && input.Answer != "" {
			return input.Answer, true
		}
	}

	if m := answerRegex.FindStringSubmatch(raw); len(m) == 2 {
		if unquoted, err := unquoteJSONString(m[1]); err == nil && unquoted != "" {
			return unquoted, true
		}
		// Unquoting failed: the capture may contain invalid escapes. Fall back
		// to returning the raw capture so the user still sees *something*.
		if m[1] != "" {
			return m[1], true
		}
	}

	return "", false
}

// unquoteJSONString decodes a JSON-escaped string body (without surrounding
// quotes) into its literal form. It wraps the body in quotes and leans on
// json.Unmarshal so we get the standard escape semantics (\n, \", \uXXXX, …)
// without reimplementing them.
func unquoteJSONString(body string) (string, error) {
	var out string
	if err := json.Unmarshal([]byte(`"`+body+`"`), &out); err != nil {
		return "", err
	}
	return out, nil
}
