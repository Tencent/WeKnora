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
		`(?s)<` + dsmlBar + `DSML` + dsmlBar + `function_calls\s*>.*?(?:</` + dsmlBar + `DSML` + dsmlBar +
			`function_calls\s*>|$)`)
)

// dsmlFilter is fed the streamed content. Everything before the first DSML
// tag passes through; from that tag on the text is held until Flush.
type dsmlFilter struct {
	held      strings.Builder
	capturing bool
}

// Feed returns the part of chunk that is safe to show now.
func (f *dsmlFilter) Feed(chunk string) string {
	if chunk == "" {
		return ""
	}
	if f.capturing {
		f.held.WriteString(chunk)
		return ""
	}
	buf := f.held.String() + chunk
	f.held.Reset()
	if idx := indexDSML(buf); idx >= 0 {
		f.capturing = true
		f.held.WriteString(buf[idx:])
		return buf[:idx]
	}
	// Keep a trailing partial marker back: the tag may be split across chunks.
	if cut := partialMarkerStart(buf); cut >= 0 {
		f.held.WriteString(buf[cut:])
		return buf[:cut]
	}
	return buf
}

// Flush ends the stream: it returns the held text that turned out not to be
// tool calls, and the calls recovered from the rest.
func (f *dsmlFilter) Flush() (string, []types.LLMToolCall) {
	held := f.held.String()
	f.held.Reset()
	f.capturing = false
	if held == "" {
		return "", nil
	}
	return extractDSMLToolCalls(held)
}

// extractDSMLToolCalls parses the DSML calls out of content. When nothing
// parses the content is returned untouched, so malformed markup stays
// visible instead of silently vanishing.
func extractDSMLToolCalls(content string) (string, []types.LLMToolCall) {
	if indexDSML(content) < 0 {
		return content, nil
	}
	matches := dsmlInvokeRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return content, nil
	}
	calls := make([]types.LLMToolCall, 0, len(matches))
	for _, m := range matches {
		args := map[string]any{}
		for _, p := range dsmlParamRe.FindAllStringSubmatch(m[2], -1) {
			args[p[1]] = dsmlParamValue(p[3], p[2])
		}
		raw, err := json.Marshal(args)
		if err != nil {
			continue
		}
		calls = append(calls, types.LLMToolCall{
			ID:       "call_dsml_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24],
			Type:     "function",
			Function: types.FunctionCall{Name: strings.TrimSpace(m[1]), Arguments: string(raw)},
		})
	}
	if len(calls) == 0 {
		return content, nil
	}
	rest := dsmlBlockRe.ReplaceAllString(content, "")
	rest = dsmlInvokeRe.ReplaceAllString(rest, "")
	return strings.TrimSpace(rest), calls
}

// dsmlParamValue decodes a parameter body. string="true" marks a raw string;
// otherwise the body is JSON (numbers, booleans, objects), with the raw text
// as the fallback when it is not.
func dsmlParamValue(body, isString string) any {
	if isString == "true" {
		return body
	}
	trimmed := strings.TrimSpace(body)
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err == nil {
		return v
	}
	return body
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
