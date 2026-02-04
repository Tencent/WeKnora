package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/types"
)

// formatFileSize formats file size in human-readable format
func formatFileSize(size int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	if size < KB {
		return fmt.Sprintf("%d B", size)
	} else if size < MB {
		return fmt.Sprintf("%.2f KB", float64(size)/KB)
	} else if size < GB {
		return fmt.Sprintf("%.2f MB", float64(size)/MB)
	}
	return fmt.Sprintf("%.2f GB", float64(size)/GB)
}

// formatDocSummary cleans and truncates document summaries for table display
func formatDocSummary(summary string, maxLen int) string {
	cleaned := strings.TrimSpace(summary)
	if cleaned == "" {
		return "-"
	}
	cleaned = strings.ReplaceAll(cleaned, "\n", " ")
	cleaned = strings.ReplaceAll(cleaned, "\r", " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	runes := []rune(cleaned)
	if len(runes) <= maxLen {
		return cleaned
	}
	return strings.TrimSpace(string(runes[:maxLen])) + "..."
}

// RecentDocInfo contains brief information about a recently added document
type RecentDocInfo struct {
	ChunkID             string
	KnowledgeBaseID     string
	KnowledgeID         string
	Title               string
	Description         string
	FileName            string
	FileSize            int64
	Type                string
	CreatedAt           string // Formatted time string
	FAQStandardQuestion string
	FAQSimilarQuestions []string
	FAQAnswers          []string
}

// SelectedDocumentInfo contains summary information about a user-selected document (via @ mention)
// Only metadata is included; content will be fetched via tools when needed
type SelectedDocumentInfo struct {
	KnowledgeID     string // Knowledge ID
	KnowledgeBaseID string // Knowledge base ID
	Title           string // Document title
	FileName        string // Original file name
	FileType        string // File type (pdf, docx, etc.)
}

// KnowledgeBaseInfo contains essential information about a knowledge base for agent prompt
type KnowledgeBaseInfo struct {
	ID          string
	Name        string
	Type        string // Knowledge base type: "document" or "faq"
	Description string
	DocCount    int
	RecentDocs  []RecentDocInfo // Recently added documents (up to 10)
}

// PlaceholderDefinition defines a placeholder exposed to UI/configuration
// Deprecated: Use types.PromptPlaceholder instead
type PlaceholderDefinition struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// AvailablePlaceholders lists all supported prompt placeholders for UI hints
// This returns agent mode specific placeholders
func AvailablePlaceholders() []PlaceholderDefinition {
	// Use centralized placeholder definitions from types package
	placeholders := types.PlaceholdersByField(types.PromptFieldAgentSystemPrompt)
	result := make([]PlaceholderDefinition, len(placeholders))
	for i, p := range placeholders {
		result[i] = PlaceholderDefinition{
			Name:        p.Name,
			Label:       p.Label,
			Description: p.Description,
		}
	}
	return result
}

// formatKnowledgeBaseList formats knowledge base information for the prompt
func formatKnowledgeBaseList(kbInfos []*KnowledgeBaseInfo) string {
	if len(kbInfos) == 0 {
		return "None"
	}

	var builder strings.Builder
	builder.WriteString("\nThe following knowledge bases have been selected by the user for this conversation. ")
	builder.WriteString("You should search within these knowledge bases to find relevant information.\n\n")
	for i, kb := range kbInfos {
		// Display knowledge base name and ID
		builder.WriteString(fmt.Sprintf("%d. **%s** (knowledge_base_id: `%s`)\n", i+1, kb.Name, kb.ID))

		// Display knowledge base type
		kbType := kb.Type
		if kbType == "" {
			kbType = "document" // Default type
		}
		builder.WriteString(fmt.Sprintf("   - Type: %s\n", kbType))

		if kb.Description != "" {
			builder.WriteString(fmt.Sprintf("   - Description: %s\n", kb.Description))
		}
		builder.WriteString(fmt.Sprintf("   - Document count: %d\n", kb.DocCount))

		// Display recent documents if available
		// For FAQ type knowledge bases, adjust the display format
		if len(kb.RecentDocs) > 0 {
			if kbType == "faq" {
				// FAQ knowledge base: show Q&A pairs in a more compact format
				builder.WriteString("   - Recent FAQ entries:\n\n")
				builder.WriteString("     | # | Question  | Answers | Chunk ID | Knowledge ID | Created At |\n")
				builder.WriteString("     |---|-------------------|---------|----------|--------------|------------|\n")
				for j, doc := range kb.RecentDocs {
					if j >= 10 { // Limit to 10 documents
						break
					}
					question := doc.FAQStandardQuestion
					if question == "" {
						question = doc.FileName
					}
					answers := "-"
					if len(doc.FAQAnswers) > 0 {
						answers = strings.Join(doc.FAQAnswers, " | ")
					}
					builder.WriteString(fmt.Sprintf("     | %d | %s | %s | `%s` | `%s` | %s |\n",
						j+1, question, answers, doc.ChunkID, doc.KnowledgeID, doc.CreatedAt))
				}
			} else {
				// Document knowledge base: show documents in standard format
				builder.WriteString("   - Recently added documents:\n\n")
				builder.WriteString("     | # | Document Name | Type | Created At | Knowledge ID | File Size | Summary |\n")
				builder.WriteString("     |---|---------------|------|------------|--------------|----------|---------|\n")
				for j, doc := range kb.RecentDocs {
					if j >= 10 { // Limit to 10 documents
						break
					}
					docName := doc.Title
					if docName == "" {
						docName = doc.FileName
					}
					// Format file size
					fileSize := formatFileSize(doc.FileSize)
					summary := formatDocSummary(doc.Description, 120)
					builder.WriteString(fmt.Sprintf("     | %d | %s | %s | %s | `%s` | %s | %s |\n",
						j+1, docName, doc.Type, doc.CreatedAt, doc.KnowledgeID, fileSize, summary))
				}
			}
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

// renderPromptPlaceholders renders placeholders in the prompt template
// Supported placeholders:
//   - {{knowledge_bases}} - Replaced with formatted knowledge base list
func renderPromptPlaceholders(template string, knowledgeBases []*KnowledgeBaseInfo) string {
	result := template

	// Replace {{knowledge_bases}} placeholder
	if strings.Contains(result, "{{knowledge_bases}}") {
		kbList := formatKnowledgeBaseList(knowledgeBases)
		result = strings.ReplaceAll(result, "{{knowledge_bases}}", kbList)
	}

	return result
}

// formatSkillsMetadata formats skills metadata for the system prompt (Level 1 - Progressive Disclosure)
// This is a lightweight representation that only includes skill name and description
func formatSkillsMetadata(skillsMetadata []*skills.SkillMetadata) string {
	if len(skillsMetadata) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("\n### Available Skills (IMPORTANT - READ CAREFULLY)\n\n")
	builder.WriteString("**You MUST actively consider using these skills for EVERY user request.**\n\n")

	builder.WriteString("#### Skill Matching Protocol (MANDATORY)\n\n")
	builder.WriteString("Before responding to ANY user query, follow this checklist:\n\n")
	builder.WriteString("1. **SCAN**: Read each skill's description and trigger conditions below\n")
	builder.WriteString("2. **MATCH**: Check if the user's intent matches ANY skill's triggers (keywords, scenarios, or task types)\n")
	builder.WriteString("3. **LOAD**: If a match is found, call `read_skill(skill_name=\"...\")` BEFORE generating your response\n")
	builder.WriteString("4. **APPLY**: Follow the skill's instructions to provide a higher-quality, structured response\n\n")

	builder.WriteString("**⚠️ CRITICAL**: Skill usage is MANDATORY when applicable. Do NOT skip skills to save time or tokens.\n\n")

	builder.WriteString("#### Available Skills\n\n")
	for i, skill := range skillsMetadata {
		builder.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, skill.Name))
		builder.WriteString(fmt.Sprintf("   %s\n\n", skill.Description))
	}

	builder.WriteString("#### Tool Reference\n\n")
	builder.WriteString("- `read_skill(skill_name)`: Load full skill instructions (MUST call before using a skill)\n")
	builder.WriteString("- `execute_skill_script(skill_name, script_path, args, input)`: Run utility scripts bundled with a skill\n")
	builder.WriteString("  - `input`: Pass data directly via stdin (use this when you have data in memory, e.g. JSON string)\n")
	builder.WriteString("  - `args`: Command-line arguments (only use `--file` if you have an actual file path in the skill directory)\n")

	return builder.String()
}

// formatSelectedDocuments formats selected documents for the prompt (summary only, no content)
func formatSelectedDocuments(docs []*SelectedDocumentInfo) string {
	if len(docs) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("\n### User Selected Documents (via @ mention)\n")
	builder.WriteString("The user has explicitly selected the following documents. ")
	builder.WriteString("**You should prioritize searching and retrieving information from these documents when answering.**\n")
	builder.WriteString("Use `list_knowledge_chunks` with the provided Knowledge IDs to fetch their content.\n\n")

	builder.WriteString("| # | Document Name | Type | Knowledge ID |\n")
	builder.WriteString("|---|---------------|------|---------------|\n")

	for i, doc := range docs {
		title := doc.Title
		if title == "" {
			title = doc.FileName
		}
		fileType := doc.FileType
		if fileType == "" {
			fileType = "-"
		}
		builder.WriteString(fmt.Sprintf("| %d | %s | %s | `%s` |\n",
			i+1, title, fileType, doc.KnowledgeID))
	}
	builder.WriteString("\n")

	return builder.String()
}

// renderPromptPlaceholdersWithStatus renders placeholders including web search status
// Supported placeholders:
//   - {{knowledge_bases}}
//   - {{web_search_status}} -> "Enabled" or "Disabled"
//   - {{current_time}} -> current time string
//   - {{skills}} -> formatted skills metadata (if any)
func renderPromptPlaceholdersWithStatus(
	template string,
	knowledgeBases []*KnowledgeBaseInfo,
	webSearchEnabled bool,
	currentTime string,
) string {
	result := renderPromptPlaceholders(template, knowledgeBases)
	status := "Disabled"
	if webSearchEnabled {
		status = "Enabled"
	}
	if strings.Contains(result, "{{web_search_status}}") {
		result = strings.ReplaceAll(result, "{{web_search_status}}", status)
	}
	if strings.Contains(result, "{{current_time}}") {
		result = strings.ReplaceAll(result, "{{current_time}}", currentTime)
	}
	// Remove {{skills}} placeholder if present but no skills provided
	// (it will be appended separately if skills exist)
	if strings.Contains(result, "{{skills}}") {
		result = strings.ReplaceAll(result, "{{skills}}", "")
	}
	return result
}

// BuildSystemPromptWithKB builds the progressive RAG system prompt with knowledge bases
// Deprecated: Use BuildSystemPrompt instead
func BuildSystemPromptWithWeb(
	knowledgeBases []*KnowledgeBaseInfo,
	systemPromptTemplate ...string,
) string {
	var template string
	if len(systemPromptTemplate) > 0 && systemPromptTemplate[0] != "" {
		template = systemPromptTemplate[0]
	} else {
		template = ProgressiveRAGSystemPrompt
	}
	currentTime := time.Now().Format(time.RFC3339)
	return renderPromptPlaceholdersWithStatus(template, knowledgeBases, true, currentTime)
}

// BuildSystemPromptWithoutWeb builds the progressive RAG system prompt without web search
// Deprecated: Use BuildSystemPrompt instead
func BuildSystemPromptWithoutWeb(
	knowledgeBases []*KnowledgeBaseInfo,
	systemPromptTemplate ...string,
) string {
	var template string
	if len(systemPromptTemplate) > 0 && systemPromptTemplate[0] != "" {
		template = systemPromptTemplate[0]
	} else {
		template = ProgressiveRAGSystemPrompt
	}
	currentTime := time.Now().Format(time.RFC3339)
	return renderPromptPlaceholdersWithStatus(template, knowledgeBases, false, currentTime)
}

// BuildPureAgentSystemPrompt builds the system prompt for Pure Agent mode (no KBs)
func BuildPureAgentSystemPrompt(
	webSearchEnabled bool,
	systemPromptTemplate ...string,
) string {
	var template string
	if len(systemPromptTemplate) > 0 && systemPromptTemplate[0] != "" {
		template = systemPromptTemplate[0]
	} else {
		template = PureAgentSystemPrompt
	}
	currentTime := time.Now().Format(time.RFC3339)
	// Pass empty KB list
	return renderPromptPlaceholdersWithStatus(template, []*KnowledgeBaseInfo{}, webSearchEnabled, currentTime)
}

// BuildSystemPromptOptions contains optional parameters for BuildSystemPrompt
type BuildSystemPromptOptions struct {
	SkillsMetadata []*skills.SkillMetadata
}

// BuildSystemPrompt builds the progressive RAG system prompt
// This is the main function to use - it uses a unified template with dynamic web search status
func BuildSystemPrompt(
	knowledgeBases []*KnowledgeBaseInfo,
	webSearchEnabled bool,
	selectedDocs []*SelectedDocumentInfo,
	systemPromptTemplate ...string,
) string {
	return BuildSystemPromptWithOptions(knowledgeBases, webSearchEnabled, selectedDocs, nil, systemPromptTemplate...)
}

// BuildSystemPromptWithOptions builds the system prompt with additional options like skills
func BuildSystemPromptWithOptions(
	knowledgeBases []*KnowledgeBaseInfo,
	webSearchEnabled bool,
	selectedDocs []*SelectedDocumentInfo,
	options *BuildSystemPromptOptions,
	systemPromptTemplate ...string,
) string {
	var basePrompt string
	var template string

	// Determine template to use
	if len(systemPromptTemplate) > 0 && systemPromptTemplate[0] != "" {
		template = systemPromptTemplate[0]
	} else if len(knowledgeBases) == 0 {
		template = PureAgentSystemPrompt
	} else {
		template = ProgressiveRAGSystemPrompt
	}

	currentTime := time.Now().Format(time.RFC3339)
	basePrompt = renderPromptPlaceholdersWithStatus(template, knowledgeBases, webSearchEnabled, currentTime)

	// Append selected documents section if any
	if len(selectedDocs) > 0 {
		basePrompt += formatSelectedDocuments(selectedDocs)
	}

	// Append skills metadata if available (Level 1 - Progressive Disclosure)
	if options != nil && len(options.SkillsMetadata) > 0 {
		basePrompt += formatSkillsMetadata(options.SkillsMetadata)
	}

	return basePrompt
}

// PureAgentSystemPrompt is the system prompt for Pure Agent mode (no Knowledge Bases)
var PureAgentSystemPrompt = `### Role
You are WeKnora, an intelligent assistant powered by ReAct. You operate in a Pure Agent mode without attached Knowledge Bases.

### Mission
To help users solve problems by planning, thinking, and using available tools (like Web Search).

### Workflow
1.  **Analyze:** Understand the user's request.
2.  **Plan:** If the task is complex, use todo_write to create a plan.
3.  **Execute:** Use available tools to gather information or perform actions.
4.  **Synthesize:** Provide a comprehensive answer.

### Tool Guidelines
*   **web_search / web_fetch:** Use these if enabled to find information from the internet.
*   **todo_write:** Use for managing multi-step tasks.
*   **thinking:** Use to plan and reflect.

### System Status
Current Time: {{current_time}}
Web Search: {{web_search_status}}
`

// ProgressiveRAGSystemPrompt is the unified progressive RAG system prompt template
// This template dynamically adapts based on web search status via {{web_search_status}} placeholder
var ProgressiveRAGSystemPrompt = `### Role
You are WeKnora, an intelligent retrieval assistant powered by Progressive Agentic RAG. You operate in a multi-tenant environment with strictly isolated knowledge bases. Your core philosophy is "Evidence-First": you never rely on internal parametric knowledge but construct answers solely from verified data retrieved from the Knowledge Base (KB) or Web (if enabled).

### Mission
To deliver accurate, traceable, and verifiable answers by orchestrating a dynamic retrieval process. You must first gauge the information landscape through preliminary retrieval, then rigorously execute and reflect upon specific research tasks. **You prioritize "Deep Reading" over superficial scanning.**

### Critical Constraints (ABSOLUTE RULES)
1.  **NO Internal Knowledge:** You must behave as if your training data does not exist regarding facts.
2.  **Mandatory Deep Read:** Whenever grep_chunks or knowledge_search returns matched knowledge_ids or chunk_ids, you **MUST** immediately call list_knowledge_chunks to read the full content of those specific chunks. Do not rely on search snippets alone.
3.  **KB First, Web Second:** Always exhaust KB strategies (including the Deep Read) before attempting Web Search (if enabled).
4.  **Strict Plan Adherence:** If a todo_write plan exists, execute it sequentially. No skipping.
5.  **Tool Privacy:** Never expose tool names to the user.

### Workflow: The "Reconnaissance-Plan-Execute" Cycle

#### Phase 1: Preliminary Reconnaissance (Mandatory Initial Step)
Before answering or creating a plan, you MUST perform a "Deep Read" test of the KB to gain preliminary cognition.
1.  **Search:** Execute grep_chunks (keyword) and knowledge_search (semantic) based on core entities.
2.  **DEEP READ (Crucial):** If the search returns IDs, you **MUST** call list_knowledge_chunks on the top relevant IDs to fetch their actual text.
3.  **Analyze:** In your think block, evaluate the *full text* you just retrieved.
    *   *Does this text fully answer the user?*
    *   *Is the information complete or partial?*

#### Phase 2: Strategic Decision & Planning
Based on the **Deep Read** results from Phase 1:
*   **Path A (Direct Answer):** If the full text provides sufficient, unambiguous evidence → Proceed to **Answer Generation**.
*   **Path B (Complex Research):** If the query involves comparison, missing data, or the content requires synthesis → Use todo_write to formulate a Work Plan.
    *   *Structure:* Break the problem into distinct retrieval tasks (e.g., "Deep read specs for Product A", "Deep read safety protocols").

#### Phase 3: Disciplined Execution & Deep Reflection (The Loop)
If in **Path B**, execute tasks in todo_write sequentially. For **EACH** task:
1.  **Search:** Perform grep_chunks / knowledge_search for the sub-task.
2.  **DEEP READ (Mandatory):** Call list_knowledge_chunks for any relevant IDs found. **Never skip this step.**
3.  **MANDATORY Deep Reflection (in think):** Pause and evaluate the full text:
    *   *Validity:* "Does this full text specifically address the sub-task?"
    *   *Gap Analysis:* "Is anything missing? Is the information outdated? Is the information irrelevant?"
    *   *Correction:* If insufficient, formulate a remedial action (e.g., "Search for synonym X", "Web Search if enabled") immediately.
    *   *Completion:* Mark task as "completed" ONLY when evidence is secured.

#### Phase 4: Final Synthesis
Only when ALL todo_write tasks are "completed":
*   Synthesize findings from the full text of all retrieved chunks.
*   Check for consistency.
*   Generate the final response.

### Core Retrieval Strategy (Strict Sequence)
For every retrieval attempt (Phase 1 or Phase 3), follow this exact chain:
1.  **Entity Anchoring (grep_chunks):** Use short keywords (1-3 words) to find candidate documents.
2.  **Semantic Expansion (knowledge_search):** Use vector search for context (filter by IDs from step 1 if applicable).
3.  **Deep Contextualization (list_knowledge_chunks): MANDATORY.**
    *   Rule: After Step 1 or 2 returns knowledge_ids, you MUST call this tool.
    *   Frequency: Call it frequently for multiple IDs to ensure you have the full results. **Do not be lazy; fetch the content.**
4.  **Graph Exploration (query_knowledge_graph):** Optional for relationships.
5.  **Web Fallback (web_search):** Use ONLY if Web Search is Enabled AND the Deep Read in Step 3 confirms the data is missing or irrelevant.

### Tool Selection Guidelines
*   **grep_chunks / knowledge_search:** Your "Index". Use these to find *where* the information might be.
*   **list_knowledge_chunks:** Your "Eyes". MUST be used after every search. Use to read what the information is.
*   **web_search / web_fetch:** Use these ONLY when Web Search is Enabled and KB retrieval is insufficient.
*   **todo_write:** Your "Manager". Tracks multi-step research.
*   **think:** Your "Conscience". Use to plan and reflect the content returned by list_knowledge_chunks.

### Final Output Standards
*   **Definitive:** Based strictly on the "Deep Read" content.
*   **Sourced(Inline, Proximate Citations):** All factual statements must include a citation immediately after the relevant claim—within the same sentence or paragraph where the fact appears: <kb doc="..." chunk_id="..." /> or <web url="..." title="..." /> (if from web).
	Citations may not be placed at the end of the answer. They must always be inserted inline, at the exact location where the referenced information is used ("proximate citation rule").
*   **Structured:** Clear hierarchy and logic.
*   **Rich Media (Markdown with Images):** When retrieved chunks contain images (indicated by the "images" field with URLs), you MUST include them in your response using standard Markdown image syntax: ![description](image_url). Place images at contextually appropriate positions within the answer to create a well-formatted, visually rich response. Images help users better understand the content, especially for diagrams, charts, screenshots, or visual explanations.

### System Status
Current Time: {{current_time}}
Web Search: {{web_search_status}}

### User Selected Knowledge Bases (via @ mention)
{{knowledge_bases}}
`

// ProgressiveRAGSystemPromptWithWeb is deprecated, use ProgressiveRAGSystemPrompt instead
// Kept for backward compatibility
var ProgressiveRAGSystemPromptWithWeb = ProgressiveRAGSystemPrompt

// ProgressiveRAGSystemPromptWithoutWeb is deprecated, use ProgressiveRAGSystemPrompt instead
// Kept for backward compatibility
var ProgressiveRAGSystemPromptWithoutWeb = ProgressiveRAGSystemPrompt
