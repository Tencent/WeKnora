package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

var todoWriteTool = BaseTool{
	name: ToolTodoWrite,
	description: `Use this tool to create and manage a structured task list for retrieval and research tasks. This helps you track progress, organize complex retrieval operations, and demonstrate thoroughness to the user.

**CRITICAL - Focus on Retrieval Tasks Only**:
- This tool is for tracking RETRIEVAL and RESEARCH tasks (e.g., searching knowledge bases, retrieving documents, gathering information)
- DO NOT include summary or synthesis tasks in todo_write - those are handled by the thinking tool
- Examples of appropriate tasks: "Search for X in knowledge base", "Retrieve information about Y", "Compare A and B"
- Examples of tasks to EXCLUDE: "Summarize findings", "Generate final answer", "Synthesize results" - these are for thinking tool

## When to Use This Tool
Use this tool proactively in these scenarios:

1. Complex multi-step tasks - When a task requires 3 or more distinct steps or actions
2. Non-trivial and complex tasks - Tasks that require careful planning or multiple operations
3. User explicitly requests todo list - When the user directly asks you to use the todo list
4. User provides multiple tasks - When users provide a list of things to be done (numbered or comma-separated)
5. After receiving new instructions - Immediately capture user requirements as todos
6. When you start working on a task - Mark it as in_progress BEFORE beginning work. Ideally you should only have one todo as in_progress at a time
7. After completing a task - Mark it as completed and add any new follow-up tasks discovered during implementation

## When NOT to Use This Tool

Skip using this tool when:
1. There is only a single, straightforward task
2. The task is trivial and tracking it provides no organizational benefit
3. The task is purely conversational or informational

NOTE that you should not use this tool if there is only one trivial task to do. In this case you are better off just doing the task directly.

## Examples of When to Use the Todo List

<example>
User: Compare WeKnora with other RAG frameworks like LangChain and LlamaIndex.
Assistant: I'll help you compare WeKnora with other RAG frameworks. Let me create a retrieval plan to gather information about each framework.
*Creates a todo list with retrieval tasks: 1) Search knowledge base for WeKnora features and architecture, 2) Use web_search to find LangChain documentation and features, 3) Use web_search to find LlamaIndex documentation and features, 4) Retrieve detailed comparison points for each framework*
*Note: Summary and synthesis will be handled by thinking tool after all retrieval tasks are completed*
Assistant: Let me start by searching the knowledge base for detailed WeKnora information.

<reasoning>
The assistant used todo_write tool correctly because:
1. The task requires multiple retrieval operations (searching KB for WeKnora, web search for LangChain and LlamaIndex)
2. Each retrieval task is specific and actionable
3. The todo list focuses on WHAT to retrieve, not HOW to summarize
4. Summary and synthesis will be handled separately by thinking tool after all retrieval is complete
5. This separation ensures clear task boundaries and avoids duplication
</reasoning>
</example>

<example>
User: I need to research the latest developments in vector databases for RAG applications.
Assistant: I'll help you research vector databases for RAG. Let me create a retrieval plan to gather comprehensive information.
*Creates todo list with retrieval tasks: 1) Search knowledge base for vector database information, 2) Use web_search to find latest vector database technologies, 3) Retrieve information about performance comparisons, 4) Gather information about integration approaches*
*Note: After completing all retrieval tasks, thinking tool will synthesize the findings into a comprehensive answer*
Assistant: Let me start by searching the knowledge base for vector database information.

<reasoning>
The assistant used todo_write tool correctly because:
1. The research task requires multiple retrieval operations (KB search, web search for latest info)
2. Each task focuses on retrieving specific information
3. The todo list tracks retrieval progress, not synthesis
4. Summary and analysis will be handled by thinking tool after retrieval is complete
5. This approach separates retrieval (todo_write) from synthesis (thinking tool)
</reasoning>
</example>

## Examples of When NOT to Use the Todo List

<example>
User: How do I print 'Hello World' in Python?
Assistant: In Python, you can print "Hello World" with this simple code:

print("Hello World")

This will output the text "Hello World" to the console when executed.</assistant>

<reasoning>
The assistant did not use the todo list because this is a single, trivial task that can be completed in one step. There's no need to track multiple tasks or steps for such a straightforward request.
</reasoning>
</example>

<example>
User: What does the git status command do?
Assistant: The git status command shows the current state of your working directory and staging area. It displays which changes have been staged, which haven't, and which files aren't being tracked by Git. This is a fundamental command for understanding the current state of your repository before making commits.

<reasoning>
The assistant did not use the todo list because this is an informational request with no actual coding task to complete. The user is simply asking for an explanation, not for the assistant to perform multiple steps or tasks.
</reasoning>
</example>

## Task States and Management

1. **Task States**: Use these states to track progress:
  - pending: Task not yet started
  - in_progress: Currently working on (limit to ONE task at a time)
  - completed: Task finished successfully

2. **Task Management**:
  - Update task status in real-time as you work
  - Mark tasks complete IMMEDIATELY after finishing (don't batch completions)
  - Only have ONE task in_progress at any time
  - Complete current tasks before starting new ones
  - Remove tasks that are no longer relevant from the list entirely

3. **Task Completion Requirements**:
  - ONLY mark a task as completed when you have FULLY accomplished it
  - If you encounter errors, blockers, or cannot finish, keep the task as in_progress
  - When blocked, create a new task describing what needs to be resolved
  - Never mark a task as completed if:
    - Tests are failing
    - Implementation is partial
    - You encountered unresolved errors
    - You couldn't find necessary files or dependencies

4. **Task Breakdown**:
  - Create specific, actionable RETRIEVAL tasks
  - Break complex retrieval needs into smaller, manageable steps
  - Use clear, descriptive task names focused on what to retrieve or research
  - **DO NOT include summary/synthesis tasks** - those are handled separately by the thinking tool

**Important**: After completing all retrieval tasks in todo_write, use the thinking tool to synthesize findings and generate the final answer. The todo_write tool tracks WHAT to retrieve, while thinking tool handles HOW to synthesize and present the information.

When in doubt, use this tool. Being proactive with task management demonstrates attentiveness and ensures you complete all retrieval requirements successfully.`,
	schema: utils.GenerateSchema[TodoWriteInput](),
}

// TodoWriteTool implements a planning tool for complex tasks
// This is an optional tool that helps organize multi-step research
type TodoWriteTool struct {
	BaseTool
}

// TodoWriteInput defines the input parameters for todo_write tool
type TodoWriteInput struct {
	Task  string     `json:"task" jsonschema:"The complex task or question you need to create a plan for"`
	Steps []PlanStep `json:"steps" jsonschema:"Array of research plan steps with status tracking"`
}

// PlanStep represents a single step in the research plan
type PlanStep struct {
	ID          string `json:"id" jsonschema:"Unique identifier for this step (e.g., 'step1', 'step2')"`
	Description string `json:"description" jsonschema:"Clear description of what to investigate or accomplish in this step"`
	Status      string `json:"status" jsonschema:"Current status: pending (not started), in_progress (executing), completed (finished)"`
}

// NewTodoWriteTool creates a new todo_write tool instance
func NewTodoWriteTool() *TodoWriteTool {
	return &TodoWriteTool{
		BaseTool: todoWriteTool,
	}
}

// Execute executes the todo_write tool
func (t *TodoWriteTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	// Parse args from json.RawMessage
	var input TodoWriteInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	if input.Task == "" {
		input.Task = "작업 설명이 제공되지 않았습니다"
	}

	// Parse plan steps
	planSteps := input.Steps

	// Generate formatted output
	output := generatePlanOutput(input.Task, planSteps)

	// Prepare structured data for response
	stepsJSON, _ := json.Marshal(planSteps)

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"task":         input.Task,
			"steps":        planSteps,
			"steps_json":   string(stepsJSON),
			"total_steps":  len(planSteps),
			"plan_created": true,
			"display_type": "plan",
		},
	}, nil
}

// Helper function to safely get string field from map
func getStringField(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// Helper function to safely get string array field from map
func getStringArrayField(m map[string]interface{}, key string) []string {
	if val, ok := m[key].([]interface{}); ok {
		result := make([]string, 0, len(val))
		for _, item := range val {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	}
	// Handle legacy string format for backward compatibility
	if val, ok := m[key].(string); ok && val != "" {
		return []string{val}
	}
	return []string{}
}

// generatePlanOutput generates a formatted plan output
func generatePlanOutput(task string, steps []PlanStep) string {
	output := "계획이 생성되었습니다\n\n"
	output += fmt.Sprintf("**작업**: %s\n\n", task)

	if len(steps) == 0 {
		output += "주의: 구체적인 단계가 제공되지 않았습니다. 체계적인 연구를 위해 3-7개의 검색 작업을 생성하는 것이 좋습니다.\n\n"
		output += "제안된 검색 프로세스 (검색 작업에 집중, 요약 포함 안 함):\n"
		output += "1. grep_chunks를 사용하여 키워드 검색으로 관련 문서 찾기\n"
		output += "2. knowledge_search를 사용하여 의미 검색으로 관련 내용 찾기\n"
		output += "3. list_knowledge_chunks를 사용하여 핵심 문서의 전체 내용 가져오기\n"
		output += "4. web_search를 사용하여 보충 정보 가져오기 (필요한 경우)\n"
		output += "\n주의: 요약 및 종합은 thinking 도구에서 처리되므로 여기에 요약 작업을 추가하지 마세요.\n"
		return output
	}

	// Count task statuses
	pendingCount := 0
	inProgressCount := 0
	completedCount := 0
	for _, step := range steps {
		switch step.Status {
		case "pending":
			pendingCount++
		case "in_progress":
			inProgressCount++
		case "completed":
			completedCount++
		}
	}
	totalCount := len(steps)
	remainingCount := pendingCount + inProgressCount

	output += "**계획 단계**:\n\n"

	// Display all steps in order
	for i, step := range steps {
		output += formatPlanStep(i+1, step)
	}

	// Add summary and emphasis on remaining tasks
	output += "\n=== 작업 진행 상황 ===\n"
	output += fmt.Sprintf("총계: %d 개 작업\n", totalCount)
	output += fmt.Sprintf("✅ 완료됨: %d 개\n", completedCount)
	output += fmt.Sprintf("🔄 진행 중: %d 개\n", inProgressCount)
	output += fmt.Sprintf("⏳ 대기 중: %d 개\n", pendingCount)

	output += "\n=== ⚠️ 중요 알림 ===\n"
	if remainingCount > 0 {
		output += fmt.Sprintf("**아직 %d 개의 작업이 완료되지 않았습니다!**\n\n", remainingCount)
		output += "**모든 작업을 완료해야 요약하거나 결론을 내릴 수 있습니다.**\n\n"
		output += "다음 작업:\n"
		if inProgressCount > 0 {
			output += "- 현재 진행 중인 작업을 계속 완료하세요\n"
		}
		if pendingCount > 0 {
			output += fmt.Sprintf("- %d 개의 대기 중인 작업을 처리하기 시작하세요\n", pendingCount)
			output += "- 각 작업을 순서대로 완료하고 건너뛰지 마세요\n"
		}
		output += "- 각 작업을 완료한 후 todo_write를 사용하여 completed로 표시하세요\n"
		output += "- 모든 작업이 완료된 후에만 최종 요약을 생성할 수 있습니다\n"
	} else {
		output += "✅ **모든 작업이 완료되었습니다!**\n\n"
		output += "이제 가능합니다:\n"
		output += "- 모든 작업의 발견 사항을 종합하세요\n"
		output += "- 완전한 최종 답변 또는 보고서를 생성하세요\n"
		output += "- 모든 측면이 충분히 연구되었는지 확인하세요\n"
	}

	return output
}

// formatPlanStep formats a single plan step for output
func formatPlanStep(index int, step PlanStep) string {
	statusEmoji := map[string]string{
		"pending":     "⏳",
		"in_progress": "🔄",
		"completed":   "✅",
		"skipped":     "⏭️",
	}

	emoji, ok := statusEmoji[step.Status]
	if !ok {
		emoji = "⏳"
	}

	output := fmt.Sprintf("  %d. %s [%s] %s\n", index, emoji, step.Status, step.Description)

	// if len(step.ToolsToUse) > 0 {
	// 	output += fmt.Sprintf("     工具: %s\n", strings.Join(step.ToolsToUse, ", "))
	// }

	return output
}
