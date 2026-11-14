package tools

import (
	"errors"
	"regexp"
	"strings"
)

// Some compatible endpoints have emitted their internal argument/opaque-state
// serialization as assistant content, with an unmatched closing argument tag.
// Match that complete shape only: explanations, quoted examples, user/tool data
// and ordinary base64 output are not protocol failures.
var protocolArtifact = regexp.MustCompile(
	"(?s)^\\s*(?:openai[_＿]reasoning\\s*[:：]\\s*[A-Za-z0-9+/_=-]{64,}|authorized\\s*=\\s*(" +
		"?:true|false))\\s*</arg_value:[a-fA-F0-9]{6,32}>\\s*$",
)

// ErrProtocolArtifact indicates an internal protocol fragment in place of an answer.
var ErrProtocolArtifact = errors.New("model returned internal protocol data instead of an answer")

// An orphan nonce-qualified protocol closer can be the entire final response.
// Ordinary HTML, quoted tags, and prose containing an example remain intact.
var orphanProtocolCloser = regexp.MustCompile(
	`^\s*(?:</(?:tool_calls|tool_call|arg_key|arg_value):[a-fA-F0-9]{6,32}>\s*)+$`,
)

// IsProtocolArtifact recognizes whole responses containing only internal protocol data.
func IsProtocolArtifact(s string) bool {
	return protocolArtifact.MatchString(s) || orphanProtocolCloser.MatchString(s)
}

// ProtocolAnswerGuard delays only a suspicious opening, including prefixes
// split across chunks. Normal answers stream as before. A candidate must be
// validated before any of its bytes reach the UI.
type ProtocolAnswerGuard struct {
	pending string
	passed  bool
}

// Feed buffers suspicious prefixes and streams ordinary answer text.
func (g *ProtocolAnswerGuard) Feed(s string) string {
	if g.passed {
		return s
	}
	g.pending += s
	trimmed := strings.TrimSpace(g.pending)
	for _, prefix := range []string{
		"openai_reasoning",
		"openai＿reasoning",
		"authorized",
		"</tool_calls:",
		"</tool_call:",
		"</arg_key:",
		"</arg_value:",
	} {
		if strings.HasPrefix(prefix, trimmed) || strings.HasPrefix(trimmed, prefix) {
			return ""
		}
	}
	g.passed = true
	out := g.pending
	g.pending = ""
	return out
}

// Flush validates and releases remaining text when the answer finishes.
func (g *ProtocolAnswerGuard) Flush() (string, error) {
	out := g.pending
	g.pending = ""
	if IsProtocolArtifact(out) {
		return "", ErrProtocolArtifact
	}
	return out, nil
}

func withoutProtocolArtifactLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if IsProtocolArtifact(line) {
			lines[i] = "[Prior assistant output contained invalid provider protocol data; " +
				"it was not a completed answer.]"
		}
	}
	return strings.Join(lines, "\n")
}
