package manifest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServedByPlugin reports whether an mcpServers contribution is served by
// the plugin's own process (declared without mcp.url) rather than a remote
// MCP endpoint.
func (c Contribution) ServedByPlugin() bool { return c.MCP == nil || c.MCP.URL == "" }

// HasToolPages reports whether an mcpServers contribution shows some tool
// results in one of the plugin's pages.
func (c Contribution) HasToolPages() bool {
	for _, v := range c.ToolViews {
		if v.View == ToolViewPage {
			return true
		}
	}
	return false
}

// Tool result views: how the chat shows a tool's structured result.
const (
	ToolViewTable    = "table"
	ToolViewCards    = "cards"
	ToolViewKV       = "kv"
	ToolViewMarkdown = "markdown"
	ToolViewJSON     = "json"
	// ToolViewPage renders the result in one of the plugin's pages.
	ToolViewPage = "page"
)

// ToolView maps a tool's structuredContent to a result view the chat
// renders; plugins bring no frontend code. Paths are dotted field paths
// into the structured content ("issues", "fields.status"); an empty Items
// path means the content itself.
type ToolView struct {
	View string `json:"view"               yaml:"view"`
	// Items is the list a table or cards show.
	Items string `json:"items,omitempty"    yaml:"items"`
	// Columns of a table, in order.
	Columns []ToolViewColumn `json:"columns,omitempty"  yaml:"columns"`
	// Title, Subtitle, Body and Link name the fields of each card; Title
	// and Link also head a kv view.
	Title    string `json:"title,omitempty"    yaml:"title"`
	Subtitle string `json:"subtitle,omitempty" yaml:"subtitle"`
	Body     string `json:"body,omitempty"     yaml:"body"`
	Link     string `json:"link,omitempty"     yaml:"link"`
	// Fields are the rows of a kv view (all top-level fields when empty).
	Fields []ToolViewColumn `json:"fields,omitempty"   yaml:"fields"`
	// Field holds the Markdown of a markdown view (the text content when
	// empty).
	Field string `json:"field,omitempty"    yaml:"field"`
	// Entry is the page of a page view, an .html file under ui/. It gets
	// the tool's arguments and result when it starts.
	Entry string `json:"entry,omitempty"    yaml:"entry"`
}

// ToolViewColumn is one table column or kv row: a field, its label, and
// optionally the field holding a URL to link it to.
type ToolViewColumn struct {
	Field string        `json:"field"          yaml:"field"`
	Title LocalizedText `json:"title,omitzero" yaml:"title"`
	Link  string        `json:"link,omitempty" yaml:"link"`
}

// UnmarshalYAML accepts a bare field name as shorthand.
func (c *ToolViewColumn) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		c.Field = n.Value
		return nil
	}
	type plain ToolViewColumn
	return n.Decode((*plain)(c))
}

// UnmarshalJSON accepts a bare field name as shorthand.
func (c *ToolViewColumn) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		c.Field = s
		return nil
	}
	type plain ToolViewColumn
	return json.Unmarshal(b, (*plain)(c))
}

var (
	toolNamePattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
	fieldPathPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)
)

func validateToolViews(views map[string]ToolView, where string, add func(string, ...any)) {
	names := make([]string, 0, len(views))
	for name := range views {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v := views[name]
		at := fmt.Sprintf("%s.toolViews.%s", where, name)
		if !toolNamePattern.MatchString(name) {
			add("%s: %q is not a tool name", at, name)
		}
		path := func(label, p string) {
			if p != "" && !fieldPathPattern.MatchString(p) {
				add("%s.%s %q must be a dotted field path", at, label, p)
			}
		}
		path("items", v.Items)
		for _, p := range []struct{ label, value string }{
			{"title", v.Title}, {"subtitle", v.Subtitle}, {"body", v.Body}, {"link", v.Link}, {"field", v.Field},
		} {
			path(p.label, p.value)
		}
		for i, c := range append(append([]ToolViewColumn{}, v.Columns...), v.Fields...) {
			if c.Field == "" {
				add("%s: column %d needs a field", at, i)
			}
			path("field", c.Field)
			path("link", c.Link)
		}
		switch v.View {
		case ToolViewTable:
			if len(v.Columns) == 0 {
				add("%s: a table needs columns", at)
			}
		case ToolViewCards:
			if v.Title == "" {
				add("%s: cards need a title field", at)
			}
		case ToolViewKV, ToolViewMarkdown, ToolViewJSON:
		case ToolViewPage:
			switch {
			case !isPackagePath(v.Entry) || !strings.HasPrefix(v.Entry, UIRoot):
				add("%s.entry %q must be a file under %s", at, v.Entry, UIRoot)
			case !strings.HasSuffix(v.Entry, ".html"):
				add("%s.entry %q must be an .html file", at, v.Entry)
			}
		default:
			add("%s.view must be table, cards, kv, markdown, json or page", at)
		}
	}
}
