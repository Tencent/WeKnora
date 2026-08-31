package agent

import (
	"errors"
	"strings"
	"unicode"
)

var errLeakedToolMarkup = errors.New("LLM returned tool-call markup as plain text")

var leakedToolMarkupPrefixes = []string{
	"<||dsml||tool_calls",
	"<|dsml|tool_calls",
	"<||dsml||invoke",
	"<|dsml|invoke",
}

// normalizeToolMarkupPrefix only normalizes characters used by known DSML
// envelopes. It deliberately does not search arbitrary prose: an assistant may
// legitimately explain or quote the protocol without that answer being a
// failed tool call.
func normalizeToolMarkupPrefix(content string) string {
	content = strings.TrimLeftFunc(content, unicode.IsSpace)
	content = strings.ReplaceAll(content, "｜", "|")
	return strings.ToLower(content)
}

// looksLikeLeakedToolMarkup reports whether the assistant started its answer
// with a provider-internal DSML tool-call envelope. Some OpenAI-compatible
// providers occasionally return this envelope in content with finish=stop
// instead of exposing structured tool_calls.
func looksLikeLeakedToolMarkup(content string) bool {
	normalized := normalizeToolMarkupPrefix(content)
	for _, prefix := range leakedToolMarkupPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func couldStartLeakedToolMarkup(content string) bool {
	normalized := normalizeToolMarkupPrefix(content)
	if normalized == "" {
		return true
	}
	for _, prefix := range leakedToolMarkupPrefixes {
		if strings.HasPrefix(prefix, normalized) {
			return true
		}
	}
	return false
}

// toolMarkupStreamGuard holds only the short, ambiguous beginning of an answer
// until it can rule out a leaked tool-call envelope. Normal prose is released
// as soon as its first non-space characters differ from every known prefix, so
// regular answer streaming is unaffected.
type toolMarkupStreamGuard struct {
	pending  string
	decided  bool
	rejected bool
}

func (g *toolMarkupStreamGuard) Feed(content string, done bool) string {
	if g.rejected {
		return ""
	}
	if g.decided {
		return content
	}

	g.pending += content
	if looksLikeLeakedToolMarkup(g.pending) {
		g.pending = ""
		g.rejected = true
		return ""
	}
	if !done && couldStartLeakedToolMarkup(g.pending) {
		return ""
	}

	out := g.pending
	g.pending = ""
	g.decided = true
	return out
}

func (g *toolMarkupStreamGuard) Rejected() bool {
	return g.rejected
}
