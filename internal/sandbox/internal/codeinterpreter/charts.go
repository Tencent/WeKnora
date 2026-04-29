package codeinterpreter

import "encoding/json"

// ChartType represents the kind of chart returned by the server.
type ChartType string

const (
	// ChartTypeLine represents a line chart.
	ChartTypeLine ChartType = "line"
	// ChartTypeScatter represents a scatter plot.
	ChartTypeScatter ChartType = "scatter"
	// ChartTypeBar represents a bar chart.
	ChartTypeBar ChartType = "bar"
	// ChartTypePie represents a pie chart.
	ChartTypePie ChartType = "pie"
	// ChartTypeBoxAndWhisker represents a box and whisker plot.
	ChartTypeBoxAndWhisker ChartType = "box_and_whisker"
	// ChartTypeSuperChart represents a super chart.
	ChartTypeSuperChart ChartType = "superchart"
	// ChartTypeUnknown represents an unknown chart type.
	ChartTypeUnknown ChartType = "unknown"
)

// ScaleType represents an axis scale type (linear, log, etc.)
type ScaleType string

const (
	// ScaleTypeLinear represents a linear scale.
	ScaleTypeLinear ScaleType = "linear"
	// ScaleTypeDatetime represents a datetime scale.
	ScaleTypeDatetime ScaleType = "datetime"
	// ScaleTypeCategorical represents a categorical scale.
	ScaleTypeCategorical ScaleType = "categorical"
	// ScaleTypeLog represents a log scale.
	ScaleTypeLog ScaleType = "log"
	// ScaleTypeSymlog represents a symlog scale.
	ScaleTypeSymlog ScaleType = "symlog"
	// ScaleTypeLogit represents a logit scale.
	ScaleTypeLogit ScaleType = "logit"
	// ScaleTypeFunction represents a function scale.
	ScaleTypeFunction ScaleType = "function"
	// ScaleTypeFunctionLog represents a function log scale.
	ScaleTypeFunctionLog ScaleType = "functionlog"
	// ScaleTypeAsinh represents an asinh scale.
	ScaleTypeAsinh ScaleType = "asinh"
	// ScaleTypeUnknown represents an unknown scale.
	ScaleTypeUnknown ScaleType = "unknown"
)

// Chart is the common interface implemented by all concrete chart types.
// Use a type switch on the concrete types to inspect specialized fields, e.g.
//
//	switch c := result.Chart.(type) {
//	case *LineChart: ...
//	case *BarChart: ...
//	}
type Chart interface {
	ChartType() ChartType
	ChartTitle() string
	// ToDict returns the raw JSON representation of the chart.
	ToJSON() map[string]any
}

// BaseChart contains the fields shared by every chart type.
type BaseChart struct {
	Type     ChartType      `json:"type"`
	Title    string         `json:"title"`
	Elements []any          `json:"elements"`
	raw      map[string]any `json:"-"`
}

// ChartType returns the type of the chart.
func (c *BaseChart) ChartType() ChartType { return c.Type }

// ChartTitle returns the title of the chart.
func (c *BaseChart) ChartTitle() string { return c.Title }

// ToJSON returns the raw JSON representation of the chart.
func (c *BaseChart) ToJSON() map[string]any { return c.raw }

// Chart2D is the base for charts that live on a 2D plane.
type Chart2D struct {
	BaseChart
	XLabel string `json:"x_label,omitempty"`
	YLabel string `json:"y_label,omitempty"`
	XUnit  string `json:"x_unit,omitempty"`
	YUnit  string `json:"y_unit,omitempty"`
}

// PointData is one series in a point based chart (line/scatter).
type PointData struct {
	Label  string   `json:"label"`
	Points [][2]any `json:"points"`
}

// PointChart is the base for line/scatter.
type PointChart struct {
	Chart2D
	XTicks      []any       `json:"x_ticks"`
	XTickLabels []string    `json:"x_tick_labels"`
	XScale      ScaleType   `json:"x_scale"`
	YTicks      []any       `json:"y_ticks"`
	YTickLabels []string    `json:"y_tick_labels"`
	YScale      ScaleType   `json:"y_scale"`
	Points      []PointData `json:"-"`
}

// LineChart represents a line chart.
type LineChart struct {
	PointChart
}

// ScatterChart represents a scatter chart.
type ScatterChart struct {
	PointChart
}

// BarData represents a single bar in a bar chart.
type BarData struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Group string `json:"group"`
}

// BarChart represents a bar chart.
type BarChart struct {
	Chart2D
	Bars []BarData `json:"-"`
}

// PieData represents a slice of a pie chart.
type PieData struct {
	Label  string  `json:"label"`
	Angle  float64 `json:"angle"`
	Radius float64 `json:"radius"`
}

// PieChart represents a pie chart.
type PieChart struct {
	BaseChart
	Slices []PieData `json:"-"`
}

// BoxAndWhiskerData represents one box-and-whisker series.
type BoxAndWhiskerData struct {
	Label         string    `json:"label"`
	Min           float64   `json:"min"`
	FirstQuartile float64   `json:"first_quartile"`
	Median        float64   `json:"median"`
	ThirdQuartile float64   `json:"third_quartile"`
	Max           float64   `json:"max"`
	Outliers      []float64 `json:"outliers"`
}

// BoxAndWhiskerChart represents a box-and-whisker chart.
type BoxAndWhiskerChart struct {
	Chart2D
	Boxes []BoxAndWhiskerData `json:"-"`
}

// SuperChart is a composite chart containing multiple sub-charts.
type SuperChart struct {
	BaseChart
	Charts []Chart `json:"-"`
}

// UnknownChart is used when the server returns a chart type that the SDK does
// not yet understand; all data is still accessible through ToDict().
type UnknownChart struct {
	BaseChart
}

// deserializeChart converts the raw JSON payload coming from the server into
// the matching Chart implementation.
func deserializeChart(data map[string]any) Chart {
	if data == nil {
		return nil
	}

	typeStr, _ := data["type"].(string)
	ct := ChartType(typeStr)

	base := BaseChart{
		Type:  ct,
		Title: getString(data, "title"),
		raw:   data,
	}
	if el, ok := data["elements"].([]any); ok {
		base.Elements = el
	}

	switch ct {
	case ChartTypeLine:
		pc := buildPointChart(base, data)
		return &LineChart{PointChart: pc}
	case ChartTypeScatter:
		pc := buildPointChart(base, data)
		return &ScatterChart{PointChart: pc}
	case ChartTypeBar:
		return buildBarChart(base, data)
	case ChartTypePie:
		pc := &PieChart{BaseChart: base}
		pc.Type = ChartTypePie
		if raw, ok := data["elements"].([]any); ok {
			for _, item := range raw {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				pc.Slices = append(pc.Slices, PieData{
					Label:  getString(m, "label"),
					Angle:  getFloat(m, "angle"),
					Radius: getFloat(m, "radius"),
				})
			}
		}
		return pc
	case ChartTypeBoxAndWhisker:
		c2 := Chart2D{
			BaseChart: base,
			XLabel:    getString(data, "x_label"),
			YLabel:    getString(data, "y_label"),
			XUnit:     getString(data, "x_unit"),
			YUnit:     getString(data, "y_unit"),
		}
		bwc := &BoxAndWhiskerChart{Chart2D: c2}
		bwc.Type = ChartTypeBoxAndWhisker
		if raw, ok := data["elements"].([]any); ok {
			for _, item := range raw {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				bwc.Boxes = append(bwc.Boxes, BoxAndWhiskerData{
					Label:         getString(m, "label"),
					Min:           getFloat(m, "min"),
					FirstQuartile: getFloat(m, "first_quartile"),
					Median:        getFloat(m, "median"),
					ThirdQuartile: getFloat(m, "third_quartile"),
					Max:           getFloat(m, "max"),
					Outliers:      getFloatSlice(m, "outliers"),
				})
			}
		}
		return bwc
	case ChartTypeSuperChart:
		sc := &SuperChart{BaseChart: base}
		sc.Type = ChartTypeSuperChart
		if raw, ok := data["elements"].([]any); ok {
			for _, item := range raw {
				if m, ok := item.(map[string]any); ok {
					sc.Charts = append(sc.Charts, deserializeChart(m))
				}
			}
		}
		return sc
	default:
		base.Type = ChartTypeUnknown
		return &UnknownChart{BaseChart: base}
	}
}

func buildBarChart(base BaseChart, data map[string]any) *BarChart {
	c2 := Chart2D{
		BaseChart: base,
		XLabel:    getString(data, "x_label"),
		YLabel:    getString(data, "y_label"),
		XUnit:     getString(data, "x_unit"),
		YUnit:     getString(data, "y_unit"),
	}
	bc := &BarChart{Chart2D: c2}
	bc.Type = ChartTypeBar
	if raw, ok := data["elements"].([]any); ok {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			bc.Bars = append(bc.Bars, BarData{
				Label: getString(m, "label"),
				Value: getString(m, "value"),
				Group: getString(m, "group"),
			})
		}
	}
	return bc
}

func buildPointChart(base BaseChart, data map[string]any) PointChart {
	c2 := Chart2D{
		BaseChart: base,
		XLabel:    getString(data, "x_label"),
		YLabel:    getString(data, "y_label"),
		XUnit:     getString(data, "x_unit"),
		YUnit:     getString(data, "y_unit"),
	}

	pc := PointChart{
		Chart2D:     c2,
		XTicks:      getInterfaceSlice(data, "x_ticks"),
		XTickLabels: getStringSlice(data, "x_tick_labels"),
		XScale:      ScaleType(getString(data, "x_scale")),
		YTicks:      getInterfaceSlice(data, "y_ticks"),
		YTickLabels: getStringSlice(data, "y_tick_labels"),
		YScale:      ScaleType(getString(data, "y_scale")),
	}

	if raw, ok := data["elements"].([]any); ok {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			pd := PointData{Label: getString(m, "label")}
			if pts, ok := m["points"].([]any); ok {
				for _, p := range pts {
					arr, ok := p.([]any)
					if !ok || len(arr) < 2 {
						continue
					}
					pd.Points = append(pd.Points, [2]any{arr[0], arr[1]})
				}
			}
			pc.Points = append(pc.Points, pd)
		}
	}
	return pc
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case float32:
			return float64(n)
		case int:
			return float64(n)
		case int64:
			return float64(n)
		case json.Number:
			f, _ := n.Float64()
			return f
		}
	}
	return 0
}

func getFloatSlice(m map[string]any, key string) []float64 {
	v, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]float64, 0, len(v))
	for _, item := range v {
		switch n := item.(type) {
		case float64:
			out = append(out, n)
		case int:
			out = append(out, float64(n))
		case json.Number:
			f, _ := n.Float64()
			out = append(out, f)
		}
	}
	return out
}

func getStringSlice(m map[string]any, key string) []string {
	v, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(v))
	for _, item := range v {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func getInterfaceSlice(m map[string]any, key string) []any {
	v, ok := m[key].([]any)
	if !ok {
		return nil
	}
	return v
}
