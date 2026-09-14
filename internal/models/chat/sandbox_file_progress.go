package chat

import (
	"encoding/json"
	"strings"
	"time"
)

// Live write/edit progress while the model is still emitting tool-call JSON:
// path as soon as it appears, then a running +N / -M as content lands. The
// SSE payload is only those stats plus a short preview — never the whole file.

const (
	sandboxFilePreviewMaxLines = 10
	writeSandboxFileName       = "write_sandbox_file"
	editSandboxFileName        = "edit_sandbox_file"
)

// sandboxProgressMinInterval caps how often we emit when only byte counts
// change (a long line with no newline). Line-count changes always emit.
var sandboxProgressMinInterval = 120 * time.Millisecond

func isSandboxMutationTool(name string) bool {
	return name == writeSandboxFileName || name == editSandboxFileName
}

type sandboxFileProgress struct {
	toolName  string
	pathEx    *jsonFieldExtractor
	contentEx *jsonFieldExtractor
	path      string
	content   string
	buf       string

	added   int
	removed int
	bytes   int
	preview string

	lastPath    string
	lastAdded   int
	lastRemoved int
	lastBytes   int
	lastPreview string
	lastEmit    time.Time
	emitted     bool

	lastParseAt  time.Time
	lastParseLen int
}

func newSandboxFileProgress(toolName string) *sandboxFileProgress {
	return &sandboxFileProgress{toolName: toolName}
}

// Feed consumes one arguments delta. ok is true when the UI should be sent
// an updated stats payload.
func (p *sandboxFileProgress) Feed(delta string) (map[string]any, bool) {
	if p == nil || delta == "" {
		return nil, false
	}
	switch p.toolName {
	case writeSandboxFileName:
		p.feedWrite(delta)
	case editSandboxFileName:
		p.feedEdit(delta)
	default:
		return nil, false
	}
	if !p.shouldEmit() {
		return nil, false
	}
	p.markEmitted()
	return p.payload(), true
}

func (p *sandboxFileProgress) feedWrite(delta string) {
	if p.pathEx == nil {
		p.pathEx = newJSONFieldExtractor("path")
	}
	if p.contentEx == nil {
		p.contentEx = newJSONFieldExtractor("content")
	}
	if chunk := p.pathEx.Feed(delta); chunk != "" {
		p.path += chunk
	}
	if chunk := p.contentEx.Feed(delta); chunk != "" {
		p.content += chunk
	}
	p.added = countContentLines(p.content)
	p.removed = 0
	p.bytes = len(p.content)
	p.preview = sandboxContentPreview(p.content)
}

func (p *sandboxFileProgress) feedEdit(delta string) {
	p.buf += delta
	if p.pathEx == nil {
		p.pathEx = newJSONFieldExtractor("path")
	}
	if chunk := p.pathEx.Feed(delta); chunk != "" {
		p.path += chunk
	}

	now := time.Now()
	if p.lastParseLen > 0 &&
		len(p.buf)-p.lastParseLen < 512 &&
		now.Sub(p.lastParseAt) < sandboxProgressMinInterval {
		return
	}
	p.lastParseAt = now
	p.lastParseLen = len(p.buf)

	obj := parsePartialJSONObject(p.buf)
	if obj == nil {
		return
	}
	if path, _ := obj["path"].(string); path != "" && p.path == "" {
		p.path = path
	}
	p.added, p.removed = editArgsLineStats(obj)
	p.bytes = len(p.buf)
	p.preview = ""
}

func (p *sandboxFileProgress) shouldEmit() bool {
	if p.path == "" && p.added == 0 && p.removed == 0 && p.bytes == 0 {
		return false
	}
	pathChanged := p.path != p.lastPath
	linesChanged := p.added != p.lastAdded || p.removed != p.lastRemoved
	if !p.emitted || pathChanged || linesChanged {
		return true
	}
	if p.bytes == p.lastBytes && p.preview == p.lastPreview {
		return false
	}
	return time.Since(p.lastEmit) >= sandboxProgressMinInterval
}

func (p *sandboxFileProgress) markEmitted() {
	p.emitted = true
	p.lastPath = p.path
	p.lastAdded = p.added
	p.lastRemoved = p.removed
	p.lastBytes = p.bytes
	p.lastPreview = p.preview
	p.lastEmit = time.Now()
}

func (p *sandboxFileProgress) payload() map[string]any {
	out := map[string]any{
		"added_lines":   p.added,
		"removed_lines": p.removed,
		"bytes":         p.bytes,
	}
	if p.path != "" {
		out["path"] = p.path
	}
	if p.preview != "" {
		out["preview"] = p.preview
	}
	return out
}

func countContentLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if s[len(s)-1] != '\n' {
		n++
	}
	return n
}

func sandboxContentPreview(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	end := len(lines)
	if end > 0 && lines[end-1] == "" {
		end--
	}
	lines = lines[:end]
	if len(lines) > sandboxFilePreviewMaxLines {
		lines = lines[:sandboxFilePreviewMaxLines]
	}
	return strings.Join(lines, "\n")
}

func parsePartialJSONObject(s string) map[string]any {
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) == nil {
		return m
	}
	closed := closePartialJSON(s)
	if closed == s {
		return nil
	}
	if json.Unmarshal([]byte(closed), &m) == nil {
		return m
	}
	return nil
}

// closePartialJSON closes an unterminated string and missing braces so a
// streaming prefix can be unmarshalled. Structural characters are ASCII, so
// byte iteration is enough.
func closePartialJSON(s string) string {
	inString := false
	escaped := false
	var stack []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if n := len(stack); n > 0 && stack[n-1] == c {
				stack = stack[:n-1]
			}
		}
	}
	if !inString && !escaped && len(stack) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(stack) + 2)
	b.WriteString(s)
	if escaped {
		b.WriteByte('\\')
	}
	if inString || escaped {
		b.WriteByte('"')
	}
	for i := len(stack) - 1; i >= 0; i-- {
		b.WriteByte(stack[i])
	}
	return b.String()
}

func editArgsLineStats(obj map[string]any) (added, removed int) {
	edits, _ := obj["edits"].([]any)
	for _, raw := range edits {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		oldS := stringFromMap(m, "old_string", "oldText")
		newS := stringFromMap(m, "new_string", "newText")
		if oldS == "" && newS == "" {
			continue
		}
		removed += countContentLines(oldS)
		added += countContentLines(newS)
	}
	if added == 0 && removed == 0 {
		oldS := stringFromMap(obj, "old_string", "oldText")
		newS := stringFromMap(obj, "new_string", "newText")
		removed = countContentLines(oldS)
		added = countContentLines(newS)
	}
	return added, removed
}

func stringFromMap(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := m[key].(string); ok {
			return s
		}
	}
	return ""
}
