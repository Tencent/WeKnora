package builtin

import (
	"sort"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
)

// Live is what this process has actually registered. Metadata tables list
// some connectors and providers that are not wired up yet (GitHub, Google
// Drive); only registered ones become contributions.
type Live struct {
	Version     string
	Connectors  *datasource.ConnectorRegistry
	WebSearch   *web_search.Registry
	IMPlatforms []string
}

// CollectSources gathers builtin metadata for what live has registered.
func CollectSources(live Live) Sources {
	src := Sources{
		Version: live.Version,
		Tools:   tools.AvailableToolDefinitions(),
	}
	for _, p := range modelruntime.List() {
		src.ModelVendors = append(src.ModelVendors, p.Definition)
	}
	if live.Connectors != nil {
		registered := toSet(live.Connectors.List())
		metas := datasource.ListAvailableConnectors()
		// The metadata table is a map, so equal priorities come back in
		// random order; break ties by type to keep the listing stable.
		sort.SliceStable(metas, func(i, j int) bool {
			if metas[i].Priority != metas[j].Priority {
				return metas[i].Priority < metas[j].Priority
			}
			return metas[i].Type < metas[j].Type
		})
		for _, meta := range metas {
			if registered[meta.Type] {
				src.Connectors = append(src.Connectors, meta)
				delete(registered, meta.Type)
			}
		}
		// A connector registered without metadata still shows up.
		for _, t := range sortedKeys(registered) {
			src.Connectors = append(src.Connectors, datasource.ConnectorMetadata{Type: t, Name: t, Priority: 1000})
		}
	}
	for _, p := range live.IMPlatforms {
		src.IMPlatforms = append(src.IMPlatforms, im.LookupPlatformInfo(p))
	}
	if live.WebSearch != nil {
		registered := toSet(live.WebSearch.Types())
		for _, info := range types.GetWebSearchProviderTypes() {
			if registered[info.ID] {
				src.WebSearch = append(src.WebSearch, info)
				delete(registered, info.ID)
			}
		}
		for _, id := range sortedKeys(registered) {
			src.WebSearch = append(src.WebSearch, types.WebSearchProviderTypeInfo{ID: id, Name: id})
		}
	}
	for _, e := range docparser.Engines() {
		src.Parsers = append(src.Parsers, Parser{Name: e.Name(), Description: e.Description()})
	}
	return src
}

// NewRegistry builds a plugin registry holding every builtin in live.
func NewRegistry(live Live) (*registry.Registry, error) {
	r := registry.New()
	if err := Register(r, CollectSources(live)); err != nil {
		return nil, err
	}
	return r, nil
}

func toSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[v] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
