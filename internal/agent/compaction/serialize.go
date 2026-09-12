package compaction

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
)

const (
	// toolResultMaxChars caps a single tool result inside the summarization
	// request. Tool output is the largest contributor to context size and the
	// summary needs its gist, not its bytes.
	toolResultMaxChars = 2000
	// textMaxChars caps user and assistant prose, which is rarely the problem
	// but should not be unbounded either.
	textMaxChars = 4000
	// toolArgsMaxChars caps rendered tool-call arguments. A write_sandbox_file
	// call carries an entire file body in its arguments; the summary needs the
	// path and the fact of the write, never the content.
	toolArgsMaxChars = 400
)

// serializeConversation renders messages as a transcript rather than passing
// them as a conversation. A model handed real messages tries to continue them;
// handed a transcript, it summarizes them.
func serializeConversation(messages []chat.Message) string {
	var parts []string
	for i := range messages {
		msg := &messages[i]
		switch msg.Role {
		case "system":
			continue
		case "user":
			if content := truncate(msg.Content, textMaxChars); content != "" {
				parts = append(parts, "[User]: "+content)
			}
		case "assistant":
			if msg.ReasoningContent != "" {
				parts = append(parts,
					"[Assistant thinking]: "+truncate(msg.ReasoningContent, textMaxChars))
			}
			if msg.Content != "" {
				parts = append(parts, "[Assistant]: "+truncate(msg.Content, textMaxChars))
			}
			if calls := serializeToolCalls(msg.ToolCalls); calls != "" {
				parts = append(parts, "[Assistant tool calls]: "+calls)
			}
		case "tool":
			if content := truncate(msg.Content, toolResultMaxChars); content != "" {
				parts = append(parts, fmt.Sprintf("[Tool result %s]: %s", msg.Name, content))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func serializeToolCalls(calls []chat.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	rendered := make([]string, 0, len(calls))
	for _, tc := range calls {
		rendered = append(rendered,
			fmt.Sprintf("%s(%s)", tc.Function.Name, renderToolArgs(tc.Function.Arguments)))
	}
	return strings.Join(rendered, "; ")
}

// renderToolArgs turns an arguments JSON blob into `key=value` pairs, dropping
// oversized values. Keys are sorted so the same call always renders the same
// way, which matters when the transcript is compared across compactions.
func renderToolArgs(arguments string) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return truncate(arguments, toolArgsMaxChars)
	}
	keys := make([]string, 0, len(parsed))
	for k := range parsed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		encoded, err := json.Marshal(parsed[k])
		if err != nil {
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, truncate(string(encoded), toolArgsMaxChars)))
	}
	return strings.Join(pairs, ", ")
}

func truncate(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return fmt.Sprintf("%s\n\n[... %d more characters truncated]",
		string(runes[:maxChars]), len(runes)-maxChars)
}

// rawArchive is the fallback when the summarizer is unavailable. It is lossy
// and unstructured, but it keeps the tool names and paths that the next round
// needs in order not to redo finished work.
func rawArchive(messages []chat.Message) string {
	var sb strings.Builder
	sb.WriteString("Raw conversation archive (LLM summarization unavailable):\n\n")
	for i := range messages {
		msg := &messages[i]
		switch msg.Role {
		case "user":
			fmt.Fprintf(&sb, "- User: %s\n", truncate(msg.Content, 500))
		case "assistant":
			if calls := serializeToolCalls(msg.ToolCalls); calls != "" {
				fmt.Fprintf(&sb, "- Assistant [%s]: %s\n", calls, truncate(msg.Content, 500))
				continue
			}
			fmt.Fprintf(&sb, "- Assistant: %s\n", truncate(msg.Content, 500))
		case "tool":
			fmt.Fprintf(&sb, "- Tool[%s]: %s\n", msg.Name, truncate(msg.Content, 500))
		}
	}
	return sb.String()
}
