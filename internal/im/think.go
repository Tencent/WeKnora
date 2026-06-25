package im

import (
	"regexp"
	"strings"
)

// StreamDisplayPhase controls how raw IM stream content is rendered.
type StreamDisplayPhase int

const (
	// StreamDisplayIntermediate shows in-progress thinking/tools (Web: expanded progress).
	StreamDisplayIntermediate StreamDisplayPhase = iota
	// StreamDisplayFinal shows answer-only text (Web: collapsed/hidden intermediate steps).
	StreamDisplayFinal
)

var (
	thinkBlockRe    = regexp.MustCompile(`(?s)<think>.*?</think>`)
	thinkOpenTailRe = regexp.MustCompile(`(?s)<think>.*$`)
)

// ragPipelineToolNames mirrors frontend RAG_PIPELINE_TOOL_NAMES.
var ragPipelineToolNames = map[string]bool{
	"query_understand": true,
	"knowledge_search": true,
}

// IsRAGPipelineToolName reports whether a tool is part of the quick-QA progress UI.
func IsRAGPipelineToolName(name string) bool {
	return ragPipelineToolNames[name]
}

// StripThinkBlocks removes <think> blocks for final IM display.
func StripThinkBlocks(content string) string {
	if content == "" {
		return ""
	}
	cleaned := thinkBlockRe.ReplaceAllString(content, "")
	cleaned = thinkOpenTailRe.ReplaceAllString(cleaned, "")
	return trimIMOuterWhitespace(cleaned)
}

// IMStreamMode distinguishes agent reasoning from quick-QA RAG pipeline display.
type IMStreamMode int

const (
	IMStreamModeAgent IMStreamMode = iota
	IMStreamModeQuickQA
)

// IMStreamParts separates stream content for IM display.
type IMStreamParts struct {
	Mode IMStreamMode

	// Quick-QA (RagPipelineProgress on Web):
	PipelineToolSteps []IMToolStep // query_understand / knowledge_search rows
	ReasoningInner    string       // model reasoning_content — separate "思考" section

	// Agent (AgentStreamDisplay on Web):
	AgentInner     string       // retracted preambles + thoughts ("思考过程")
	AgentToolSteps []IMToolStep // tool progress lines (one row per tool_call_id)
	LiveAnswer     string       // optimistic answer stream before tools retract it

	Answer string // final answer (complete / knowledge QA)
}

func agentThinkContent(parts IMStreamParts) string {
	toolLines := renderIMToolSteps(parts.AgentToolSteps, FormatIMToolLine)
	return mergeIMNarrativeAndTools(parts.AgentInner, toolLines)
}

func quickQAPipelineContent(parts IMStreamParts) string {
	return renderIMToolSteps(parts.PipelineToolSteps, FormatIMRagPipelineLine)
}

// RAGThinkingStyle matches Web RagPipelineProgress thinking row (agent.think).
var RAGThinkingStyle = ThinkBlockStyle{
	ThinkingHeader: "> 💭 **思考中...**\n",
	ThoughtHeader:  "> 💭 **思考**\n",
	LinePrefix:     "> ",
	LineSuffix:     "",
	Separator:      "\n---\n\n",
}

// WrapThinkBlock wraps inner content with think tags for formatting.
func WrapThinkBlock(inner string) string {
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return ""
	}
	return "<think>\n" + inner + "\n</think>"
}

// BuildIMAgentStreamRaw assembles agent-mode raw content.
func BuildIMAgentStreamRaw(parts IMStreamParts, agentInProgress bool) string {
	thinkTagged := WrapThinkBlock(agentThinkContent(parts))
	if agentInProgress || strings.TrimSpace(parts.Answer) == "" {
		return thinkTagged
	}
	if thinkTagged == "" {
		return parts.Answer
	}
	return thinkTagged + "\n\n" + parts.Answer
}

// formatIMAgentIntermediate mirrors Web AgentStreamDisplay:
// stream answer text first; when tools run, retract into "思考过程";
// once tools exist, keep the think block visible above the streaming answer.
func formatIMAgentIntermediate(parts IMStreamParts) string {
	think := strings.TrimSpace(agentThinkContent(parts))
	live := strings.TrimSpace(parts.LiveAnswer)

	var sections []string
	if think != "" {
		sections = append(sections, FormatIMDisplayContent(WrapThinkBlock(think), StreamDisplayIntermediate))
	}
	if live != "" {
		sections = append(sections, live)
	}
	return strings.Join(sections, MarkdownThinkStyle.Separator)
}

// formatIMQuickQAIntermediate mirrors Web RagPipelineProgress:
// pipeline steps as plain lines, model reasoning in a separate "思考" block,
// and once answer starts streaming the progress collapses to answer-only preview.
func formatIMQuickQAIntermediate(parts IMStreamParts) string {
	if answer := strings.TrimSpace(parts.Answer); answer != "" {
		return answer
	}

	var sections []string
	if p := strings.TrimSpace(quickQAPipelineContent(parts)); p != "" {
		sections = append(sections, p)
	}
	if r := strings.TrimSpace(parts.ReasoningInner); r != "" {
		sections = append(sections, TransformThinkBlocks(WrapThinkBlock(r), RAGThinkingStyle))
	}
	return strings.Join(sections, "\n\n")
}

// FormatIMIntermediateFromParts formats in-progress IM stream display.
func FormatIMIntermediateFromParts(parts IMStreamParts, agentInProgress bool) string {
	if parts.Mode == IMStreamModeQuickQA {
		return formatIMQuickQAIntermediate(parts)
	}
	_ = agentInProgress
	return formatIMAgentIntermediate(parts)
}

// FormatIMFinalFromParts returns the final replace frame (answer-only for all modes).
func FormatIMFinalFromParts(parts IMStreamParts) string {
	answer := strings.TrimSpace(parts.Answer)
	if answer != "" {
		return answer
	}
	// Fallback for embedded think tags in non-agent answers.
	return FormatIMDisplayContent(BuildIMAgentStreamRaw(parts, false), StreamDisplayFinal)
}

// FormatIMDisplayContent formats raw stream content for the given display phase.
func FormatIMDisplayContent(raw string, phase StreamDisplayPhase) string {
	switch phase {
	case StreamDisplayFinal:
		return StripThinkBlocks(raw)
	default:
		return TransformThinkBlocks(raw, MarkdownThinkStyle)
	}
}

func trimIMOuterWhitespace(s string) string {
	return strings.TrimSpace(s)
}

// ThinkBlockStyle controls how <think>...</think> blocks are rendered.
type ThinkBlockStyle struct {
	// ThinkingHeader is shown when the think block is still in-progress (no closing tag yet).
	ThinkingHeader string // e.g. "> 💭 **思考中...**\n" or "💭 _思考中..._\n"
	// ThoughtHeader is shown before the think block content.
	ThoughtHeader string // e.g. "> 💭 **思考过程**\n" or "💭 *思考过程*\n"
	// LinePrefix is prepended to each line of think content.
	LinePrefix string // e.g. "> " or "> _"
	// LineSuffix is appended to each line of think content (before newline).
	LineSuffix string // e.g. "" or "_"
	// Separator is inserted between the think block and the rest of the content.
	Separator string // e.g. "\n---\n\n" or "\n"
}

// MarkdownThinkStyle renders think blocks as markdown blockquotes.
// Used by DingTalk and Feishu.
var MarkdownThinkStyle = ThinkBlockStyle{
	ThinkingHeader: "> 💭 **思考中...**\n",
	ThoughtHeader:  "> 💭 **思考过程**\n",
	LinePrefix:     "> ",
	LineSuffix:     "",
	Separator:      "\n---\n\n",
}

// TelegramThinkStyle renders think blocks as blockquotes for Telegram.
// Uses the same blockquote format as other platforms for reliable rendering
// during streaming (where incomplete markdown can cause API failures).
var TelegramThinkStyle = ThinkBlockStyle{
	ThinkingHeader: "> 💭 *思考中...*\n",
	ThoughtHeader:  "> 💭 *思考过程*\n",
	LinePrefix:     "> ",
	LineSuffix:     "",
	Separator:      "\n---\n\n",
}

// TransformThinkBlocks converts <think>...</think> blocks using the given style.
// Handles both complete blocks and in-progress blocks (where </think> has not
// yet arrived during streaming).
func TransformThinkBlocks(content string, style ThinkBlockStyle) string {
	const (
		openTag  = "<think>"
		closeTag = "</think>"
	)

	openIdx := strings.Index(content, openTag)
	if openIdx < 0 {
		return content
	}

	before := content[:openIdx]
	after := content[openIdx+len(openTag):]

	closeIdx := strings.Index(after, closeTag)
	thinkClosed := closeIdx >= 0

	var thinkContent, rest string
	if thinkClosed {
		thinkContent = after[:closeIdx]
		rest = after[closeIdx+len(closeTag):]
	} else {
		thinkContent = after
	}

	thinkContent = strings.TrimSpace(thinkContent)

	var result strings.Builder
	result.WriteString(before)

	if thinkContent == "" {
		if !thinkClosed {
			result.WriteString(style.ThinkingHeader)
			return result.String()
		}
		result.WriteString(strings.TrimLeft(rest, "\n"))
		return result.String()
	}

	result.WriteString(style.ThoughtHeader)
	for _, line := range strings.Split(thinkContent, "\n") {
		result.WriteString(style.LinePrefix)
		result.WriteString(line)
		result.WriteString(style.LineSuffix)
		result.WriteString("\n")
	}

	if thinkClosed {
		rest = strings.TrimLeft(rest, "\n")
		if rest != "" {
			result.WriteString(style.Separator)
			result.WriteString(rest)
		}
	}

	return result.String()
}
