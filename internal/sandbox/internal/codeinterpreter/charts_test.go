package codeinterpreter

import (
	"encoding/json"
	"testing"
)

func mustUnmarshal(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestDeserializeChart_Bar(t *testing.T) {
	raw := `{
		"type": "bar",
		"title": "Book Sales by Authors",
		"x_label": "Authors",
		"y_label": "Number of Books Sold",
		"elements": [
			{"label": "Author A", "value": "100", "group": "Books Sold"},
			{"label": "Author B", "value": "200", "group": "Books Sold"},
			{"label": "Author C", "value": "300", "group": "Books Sold"},
			{"label": "Author D", "value": "400", "group": "Books Sold"}
		]
	}`
	c := deserializeChart(mustUnmarshal(t, raw))
	bc, ok := c.(*BarChart)
	if !ok {
		t.Fatalf("expected *BarChart, got %T", c)
	}
	if bc.Type != ChartTypeBar {
		t.Errorf("unexpected type: %s", bc.Type)
	}
	if bc.Title != "Book Sales by Authors" {
		t.Errorf("unexpected title: %q", bc.Title)
	}
	if bc.XLabel != "Authors" || bc.YLabel != "Number of Books Sold" {
		t.Errorf("labels not parsed: x=%q y=%q", bc.XLabel, bc.YLabel)
	}
	if bc.XUnit != "" || bc.YUnit != "" {
		t.Errorf("expected empty units, got x=%q y=%q", bc.XUnit, bc.YUnit)
	}
	if len(bc.Bars) != 4 {
		t.Fatalf("expected 4 bars, got %d", len(bc.Bars))
	}

	labels := []string{}
	values := []string{}
	groups := []string{}
	for _, b := range bc.Bars {
		labels = append(labels, b.Label)
		values = append(values, b.Value)
		groups = append(groups, b.Group)
	}
	want := []string{"Author A", "Author B", "Author C", "Author D"}
	for i := range want {
		if labels[i] != want[i] {
			t.Errorf("label[%d] got %q want %q", i, labels[i], want[i])
		}
	}
	for i, v := range []string{"100", "200", "300", "400"} {
		if values[i] != v {
			t.Errorf("value[%d] got %q want %q", i, values[i], v)
		}
	}
	for _, g := range groups {
		if g != "Books Sold" {
			t.Errorf("unexpected group: %q", g)
		}
	}
}

func TestDeserializeChart_PieDetailed(t *testing.T) {
	raw := `{
		"type": "pie",
		"title": "Will I wake up early tomorrow?",
		"elements": [
			{"label": "No", "angle": 324, "radius": 1},
			{"label": "No, in blue", "angle": 36, "radius": 1}
		]
	}`
	c := deserializeChart(mustUnmarshal(t, raw))
	pc, ok := c.(*PieChart)
	if !ok {
		t.Fatalf("expected *PieChart, got %T", c)
	}
	if pc.Title != "Will I wake up early tomorrow?" {
		t.Errorf("unexpected title: %q", pc.Title)
	}
	if len(pc.Slices) != 2 {
		t.Fatalf("expected 2 slices, got %d", len(pc.Slices))
	}
	if pc.Slices[0].Label != "No" || pc.Slices[0].Angle != 324 || pc.Slices[0].Radius != 1 {
		t.Errorf("first slice wrong: %+v", pc.Slices[0])
	}
	if pc.Slices[1].Label != "No, in blue" || pc.Slices[1].Angle != 36 {
		t.Errorf("second slice wrong: %+v", pc.Slices[1])
	}
}

func TestDeserializeChart_Scatter(t *testing.T) {
	raw := `{
		"type": "scatter",
		"title": null,
		"x_label": "A",
		"y_label": "B",
		"x_scale": "linear",
		"y_scale": "linear",
		"x_ticks": [0.1, 0.2, 0.3],
		"y_ticks": [0.5, 0.6, 0.7],
		"x_tick_labels": ["0.1", "0.2", "0.3"],
		"y_tick_labels": ["0.5", "0.6", "0.7"],
		"elements": [
			{"label": "Dataset 1", "points": [[1, 2], [3, 4], [5, 6], [7, 8], [9, 10]]},
			{"label": "Dataset 2", "points": [[1, 1], [2, 2], [3, 3], [4, 4], [5, 5], [6, 6], [7, 7], [8, 8], [9, 9], [10, 10]]}
		]
	}`

	c := deserializeChart(mustUnmarshal(t, raw))
	sc, ok := c.(*ScatterChart)
	if !ok {
		t.Fatalf("expected *ScatterChart, got %T", c)
	}
	if sc.Title != "" {
		t.Errorf("expected empty title, got %q", sc.Title)
	}
	if sc.XLabel != "A" || sc.YLabel != "B" {
		t.Errorf("labels: %+v", sc)
	}
	if sc.XScale != ScaleTypeLinear || sc.YScale != ScaleTypeLinear {
		t.Errorf("scale wrong: x=%s y=%s", sc.XScale, sc.YScale)
	}
	if len(sc.Points) != 2 {
		t.Fatalf("expected 2 datasets, got %d", len(sc.Points))
	}
	if sc.Points[0].Label != "Dataset 1" || len(sc.Points[0].Points) != 5 {
		t.Errorf("dataset 1 wrong: %+v", sc.Points[0])
	}
	if sc.Points[1].Label != "Dataset 2" || len(sc.Points[1].Points) != 10 {
		t.Errorf("dataset 2 wrong: %+v", sc.Points[1])
	}
}

func TestDeserializeChart_BoxAndWhisker(t *testing.T) {
	raw := `{
		"type": "box_and_whisker",
		"title": "Exam Scores Distribution",
		"x_label": "Class",
		"y_label": "Score",
		"elements": [
			{"label": "Class A", "min": 78, "first_quartile": 85, "median": 88, "third_quartile": 90, "max": 92, "outliers": []},
			{"label": "Class B", "min": 84, "first_quartile": 84.75, "median": 88, "third_quartile": 90.5, "max": 95, "outliers": [76]},
			{"label": "Class C", "min": 75, "first_quartile": 79, "median": 82, "third_quartile": 86, "max": 88, "outliers": []}
		]
	}`
	c := deserializeChart(mustUnmarshal(t, raw))
	bw, ok := c.(*BoxAndWhiskerChart)
	if !ok {
		t.Fatalf("expected *BoxAndWhiskerChart, got %T", c)
	}
	if bw.Type != ChartTypeBoxAndWhisker {
		t.Errorf("unexpected type: %s", bw.Type)
	}
	if bw.XLabel != "Class" || bw.YLabel != "Score" {
		t.Errorf("labels: %+v", bw)
	}
	if bw.XUnit != "" || bw.YUnit != "" {
		t.Errorf("expected empty units")
	}
	if len(bw.Boxes) != 3 {
		t.Fatalf("expected 3 boxes, got %d", len(bw.Boxes))
	}
	cases := []struct {
		label                    string
		min, fq, median, tq, max float64
		outlierLen               int
	}{
		{"Class A", 78, 85, 88, 90, 92, 0},
		{"Class B", 84, 84.75, 88, 90.5, 95, 1},
		{"Class C", 75, 79, 82, 86, 88, 0},
	}
	for i, c := range cases {
		b := bw.Boxes[i]
		if b.Label != c.label || b.Min != c.min || b.FirstQuartile != c.fq ||
			b.Median != c.median || b.ThirdQuartile != c.tq || b.Max != c.max ||
			len(b.Outliers) != c.outlierLen {
			t.Errorf("box[%d] mismatch: %+v (want %+v)", i, b, c)
		}
	}
	if bw.Boxes[1].Outliers[0] != 76 {
		t.Errorf("outlier got %v, want 76", bw.Boxes[1].Outliers[0])
	}
}

func TestDeserializeChart_Super(t *testing.T) {
	raw := `{
		"type": "superchart",
		"title": "Multiple Charts Example",
		"elements": [
			{
				"type": "line",
				"title": "Sine Wave",
				"elements": [
					{"label": "sin", "points": [[0, 0], [1, 0.5]]}
				]
			},
			{
				"type": "scatter",
				"title": "Scatter Plot",
				"x_label": "X",
				"y_label": "Y",
				"elements": [
					{"label": "Dataset 1", "points": [[0, 0], [1, 1], [2, 2], [3, 3], [4, 4]]}
				]
			}
		]
	}`
	c := deserializeChart(mustUnmarshal(t, raw))
	sc, ok := c.(*SuperChart)
	if !ok {
		t.Fatalf("expected *SuperChart, got %T", c)
	}
	if sc.Type != ChartTypeSuperChart {
		t.Errorf("type: %s", sc.Type)
	}
	if sc.Title != "Multiple Charts Example" {
		t.Errorf("title: %q", sc.Title)
	}
	if len(sc.Charts) != 2 {
		t.Fatalf("expected 2 sub-charts, got %d", len(sc.Charts))
	}
	lc, ok := sc.Charts[0].(*LineChart)
	if !ok {
		t.Fatalf("first sub-chart not line: %T", sc.Charts[0])
	}
	if lc.Title != "Sine Wave" || lc.XLabel != "" {
		t.Errorf("line title/label: %+v", lc)
	}
	if len(lc.Points) != 1 || len(lc.Points[0].Points) != 2 {
		t.Errorf("line points wrong: %+v", lc.Points)
	}
	sct, ok := sc.Charts[1].(*ScatterChart)
	if !ok {
		t.Fatalf("second sub-chart not scatter: %T", sc.Charts[1])
	}
	if sct.XLabel != "X" || sct.YLabel != "Y" {
		t.Errorf("scatter labels: %+v", sct)
	}
	if len(sct.Points) != 1 || len(sct.Points[0].Points) != 5 {
		t.Errorf("scatter points wrong: %+v", sct.Points)
	}
}

func TestDeserializeChart_LogScale(t *testing.T) {
	raw := `{
		"type": "line",
		"title": "Chart with Log Scale on Y-axis",
		"x_label": "X-axis",
		"y_label": "Y-axis (log scale)",
		"y_unit": "log scale",
		"x_scale": "linear",
		"y_scale": "log",
		"elements": [{"label": "y = e^x", "points": [[1, 2.7], [2, 7.3]]}]
	}`
	c := deserializeChart(mustUnmarshal(t, raw))
	lc, ok := c.(*LineChart)
	if !ok {
		t.Fatalf("expected *LineChart, got %T", c)
	}
	if lc.YScale != ScaleTypeLog || lc.XScale != ScaleTypeLinear {
		t.Errorf("scales: x=%s y=%s", lc.XScale, lc.YScale)
	}
	if lc.YUnit != "log scale" {
		t.Errorf("y unit: %q", lc.YUnit)
	}
}

func TestDeserializeChart_DatetimeScale(t *testing.T) {
	raw := `{
		"type": "line",
		"title": "T",
		"x_scale": "datetime",
		"y_scale": "linear",
		"elements": []
	}`
	c := deserializeChart(mustUnmarshal(t, raw))
	lc, ok := c.(*LineChart)
	if !ok {
		t.Fatalf("expected *LineChart")
	}
	if lc.XScale != ScaleTypeDatetime {
		t.Errorf("want datetime, got %s", lc.XScale)
	}
}

func TestDeserializeChart_CategoricalScale(t *testing.T) {
	raw := `{
		"type": "line",
		"title": "T",
		"x_scale": "linear",
		"y_scale": "categorical",
		"elements": []
	}`
	c := deserializeChart(mustUnmarshal(t, raw))
	lc, ok := c.(*LineChart)
	if !ok {
		t.Fatalf("expected *LineChart")
	}
	if lc.YScale != ScaleTypeCategorical {
		t.Errorf("want categorical, got %s", lc.YScale)
	}
}

func TestChart_JSONSerialization(t *testing.T) {
	raw := `{
		"type": "scatter",
		"title": "S",
		"elements": [{"label": "Dataset", "points": [[0.1, 0.2]]}]
	}`
	result := newResultFromRaw(map[string]any{
		"type":           "result",
		"chart":          mustUnmarshal(t, raw),
		"is_main_result": true,
	})
	if result.Chart == nil {
		t.Fatal("expected chart to be set")
	}
	if result.Chart.ChartType() != ChartTypeScatter {
		t.Errorf("type: %s", result.Chart.ChartType())
	}

	exec := NewExecution()
	exec.Results = append(exec.Results, result)
	exec.Results[0].IsMainResult = true

	serialized, err := exec.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if len(serialized) == 0 {
		t.Error("expected non-empty serialization")
	}

	// ToJSON on the chart must round-trip through json.
	dict := result.Chart.ToJSON()
	b, err := json.Marshal(dict)
	if err != nil {
		t.Fatalf("marshal chart dict: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back["type"] != "scatter" {
		t.Errorf("expected scatter in roundtrip, got %v", back["type"])
	}
}

func TestDeserializeChart_UnknownKeepsElements(t *testing.T) {
	raw := `{
		"type": "weird",
		"title": "Two Concentric Circles",
		"elements": []
	}`
	c := deserializeChart(mustUnmarshal(t, raw))
	if c.ChartType() != ChartTypeUnknown {
		t.Errorf("expected unknown")
	}
	if c.ChartTitle() != "Two Concentric Circles" {
		t.Errorf("title: %q", c.ChartTitle())
	}
	uc, ok := c.(*UnknownChart)
	if !ok {
		t.Fatalf("expected *UnknownChart, got %T", c)
	}
	if len(uc.Elements) != 0 {
		t.Errorf("expected 0 elements, got %d", len(uc.Elements))
	}
}

func TestDeserializeChart_NilInput(t *testing.T) {
	if deserializeChart(nil) != nil {
		t.Error("deserializeChart(nil) should return nil")
	}
}
