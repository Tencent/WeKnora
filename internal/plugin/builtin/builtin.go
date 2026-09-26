// Package builtin describes the capabilities compiled into WeKnora as builtin
// plugins, so the plugin registry lists them next to third-party ones.
//
// Contributions are grouped into one plugin per vendor: Feishu's wiki and
// drive connectors and its IM channel all belong to weknora.feishu, which is
// the granularity a tenant enables or disables and the granularity a
// third-party plugin ships at.
package builtin

import (
	"encoding/base64"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
)

// Sources is what builtin plugins are described from: the metadata each
// domain already keeps, limited to what is actually registered in this
// process.
type Sources struct {
	// Version is the WeKnora build version; builtins share it.
	Version      string
	ModelVendors []*providers.Definition
	Connectors   []datasource.ConnectorMetadata
	IMPlatforms  []im.PlatformInfo
	WebSearch    []types.WebSearchProviderTypeInfo
	Tools        []tools.AvailableTool
	Parsers      []Parser
}

// Parser is the metadata of one locally registered parser engine.
type Parser struct {
	Name        string
	Description string
}

// Plugin IDs that group contributions without a vendor of their own.
const (
	agentToolsKey = "agent-tools"
	docParserKey  = "docparser"
)

// vendorOverrides maps a contribution ID to its vendor where the two differ.
var vendorOverrides = map[manifest.Point]map[string]string{
	manifest.PointConnectors: {
		types.ConnectorTypeLark:        "feishu",
		types.ConnectorTypeFeishuDrive: "feishu",
		types.ConnectorTypeLarkDrive:   "feishu",
	},
	manifest.PointIMChannels: {"lark": "feishu"},
	manifest.PointParsers: {
		"builtin":            docParserKey,
		"simple":             docParserKey,
		"anydoc":             docParserKey,
		"mineru_cloud":       "mineru",
		"paddleocr_vl":       "paddleocr",
		"paddleocr_vl_cloud": "paddleocr",
	},
}

// vendorNames names plugins that group several contributions. A vendor absent
// here takes the name of its first contribution, model vendors first.
var vendorNames = map[string]manifest.LocalizedText{
	"feishu":      manifest.Text("Feishu / Lark", map[string]string{"zh-CN": "飞书 / Lark"}),
	"dingtalk":    manifest.Text("DingTalk", map[string]string{"zh-CN": "钉钉"}),
	"mineru":      manifest.Text("MinerU", nil),
	"paddleocr":   manifest.Text("PaddleOCR-VL", nil),
	docParserKey:  manifest.Text("Document parsing", map[string]string{"zh-CN": "文档解析"}),
	agentToolsKey: manifest.Text("Agent tools", map[string]string{"zh-CN": "Agent 内置工具"}),
}

// requiredVendors are builtins a tenant may not disable: without them agents
// and document import stop working altogether.
var requiredVendors = map[string]bool{agentToolsKey: true, docParserKey: true}

// Manifests describes every builtin capability in src as builtin plugins,
// sorted by plugin ID.
func Manifests(src Sources) []*manifest.Manifest {
	b := &builder{version: normalizeVersion(src.Version), plugins: make(map[string]*manifest.Manifest)}
	// Model vendors go first so a vendor that also offers web search (Zhipu)
	// is named after its model brand.
	for _, d := range src.ModelVendors {
		b.add(manifest.PointModelVendors, d.ID, manifest.Contribution{
			ID:          d.ID,
			Name:        manifest.Text(d.Name, d.Names),
			Description: manifest.Text(d.Description, d.Descriptions),
			Order:       d.Order,
			Extra:       map[string]any{"modelTypes": d.ModelTypes},
		})
	}
	for _, d := range src.ModelVendors {
		// The vendor's logo stands for its plugin.
		if len(d.Icon) > 0 {
			if m := b.plugins[pluginID(b.vendor(manifest.PointModelVendors, d.ID))]; m != nil && m.IconData == "" {
				m.IconData = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(d.Icon)
			}
		}
	}
	for _, c := range src.Connectors {
		b.add(manifest.PointConnectors, c.Type, manifest.Contribution{
			ID:           c.Type,
			Name:         manifest.Text(c.Name, nil),
			Description:  manifest.Text(c.Description, nil),
			Icon:         c.Icon,
			Capabilities: c.Capabilities,
			Order:        c.Priority,
			Extra:        map[string]any{"authType": c.AuthType},
		})
	}
	for _, p := range src.IMPlatforms {
		b.add(manifest.PointIMChannels, p.ID, manifest.Contribution{
			ID:    p.ID,
			Name:  manifest.Text(p.Name, p.Names),
			Order: p.Order,
		})
	}
	for i, w := range src.WebSearch {
		b.add(manifest.PointWebSearch, w.ID, manifest.Contribution{
			ID:          w.ID,
			Name:        manifest.Text(w.Name, nil),
			Description: manifest.Text(w.Description, nil),
			Order:       i,
		})
	}
	for i, t := range src.Tools {
		b.addTo(agentToolsKey, manifest.PointTools, t.Name, manifest.Contribution{
			ID:          t.Name,
			Name:        manifest.Text(t.Label, nil),
			Description: manifest.Text(t.Description, nil),
			Order:       i,
		})
	}
	for i, p := range src.Parsers {
		b.add(manifest.PointParsers, p.Name, manifest.Contribution{
			ID:          p.Name,
			Name:        manifest.Text(p.Name, nil),
			Description: manifest.Text(p.Description, nil),
			Order:       i,
		})
	}
	return b.result()
}

// Register adds every builtin in src to r.
func Register(r *registry.Registry, src Sources) error {
	for _, m := range Manifests(src) {
		if err := r.Register(m); err != nil {
			return err
		}
	}
	return nil
}

type builder struct {
	version string
	plugins map[string]*manifest.Manifest
}

func (b *builder) add(point manifest.Point, id string, c manifest.Contribution) {
	b.addTo(b.vendor(point, id), point, id, c)
}

// vendor is the plugin key a builtin contribution files under.
func (b *builder) vendor(point manifest.Point, id string) string {
	if v, ok := vendorOverrides[point][id]; ok {
		return v
	}
	return id
}

// addTo files a contribution under a vendor's plugin. The contribution keeps
// its bare ID as alias, since that is what existing rows store.
func (b *builder) addTo(vendor string, point manifest.Point, alias string, c manifest.Contribution) {
	c.Aliases = []string{alias}
	id := pluginID(vendor)
	m, ok := b.plugins[id]
	if !ok {
		name, named := vendorNames[vendor]
		if !named {
			name = c.Name
		}
		m = &manifest.Manifest{
			SchemaVersion: manifest.SchemaVersion,
			ID:            id,
			Version:       b.version,
			Name:          name,
			Publisher:     manifest.Publisher{ID: manifest.BuiltinPublisher, Name: "WeKnora"},
			Builtin:       true,
			Required:      requiredVendors[vendor],
			Runtime:       manifest.Runtime{Type: manifest.RuntimeBuiltin},
			Contributes:   manifest.Contributions{},
		}
		b.plugins[id] = m
	}
	m.Contributes[point] = append(m.Contributes[point], c)
}

func (b *builder) result() []*manifest.Manifest {
	out := make([]*manifest.Manifest, 0, len(b.plugins))
	for _, m := range b.plugins {
		// A plugin with a single contribution borrows its description.
		if all := allContributions(m); len(all) == 1 {
			m.Description = all[0].Description
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func allContributions(m *manifest.Manifest) []manifest.Contribution {
	var out []manifest.Contribution
	for _, list := range m.Contributes {
		out = append(out, list...)
	}
	return out
}

// devVersion stands in when the build carries no usable version (local
// builds report "unknown").
const devVersion = "0.0.0-dev"

// normalizeVersion turns a build version ("v0.8.2", "0.8.2") into the bare
// semantic version manifests require.
func normalizeVersion(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	probe := &manifest.Manifest{Version: v}
	if !probe.HasValidVersion() {
		return devVersion
	}
	return v
}

// pluginID turns a vendor key into a builtin plugin ID. Plugin IDs allow no
// underscores, so azure_openai becomes weknora.azure-openai.
func pluginID(vendor string) string {
	return manifest.BuiltinPublisher + "." + strings.ReplaceAll(vendor, "_", "-")
}
