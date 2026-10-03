package compaction

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
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
//
// It stands in for a summary, so it is held to budget tokens like one, keeping
// the newest messages. Unbounded, an archive of a history that filled the
// window could be nearly as large as the history, and a compaction that frees
// nothing leaves the request over the window.
func rawArchive(messages []chat.Message, budget int, estimator *agenttoken.Estimator) string {
	lines := make([]string, 0, len(messages))
	for i := range messages {
		if line := archiveLine(&messages[i]); line != "" {
			lines = append(lines, line)
		}
	}
	kept := len(lines)
	if budget > 0 && estimator != nil {
		used := 0
		for i := len(lines) - 1; i >= 0; i-- {
			used += estimator.EstimateString(lines[i])
			if used > budget && i < len(lines)-1 {
				kept = len(lines) - 1 - i
				break
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("Raw conversation archive (LLM summarization unavailable):\n\n")
	if omitted := len(lines) - kept; omitted > 0 {
		fmt.Fprintf(&sb, "[%d earlier messages omitted]\n", omitted)
	}
	for _, line := range lines[len(lines)-kept:] {
		sb.WriteString(line)
	}
	return sb.String()
}

func archiveLine(msg *chat.Message) string {
	switch msg.Role {
	case "user":
		return fmt.Sprintf("- User: %s\n", truncate(msg.Content, 500))
	case "assistant":
		if calls := serializeToolCalls(msg.ToolCalls); calls != "" {
			return fmt.Sprintf("- Assistant [%s]: %s\n", calls, truncate(msg.Content, 500))
		}
		return fmt.Sprintf("- Assistant: %s\n", truncate(msg.Content, 500))
	case "tool":
		return fmt.Sprintf("- Tool[%s]: %s\n", msg.Name, truncate(msg.Content, 500))
	}
	return ""
}
