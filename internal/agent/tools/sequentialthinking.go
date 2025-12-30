package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var sequentialThinkingTool = BaseTool{
	name: ToolThinking,
	description: `A detailed tool for dynamic and reflective problem-solving through thoughts.

This tool helps analyze problems through a flexible thinking process that can adapt and evolve.

Each thought can build on, question, or revise previous insights as understanding deepens.

## When to Use This Tool

- Breaking down complex problems into steps
- Planning and design with room for revision
- Analysis that might need course correction
- Problems where the full scope might not be clear initially
- Problems that require a multi-step solution
- Tasks that need to maintain context over multiple steps
- Situations where irrelevant information needs to be filtered out

## Key Features

- You can adjust total_thoughts up or down as you progress
- You can question or revise previous thoughts
- You can add more thoughts even after reaching what seemed like the end
- You can express uncertainty and explore alternative approaches
- Not every thought needs to build linearly - you can branch or backtrack
- Generates a solution hypothesis
- Verifies the hypothesis based on the Chain of Thought steps
- Repeats the process until satisfied
- Provides a correct answer

## Parameters Explained

- **thought**: Your current thinking step, which can include:
  * Regular analytical steps
  * Revisions of previous thoughts
  * Questions about previous decisions
  * Realizations about needing more analysis
  * Changes in approach
  * Hypothesis generation
  * Hypothesis verification
  
  **CRITICAL - User-Friendly Thinking**: Write your thoughts in natural, user-friendly language. NEVER mention tool names (like "grep_chunks", "knowledge_search", "web_search", etc.) in your thinking process. Instead, describe your actions in plain language:
  - ❌ BAD: "I'll use grep_chunks to search for keywords, then knowledge_search for semantic understanding"
  - ✅ GOOD: "I'll start by searching for key terms in the knowledge base, then explore related concepts"
  - ❌ BAD: "After grep_chunks returns results, I'll use knowledge_search"
  - ✅ GOOD: "After finding relevant documents, I'll search for semantically related content"
  
  Write thinking as if explaining your reasoning to a user, not documenting technical steps. Focus on WHAT you're trying to find and WHY, not HOW (which tools you'll use).

- **next_thought_needed**: True if you need more thinking, even if at what seemed like the end
- **thought_number**: Current number in sequence (can go beyond initial total if needed)
- **total_thoughts**: Current estimate of thoughts needed (can be adjusted up/down)
- **is_revision**: A boolean indicating if this thought revises previous thinking
- **revises_thought**: If is_revision is true, which thought number is being reconsidered
- **branch_from_thought**: If branching, which thought number is the branching point
- **branch_id**: Identifier for the current branch (if any)
- **needs_more_thoughts**: If reaching end but realizing more thoughts needed

## Best Practices

1. Start with an initial estimate of needed thoughts, but be ready to adjust
2. Feel free to question or revise previous thoughts
3. Don't hesitate to add more thoughts if needed, even at the "end"
4. Express uncertainty when present
5. Mark thoughts that revise previous thinking or branch into new paths
6. Ignore information that is irrelevant to the current step
7. Generate a solution hypothesis when appropriate
8. Verify the hypothesis based on the Chain of Thought steps
9. Repeat the process until satisfied with the solution
10. Provide a single, ideally correct answer as the final output
11. Only set next_thought_needed to false when truly done and a satisfactory answer is reached`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "thought": {
      "type": "string",
      "description": "Your current thinking step. Write in natural, user-friendly language. NEVER mention tool names (like \"grep_chunks\", \"knowledge_search\", \"web_search\", etc.). Instead, describe actions in plain language (e.g., \"I'll search for key terms\" instead of \"I'll use grep_chunks\"). Focus on WHAT you're trying to find and WHY, not HOW (which tools you'll use)."
    },
    "nextThoughtNeeded": {
      "type": "boolean",
      "description": "Whether another thought step is needed"
    },
    "thoughtNumber": {
      "type": "integer",
      "description": "Current thought number (numeric value, e.g., 1, 2, 3)",
      "minimum": 1
    },
    "totalThoughts": {
      "type": "integer",
      "description": "Estimated total thoughts needed (numeric value, e.g., 5, 10)",
      "minimum": 5
    },
    "isRevision": {
      "type": "boolean",
      "description": "Whether this revises previous thinking"
    },
    "revisesThought": {
      "type": "integer",
      "description": "Which thought is being reconsidered",
      "minimum": 1
    },
    "branchFromThought": {
      "type": "integer",
      "description": "Branching point thought number",
      "minimum": 1
    },
    "branchId": {
      "type": "string",
      "description": "Branch identifier"
    },
    "needsMoreThoughts": {
      "type": "boolean",
      "description": "If more thoughts are needed"
    }
  },
  "required": ["thought", "nextThoughtNeeded", "thoughtNumber", "totalThoughts"]
}`),
}

// SequentialThinkingTool is a dynamic and reflective problem-solving tool
// This tool helps analyze problems through a flexible thinking process that can adapt and evolve
type SequentialThinkingTool struct {
	BaseTool
	thoughtHistory []SequentialThinkingInput
	branches       map[string][]SequentialThinkingInput
}

// SequentialThinkingInput defines the input parameters for sequential thinking tool
type SequentialThinkingInput struct {
	Thought           string `json:"thought"`
	ThoughtNumber     int    `json:"thought_number"`
	TotalThoughts     int    `json:"total_thoughts"`
	IsRevision        bool   `json:"is_revision,omitempty"`
	RevisesThought    *int   `json:"revises_thought,omitempty"`
	BranchFromThought *int   `json:"branch_from_thought,omitempty"`
	BranchID          string `json:"branch_id,omitempty"`
	NeedsMoreThoughts bool   `json:"needs_more_thoughts,omitempty"`
	NextThoughtNeeded bool   `json:"next_thought_needed"`
}

// NewSequentialThinkingTool creates a new sequential thinking tool instance
func NewSequentialThinkingTool() *SequentialThinkingTool {
	return &SequentialThinkingTool{
		BaseTool:       sequentialThinkingTool,
		thoughtHistory: make([]SequentialThinkingInput, 0),
		branches:       make(map[string][]SequentialThinkingInput),
	}
}

// Execute executes the sequential thinking tool
func (t *SequentialThinkingTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][SequentialThinking] Execute started")

	// Parse args from json.RawMessage
	var input SequentialThinkingInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][SequentialThinking] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	// Validate and parse input
	if err := t.validate(input); err != nil {
		logger.Errorf(ctx, "[Tool][SequentialThinking] Validation failed: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Validation failed: %v", err),
		}, err
	}

	// Adjust totalThoughts if thoughtNumber exceeds it
	if input.ThoughtNumber > input.TotalThoughts {
		input.TotalThoughts = input.ThoughtNumber
	}

	// Add to thought history
	t.thoughtHistory = append(t.thoughtHistory, input)

	// Handle branching
	if input.BranchFromThought != nil && input.BranchID != "" {
		if t.branches[input.BranchID] == nil {
			t.branches[input.BranchID] = make([]SequentialThinkingInput, 0)
		}
		t.branches[input.BranchID] = append(t.branches[input.BranchID], input)
	}

	logger.Debugf(ctx, "[Tool][SequentialThinking] %s", input.Thought)

	// Prepare response data
	branchKeys := make([]string, 0, len(t.branches))
	for k := range t.branches {
		branchKeys = append(branchKeys, k)
	}

	incomplete := input.NextThoughtNeeded || input.NeedsMoreThoughts ||
		input.ThoughtNumber < input.TotalThoughts

	responseData := map[string]interface{}{
		"thought_number":         input.ThoughtNumber,
		"total_thoughts":         input.TotalThoughts,
		"next_thought_needed":    input.NextThoughtNeeded,
		"branches":               branchKeys,
		"thought_history_length": len(t.thoughtHistory),
		"display_type":           "thinking",
		"thought":                input.Thought,
		"incomplete_steps":       incomplete,
	}

	logger.Infof(
		ctx,
		"[Tool][SequentialThinking] Execute completed - Thought %d/%d",
		input.ThoughtNumber,
		input.TotalThoughts,
	)

	outputMsg := "Thought process recorded"
	if incomplete {
		outputMsg = "Thought process recorded - unfinished steps remain, continue exploring and calling tools"
	}

	return &types.ToolResult{
		Success: true,
		Output:  outputMsg,
		Data:    responseData,
	}, nil
}

// validate validates the input thought data
func (t *SequentialThinkingTool) validate(data SequentialThinkingInput) error {
	// Validate thought (required)
	if data.Thought == "" {
		return fmt.Errorf("invalid thought: must be a non-empty string")
	}

	// Validate thoughtNumber (required)
	if data.ThoughtNumber < 1 {
		return fmt.Errorf("invalid thoughtNumber: must be >= 1")
	}

	// Validate totalThoughts (required)
	if data.TotalThoughts < 1 {
		return fmt.Errorf("invalid totalThoughts: must be >= 1")
	}

	return nil
}
