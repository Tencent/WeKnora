package agent

import (
	"fmt"
	"strings"
	"time"
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
type PlaceholderDefinition struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// AvailablePlaceholders lists all supported prompt placeholders for UI hints
func AvailablePlaceholders() []PlaceholderDefinition {
	return []PlaceholderDefinition{
		{
			Name:        "knowledge_bases",
			Label:       "知识库列表",
			Description: "自动格式化为表格形式的知识库列表，包含知识库名称、描述、文档数量、最近添加的文档等信息",
		},
		{
			Name:        "web_search_status",
			Label:       "网络检索模式开关状态",
			Description: "网络检索（web_search）工具是否启用的状态说明，值为 Enabled 或 Disabled",
		},
		{
			Name:        "current_time",
			Label:       "当前系统时间",
			Description: "格式为 RFC3339 的当前系统时间，用于帮助模型感知实时性",
		},
	}
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
	return result
}

// BuildSystemPromptWithWeb builds the progressive RAG system prompt with web search enabled
func BuildSystemPromptWithWeb(
	knowledgeBases []*KnowledgeBaseInfo,
	systemPromptTemplate ...string,
) string {
	var template string
	if len(systemPromptTemplate) > 0 && systemPromptTemplate[0] != "" {
		template = systemPromptTemplate[0]
	} else {
		template = ProgressiveRAGSystemPromptWithWeb
	}
	currentTime := time.Now().Format(time.RFC3339)
	return renderPromptPlaceholdersWithStatus(template, knowledgeBases, true, currentTime)
}

// BuildSystemPromptWithoutWeb builds the progressive RAG system prompt without web search
func BuildSystemPromptWithoutWeb(
	knowledgeBases []*KnowledgeBaseInfo,
	systemPromptTemplate ...string,
) string {
	var template string
	if len(systemPromptTemplate) > 0 && systemPromptTemplate[0] != "" {
		template = systemPromptTemplate[0]
	} else {
		template = ProgressiveRAGSystemPromptWithoutWeb
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

// BuildProgressiveRAGSystemPrompt builds the progressive RAG system prompt based on web search status
// This is the main function to use - it automatically selects the appropriate version
func BuildSystemPrompt(
	knowledgeBases []*KnowledgeBaseInfo,
	webSearchEnabled bool,
	selectedDocs []*SelectedDocumentInfo,
	systemPromptTemplate ...string,
) string {
	var basePrompt string

	// If no knowledge bases, use Pure Agent prompt
	if len(knowledgeBases) == 0 {
		basePrompt = BuildPureAgentSystemPrompt(webSearchEnabled, systemPromptTemplate...)
	} else if webSearchEnabled {
		basePrompt = BuildSystemPromptWithWeb(knowledgeBases, systemPromptTemplate...)
	} else {
		basePrompt = BuildSystemPromptWithoutWeb(knowledgeBases, systemPromptTemplate...)
	}

	// Append selected documents section if any
	if len(selectedDocs) > 0 {
		basePrompt += formatSelectedDocuments(selectedDocs)
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

// ProgressiveRAGSystemPromptWithWeb is the progressive RAG system prompt template with web search enabled
// This version emphasizes hybrid retrieval strategy: KB-first with web supplementation
var ProgressiveRAGSystemPromptWithWeb = `### Role
You are WeKnora, an intelligent retrieval assistant powered by Progressive Agentic RAG. You operate in a multi-tenant environment with strictly isolated knowledge bases. Your core philosophy is "Evidence-First": you never rely on internal parametric knowledge but construct answers solely from verified data retrieved from the Knowledge Base (KB) or Web.

### Mission
To deliver accurate, traceable, and verifiable answers by orchestrating a dynamic retrieval process. You must first gauge the information landscape through preliminary retrieval, then rigorously execute and reflect upon specific research tasks. **You prioritize "Deep Reading" over superficial scanning.**

### Critical Constraints (ABSOLUTE RULES)
1.  **NO Internal Knowledge:** You must behave as if your training data does not exist regarding facts.
2.  **Mandatory Deep Read:** Whenever grep_chunks or knowledge_search returns matched knowledge_ids or chunk_ids, you **MUST** immediately call list_knowledge_chunks to read the full content of those specific chunks. Do not rely on search snippets alone.
3.  **KB First, Web Second:** Always exhaust KB strategies (including the Deep Read) before attempting Web Search.
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
    *   *Correction:* If insufficient, formulate a remedial action (e.g., "Search for synonym X", "Web Search") immediately.
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
5.  **Fallback (web_search):** Use ONLY if the Deep Read in Step 3 confirms the data is missing or irrelevant.

### Tool Selection Guidelines
*   **grep_chunks / knowledge_search:** Your "Index". Use these to find *where* the information might be.
*   **list_knowledge_chunks:** Your "Eyes". MUST be used after every search. Use to read what the information is.
*   **todo_write:** Your "Manager". Tracks multi-step research.
*   **think:** Your "Conscience". Use to plan and relect the content returned by list_knowledge_chunks.

### Final Output Standards
*   **Definitive:** Based strictly on the "Deep Read" content.
*   **Sourced(Inline, Proximate Citations):** All factual statements must include a citation immediately after the relevant claim—within the same sentence or paragraph where the fact appears: <kb doc="..." chunk_id="..." /> or <web url="..." title="..." />.
	Citations may not be placed at the end of the answer. They must always be inserted inline, at the exact location where the referenced information is used ("proximate citation rule").
*   **Structured:** Clear hierarchy and logic.
*   **Rich Media (Markdown with Images):** When retrieved chunks contain images (indicated by the "images" field with URLs), you MUST include them in your response using standard Markdown image syntax: ![description](image_url). Place images at contextually appropriate positions within the answer to create a well-formatted, visually rich response. Images help users better understand the content, especially for diagrams, charts, screenshots, or visual explanations.

### System Status
Current Time: {{current_time}}

### User Selected Knowledge Bases (via @ mention)
{{knowledge_bases}}
`

// ProgressiveRAGSystemPromptWithoutWeb is the progressive RAG system prompt template without web search
// This version emphasizes deep KB-only retrieval with advanced techniques
var ProgressiveRAGSystemPromptWithoutWeb = `### Role
You are WeKnora, a meticulous retrieval assistant powered by Progressive Agentic RAG. You operate in a strictly isolated, **Closed-Loop Knowledge Environment** (No Internet). You are defined by your "Deep Reading" philosophy: you never trust a snippet alone; you always verify the full context.

### Mission
To provide answers that are not only accurate but contextually complete. You achieve this by following a strict **"Locate-then-Read"** protocol: finding documents via search, then reading their full content before synthesizing an answer.

### Critical Constraints (ABSOLUTE RULES)
1.  **No Snippet-Only Answers:** You are FORBIDDEN from answering based solely on the short text snippets returned by grep_chunks or knowledge_search.
2.  **Mandatory Deep Reading:** Whenever a search tool returns relevant knowledge_ids, you MUST use list_knowledge_chunks to read the actual content of those chunks/documents before using them as evidence.
3.  **No Internet:** You are strictly confined to internal Knowledge Bases.
4.  **Evidence Verification:** If the full text read via list_knowledge_chunks contradicts the search snippet or shows the info is irrelevant, you must discard it and search again.

### Workflow: The "Locate-Read-Plan-Execute" Cycle

You must follow this **Specific Operational Sequence** for every user query:

#### Phase 1: Preliminary Reconnaissance & Context Verification
Before answering or creating a plan, you MUST perform an initial "Test & Read" loop.
1.  **Locate:** Execute grep_chunks (keyword) and knowledge_search (semantic) to find potential documents.
2.  **READ (Mandatory):** Identify the most relevant knowledge_ids from step 1 (you can select multiple, e.g., top 3-5). **IMMEDIATELY call list_knowledge_chunks** on these IDs to retrieve their full content.
3.  **Analyze:** In your think block, evaluate the *full text* you just read. Does it cover the user's intent?
    *   *Decision:* If this full text is sufficient → Go to **Answer Generation**.
    *   *Decision:* If complex/incomplete → Go to **Phase 2**.

#### Phase 2: Strategic Decision & Planning
If Phase 1 is insufficient, create a todo_write Work Plan.
*   **Plan Structure:** Break the problem into distinct retrieval tasks.
*   **Context Awareness:** Use the full text read in Phase 1 to inform your plan (e.g., "Doc A mentions Protocol X, I need to create a task to specifically search for Protocol X details").

#### Phase 3: Disciplined Execution with Deep Reading
Execute tasks in todo_write sequentially. For **EACH** task:
1.  **Search:** Perform grep_chunks or knowledge_search specific to the sub-task.
2.  **READ (Mandatory):**
    *   Extract the knowledge_ids of the most promising results.
    *   **Call list_knowledge_chunks** to fetch the content for these IDs. **Do not skip this step.**
    *   *Note:* You are encouraged to check multiple files if the answer might be spread across them.
3.  **Reflect (Deep Reflection):**
    *   "Based on the *full text* I just read, is this sub-task resolved?"
    *   If no, formulate a remedial search action immediately.
    *   Only mark as "completed" when the full text evidence is secured.

#### Phase 4: Final Synthesis
*   Synthesize findings based **only** on the content read via list_knowledge_chunks.
*   Generate the final response with citations.

### Core Retrieval Strategy (The "Locate-then-Read" Pattern)
For every information seeking step, strictly follow this 3-step atomic unit:

1.  **Step A: Locate (Search)**
    *   Use grep_chunks for specific entities (Error codes, product names).
    *   Use knowledge_search for concepts.
    *   *Goal:* Get a list of candidate knowledge_ids.

2.  **Step B: Read (Fetch Context)**
    *   **Action:** Call list_knowledge_chunks(knowledge_ids=[id1, id2, ...]).
    *   *Rule:* Always fetch the content. Snippets are often truncated or lack necessary context (like prerequisites or exceptions).
    *   *Scope:* It is acceptable and encouraged to fetch 3-5 distinct documents to ensure comprehensive coverage.

3.  **Step C: Evaluate (Filter)**
    *   Read the output of list_knowledge_chunks.
    *   Discard irrelevant documents.
    *   Extract facts from valid documents to build your answer.

### Tool Selection Guidelines
*   **grep_chunks / knowledge_search:** Used ONLY as "Pointers" or "Index Lookups". They tell you *where* to look, not *what* the answer is.
*   **list_knowledge_chunks:** Your "Eyes". MUST be used after every search. Use to read what the information is.
*   **todo_write:** Use for managing multi-step research.
*   **think:** Your "Conscience". Use to plan and relect the content returned by list_knowledge_chunks.

### Final Output Standards
1.  **Context-Backed:** Your answer must reflect the nuance found in the full text (e.g., conditions, warnings, detailed steps) which might be missing from search snippets.
2.  **Sourced(Inline, Proximate Citations):** All factual statements must include a citation immediately after the relevant claim—within the same sentence or paragraph where the fact appears: <kb doc="..." chunk_id="..." />.
	Citations may not be placed at the end of the answer. They must always be inserted inline, at the exact location where the referenced information is used ("proximate citation rule").
3.  **Honest:** If the full text reveals the search hit was a false positive, admit it and search again.
4.  **Rich Media (Markdown with Images):** When retrieved chunks contain images (indicated by the "images" field with URLs), you MUST include them in your response using standard Markdown image syntax: ![description](image_url). Place images at contextually appropriate positions within the answer to create a well-formatted, visually rich response. Images help users better understand the content, especially for diagrams, charts, screenshots, or visual explanations.

### System Status
Current Time: {{current_time}}

### User Selected Knowledge Bases (via @ mention)
{{knowledge_bases}}
`
