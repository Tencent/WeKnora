package builtin

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
)

func sampleSources() Sources {
	return Sources{
		Version: "v0.8.2",
		ModelVendors: []*providers.Definition{
			{ID: "zhipu", Name: "Zhipu AI", Names: map[string]string{"zh-CN": "智谱"}},
			{ID: "azure_openai", Name: "Azure OpenAI"},
		},
		Connectors: []datasource.ConnectorMetadata{
			{Type: types.ConnectorTypeFeishu, Name: "Feishu (飞书)", Capabilities: []string{"incremental"}},
			{Type: types.ConnectorTypeLarkDrive, Name: "Lark Drive"},
			{Type: types.ConnectorTypeNotion, Name: "Notion", Priority: 1},
		},
		IMPlatforms: []im.PlatformInfo{im.LookupPlatformInfo("feishu"), im.LookupPlatformInfo("lark")},
		WebSearch:   []types.WebSearchProviderTypeInfo{{ID: "zhipu", Name: "Zhipu Search"}, {ID: "bing", Name: "Bing"}},
		Tools:       []tools.AvailableTool{{Name: "search_knowledge", Label: "检索知识库"}},
		Parsers:     []Parser{{Name: "builtin"}, {Name: "simple"}, {Name: "mineru"}, {Name: "mineru_cloud"}},
	}
}

func TestManifestsGroupByVendor(t *testing.T) {
	byID := map[string]*manifest.Manifest{}
	for _, m := range Manifests(sampleSources()) {
		byID[m.ID] = m
	}
	want := []string{
		"weknora.zhipu", "weknora.azure-openai", "weknora.feishu", "weknora.notion",
		"weknora.bing", "weknora.agent-tools", "weknora.docparser", "weknora.mineru",
	}
	if len(byID) != len(want) {
		t.Fatalf("got %d plugins, want %d: %v", len(byID), len(want), byID)
	}
	for _, id := range want {
		if byID[id] == nil {
			t.Fatalf("missing plugin %s", id)
		}
	}

	feishu := byID["weknora.feishu"]
	if n := len(feishu.Contributes[manifest.PointConnectors]); n != 2 {
		t.Fatalf("feishu connectors = %d, want 2 (wiki + lark drive)", n)
	}
	if n := len(feishu.Contributes[manifest.PointIMChannels]); n != 2 {
		t.Fatalf("feishu IM channels = %d, want 2 (feishu + lark)", n)
	}
	if got := feishu.Name.Resolve("zh-CN"); got != "飞书 / Lark" {
		t.Fatalf("feishu name = %q", got)
	}

	zhipu := byID["weknora.zhipu"]
	if zhipu.Name.Resolve("zh-CN") != "智谱" {
		t.Fatalf("a vendor with a model brand is named after it, got %+v", zhipu.Name)
	}
	if len(zhipu.Contributes[manifest.PointWebSearch]) != 1 {
		t.Fatal("zhipu web search should join the zhipu plugin")
	}

	if !byID["weknora.agent-tools"].Required || !byID["weknora.docparser"].Required {
		t.Fatal("agent tools and the default parser must be required")
	}
	if byID["weknora.notion"].Required {
		t.Fatal("ordinary vendors must not be required")
	}
	if byID["weknora.bing"].Version != "0.8.2" {
		t.Fatalf("version = %q", byID["weknora.bing"].Version)
	}
}

func TestRegisterResolvesLegacyIDs(t *testing.T) {
	r := registry.New()
	if err := Register(r, sampleSources()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cases := []struct {
		point manifest.Point
		alias string
		want  string
	}{
		{manifest.PointConnectors, "lark_drive", "weknora.feishu/lark_drive"},
		{manifest.PointIMChannels, "lark", "weknora.feishu/lark"},
		{manifest.PointModelVendors, "azure_openai", "weknora.azure-openai/azure_openai"},
		{manifest.PointParsers, "mineru_cloud", "weknora.mineru/mineru_cloud"},
		{manifest.PointTools, "search_knowledge", "weknora.agent-tools/search_knowledge"},
	}
	for _, c := range cases {
		e, ok := r.Resolve(c.point, c.alias)
		if !ok || e.QualifiedID != c.want {
			t.Errorf("Resolve(%s, %s) = %q, %v; want %q", c.point, c.alias, e.QualifiedID, ok, c.want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{
		"v0.8.2": "0.8.2", "0.8.2": "0.8.2", "unknown": devVersion, "": devVersion, "0.8": devVersion,
	} {
		if got := normalizeVersion(in); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCollectSourcesKeepsOnlyRegistered(t *testing.T) {
	ws := web_search.NewRegistry()
	ws.Register("bing", nil)
	ws.Register("custom-search", nil)
	connectors := datasource.NewConnectorRegistry()

	src := CollectSources(Live{
		Version:     "0.8.2",
		Connectors:  connectors,
		WebSearch:   ws,
		IMPlatforms: []string{"slack", "brand-new"},
	})

	var webSearch []string
	for _, w := range src.WebSearch {
		webSearch = append(webSearch, w.ID)
	}
	if strings.Join(webSearch, ",") != "bing,custom-search" {
		t.Fatalf("web search = %v, want the registered ones only, unknown last", webSearch)
	}
	if len(src.Connectors) != 0 {
		t.Fatalf("no connector is registered, got %v", src.Connectors)
	}
	if len(src.IMPlatforms) != 2 || src.IMPlatforms[1].Name != "brand-new" {
		t.Fatalf("IM platforms = %+v", src.IMPlatforms)
	}
	if len(src.Parsers) == 0 || len(src.Tools) == 0 || len(src.ModelVendors) == 0 {
		t.Fatal("process-wide registries (parsers, tools, model vendors) should be collected")
	}
	if _, err := NewRegistry(Live{Version: "0.8.2", WebSearch: ws}); err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
}
