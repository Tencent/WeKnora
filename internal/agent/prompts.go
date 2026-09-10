package agent

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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

// SelectedDocumentInfo contains summary information about a user-selected document (via @ mention).
// Injected into the user message runtime_context (pinned_documents); content is fetched via tools.
type SelectedDocumentInfo struct {
	KnowledgeID     string // Knowledge ID
	KnowledgeBaseID string // Knowledge base ID
	Title           string // Document title
	FileName        string // Original file name
	FileType        string // File type (pdf, docx, etc.)
}

// PinnedMCPServiceInfo describes an MCP service explicitly @mentioned for this turn.
type PinnedMCPServiceInfo struct {
	Discoverable bool // Available through the scoped MCP directory.
	ID           string
	Name         string
	Description  string
	ToolNames    []string // Registered tool.function names for this service (mcp_{service}_{tool})
}

// PinnedSkillInfo describes a skill explicitly @mentioned for this turn.
type PinnedSkillInfo struct {
	Name        string
	Description string
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

// Runtime-catalog budgeting (#3158): the bound-KB block is part of the user
// message that every LLM call of a turn re-sends, so an uncapped catalog — a
// kb=all agent over a multi-KB tenant — exceeds the context window before the
// first response and burns that window again on every round of a turn that can
// never succeed. The budget is measured in runes (≈ characters, not bytes;
// names and descriptions are commonly CJK) and degrades uniformly for every
// bound KB, in the order that hurts routing least: FAQ answers go first
// (questions are the routing signal, answers are retrievable content), then
// per-KB document entries and descriptions; the one-line identity row
// (id/name/type/doc_count) is the last thing to go.
const (
	// maxRuntimeCatalogRunes caps the rendered <knowledge_bases> block:
	// roughly four full-detail KBs, or ~60 identity rows.
	maxRuntimeCatalogRunes = 8192

	// kbNameMaxRunes caps one KB or document name wherever it renders.
	kbNameMaxRunes = 80

	// kbDescriptionMaxRunes caps one KB description wherever it renders.
	kbDescriptionMaxRunes = 200

	// faqAnswerMaxRunes caps one FAQ answer; the full answer remains
	// retrievable through the FAQ tools.
	faqAnswerMaxRunes = 200
)

// catalogDetailTier selects how much per-KB detail the catalog carries. The
// whole catalog renders at one tier so the model sees a uniform shape for
// every bound KB.
type catalogDetailTier int

const (
	// catalogTierFull renders descriptions, recent documents and FAQ
	// questions with answers, each item under its own rune cap.
	catalogTierFull catalogDetailTier = iota
	// catalogTierNoFAQAnswers drops FAQ answers but keeps the questions.
	catalogTierNoFAQAnswers
	// catalogTierIdentity keeps only the one-line identity row per KB.
	catalogTierIdentity
)

// formatKnowledgeBaseList formats knowledge base information as XML for the
// prompt, within the runtime-catalog rune budget.
func formatKnowledgeBaseList(kbInfos []*KnowledgeBaseInfo) string {
	if len(kbInfos) == 0 {
		return "<knowledge_bases />"
	}
	// The identity tier's renderer caps its own row list, so it always fits;
	// richer tiers are returned only when they fit whole.
	for _, tier := range []catalogDetailTier{catalogTierFull, catalogTierNoFAQAnswers} {
		if rendered := renderKBCatalog(kbInfos, tier); utf8.RuneCountInString(rendered) <= maxRuntimeCatalogRunes {
			return rendered
		}
	}
	return renderKBCatalog(kbInfos, catalogTierIdentity)
}

// renderKBCatalog renders every bound KB at the given detail tier.
func renderKBCatalog(kbInfos []*KnowledgeBaseInfo, tier catalogDetailTier) string {
	var b strings.Builder
	b.WriteString("<knowledge_bases>\n")
	if tier == catalogTierIdentity {
		renderKBCatalogIdentityRows(&b, kbInfos)
	} else {
		for _, kb := range kbInfos {
			renderKnowledgeBaseEntry(&b, kb, tier)
		}
	}
	b.WriteString("</knowledge_bases>")
	return b.String()
}

// kbIdentityLine renders the one identity row every tier depends on for
// routing: id, name, type, document count and capabilities. The row is
// rendered without its closing token: richer tiers append ">\n" and nested
// content, the identity tier appends " />\n".
func kbIdentityLine(kb *KnowledgeBaseInfo) string {
	kbType := kb.Type
	if kbType == "" {
		kbType = "document"
	}
	capsAttr := ""
	if len(kb.Capabilities) > 0 {
		capsAttr = fmt.Sprintf(" capabilities=\"%s\"", escapeXMLAttr(strings.Join(kb.Capabilities, ",")))
	}
	return fmt.Sprintf("<knowledge_base id=\"%s\" name=\"%s\" type=\"%s\" doc_count=\"%d\"%s",
		escapeXMLAttr(kb.ID),
		escapeXMLAttr(truncateRunes(kb.Name, kbNameMaxRunes)),
		escapeXMLAttr(kbType),
		kb.DocCount,
		capsAttr)
}

// identityNoteAllowance reserves room for the trailing omitted-count note so
// the note's own cost never pushes the block past the budget. It covers notes
// naming up to 999 omitted KBs.
const identityNoteAllowance = 96

// renderKBCatalogIdentityRows emits one self-closing identity row per KB,
// stopping when the budget is exhausted and summarizing the remainder in a
// trailing note. The omitted KBs stay registered in the request's handle
// table, so id-bearing tool calls against them still resolve.
func renderKBCatalogIdentityRows(b *strings.Builder, kbInfos []*KnowledgeBaseInfo) {
	remaining := maxRuntimeCatalogRunes - len("<knowledge_bases>\n") - len("</knowledge_bases>") - identityNoteAllowance
	omitted := 0
	for _, kb := range kbInfos {
		line := kbIdentityLine(kb) + " />\n"
		n := utf8.RuneCountInString(line)
		if n > remaining {
			omitted++
			continue
		}
		remaining -= n
		b.WriteString(line)
	}
	if omitted > 0 {
		b.WriteString(fmt.Sprintf(
			"<note>%d more knowledge bases are bound for this turn but not listed.</note>\n",
			omitted))
	}
}

func renderKnowledgeBaseEntry(b *strings.Builder, kb *KnowledgeBaseInfo, tier catalogDetailTier) {
	b.WriteString(kbIdentityLine(kb) + ">\n")
	if kb.Description != "" {
		b.WriteString(fmt.Sprintf("<description>%s</description>\n",
			escapeXMLAttr(formatDocSummary(kb.Description, kbDescriptionMaxRunes))))
	}
	if len(kb.RecentDocs) == 0 {
		b.WriteString("</knowledge_base>\n")
		return
	}
	kbType := kb.Type
	if kbType == "" {
		kbType = "document"
	}
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
				escapeXMLAttr(doc.ChunkID), escapeXMLAttr(doc.KnowledgeID), escapeXMLAttr(doc.CreatedAt)))
			b.WriteString(fmt.Sprintf("<question>%s</question>\n", escapeXMLAttr(question)))
			if tier == catalogTierFull {
				for _, ans := range doc.FAQAnswers {
					b.WriteString(fmt.Sprintf("<answer>%s</answer>\n",
						escapeXMLAttr(formatDocSummary(ans, faqAnswerMaxRunes))))
				}
			}
			b.WriteString("</faq>\n")
		}
		b.WriteString("</faq_entries>\n")
	} else {
		b.WriteString("<recent_documents>\n")
		for j, doc := range kb.RecentDocs {
			if j >= 2 {
				break
			}
			docName := doc.Title
			if docName == "" {
				docName = doc.FileName
			}
			fileSize := formatFileSize(doc.FileSize)
			b.WriteString(fmt.Sprintf("<document knowledge_id=\"%s\" type=\"%s\" file_size=\"%s\" created_at=\"%s\">\n",
				escapeXMLAttr(doc.KnowledgeID), escapeXMLAttr(doc.Type),
				escapeXMLAttr(fileSize), escapeXMLAttr(doc.CreatedAt)))
			b.WriteString(fmt.Sprintf("<name>%s</name>\n", escapeXMLAttr(truncateRunes(docName, kbNameMaxRunes))))
			if doc.Description != "" {
				b.WriteString(fmt.Sprintf("<summary>%s</summary>\n",
					escapeXMLAttr(formatDocSummary(doc.Description, 120))))
			}
			b.WriteString("</document>\n")
		}
		b.WriteString("</recent_documents>\n")
	}
	b.WriteString("</knowledge_base>\n")
}

// truncateRunes shortens s to at most maxRunes characters (never bytes —
// names are commonly multi-byte), appending an ellipsis when truncated.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
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
//   - `<must_use>` is NOT a placeholder — when the user @mentions MCP/Skill,
//     observe.buildMustUseBlock injects it as a sibling block in the user
//     message; system prompts document it by convention (see agent_system_prompt.yaml).
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
func formatSkillsMetadata(skillsMetadata []*skills.SkillMetadata, shellExecEnabled bool) string {
	if len(skillsMetadata) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nAvailable skills: read a relevant skill's listed SKILL.md resource with read_file before applying it. Load additional files only as needed; the returned file list already identifies bundled scripts.\n")
	for _, skill := range skillsMetadata {
		if skill != nil {
			fmt.Fprintf(&b, "- %s: %s (read_file path=%q)\n", skill.Name, skill.Description, "skill://"+skill.Name+"/SKILL.md")
		}
	}
	return b.String()
}

// formatToolGuidance uses the actual registry, so disabled capabilities never
// leak into the runtime instructions. Mechanics and limits live in tool schemas.
func formatToolGuidance(names []string) string {
	if len(names) == 0 {
		return ""
	}
	has := func(name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	b.WriteString("\n\nTool execution: use only the tools provided for this turn. Plan internally; use a planning tool only when it helps. Read known paths directly. Batch independent reads; keep dependent operations in order. Inspect results before claiming completion.\n")
	b.WriteString("For long-running operations, prefer a documented asynchronous mode when available. Use the returned task ID to wait or poll at the recommended interval and retrieve the completed result; after a timeout, check the existing task before resubmitting.\n")
	b.WriteString("On failure, use the reported cause to correct the input or environment. Retry only after something relevant changes. Permission, policy, or missing-configuration failures are not fixed by switching tools; report the concrete blocker if it cannot be corrected within this session.\n")
	if has("read_file") {
		b.WriteString("Use read_file for workspace files, saved web:// pages and listed skill:// resources. " +
			"In older instructions, translate read_skill(skill_name, file_path) to " +
			"read_file(path=skill://<name>/<file_path or SKILL.md>) and read_sandbox_file to read_file.\n")
	}
	if has("shell_exec") || has("write_sandbox_file") {
		b.WriteString("Session workspace: /workspace. Preserve uploaded originals in /workspace/input. " +
			"/workspace/output is the only directory collected for download, " +
			"so it takes finished deliverables only; " +
			"keep drafts and intermediate files in another directory under /workspace. " +
			"Commands start from their specified working directory on every call. " +
			"Files and installed packages persist within the session.\n")
		b.WriteString(sandboxArtifactReferenceGuidance())
	}
	if has("shell_exec") && has("read_file") {
		b.WriteString("For listed skills, run bundled scripts and your own scripts with " +
			"shell_exec(skill_name=..., command=...). This selects an installed skill's runtime " +
			"or stages host skill resources, and applies scoped credentials; " +
			"use $WEKNORA_SKILL_DIR for bundled files.\n")
		b.WriteString("In older instructions, translate execute_skill_script(skill_name, script_path, ...) to shell_exec(skill_name=..., command=...).\n")
	}

	return b.String()
}

// sandboxArtifactReferenceGuidance tells the model how to point at a file it
// generated in the sandbox from its final answer.
//
// Without this, models improvise a Markdown image with the bare file name
// (`![评分](市场画像评分.html)`), which the browser cannot resolve — the answer
// renders a broken image icon. The `sandbox:` prefix makes the intent explicit
// so the server can bind the name to the artifact index it hands the client.
func sandboxArtifactReferenceGuidance() string {
	var builder strings.Builder
	builder.WriteString("  - Include key generated deliverables in your final answer as ")
	builder.WriteString("`![description](sandbox:<file name>)` using the exact file name and no directory path\n")
	builder.WriteString("    - Images render inline; charts, tables, and documents ")
	builder.WriteString("render as a card the user clicks to preview\n")
	builder.WriteString("    - Never reference a sandbox path (`/workspace/output/...`) ")
	builder.WriteString("or a bare file name directly — neither resolves in the browser\n")
	builder.WriteString("    - Prefer output file names without spaces or parentheses; ")
	builder.WriteString("they keep the reference unambiguous\n")
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
	SelectedTools    []string
	SkillsMetadata   []*skills.SkillMetadata
	ShellExecEnabled bool
	Language         string         // User language name for {{language}} placeholder (e.g. "Chinese (Simplified)")
	Config           *config.Config // Config for reading prompt templates; nil falls back to hardcoded defaults
}

// BuildSystemPrompt builds the progressive RAG system prompt
// This is the main function to use - it uses a unified template with dynamic web search status
func BuildSystemPrompt(
	knowledgeBases []*KnowledgeBaseInfo,
	webSearchEnabled bool,
	systemPromptTemplate ...string,
) string {
	return BuildSystemPromptWithOptions(knowledgeBases, webSearchEnabled, nil, systemPromptTemplate...)
}

// BuildSystemPromptWithOptions builds the system prompt with additional options like skills
func BuildSystemPromptWithOptions(
	knowledgeBases []*KnowledgeBaseInfo,
	webSearchEnabled bool,
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

	currentTime := time.Now().Format("2006-01-02")
	language := ""
	if options != nil {
		language = options.Language
	}
	basePrompt = renderPromptPlaceholdersWithStatus(template, knowledgeBases, webSearchEnabled, currentTime, language)
	basePrompt += "\n\n" + steerGuidance

	if options != nil {
		basePrompt += formatGroundingGuidance(options.SelectedTools)
		basePrompt += formatToolGuidance(options.SelectedTools)
	} else {
		basePrompt += formatGroundingGuidance(nil)
	}

	// Append skills metadata if available (Level 1 - Progressive Disclosure)
	if options != nil && len(options.SkillsMetadata) > 0 {
		basePrompt += formatSkillsMetadata(options.SkillsMetadata, options.ShellExecEnabled)
	}

	return basePrompt
}

// Apply to custom prompts too: mid-run delivery is a harness capability.
const steerGuidance = "<steering_guidance>\n" +
	"Messages in <steer_message> guide the task in progress. Apply them in context; " +
	"respond briefly when appropriate, then continue unfinished work. Preserve unfinished objectives, " +
	"accepted constraints and useful tool results unless explicitly changed. " +
	"Acknowledging guidance alone does not complete the task. Follow explicit cancellation or replacement requests. " +
	"Hide delivery tags. Untagged subsequent requests are ordinary user messages.\n</steering_guidance>"

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
