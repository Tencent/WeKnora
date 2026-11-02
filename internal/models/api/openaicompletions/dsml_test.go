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

// DeepSeek V4.1 Flash emits a doubled bar and names the envelope `calls`
// instead of `function_calls`. This is the exact block a real turn leaked
// into the content channel; it must be recovered, not shown to the user.
func TestExtractDSMLDoubledBarCallsEnvelope(t *testing.T) {
	in := "<｜｜DSML｜｜ calls>\n" +
		"<｜｜DSML｜｜ invoke name=\"read_file\">\n" +
		"<｜｜DSML｜｜ parameter name=\"detail\" string=\"true\">brief</｜｜DSML｜｜ parameter>\n" +
		"<｜｜DSML｜｜ parameter name=\"path\" string=\"true\">/tmp/preview/p-02.png</｜｜DSML｜｜ parameter>\n" +
		"</｜｜DSML｜｜ invoke>\n" +
		"</｜｜DSML｜｜ calls>"
	opts := &api.Options{Tools: []api.Tool{{
		Type: "function", Function: api.FunctionDef{Name: "read_file"},
	}}}
	rest, calls := extractDSMLToolCalls(in, opts)
	if rest != "" {
		t.Fatalf("rest = %q", rest)
	}
	if len(calls) != 1 || calls[0].Function.Name != "read_file" {
		t.Fatalf("calls = %+v", calls)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args["detail"] != "brief" || args["path"] != "/tmp/preview/p-02.png" {
		t.Fatalf("args = %v", args)
	}
}

func TestDSMLFilterDoubledBarByteByByte(t *testing.T) {
	in := "<｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name=\"read_file\">" +
		"<｜｜DSML｜｜ parameter name=\"path\" string=\"true\">/tmp/p.png</｜｜DSML｜｜ parameter>" +
		"</｜｜DSML｜｜ invoke></｜｜DSML｜｜ calls>"
	opts := &api.Options{Tools: []api.Tool{{
		Type: "function", Function: api.FunctionDef{Name: "read_file"},
	}}}
	f := dsmlFilter{options: opts}
	shown := ""
	for i := range len(in) {
		shown += f.Feed(in[i : i+1])
	}
	rest, calls := f.Flush(true)
	if shown != "" || rest != "" {
		t.Fatalf("shown = %q rest = %q", shown, rest)
	}
	if len(calls) != 1 || calls[0].Function.Name != "read_file" {
		t.Fatalf("calls = %+v", calls)
	}
}
