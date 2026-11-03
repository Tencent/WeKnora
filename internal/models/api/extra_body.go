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
// already set. An isolated call skips IsolatedExtraBodyKeys.
func MergeExtraBody(body, extra map[string]any, isolated bool) {
	for k, v := range extra {
		if _, exists := body[k]; exists {
			continue
		}
		if isolated && isIsolatedKey(k) {
			continue
		}
		body[k] = v
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
