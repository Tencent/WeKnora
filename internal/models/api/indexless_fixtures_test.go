package api

// Vendor tool-call deltas, used to pin the index-dropping gateway shapes.
//
// Topology source: two public vendor APIs, each asked twice to answer with two
// parallel tool calls — the same tool twice, and two different tools:
//
//	deepseek-same-tool     deepseek-chat, same tool twice
//	deepseek-two-tools     deepseek-chat, two different tools
//	aliyun-same-tool       qwen-plus, same tool twice
//	aliyun-two-tools       qwen-plus, two different tools
//
// The shipped .jsonl fixtures rebuild those streams with the vendor index
// removed, so what they exercise is the index-less path the third-party
// gateways take. Every value that identified the request, the account or the
// moment it was taken — request id, created timestamp, system fingerprint,
// token counters, tool-call ids — has been replaced with a synthetic stand-in;
// the tool-call delta sequence is unchanged, and the tool-call ids stay
// distinct within a stream so "two parallel calls" keeps meaning two.
//
// indexlessBaseDeltas holds every delta.tool_calls[] object of those streams
// (empty fields omitted); the vendor index is kept so a test can tell which call
// an entry belongs to. indexlessWantCalls is the non-stream message.tool_calls
// ground truth of the same request — ids differ between the two calls because
// the vendor generates them per request, so name and arguments are the stable
// comparison. The gateway shapes under test are rebuilt from these entries at
// run time; nothing here is derived from the code under test.

var indexlessBaseDeltas = map[string][]ToolCallDelta{
	"deepseek-same-tool": {
		{Index: 0, ID: "call_deepseek_weather_1", Type: "function", Name: "get_weather"},
		{Index: 0, Arguments: "{"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "city"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: ": "},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "北京"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "}"},
		{Index: 1, ID: "call_deepseek_weather_2", Type: "function", Name: "get_weather"},
		{Index: 1, Arguments: "{"},
		{Index: 1, Arguments: "\""},
		{Index: 1, Arguments: "city"},
		{Index: 1, Arguments: "\""},
		{Index: 1, Arguments: ": "},
		{Index: 1, Arguments: "\""},
		{Index: 1, Arguments: "上海"},
		{Index: 1, Arguments: "\""},
		{Index: 1, Arguments: "}"},
	},
	"deepseek-two-tools": {
		{Index: 0, ID: "call_deepseek_weather_1", Type: "function", Name: "get_weather"},
		{Index: 0, Arguments: "{"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "city"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: ": "},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "北京"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "}"},
		{Index: 1, ID: "call_deepseek_time_2", Type: "function", Name: "get_local_time"},
		{Index: 1, Arguments: "{"},
		{Index: 1, Arguments: "\""},
		{Index: 1, Arguments: "city"},
		{Index: 1, Arguments: "\""},
		{Index: 1, Arguments: ": "},
		{Index: 1, Arguments: "\""},
		{Index: 1, Arguments: "上海"},
		{Index: 1, Arguments: "\""},
		{Index: 1, Arguments: "}"},
	},
	"aliyun-same-tool": {
		{Index: 0, ID: "call_qwen_weather_1", Type: "function", Name: "get_weather"},
		{Index: 0, Type: "function", Arguments: "{\"city\": \""},
		{Index: 0, Type: "function", Arguments: "北京\"}"},
		{Index: 1, ID: "call_qwen_weather_2", Type: "function", Name: "get_weather", Arguments: "{\""},
		{Index: 1, Type: "function", Arguments: "city\": \"上海\"}"},
		{Index: 1, Type: "function"},
	},
	"aliyun-two-tools": {
		{Index: 0, ID: "call_qwen_weather_1", Type: "function", Name: "get_weather"},
		{Index: 0, Type: "function", Arguments: "{\"city\": \""},
		{Index: 0, Type: "function", Arguments: "北京\"}"},
		{Index: 1, ID: "call_qwen_time_2", Type: "function", Name: "get_local_time"},
		{Index: 1, Type: "function", Arguments: "{\"city\": \"上海\"}"},
		{Index: 1, Type: "function"},
	},
}

var indexlessWantCalls = map[string][]wantCall{
	"deepseek-same-tool": {
		{Name: "get_weather", Arguments: "{\"city\": \"北京\"}"},
		{Name: "get_weather", Arguments: "{\"city\": \"上海\"}"},
	},
	"deepseek-two-tools": {
		{Name: "get_weather", Arguments: "{\"city\": \"北京\"}"},
		{Name: "get_local_time", Arguments: "{\"city\": \"上海\"}"},
	},
	"aliyun-same-tool": {
		{Name: "get_weather", Arguments: "{\"city\": \"北京\"}"},
		{Name: "get_weather", Arguments: "{\"city\": \"上海\"}"},
	},
	"aliyun-two-tools": {
		{Name: "get_weather", Arguments: "{\"city\": \"北京\"}"},
		{Name: "get_local_time", Arguments: "{\"city\": \"上海\"}"},
	},
}
