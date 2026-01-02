package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// formatFileSize 파일 크기를 사람이 읽을 수 있는 형식으로 포맷
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

// formatDocSummary 표 표시를 위해 문서 요약 정리 및 자르기
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

// RecentDocInfo 최근 추가된 문서에 대한 간략한 정보
type RecentDocInfo struct {
	ChunkID             string
	KnowledgeBaseID     string
	KnowledgeID         string
	Title               string
	Description         string
	FileName            string
	FileSize            int64
	Type                string
	CreatedAt           string // 포맷된 시간 문자열
	FAQStandardQuestion string
	FAQSimilarQuestions []string
	FAQAnswers          []string
}

// SelectedDocumentInfo 사용자가 선택한 문서에 대한 요약 정보 (@ 멘션 통해)
// 메타데이터만 포함되며, 내용은 필요할 때 도구를 통해 가져옵니다
type SelectedDocumentInfo struct {
	KnowledgeID     string // 지식 ID
	KnowledgeBaseID string // 지식베이스 ID
	Title           string // 문서 제목
	FileName        string // 원본 파일 이름
	FileType        string // 파일 유형 (pdf, docx 등)
}

// KnowledgeBaseInfo 에이전트 프롬프트를 위한 지식베이스 필수 정보
type KnowledgeBaseInfo struct {
	ID          string
	Name        string
	Type        string // 지식베이스 유형: "document" 또는 "faq"
	Description string
	DocCount    int
	RecentDocs  []RecentDocInfo // 최근 추가된 문서 (최대 10개)
}

// PlaceholderDefinition UI/구성에 노출되는 플레이스홀더 정의
// Deprecated: types.PromptPlaceholder를 대신 사용하세요
type PlaceholderDefinition struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// AvailablePlaceholders UI 힌트를 위한 지원되는 모든 프롬프트 플레이스홀더 나열
// 에이전트 모드 전용 플레이스홀더 반환
func AvailablePlaceholders() []PlaceholderDefinition {
	// types 패키지의 중앙 집중식 플레이스홀더 정의 사용
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

// formatKnowledgeBaseList 프롬프트에 사용할 지식베이스 정보 포맷
func formatKnowledgeBaseList(kbInfos []*KnowledgeBaseInfo) string {
	if len(kbInfos) == 0 {
		return "None"
	}

	var builder strings.Builder
	builder.WriteString("\nThe following knowledge bases have been selected by the user for this conversation. ")
	builder.WriteString("You should search within these knowledge bases to find relevant information.\n\n")
	for i, kb := range kbInfos {
		// 지식베이스 이름 및 ID 표시
		builder.WriteString(fmt.Sprintf("%d. **%s** (knowledge_base_id: `%s`)\n", i+1, kb.Name, kb.ID))

		// 지식베이스 유형 표시
		kbType := kb.Type
		if kbType == "" {
			kbType = "document" // 기본 유형
		}
		builder.WriteString(fmt.Sprintf("   - Type: %s\n", kbType))

		if kb.Description != "" {
			builder.WriteString(fmt.Sprintf("   - Description: %s\n", kb.Description))
		}
		builder.WriteString(fmt.Sprintf("   - Document count: %d\n", kb.DocCount))

		// 사용 가능한 경우 최근 문서 표시
		// FAQ 유형 지식베이스의 경우 표시 형식 조정
		if len(kb.RecentDocs) > 0 {
			if kbType == "faq" {
				// FAQ 지식베이스: Q&A 쌍을 더 간결한 형식으로 표시
				builder.WriteString("   - Recent FAQ entries:\n\n")
				builder.WriteString("     | # | Question  | Answers | Chunk ID | Knowledge ID | Created At |\n")
				builder.WriteString("     |---|-------------------|---------|----------|--------------|------------|\n")
				for j, doc := range kb.RecentDocs {
					if j >= 10 { // 최대 10개 문서로 제한
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
				// 문서 지식베이스: 표준 형식으로 문서 표시
				builder.WriteString("   - Recently added documents:\n\n")
				builder.WriteString("     | # | Document Name | Type | Created At | Knowledge ID | File Size | Summary |\n")
				builder.WriteString("     |---|---------------|------|------------|--------------|----------|---------|\n")
				for j, doc := range kb.RecentDocs {
					if j >= 10 { // 최대 10개 문서로 제한
						break
					}
					docName := doc.Title
					if docName == "" {
						docName = doc.FileName
					}
					// 파일 크기 포맷
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

// renderPromptPlaceholders 프롬프트 템플릿의 플레이스홀더 렌더링
// 지원되는 플레이스홀더:
//   - {{knowledge_bases}} - 포맷된 지식베이스 목록으로 대체
func renderPromptPlaceholders(template string, knowledgeBases []*KnowledgeBaseInfo) string {
	result := template

	// {{knowledge_bases}} 플레이스홀더 대체
	if strings.Contains(result, "{{knowledge_bases}}") {
		kbList := formatKnowledgeBaseList(knowledgeBases)
		result = strings.ReplaceAll(result, "{{knowledge_bases}}", kbList)
	}

	return result
}

// formatSelectedDocuments 선택된 문서를 프롬프트용으로 포맷 (요약만, 내용 없음)
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

// renderPromptPlaceholdersWithStatus 웹 검색 상태를 포함하여 플레이스홀더 렌더링
// 지원되는 플레이스홀더:
//   - {{knowledge_bases}}
//   - {{web_search_status}} -> "Enabled" 또는 "Disabled"
//   - {{current_time}} -> 현재 시간 문자열
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

// BuildSystemPromptWithKB 지식베이스가 있는 점진적 RAG 시스템 프롬프트 빌드
// Deprecated: BuildSystemPrompt를 대신 사용하세요
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

// BuildSystemPromptWithoutWeb 웹 검색이 없는 점진적 RAG 시스템 프롬프트 빌드
// Deprecated: BuildSystemPrompt를 대신 사용하세요
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

// BuildPureAgentSystemPrompt Pure Agent 모드(KB 없음)를 위한 시스템 프롬프트 빌드
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
	// 빈 KB 목록 전달
	return renderPromptPlaceholdersWithStatus(template, []*KnowledgeBaseInfo{}, webSearchEnabled, currentTime)
}

// BuildSystemPrompt 점진적 RAG 시스템 프롬프트 빌드
// 이것이 주로 사용해야 할 함수입니다 - {{web_search_status}} 플레이스홀더를 통해 동적으로 적응하는 통합 템플릿을 사용합니다
func BuildSystemPrompt(
	knowledgeBases []*KnowledgeBaseInfo,
	webSearchEnabled bool,
	selectedDocs []*SelectedDocumentInfo,
	systemPromptTemplate ...string,
) string {
	var basePrompt string
	var template string

	// 사용할 템플릿 결정
	if len(systemPromptTemplate) > 0 && systemPromptTemplate[0] != "" {
		template = systemPromptTemplate[0]
	} else if len(knowledgeBases) == 0 {
		template = PureAgentSystemPrompt
	} else {
		template = ProgressiveRAGSystemPrompt
	}

	currentTime := time.Now().Format(time.RFC3339)
	basePrompt = renderPromptPlaceholdersWithStatus(template, knowledgeBases, webSearchEnabled, currentTime)

	// 선택된 문서 섹션이 있으면 추가
	if len(selectedDocs) > 0 {
		basePrompt += formatSelectedDocuments(selectedDocs)
	}

	return basePrompt
}

// PureAgentSystemPrompt는 Pure Agent 모드(지식베이스 없음)를 위한 시스템 프롬프트입니다
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

// ProgressiveRAGSystemPrompt는 통합 점진적 RAG 시스템 프롬프트 템플릿입니다
// 이 템플릿은 {{web_search_status}} 플레이스홀더를 통해 웹 검색 상태에 따라 동적으로 적응합니다
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

// ProgressiveRAGSystemPromptWithWeb은 더 이상 사용되지 않으며, ProgressiveRAGSystemPrompt를 대신 사용하세요
// 하위 호환성을 위해 유지됨
var ProgressiveRAGSystemPromptWithWeb = ProgressiveRAGSystemPrompt

// ProgressiveRAGSystemPromptWithoutWeb은 더 이상 사용되지 않으며, ProgressiveRAGSystemPrompt를 대신 사용하세요
// 하위 호환성을 위해 유지됨
var ProgressiveRAGSystemPromptWithoutWeb = ProgressiveRAGSystemPrompt
