package im

import "strings"

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
