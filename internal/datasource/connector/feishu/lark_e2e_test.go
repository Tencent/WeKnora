package feishu

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// A Lark connector must surface wiki links on the Lark host. Before Lark became
// its own connector type, every resource URL was hardcoded to feishu.cn, so Lark
// users got links into a cloud their content does not live on.
//
// The fake server stands in for the Open Platform (base_url points at it), but
// the resource URL is built from the region's web host, so this exercises the
// real ListResources → wikiNodeToResource → region.wikiURL path.
func TestListResources_ResourceURLFollowsRegion(t *testing.T) {
	cases := []struct {
		region      Region
		wantURL     string
		forbiddenIn string
	}{
		{RegionFeishu, "https://feishu.cn/wiki/space1", "larksuite"},
		{RegionLark, "https://larksuite.com/wiki/space1", "feishu"},
	}

	for _, c := range cases {
		t.Run(c.region.Label, func(t *testing.T) {
			ts, cfg := fakeFeishu(nil)
			defer ts.Close()

			conn := NewConnector(c.region)
			resources, err := conn.ListResources(context.Background(), makeConfig(cfg, nil), "")
			if err != nil {
				t.Fatalf("ListResources: %v", err)
			}
			if len(resources) != 1 {
				t.Fatalf("got %d resources, want 1", len(resources))
			}
			if got := resources[0].URL; got != c.wantURL {
				t.Errorf("resource URL = %q, want %q", got, c.wantURL)
			}
			if strings.Contains(resources[0].URL, c.forbiddenIn) {
				t.Errorf("%s resource URL leaks the other cloud's host: %q", c.region.Label, resources[0].URL)
			}
		})
	}
}

// A Lark connector with no base_url override must reach open.larksuite.com,
// never open.feishu.cn — the SDK-less client has no default of its own, so the
// region is the only thing standing between a Lark app and the wrong cloud.
func TestClient_ResolvesRegionHostWithoutOverride(t *testing.T) {
	cases := []struct {
		region Region
		want   string
	}{
		{RegionFeishu, "https://open.feishu.cn"},
		{RegionLark, "https://open.larksuite.com"},
	}

	for _, c := range cases {
		t.Run(c.region.Label, func(t *testing.T) {
			cfg, err := parseFeishuConfig(makeConfigNoBaseURL(), c.region)
			if err != nil {
				t.Fatalf("parseFeishuConfig: %v", err)
			}
			client := NewClient(cfg)
			if client.baseURL != c.want {
				t.Errorf("client baseURL = %q, want %q", client.baseURL, c.want)
			}
		})
	}
}

// makeConfigNoBaseURL builds credentials without base_url, so the region default
// is what resolves.
func makeConfigNoBaseURL() *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_id":     "cli_x",
			"app_secret": "s",
		},
	}
}
