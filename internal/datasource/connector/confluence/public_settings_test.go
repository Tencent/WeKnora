package confluence

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestPublicSettingsOverrideLegacyCredentials(t *testing.T) {
	ds := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"edition": "server", "base_url": "https://old.example", "username": "old", "api_token": "secret"},
		Settings:    map[string]interface{}{"edition": "cloud", "base_url": "https://new.example/wiki", "username": "new"},
	}
	cfg, err := parseConfig(ds)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.edition != "cloud" || cfg.baseURL != "https://new.example/wiki" || cfg.username != "new" || cfg.secret != "secret" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	delete(ds.Settings, "username")
	if got := configValue(ds, "username"); got != "old" {
		t.Fatalf("legacy fallback = %q", got)
	}
}
