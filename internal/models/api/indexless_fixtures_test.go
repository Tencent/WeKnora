package api

// Real vendor tool-call deltas, used to pin the index-dropping gateway shapes.
//
// Source: the four streams captured for the parallel-tool-call issue, see
// upstream-issues/3570-experiment/captures/:
//
//	deepseek-same-tool     <- deepseek-same-tool-twice.json   (deepseek-chat)
//	deepseek-two-tools     <- deepseek-two-different-tools.json
//	aliyun-same-tool       <- aliyun-same-tool-twice.json     (qwen-plus)
//	aliyun-two-tools       <- aliyun-two-different-tools.json
//
// indexlessBaseDeltas holds every delta.tool_calls[] object exactly as captured
// (empty fields omitted); the vendor index is kept so a test can tell which call
// an entry belongs to. indexlessWantCalls is the non-stream message.tool_calls
// ground truth of the same request — ids differ between the two calls because
// the vendor generates them per request, so name and arguments are the stable
// comparison. The gateway shapes under test are rebuilt from these entries at
// run time; nothing here is derived from the code under test.

var indexlessBaseDeltas = map[string][]ToolCallDelta{
	"deepseek-same-tool": {
		{Index: 0, ID: "call_00_mc1dZisjKFG9XCUiqJVb3060", Type: "function", Name: "get_weather"},
		{Index: 0, Arguments: "{"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "city"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: ": "},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "北京"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "}"},
		{Index: 1, ID: "call_01_0FYDyW75xoGk4USNE50i3152", Type: "function", Name: "get_weather"},
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
		{Index: 0, ID: "call_00_yovczilo8KWbXMy3CgWT0093", Type: "function", Name: "get_weather"},
		{Index: 0, Arguments: "{"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "city"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: ": "},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "北京"},
		{Index: 0, Arguments: "\""},
		{Index: 0, Arguments: "}"},
		{Index: 1, ID: "call_01_F6j2pwBY5wfEUa3QRqPK9991", Type: "function", Name: "get_local_time"},
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
		{Index: 0, ID: "call_e20759270f864d6cae43f3", Type: "function", Name: "get_weather"},
		{Index: 0, Type: "function", Arguments: "{\"city\": \""},
		{Index: 0, Type: "function", Arguments: "北京\"}"},
		{Index: 1, ID: "call_bfad3352e82641399b9337", Type: "function", Name: "get_weather", Arguments: "{\""},
		{Index: 1, Type: "function", Arguments: "city\": \"上海\"}"},
		{Index: 1, Type: "function"},
	},
	"aliyun-two-tools": {
		{Index: 0, ID: "call_b1e7bf092d8f48e6b9c732", Type: "function", Name: "get_weather"},
		{Index: 0, Type: "function", Arguments: "{\"city\": \""},
		{Index: 0, Type: "function", Arguments: "北京\"}"},
		{Index: 1, ID: "call_9bcf375915b4412db2d2b2", Type: "function", Name: "get_local_time"},
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
