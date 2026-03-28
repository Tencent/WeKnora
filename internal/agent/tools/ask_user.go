package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

var askUserTool = BaseTool{
	name: ToolNameAskUser,
	description: `Pause execution and ask the user a clarifying question. Use when the query is ambiguous, information is insufficient, or confirmation is needed before a high-risk operation.

## When to Use This Tool

- The user's query is ambiguous and could be interpreted in multiple ways
- Critical information is missing that affects the quality of the answer
- A high-risk operation requires explicit user confirmation before proceeding
- The user's intent is unclear and guessing could lead to a wrong or harmful result

## When NOT to Use This Tool

- The query is clear and has an obvious answer
- You can make a reasonable assumption without significant risk
- The missing information is non-critical and won't affect the answer quality

## Parameters

- **question**: The clarifying question to present to the user
- **options**: (Optional) Suggested answer options for the user to choose from
- **reason**: Why this clarification is needed — helps the user understand the context`,
	schema: utils.GenerateSchema[AskUserInput](),
}

// AskUserInput defines the input parameters for the ask_user tool
type AskUserInput struct {
	Question string   `json:"question" jsonschema:"The clarifying question to ask the user"`
	Options  []string `json:"options,omitempty" jsonschema:"Optional suggested answer options for the user to choose from"`
	Reason   string   `json:"reason" jsonschema:"Why this clarification is needed"`
}

// AskUserTool allows the agent to pause and request user input
type AskUserTool struct {
	BaseTool
}

// NewAskUserTool creates a new ask_user tool instance
func NewAskUserTool() *AskUserTool {
	return &AskUserTool{
		BaseTool: askUserTool,
	}
}

// Execute executes the ask_user tool
func (t *AskUserTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][AskUser] Execute started")

	// Parse input
	var input AskUserInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][AskUser] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}

	// Validate required fields
	if input.Question == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "question is required",
		}, nil
	}
	if input.Reason == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "reason is required",
		}, nil
	}

	// Format the output
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("## Clarification Needed\n\n"))
	builder.WriteString(fmt.Sprintf("**Question**: %s\n\n", input.Question))
	builder.WriteString(fmt.Sprintf("**Reason**: %s\n\n", input.Reason))
	if len(input.Options) > 0 {
		builder.WriteString("**Suggested Options**:\n")
		for i, option := range input.Options {
			builder.WriteString(fmt.Sprintf("%d. %s\n", i+1, option))
		}
	}

	// Build structured data signaling that user input is needed
	resultData := map[string]interface{}{
		"requires_user_input": true,
		"question":            input.Question,
		"reason":              input.Reason,
	}
	if len(input.Options) > 0 {
		resultData["options"] = input.Options
	}

	logger.Infof(ctx, "[Tool][AskUser] Question posed to user: %s", input.Question)

	return &types.ToolResult{
		Success: true,
		Output:  builder.String(),
		Data:    resultData,
	}, nil
}
