package openaicompletions

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// DeepSeek models (V3.2 and later) sometimes leak their native tool-call
// syntax into the content channel instead of returning structured
// tool_calls, typically when the serving gateway's parser misses it:
//
//	<｜DSML｜function_calls>
//	<｜DSML｜invoke name="search_knowledge">
//	<｜DSML｜parameter name="query" string="true">foo</｜DSML｜parameter>
//	</｜DSML｜invoke>
//	</｜DSML｜function_calls>
//
// Left alone, the round reaches the agent as a plain answer and the turn ends
// with the markup shown to the user. dsmlFilter holds that markup back from
// the stream and recovers the calls from it.

// dsmlMarkers are the openings of a DSML tag, with the full-width bar the
// model emits and the ASCII bar some gateways normalise it to.
var dsmlMarkers = []string{"<｜DSML｜", "<|DSML|"}

// dsmlIndexBase keeps recovered calls clear of the vendor's own indices.
const dsmlIndexBase = 1 << 20

const dsmlBar = `\s*[｜|]\s*`

var (
	dsmlInvokeRe = regexp.MustCompile(
		`(?s)<` + dsmlBar + `DSML` + dsmlBar + `invoke\s+name\s*=\s*"([^"]+)"\s*>(.*?)</` +
			dsmlBar + `DSML` + dsmlBar + `invoke\s*>`)
	dsmlParamRe = regexp.MustCompile(
		`(?s)<` + dsmlBar + `DSML` + dsmlBar + `parameter\s+name\s*=\s*"([^"]+)"` +
			`(?:\s+string\s*=\s*"(true|false)")?\s*>(.*?)</` + dsmlBar + `DSML` + dsmlBar + `parameter\s*>`)
	dsmlBlockRe = regexp.MustCompile(
		`(?s)^<` + dsmlBar + `DSML` + dsmlBar + `function_calls\s*>(.*?)</` + dsmlBar + `DSML` + dsmlBar +
			`function_calls\s*>\s*$`)
)

// dsmlFilter is fed the streamed content. Everything before the first DSML
// tag passes through; from that tag on the text is held until Flush.
type dsmlFilter struct {
	options   *api.Options
	prefix    strings.Builder
	held      strings.Builder
	capturing bool
}

// Feed returns the part of chunk that is safe to show now.
func (f *dsmlFilter) Feed(chunk string) string {
	if f.options == nil || len(f.options.Tools) == 0 || f.options.ToolChoice == "none" {
		return chunk
	}
	if chunk == "" {
		return ""
	}
	if f.capturing {
		f.held.WriteString(chunk)
		return ""
	}
	buf := f.held.String() + chunk
	f.held.Reset()
	shown := buf
	if idx := indexDSML(buf); idx >= 0 {
		f.capturing = true
		f.held.WriteString(buf[idx:])
		shown = buf[:idx]
	} else if cut := partialMarkerStart(buf); cut >= 0 {
		// Keep a trailing partial marker back: the tag may be split across chunks.
		f.held.WriteString(buf[cut:])
		shown = buf[:cut]
	}
	f.prefix.WriteString(shown)
	return shown
}

// Flush ends the stream: it returns the held text that turned out not to be
// tool calls, and the calls recovered from the rest.
func (f *dsmlFilter) Flush(recoverCalls bool) (string, []types.LLMToolCall) {
	prefix := f.prefix.String()
	held := f.held.String()
	f.prefix.Reset()
	f.held.Reset()
	f.capturing = false
	if held == "" || !recoverCalls {
		return held, nil
	}
	// Include already-streamed text when checking whether the marker belongs
	// to a quoted example, but never emit that prefix a second time.
	rest, calls := extractDSMLToolCalls(prefix+held, f.options)
	return rest[len(prefix):], calls
}

// extractDSMLToolCalls recovers a complete trailing protocol block, optionally
// preceded by a plain-text preamble. Ambiguous examples, undeclared tools and
// malformed calls stay intact: a partial parse must not silently change a call.
func extractDSMLToolCalls(content string, opts *api.Options) (string, []types.LLMToolCall) {
	if opts == nil || len(opts.Tools) == 0 || opts.ToolChoice == "none" {
		return content, nil
	}
	start := indexDSML(content)
	if start < 0 {
		return content, nil
	}
	prefix := content[:start]
	// Only recover a block starting on its own line. Code spans/fences in the
	// preamble make the context ambiguous, so conservatively leave it alone.
	lineStart := strings.LastIndexByte(prefix, '\n') + 1
	if strings.TrimSpace(prefix[lineStart:]) != "" ||
		strings.HasPrefix(prefix[lineStart:], "    ") || strings.HasPrefix(prefix[lineStart:], "\t") ||
		strings.Contains(prefix, "`") || strings.Contains(prefix, "~~~") {
		return content, nil
	}
	block := dsmlBlockRe.FindStringSubmatch(content[start:])
	if len(block) == 0 {
		return content, nil
	}
	allowed := make(map[string]bool, len(opts.Tools))
	for _, tool := range opts.Tools {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		name := tool.Function.Name
		if opts.ToolChoice == "" || opts.ToolChoice == "auto" || opts.ToolChoice == "required" ||
			opts.ToolChoice == name {
			allowed[name] = true
		}
	}
	inner := block[1]
	matches := dsmlInvokeRe.FindAllStringSubmatchIndex(inner, -1)
	calls := make([]types.LLMToolCall, 0, len(matches))
	consumed := 0
	for _, m := range matches {
		name, body := inner[m[2]:m[3]], inner[m[4]:m[5]]
		if strings.TrimSpace(inner[consumed:m[0]]) != "" || !allowed[name] {
			return content, nil
		}
		consumed = m[1]
		args := map[string]any{}
		paramEnd := 0
		for _, p := range dsmlParamRe.FindAllStringSubmatchIndex(body, -1) {
			key := body[p[2]:p[3]]
			if strings.TrimSpace(body[paramEnd:p[0]]) != "" {
				return content, nil
			}
			if _, duplicate := args[key]; duplicate {
				return content, nil
			}
			paramEnd = p[1]
			isString := ""
			if p[4] >= 0 {
				isString = body[p[4]:p[5]]
			}
			value, ok := dsmlParamValue(body[p[6]:p[7]], isString)
			if !ok {
				return content, nil
			}
			args[key] = value
		}
		if strings.TrimSpace(body[paramEnd:]) != "" {
			return content, nil
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return content, nil
		}
		calls = append(calls, types.LLMToolCall{
			ID:       "call_dsml_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24],
			Type:     "function",
			Function: types.FunctionCall{Name: name, Arguments: string(raw)},
		})
	}
	if len(calls) == 0 || strings.TrimSpace(inner[consumed:]) != "" ||
		(opts.ParallelToolCalls != nil && !*opts.ParallelToolCalls && len(calls) > 1) {
		return content, nil
	}
	return prefix, calls
}

// dsmlParamValue keeps JSON numbers verbatim instead of rounding them through
// float64. An explicitly non-string parameter must contain complete JSON.
func dsmlParamValue(body, isString string) (any, bool) {
	if isString == "true" {
		return body, true
	}
	trimmed := strings.TrimSpace(body)
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed), true
	}
	return body, isString == ""
}

// dsmlToolCallDeltas turns recovered calls into stream deltas numbered after
// any native calls so the two never share an index.
func dsmlToolCallDeltas(calls []types.LLMToolCall, base int) []api.ToolCallDelta {
	deltas := make([]api.ToolCallDelta, 0, len(calls))
	for i, c := range calls {
		deltas = append(deltas, api.ToolCallDelta{
			Index: base + i, ID: c.ID, Type: c.Type, Name: c.Function.Name, Arguments: c.Function.Arguments,
		})
	}
	return deltas
}

func indexDSML(s string) int {
	best := -1
	for _, m := range dsmlMarkers {
		if i := strings.Index(s, m); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	return best
}

// partialMarkerStart reports where a trailing prefix of a DSML marker begins,
// or -1.
func partialMarkerStart(s string) int {
	i := strings.LastIndexByte(s, '<')
	if i < 0 {
		return -1
	}
	tail := s[i:]
	for _, m := range dsmlMarkers {
		if len(tail) < len(m) && strings.HasPrefix(m, tail) {
			return i
		}
	}
	return -1
}
