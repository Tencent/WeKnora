package manifest

// Point names an extension point: a kind of capability a plugin contributes.
// The string is also the key under `contributes` in a manifest.
type Point string

// Extension points with a builtin implementation today. Points the design
// opens later (mcpServers, skills, events, pipelineHooks, ui, ...) are added
// here when their first contribution lands, so a manifest can never declare a
// point nothing consumes.
const (
	PointModelVendors Point = "modelVendors"
	PointConnectors   Point = "connectors"
	PointIMChannels   Point = "imChannels"
	PointWebSearch    Point = "webSearch"
	PointTools        Point = "tools"
	PointParsers      Point = "parsers"
)

// PointInfo describes an extension point.
type PointInfo struct {
	Point Point `json:"point"`
	// ThirdParty reports whether plugins other than builtins may contribute
	// to this point yet. Builtins always may.
	ThirdParty bool `json:"thirdParty"`
}

// points lists every known extension point in display order. Vector stores,
// object storage and sandboxes are deliberately absent: they are
// infrastructure owned by WeKnora itself, not extension points.
var points = []PointInfo{
	{Point: PointModelVendors},
	{Point: PointConnectors},
	{Point: PointIMChannels},
	{Point: PointWebSearch},
	{Point: PointTools},
	{Point: PointParsers},
}

// Points returns every known extension point in display order.
func Points() []PointInfo {
	out := make([]PointInfo, len(points))
	copy(out, points)
	return out
}

// LookupPoint returns the info for a point name.
func LookupPoint(p Point) (PointInfo, bool) {
	for _, info := range points {
		if info.Point == p {
			return info, true
		}
	}
	return PointInfo{}, false
}
