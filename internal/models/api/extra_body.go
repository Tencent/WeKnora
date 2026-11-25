package api

// IsolatedExtraBodyKeys are extra_body keys an isolated call never sends.
// Tool and session keys would add provider tools or tie the request to stored
// conversation state. temperature is sent as 0 when the model accepts it, and
// a configured value must not replace that or appear when thinking rejects it.
var IsolatedExtraBodyKeys = []string{
	"tools", "tool_choice", "parallel_tool_calls", "functions", "function_call",
	"previous_response_id", "conversation", "store", "include", "plugins", "web_search_options",
	"temperature",
}

// MergeExtraBody copies configured extra_body keys the request does not
// already set. When both sides hold an object under the same key, the
// configured object's missing fields are added to a copy of the request's
// object, so extra_body can extend a field the request builds itself (for
// example thinking.block_binding) without replacing it. An isolated call skips
// IsolatedExtraBodyKeys.
func MergeExtraBody(body, extra map[string]any, isolated bool) {
	for k, v := range extra {
		if isolated && isIsolatedKey(k) {
			continue
		}
		existing, exists := body[k]
		if !exists {
			body[k] = v
			continue
		}
		dst, ok := existing.(map[string]any)
		src, ok2 := v.(map[string]any)
		if !ok || !ok2 {
			continue
		}
		merged := make(map[string]any, len(dst)+len(src))
		for dk, dv := range dst {
			merged[dk] = dv
		}
		MergeExtraBody(merged, src, false)
		body[k] = merged
	}
}

func isIsolatedKey(k string) bool {
	for _, key := range IsolatedExtraBodyKeys {
		if key == k {
			return true
		}
	}
	return false
}
