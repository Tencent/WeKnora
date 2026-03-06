package tools

import (
	"context"
	"encoding/json"
	"fmt"

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

	var input FinalAnswerInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][FinalAnswer] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	if input.Answer == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "answer must be a non-empty string",
		}, fmt.Errorf("answer must be a non-empty string")
	}

	logger.Infof(ctx, "[Tool][FinalAnswer] Answer length: %d characters", len(input.Answer))

	return &types.ToolResult{
		Success: true,
		Output:  input.Answer,
	}, nil
}
