package types

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
)

func TestRedactedPlaceholderMatchesConfigSchema(t *testing.T) {
	if RedactedSecretPlaceholder != configschema.RedactedPlaceholder {
		t.Fatal("configschema.RedactedPlaceholder must equal RedactedSecretPlaceholder")
	}
}

func TestWebSearchConfigSchemas(t *testing.T) {
	for _, info := range GetWebSearchProviderTypes() {
		s := info.ConfigSchema
		if s == nil {
			t.Fatalf("%s: no config schema", info.ID)
		}
		if err := s.Check(); err != nil {
			t.Errorf("%s: invalid schema: %v", info.ID, err)
		}
		_, hasKey := s.Properties["api_key"]
		if hasKey != (info.RequiresAPIKey || info.SupportsOptionalAPIKey) {
			t.Errorf("%s: api_key present = %v", info.ID, hasKey)
		}
		if hasKey && !s.Properties["api_key"].Secret {
			t.Errorf("%s: api_key must be secret", info.ID)
		}
		if _, ok := s.Properties["base_url"]; ok != info.RequiresBaseURL {
			t.Errorf("%s: base_url present = %v", info.ID, ok)
		}
		if _, ok := s.Properties["proxy_url"]; ok != info.SupportsProxy {
			t.Errorf("%s: proxy_url present = %v", info.ID, ok)
		}
		extra := s.Properties["extra_config"]
		if (extra != nil) != (len(info.ConfigFields) > 0) {
			t.Errorf("%s: extra_config present = %v", info.ID, extra != nil)
		}
		if extra != nil && len(extra.Properties) != len(info.ConfigFields) {
			t.Errorf("%s: %d extra fields for %d config fields", info.ID, len(extra.Properties), len(info.ConfigFields))
		}
	}
}

func TestWebSearchConfigSchemaValidatesStoredShape(t *testing.T) {
	var zhipu WebSearchProviderTypeInfo
	for _, info := range GetWebSearchProviderTypes() {
		if info.ID == "zhipu" {
			zhipu = info
		}
	}
	s := zhipu.ConfigSchema
	// The shape WebSearchProviderParameters marshals to.
	params := map[string]any{
		"api_key":      "***",
		"extra_config": map[string]any{"search_engine": "search_pro", "content_size": "high"},
	}
	if errs := configschema.Validate(s, params); errs != nil {
		t.Fatalf("stored parameters rejected: %v", errs)
	}
	params["extra_config"] = map[string]any{"search_engine": "bogus", "content_size": "high"}
	errs := configschema.Validate(s, params)
	if len(errs) != 1 || errs[0].Path != "extra_config.search_engine" || errs[0].Code != configschema.CodeEnum {
		t.Fatalf("want enum error on extra_config.search_engine, got %v", errs)
	}
	if key := s.Properties["extra_config"].Properties["search_engine"].I18nKeys["title"]; key !=
		"webSearchSettings.configFields.searchEngine" {
		t.Fatalf("label key not carried over: %q", key)
	}
}
