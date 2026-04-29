package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/config"
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
	// Capabilities lists the retrieval surfaces this KB exposes. Any subset of
	// {"wiki", "chunks"}. "chunks" is present when the KB has vector and/or
	// keyword (BM25) indexing enabled. This is the *deterministic* source of
	// truth the agent should consult before picking a retrieval strategy —
	// significantly more reliable than running probing searches.
	Capabilities []string
	RecentDocs   []RecentDocInfo // Recently added documents (up to 10)
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

// formatKnowledgeBaseList formats knowledge base information as XML for the prompt
func formatKnowledgeBaseList(kbInfos []*KnowledgeBaseInfo) string {
	if len(kbInfos) == 0 {
		return "<knowledge_bases />"
	}

	var b strings.Builder
	b.WriteString("<knowledge_bases>\n")
	for _, kb := range kbInfos {
		kbType := kb.Type
		if kbType == "" {
			kbType = "document"
		}
		capsAttr := ""
		if len(kb.Capabilities) > 0 {
			capsAttr = fmt.Sprintf(" capabilities=\"%s\"", strings.Join(kb.Capabilities, ","))
		}
		b.WriteString(fmt.Sprintf("<knowledge_base id=\"%s\" name=\"%s\" type=\"%s\" doc_count=\"%d\"%s>\n",
			kb.ID, kb.Name, kbType, kb.DocCount, capsAttr))
		if kb.Description != "" {
			b.WriteString(fmt.Sprintf("<description>%s</description>\n", kb.Description))
		}

		if len(kb.RecentDocs) > 0 {
			if kbType == "faq" {
				b.WriteString("<faq_entries>\n")
				for j, doc := range kb.RecentDocs {
					if j >= 10 {
						break
					}
					question := doc.FAQStandardQuestion
					if question == "" {
						question = doc.FileName
					}
					b.WriteString(fmt.Sprintf("<faq chunk_id=\"%s\" knowledge_id=\"%s\" created_at=\"%s\">\n",
						doc.ChunkID, doc.KnowledgeID, doc.CreatedAt))
					b.WriteString(fmt.Sprintf("<question>%s</question>\n", question))
					if len(doc.FAQAnswers) > 0 {
						for _, ans := range doc.FAQAnswers {
							b.WriteString(fmt.Sprintf("<answer>%s</answer>\n", ans))
						}
					}
					b.WriteString("</faq>\n")
				}
				b.WriteString("</faq_entries>\n")
			} else {
				b.WriteString("<recent_documents>\n")
				for j, doc := range kb.RecentDocs {
					if j >= 10 {
						break
					}
					docName := doc.Title
					if docName == "" {
						docName = doc.FileName
					}
					fileSize := formatFileSize(doc.FileSize)
					b.WriteString(fmt.Sprintf("<document knowledge_id=\"%s\" type=\"%s\" file_size=\"%s\" created_at=\"%s\">\n",
						doc.KnowledgeID, doc.Type, fileSize, doc.CreatedAt))
					b.WriteString(fmt.Sprintf("<name>%s</name>\n", docName))
					if doc.Description != "" {
						summary := formatDocSummary(doc.Description, 120)
						b.WriteString(fmt.Sprintf("<summary>%s</summary>\n", summary))
					}
					b.WriteString("</document>\n")
				}
				b.WriteString("</recent_documents>\n")
			}
		}
		b.WriteString("</knowledge_base>\n")
	}
	b.WriteString("</knowledge_bases>")
	return b.String()
}

// renderPromptPlaceholders renders placeholders in the prompt template.
//
// Supported placeholders:
//   - {{knowledge_bases}} - Historically expanded to the full bound-KB XML
//     block. Since that block now lives in the user message's
//     `<runtime_context>` (see observe.buildRuntimeContextBlock), the
//     placeholder is expanded to a short pointer so legacy / custom
//     templates that still reference `{{knowledge_bases}}` degrade
//     gracefully instead of dumping the detail twice.
func renderPromptPlaceholders(template string, knowledgeBases []*KnowledgeBaseInfo) string {
	result := template

	if strings.Contains(result, "{{knowledge_bases}}") {
		var replacement string
		if len(knowledgeBases) == 0 {
			replacement = "(no knowledge bases bound to this session)"
		} else {
			replacement = "(see `<bound_knowledge_bases>` inside the user message's `<runtime_context>` for the current bound KB list and their capabilities)"
		}
		result = strings.ReplaceAll(result, "{{knowledge_bases}}", replacement)
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
//   - {{language}} -> user language name (e.g. "Chinese (Simplified)", "English")
//   - {{skills}} -> formatted skills metadata (if any)
func renderPromptPlaceholdersWithStatus(
	template string,
	knowledgeBases []*KnowledgeBaseInfo,
	webSearchEnabled bool,
	currentTime string,
	language string,
) string {
	// Knowledge bases need special formatting, so handle it first
	result := renderPromptPlaceholders(template, knowledgeBases)

	status := "Disabled"
	if webSearchEnabled {
		status = "Enabled"
	}

	result = types.RenderPromptPlaceholders(result, types.PlaceholderValues{
		"web_search_status": status,
		"current_time":      currentTime,
		"language":          language,
		"skills":            "", // Remove {{skills}} placeholder; skills are appended separately if present
	})
	return result
}

// BuildSystemPromptOptions contains optional parameters for BuildSystemPrompt
type BuildSystemPromptOptions struct {
	SkillsMetadata []*skills.SkillMetadata
	Language       string         // User language name for {{language}} placeholder (e.g. "Chinese (Simplified)")
	Config         *config.Config // Config for reading prompt templates; nil falls back to hardcoded defaults
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
		var cfg *config.Config
		if options != nil {
			cfg = options.Config
		}
		template = GetPureAgentSystemPrompt(cfg)
	} else {
		var cfg *config.Config
		if options != nil {
			cfg = options.Config
		}
		template = GetProgressiveRAGSystemPrompt(cfg)
	}

	currentTime := time.Now().Format(time.RFC3339)
	language := ""
	if options != nil {
		language = options.Language
	}
	basePrompt = renderPromptPlaceholdersWithStatus(template, knowledgeBases, webSearchEnabled, currentTime, language)

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

// GetPureAgentSystemPrompt returns the Pure Agent system prompt from config templates.
// The template must be defined in config/prompt_templates/agent_system_prompt.yaml
// with mode "pure". Returns empty string if config is nil or template not found.
func GetPureAgentSystemPrompt(cfg *config.Config) string {
	if cfg != nil && cfg.PromptTemplates != nil {
		if t := config.DefaultTemplateByMode(cfg.PromptTemplates.AgentSystemPrompt, "pure"); t != nil && t.Content != "" {
			return t.Content
		}
	}
	return ""
}

// GetProgressiveRAGSystemPrompt returns the Progressive RAG Agent system prompt from config templates.
// The template must be defined in config/prompt_templates/agent_system_prompt.yaml
// with mode "rag". Returns empty string if config is nil or template not found.
func GetProgressiveRAGSystemPrompt(cfg *config.Config) string {
	if cfg != nil && cfg.PromptTemplates != nil {
		if t := config.DefaultTemplateByMode(cfg.PromptTemplates.AgentSystemPrompt, "rag"); t != nil && t.Content != "" {
			return t.Content
		}
	}
	return ""
}
