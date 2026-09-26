package manifest

// Point names an extension point: a kind of capability a plugin contributes.
// The string is also the key under `contributes` in a manifest.
type Point string

// Extension points. A point is added here when its first contribution lands,
// so a manifest can never declare a point nothing consumes.
const (
	PointModelVendors Point = "modelVendors"
	PointConnectors   Point = "connectors"
	PointIMChannels   Point = "imChannels"
	PointWebSearch    Point = "webSearch"
	PointTools        Point = "tools"
	PointParsers      Point = "parsers"
	// PointSkills contributes SKILL.md skill directories from the package.
	PointSkills Point = "skills"
	// PointMCPServers contributes MCP servers whose tools agents can use.
	PointMCPServers Point = "mcpServers"
	// PointPages contributes pages of their own, opened from the toolbox.
	PointPages Point = "pages"
	// PointSettingsSections contributes sections of the settings dialog.
	PointSettingsSections Point = "settingsSections"
	// PointKBTabs contributes tabs of the knowledge base page.
	PointKBTabs Point = "kbTabs"
)

// UIRoot is the package directory plugin pages live in. Only files under it
// are served to browsers.
const UIRoot = "ui/"

// IsUIPoint reports whether a point contributes a sandboxed page (an entry
// HTML file under UIRoot) rather than a capability.
func IsUIPoint(p Point) bool {
	return p == PointPages || p == PointSettingsSections || p == PointKBTabs
}

// PointInfo describes an extension point.
type PointInfo struct {
	Point Point `json:"point"`
	// ThirdParty reports whether plugins other than builtins may contribute
	// to this point yet. Builtins always may.
	ThirdParty bool `json:"thirdParty"`
	// Declarative reports whether a plugin without code (runtime
	// declarative) can contribute here: the manifest and package files
	// describe the contribution completely.
	Declarative bool `json:"declarative"`
}

// points lists every known extension point in display order. Vector stores,
// object storage and sandboxes are deliberately absent: they are
// infrastructure owned by WeKnora itself, not extension points.
var points = []PointInfo{
	{Point: PointModelVendors, ThirdParty: true, Declarative: true},
	{Point: PointConnectors, ThirdParty: true},
	{Point: PointIMChannels},
	{Point: PointWebSearch, ThirdParty: true},
	{Point: PointTools},
	{Point: PointParsers, ThirdParty: true},
	{Point: PointSkills, ThirdParty: true, Declarative: true},
	{Point: PointMCPServers, ThirdParty: true, Declarative: true},
	// Pages are static files; a plugin with code can also answer their
	// requests (the ui/request endpoint).
	{Point: PointPages, ThirdParty: true, Declarative: true},
	{Point: PointSettingsSections, ThirdParty: true, Declarative: true},
	{Point: PointKBTabs, ThirdParty: true, Declarative: true},
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
