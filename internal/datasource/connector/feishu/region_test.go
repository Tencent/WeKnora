package feishu

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRegions_AreDistinct(t *testing.T) {
	if RegionFeishu.ConnectorType == RegionLark.ConnectorType {
		t.Errorf("regions share ConnectorType %q", RegionFeishu.ConnectorType)
	}
	if RegionFeishu.OpenBaseURL == RegionLark.OpenBaseURL {
		t.Errorf("regions share OpenBaseURL %q", RegionFeishu.OpenBaseURL)
	}
	if RegionFeishu.WebBaseURL == RegionLark.WebBaseURL {
		t.Errorf("regions share WebBaseURL %q", RegionFeishu.WebBaseURL)
	}
	if RegionFeishu.ConnectorType != types.ConnectorTypeFeishu {
		t.Errorf("RegionFeishu.ConnectorType = %q, want %q", RegionFeishu.ConnectorType, types.ConnectorTypeFeishu)
	}
	if RegionLark.ConnectorType != types.ConnectorTypeLark {
		t.Errorf("RegionLark.ConnectorType = %q, want %q", RegionLark.ConnectorType, types.ConnectorTypeLark)
	}
}

func TestConnectorType_FollowsRegion(t *testing.T) {
	if got := NewConnector(RegionFeishu).Type(); got != types.ConnectorTypeFeishu {
		t.Errorf("Feishu connector Type() = %q, want %q", got, types.ConnectorTypeFeishu)
	}
	if got := NewConnector(RegionLark).Type(); got != types.ConnectorTypeLark {
		t.Errorf("Lark connector Type() = %q, want %q", got, types.ConnectorTypeLark)
	}
}

// The wiki link shown in the resource picker must point at the cloud the data
// actually lives on. Feishu's value is the pre-existing one and must not drift.
func TestRegion_WikiURL(t *testing.T) {
	if got, want := RegionFeishu.wikiURL("spc123"), "https://feishu.cn/wiki/spc123"; got != want {
		t.Errorf("Feishu wikiURL = %q, want %q", got, want)
	}
	if got, want := RegionLark.wikiURL("spc123"), "https://larksuite.com/wiki/spc123"; got != want {
		t.Errorf("Lark wikiURL = %q, want %q", got, want)
	}
	// A Lark link must never point at the Feishu host — that was the bug.
	if strings.Contains(RegionLark.wikiURL("x"), "feishu") {
		t.Errorf("Lark wikiURL leaks the Feishu host: %q", RegionLark.wikiURL("x"))
	}
}

// base_url stays an override so data sources created before the lark connector
// existed — a "feishu" connector pointed at open.larksuite.com — keep working.
func TestParseFeishuConfig_BaseURLDefaultsToRegion(t *testing.T) {
	cases := []struct {
		name    string
		region  Region
		baseURL string
		want    string
	}{
		{"feishu default", RegionFeishu, "", "https://open.feishu.cn"},
		{"lark default", RegionLark, "", "https://open.larksuite.com"},
		{"explicit override wins on feishu", RegionFeishu, "https://open.larksuite.com", "https://open.larksuite.com"},
		{"explicit override wins on lark", RegionLark, "https://open.feishu.cn", "https://open.feishu.cn"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			creds := map[string]interface{}{"app_id": "cli_x", "app_secret": "s"}
			if c.baseURL != "" {
				creds["base_url"] = c.baseURL
			}
			cfg, err := parseFeishuConfig(&types.DataSourceConfig{Credentials: creds}, c.region)
			if err != nil {
				t.Fatalf("parseFeishuConfig: %v", err)
			}
			if got := cfg.GetBaseURL(); got != c.want {
				t.Errorf("GetBaseURL() = %q, want %q", got, c.want)
			}
		})
	}
}
