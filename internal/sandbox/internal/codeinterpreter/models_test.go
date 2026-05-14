package codeinterpreter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecution_Text_OnlyMainResult(t *testing.T) {
	e := NewExecution()
	e.Results = append(e.Results,
		&Result{Text: "intermediate", IsMainResult: false},
		&Result{Text: "final", IsMainResult: true},
	)
	if e.Text() != "final" {
		t.Errorf("got %q, want %q", e.Text(), "final")
	}
}

func TestExecution_Text_NoMain(t *testing.T) {
	e := NewExecution()
	e.Results = append(e.Results, &Result{Text: "x", IsMainResult: false})
	if e.Text() != "" {
		t.Errorf("expected empty, got %q", e.Text())
	}
}

func TestExecution_ToJSON(t *testing.T) {
	e := NewExecution()
	e.Results = append(e.Results, &Result{Text: "1", IsMainResult: true})
	e.Logs.Stdout = []string{"hello\n"}
	e.ExecutionCount = 5
	s, err := e.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(s), &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back["execution_count"].(float64) != 5 {
		t.Errorf("exec count: %v", back["execution_count"])
	}
	results, ok := back["results"].([]any)
	if !ok || len(results) != 1 {
		t.Errorf("results: %+v", back["results"])
	}
}

func TestExecutionError_ErrorAndJSON(t *testing.T) {
	e := &ExecutionError{Name: "NameError", Value: "x", Traceback: "tb"}
	msg := e.Error()
	if !strings.Contains(msg, "NameError") || !strings.Contains(msg, "x") || !strings.Contains(msg, "tb") {
		t.Errorf("unexpected: %q", msg)
	}

	js := e.ToJSON()
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if m["name"] != "NameError" || m["value"] != "x" {
		t.Errorf("json: %+v", m)
	}
}

func TestOutputMessage_String(t *testing.T) {
	o := OutputMessage{Line: "hello"}
	if o.String() != "hello" {
		t.Errorf("got %q", o.String())
	}
}

func TestResult_StringWithText(t *testing.T) {
	r := &Result{Text: "abc"}
	s := r.String()
	if !strings.Contains(s, "abc") {
		t.Errorf("result string: %q", s)
	}
}

func TestResult_StringWithoutText(t *testing.T) {
	r := &Result{HTML: "<x/>"}
	s := r.String()
	if !strings.Contains(s, "Formats:") {
		t.Errorf("result string w/o text: %q", s)
	}
}

func TestNewResultFromRaw_AllKnownFormats(t *testing.T) {
	raw := map[string]any{
		"text":           "t",
		"html":           "<h/>",
		"markdown":       "# m",
		"svg":            "<svg/>",
		"png":            "p",
		"jpeg":           "j",
		"pdf":            "d",
		"latex":          "l",
		"json":           map[string]any{"k": "v"},
		"javascript":     "js",
		"data":           map[string]any{"x": 1},
		"chart":          map[string]any{"type": "line", "title": "c", "elements": []any{}},
		"is_main_result": true,
		"extra":          map[string]any{"foo": "bar"},
	}
	r := newResultFromRaw(raw)

	if r.Text != "t" || r.HTML != "<h/>" || r.Markdown != "# m" || r.SVG != "<svg/>" {
		t.Errorf("basic fields wrong: %+v", r)
	}
	if r.PNG != "p" || r.JPEG != "j" || r.PDF != "d" || r.LaTeX != "l" || r.JavaScript != "js" {
		t.Errorf("secondary fields wrong: %+v", r)
	}
	if r.JSON == nil || r.JSON["k"] != "v" {
		t.Errorf("json field: %+v", r.JSON)
	}
	if r.Data == nil || r.Data["x"] != 1 {
		t.Errorf("data field: %+v", r.Data)
	}
	if r.Chart == nil || r.Chart.ChartType() != ChartTypeLine {
		t.Errorf("chart missing or wrong type: %+v", r.Chart)
	}
	if !r.IsMainResult {
		t.Error("is_main_result not parsed")
	}
	if r.Extra == nil || r.Extra["foo"] != "bar" {
		t.Errorf("extra not parsed: %+v", r.Extra)
	}

	// Formats must contain every key we fed in (including 'chart' and
	// the custom 'foo' Extra).
	want := []string{"text", "html", "markdown", "svg", "png", "jpeg", "pdf", "latex", "json", "javascript", "data", "chart", "foo"}
	formats := r.Formats()
	fmtSet := map[string]bool{}
	for _, f := range formats {
		fmtSet[f] = true
	}
	for _, w := range want {
		if !fmtSet[w] {
			t.Errorf("missing format %q in %+v", w, formats)
		}
	}
}

func TestNewResultFromRaw_UnknownKeysGoToExtra(t *testing.T) {
	raw := map[string]any{
		"text":         "t",
		"custom/mime":  "data-one",
		"another_mime": "data-two",
	}
	r := newResultFromRaw(raw)
	if r.Extra["custom/mime"] != "data-one" || r.Extra["another_mime"] != "data-two" {
		t.Errorf("extras not captured: %+v", r.Extra)
	}
}

func TestGetFloat_AllTypes(t *testing.T) {
	m := map[string]any{
		"a": float64(1.5),
		"b": float32(2.5),
		"c": int(3),
		"d": int64(4),
		"e": json.Number("5.5"),
		"f": "not-a-number",
	}
	cases := []struct {
		key  string
		want float64
	}{
		{"a", 1.5}, {"b", 2.5}, {"c", 3}, {"d", 4}, {"e", 5.5}, {"f", 0},
		{"missing", 0},
	}
	for _, c := range cases {
		if got := getFloat(m, c.key); got != c.want {
			t.Errorf("getFloat(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

func TestGetFloatSlice(t *testing.T) {
	m := map[string]any{
		"a": []any{float64(1), int(2), json.Number("3.3")},
		"b": "nope",
	}
	if got := getFloatSlice(m, "a"); len(got) != 3 || got[2] != 3.3 {
		t.Errorf("got %+v", got)
	}
	if got := getFloatSlice(m, "b"); got != nil {
		t.Errorf("expected nil for string, got %+v", got)
	}
	if got := getFloatSlice(m, "missing"); got != nil {
		t.Error("expected nil for missing")
	}
}

func TestGetStringSlice(t *testing.T) {
	m := map[string]any{
		"a": []any{"x", "y", 3, "z"}, // mixed types — non-strings dropped
	}
	got := getStringSlice(m, "a")
	if len(got) != 3 || got[0] != "x" || got[2] != "z" {
		t.Errorf("got %+v", got)
	}
}

func TestGetInt64(t *testing.T) {
	m := map[string]any{
		"a": float64(10),
		"b": int(20),
		"c": int64(30),
		"d": json.Number("40"),
	}
	for _, c := range []struct {
		key  string
		want int64
	}{{"a", 10}, {"b", 20}, {"c", 30}, {"d", 40}, {"missing", 0}} {
		if got := getInt64(m, c.key); got != c.want {
			t.Errorf("getInt64(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

func TestChartInterface_DelegatesMethods(t *testing.T) {
	c := deserializeChart(map[string]any{
		"type":     "bar",
		"title":    "T",
		"elements": []any{},
	})
	if c.ChartType() != ChartTypeBar {
		t.Errorf("chart type: %s", c.ChartType())
	}
	if c.ChartTitle() != "T" {
		t.Errorf("chart title: %s", c.ChartTitle())
	}
	if c.ToJSON()["type"] != "bar" {
		t.Errorf("ToJSON missing type: %+v", c.ToJSON())
	}
}
