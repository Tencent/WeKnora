// leaks.go reports durable identifiers that survive encoding. The registry
// compacts every identifier it knows about, but a raw UUID can still reach the
// model through text it never registered: an error string, a slug, a tool
// result of unknown shape. Each such occurrence is a place where the text
// codec is doing work that the producer should have done. Reporting them at
// the model-call boundary is how those producers are found and fixed, and it
// is the precondition for eventually retiring text-level compaction.

package modelcontext

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
)

var uuidShapeRE = regexp.MustCompile(
	`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`,
)

// IdentifierLeak is one message field that still carries UUID-shaped values
// after EncodeMessages.
type IdentifierLeak struct {
	MessageIndex int
	Role         string
	// ToolName is the tool that produced a tool message or the tool an
	// assistant call targets; empty for plain content.
	ToolName string
	// Field names the message part: content, reasoning, multi_content, or
	// tool_call_arguments.
	Field string
	// Registered holds leaked values the registry knows a handle for: the
	// codec missed a rewrite site. Unregistered holds values that never
	// entered the registry: the producer emitted a raw identifier.
	Registered   []string
	Unregistered []string
}

// LeakedIdentifiers scans an encoded message list for UUID-shaped values.
// User-authored messages are skipped: a user may legitimately paste an ID.
// Everything else the application composed, so any UUID there is a leak.
func (r *Registry) LeakedIdentifiers(messages []chat.Message) []IdentifierLeak {
	if r == nil {
		return nil
	}
	var leaks []IdentifierLeak
	for i := range messages {
		msg := &messages[i]
		if msg.Role == "user" {
			continue
		}
		leaks = r.appendLeak(leaks, i, msg.Role, msg.Name, "content", msg.Content)
		leaks = r.appendLeak(leaks, i, msg.Role, msg.Name, "reasoning", msg.ReasoningContent)
		for _, part := range msg.MultiContent {
			if part.Type == "text" {
				leaks = r.appendLeak(leaks, i, msg.Role, msg.Name, "multi_content", part.Text)
			}
		}
		for _, call := range msg.ToolCalls {
			leaks = r.appendLeak(
				leaks, i, msg.Role, call.Function.Name, "tool_call_arguments", call.Function.Arguments,
			)
		}
	}
	return leaks
}

func (r *Registry) appendLeak(
	leaks []IdentifierLeak, index int, role, toolName, field, text string,
) []IdentifierLeak {
	if text == "" {
		return leaks
	}
	matches := uuidShapeRE.FindAllString(text, -1)
	if len(matches) == 0 {
		return leaks
	}
	seen := make(map[string]struct{}, len(matches))
	leak := IdentifierLeak{MessageIndex: index, Role: role, ToolName: toolName, Field: field}
	for _, match := range matches {
		key := strings.ToLower(match)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if r.knowsDurable(match) {
			leak.Registered = append(leak.Registered, match)
		} else {
			leak.Unregistered = append(leak.Unregistered, match)
		}
	}
	sort.Strings(leak.Registered)
	sort.Strings(leak.Unregistered)
	return append(leaks, leak)
}

// knowsDurable reports whether any handle space has registered the value.
func (r *Registry) knowsDurable(value string) bool {
	if r.sources != nil && r.sources.handleForDurable(value) != "" {
		return true
	}
	if _, ok := r.issues.Handle(value); ok {
		return true
	}
	if _, ok := r.mcpServers.Handle(value); ok {
		return true
	}
	_, ok := r.mcpTools.Handle(value)
	return ok
}

// maxLeakSamples bounds how many identifiers one summary line quotes.
const maxLeakSamples = 3

// SummarizeLeaks renders leaks as one log line per message field so the
// producing tool or role can be identified without dumping the context.
func SummarizeLeaks(leaks []IdentifierLeak) string {
	if len(leaks) == 0 {
		return ""
	}
	lines := make([]string, 0, len(leaks))
	for _, leak := range leaks {
		origin := leak.Role
		if leak.ToolName != "" {
			origin += "/" + leak.ToolName
		}
		lines = append(lines, fmt.Sprintf(
			"message[%d] %s %s: %d registered %s, %d unregistered %s",
			leak.MessageIndex, origin, leak.Field,
			len(leak.Registered), sampleIDs(leak.Registered),
			len(leak.Unregistered), sampleIDs(leak.Unregistered),
		))
	}
	return strings.Join(lines, "; ")
}

func sampleIDs(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	if len(ids) > maxLeakSamples {
		return "[" + strings.Join(ids[:maxLeakSamples], " ") + " ...]"
	}
	return "[" + strings.Join(ids, " ") + "]"
}
