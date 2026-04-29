package codeinterpreter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeserializeChart_Line(t *testing.T) {
	rawJSON := `{
		"type": "line",
		"title": "Sample Line",
		"x_label": "Time",
		"y_label": "Value",
		"x_ticks": [1, 2, 3],
		"x_tick_labels": ["a", "b", "c"],
		"x_scale": "linear",
		"y_ticks": [0.1, 0.2, 0.3],
		"y_tick_labels": ["0.1", "0.2", "0.3"],
		"y_scale": "log",
		"elements": [
			{"label": "series1", "points": [[1, 0.1], [2, 0.2]]}
		]
	}`

	var data map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &data); err != nil {
		t.Fatal(err)
	}

	c := deserializeChart(data)
	lc, ok := c.(*LineChart)
	if !ok {
		t.Fatalf("expected *LineChart, got %T", c)
	}
	if lc.ChartType() != ChartTypeLine {
		t.Errorf("expected line, got %s", lc.ChartType())
	}
	if lc.Title != "Sample Line" {
		t.Errorf("unexpected title: %q", lc.Title)
	}
	if lc.XLabel != "Time" || lc.YLabel != "Value" {
		t.Errorf("labels not parsed")
	}
	if lc.YScale != ScaleTypeLog {
		t.Errorf("expected log y-scale, got %s", lc.YScale)
	}
	if len(lc.Points) != 1 || lc.Points[0].Label != "series1" || len(lc.Points[0].Points) != 2 {
		t.Errorf("points not parsed correctly: %+v", lc.Points)
	}
}

func TestDeserializeChart_Pie(t *testing.T) {
	raw := `{
		"type": "pie",
		"title": "Pie!",
		"elements": [
			{"label": "a", "angle": 1.5, "radius": 1},
			{"label": "b", "angle": 2.0, "radius": 1}
		]
	}`
	var data map[string]any
	_ = json.Unmarshal([]byte(raw), &data)

	c := deserializeChart(data)
	pc, ok := c.(*PieChart)
	if !ok {
		t.Fatalf("expected *PieChart, got %T", c)
	}
	if len(pc.Slices) != 2 {
		t.Errorf("expected 2 slices, got %d", len(pc.Slices))
	}
	if pc.Slices[0].Label != "a" || pc.Slices[0].Angle != 1.5 {
		t.Errorf("slice mismatch: %+v", pc.Slices[0])
	}
}

func TestDeserializeChart_Unknown(t *testing.T) {
	raw := `{"type": "weird_chart", "title": "?", "elements": []}`
	var data map[string]any
	_ = json.Unmarshal([]byte(raw), &data)

	c := deserializeChart(data)
	if c.ChartType() != ChartTypeUnknown {
		t.Errorf("expected unknown, got %s", c.ChartType())
	}
}

func TestParseOutput_StreamFlow(t *testing.T) {
	lines := []string{
		`{"type": "stdout", "text": "hello\n", "timestamp": 1}`,
		`{"type": "stderr", "text": "oops\n", "timestamp": 2}`,
		`{"type": "result", "text": "42", "is_main_result": true}`,
		`{"type": "number_of_executions", "execution_count": 3}`,
	}

	execution := NewExecution()
	stdoutN := 0
	stderrN := 0
	resultN := 0

	opts := &RunCodeOpts{
		OnStdout: func(msg OutputMessage) { stdoutN++ },
		OnStderr: func(msg OutputMessage) { stderrN++ },
		OnResult: func(r *Result) { resultN++ },
	}

	for _, line := range lines {
		if err := parseOutputLine(execution, line, opts); err != nil {
			t.Fatal(err)
		}
	}

	if stdoutN != 1 || stderrN != 1 || resultN != 1 {
		t.Errorf("unexpected callback counts: stdout=%d stderr=%d result=%d", stdoutN, stderrN, resultN)
	}
	if execution.ExecutionCount != 3 {
		t.Errorf("expected execution count 3, got %d", execution.ExecutionCount)
	}
	if execution.Text() != "42" {
		t.Errorf("expected text %q, got %q", "42", execution.Text())
	}
	if !strings.Contains(strings.Join(execution.Logs.Stdout, ""), "hello") {
		t.Errorf("stdout not captured: %v", execution.Logs.Stdout)
	}
}

func TestParseOutput_Error(t *testing.T) {
	execution := NewExecution()
	line := `{"type": "error", "name": "NameError", "value": "x is not defined", "traceback": "..."}`
	var called bool
	opts := &RunCodeOpts{
		OnError: func(err *ExecutionError) { called = true },
	}
	if err := parseOutputLine(execution, line, opts); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("OnError not called")
	}
	if execution.Error == nil || execution.Error.Name != "NameError" {
		t.Errorf("error not captured: %+v", execution.Error)
	}
}

func TestResult_Formats(t *testing.T) {
	raw := map[string]any{
		"type":           "result",
		"text":           "x",
		"html":           "<x/>",
		"png":            "base64...",
		"is_main_result": true,
		"custom_mime":    "something",
	}
	r := newResultFromRaw(raw)
	formats := r.Formats()

	want := map[string]bool{
		"text": true, "html": true, "png": true, "custom_mime": true,
	}
	for k := range want {
		found := false
		for _, f := range formats {
			if f == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in formats, got %v", k, formats)
		}
	}

	if !r.IsMainResult {
		t.Error("expected IsMainResult to be true")
	}
	if r.Extra["custom_mime"] != "something" {
		t.Errorf("custom mime not captured in Extra")
	}
}

func TestContextFromJSON(t *testing.T) {
	m := map[string]any{"id": "ctx-1", "language": "python", "cwd": "/tmp"}
	c := contextFromJSON(m)
	if c.ID != "ctx-1" || c.Language != "python" || c.Cwd != "/tmp" {
		t.Errorf("context mismatch: %+v", c)
	}
}

func TestMapHTTPError(t *testing.T) {
	err := mapHTTPError(404, "not found")
	if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("expected NotFoundError, got %T", err)
	}
	err = mapHTTPError(401, "bad key")
	if _, ok := err.(*AuthenticationError); !ok {
		t.Errorf("expected AuthenticationError, got %T", err)
	}
	err = mapHTTPError(429, "slow down")
	if _, ok := err.(*RateLimitError); !ok {
		t.Errorf("expected RateLimitError, got %T", err)
	}
	err = mapHTTPError(502, "timeout")
	if _, ok := err.(*TimeoutError); !ok {
		t.Errorf("expected TimeoutError, got %T", err)
	}
	err = mapHTTPError(500, "boom")
	if _, ok := err.(*SandboxError); !ok {
		t.Errorf("expected SandboxError, got %T", err)
	}
}

func TestContextID(t *testing.T) {
	id, err := contextID("abc")
	if err != nil || id != "abc" {
		t.Errorf("unexpected: id=%s err=%v", id, err)
	}

	id, err = contextID(&Context{ID: "x"})
	if err != nil || id != "x" {
		t.Errorf("unexpected: id=%s err=%v", id, err)
	}

	_, err = contextID(123)
	if _, ok := err.(*InvalidArgumentError); !ok {
		t.Errorf("expected InvalidArgumentError, got %T", err)
	}
}
