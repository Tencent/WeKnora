package openaicompletions

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
)

var dsmlTestOptions = &api.Options{Tools: []api.Tool{{
	Type: "function", Function: api.FunctionDef{Name: "search_knowledge"},
}}}

const dsmlSample = "先查一下。\n<｜DSML｜function_calls>\n" +
	"<｜DSML｜invoke name=\"search_knowledge\">\n" +
	"<｜DSML｜parameter name=\"query\" string=\"true\">c2p_tber_0024</｜DSML｜parameter>\n" +
	"<｜DSML｜parameter name=\"limit\" string=\"false\">5</｜DSML｜parameter>\n" +
	"</｜DSML｜invoke>\n" +
	"<｜DSML｜invoke name=\"search_knowledge\">\n" +
	"<｜DSML｜parameter name=\"query\" string=\"true\">c2p_err_0024 是什么故障</｜DSML｜parameter>\n" +
	"</｜DSML｜invoke>\n" +
	"</｜DSML｜function_calls>"

func TestExtractDSMLToolCalls(t *testing.T) {
	rest, calls := extractDSMLToolCalls(dsmlSample, dsmlTestOptions)
	if rest != "先查一下。\n" {
		t.Fatalf("rest = %q", rest)
	}
	if len(calls) != 2 || calls[0].Function.Name != "search_knowledge" {
		t.Fatalf("calls = %+v", calls)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args["query"] != "c2p_tber_0024" || args["limit"] != float64(5) {
		t.Fatalf("args = %v", args)
	}
	if calls[0].ID == "" || calls[0].ID == calls[1].ID {
		t.Fatalf("ids = %q %q", calls[0].ID, calls[1].ID)
	}
}

func TestDSMLFilterStreamedByteByByte(t *testing.T) {
	f := dsmlFilter{options: dsmlTestOptions}
	shown := ""
	for i := range len(dsmlSample) {
		shown += f.Feed(dsmlSample[i : i+1])
	}
	rest, calls := f.Flush(true)
	if shown != "先查一下。\n" || rest != "" {
		t.Fatalf("shown = %q rest = %q", shown, rest)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d", len(calls))
	}
}

func TestDSMLFilterPassesPlainText(t *testing.T) {
	f := dsmlFilter{options: dsmlTestOptions}
	shown := f.Feed("a < b and <c")
	rest, calls := f.Flush(true)
	if shown+rest != "a < b and <c" || calls != nil {
		t.Fatalf("shown=%q rest=%q calls=%v", shown, rest, calls)
	}
}

func TestDSMLMalformedKeptVisible(t *testing.T) {
	in := "x <｜DSML｜function_calls> broken"
	rest, calls := extractDSMLToolCalls(in, dsmlTestOptions)
	if rest != in || calls != nil {
		t.Fatalf("rest=%q calls=%v", rest, calls)
	}
}
