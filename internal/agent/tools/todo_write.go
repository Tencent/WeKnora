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
		input.Task = "未提供任务描述"
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
	output := "计划已创建\n\n"
	output += fmt.Sprintf("**任务**: %s\n\n", task)

	if len(steps) == 0 {
		output += "注意：未提供具体步骤。建议创建3-7个检索任务以系统化研究。\n\n"
		output += "建议的检索流程（专注于检索任务，不包含总结）：\n"
		output += "1. 使用 grep_chunks 搜索关键词定位相关文档\n"
		output += "2. 使用 knowledge_search 进行语义搜索获取相关内容\n"
		output += "3. 使用 list_knowledge_chunks 获取关键文档的完整内容\n"
		output += "4. 使用 web_search 获取补充信息（如需要）\n"
		output += "\n注意：总结和综合由 thinking 工具处理，不要在此处添加总结任务。\n"
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

	output += "**计划步骤**:\n\n"

	// Display all steps in order
	for i, step := range steps {
		output += formatPlanStep(i+1, step)
	}

	// Add summary and emphasis on remaining tasks
	output += "\n=== 任务进度 ===\n"
	output += fmt.Sprintf("总计: %d 个任务\n", totalCount)
	output += fmt.Sprintf("✅ 已完成: %d 个\n", completedCount)
	output += fmt.Sprintf("🔄 进行中: %d 个\n", inProgressCount)
	output += fmt.Sprintf("⏳ 待处理: %d 个\n", pendingCount)

	output += "\n=== ⚠️ 重要提醒 ===\n"
	if remainingCount > 0 {
		output += fmt.Sprintf("**还有 %d 个任务未完成！**\n\n", remainingCount)
		output += "**必须完成所有任务后才能总结或得出结论。**\n\n"
		output += "下一步操作：\n"
		if inProgressCount > 0 {
			output += "- 继续完成当前进行中的任务\n"
		}
		if pendingCount > 0 {
			output += fmt.Sprintf("- 开始处理 %d 个待处理任务\n", pendingCount)
			output += "- 按顺序完成每个任务，不要跳过\n"
		}
		output += "- 完成每个任务后，更新 todo_write 标记为 completed\n"
		output += "- 只有在所有任务完成后，才能生成最终总结\n"
	} else {
		output += "✅ **所有任务已完成！**\n\n"
		output += "现在可以：\n"
		output += "- 综合所有任务的发现\n"
		output += "- 生成完整的最终答案或报告\n"
		output += "- 确保所有方面都已充分研究\n"
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
