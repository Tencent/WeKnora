package types

import "github.com/Tencent/WeKnora/internal/plugin/configschema"

// Field groups of the web search form, matching its drawer sections.
const (
	webSearchGroupCredentials = "credentials"
	webSearchGroupOptions     = "options"
)

// BuildConfigSchema describes the parameters of one provider type
// (WebSearchProviderParameters) as a config schema. The flags and config
// fields on the type info are what the frontend renders from today; this is
// the same form in the shape every integration moves to. Titles point at the
// frontend's existing webSearchSettings.* strings.
func (info WebSearchProviderTypeInfo) BuildConfigSchema() *configschema.Schema {
	s := configschema.Object()
	if info.RequiresBaseURL {
		s.Set("base_url", &configschema.Schema{
			Type:        configschema.TypeString,
			Format:      "uri",
			Title:       "Instance URL",
			Placeholder: "https://searxng.example.com",
			I18nKeys: map[string]string{
				"title":       "webSearchSettings.baseUrlLabel",
				"placeholder": "webSearchSettings.baseUrlPlaceholder",
			},
			Group: webSearchGroupCredentials,
			Order: 10,
		}, true)
	}
	if info.RequiresAPIKey || info.SupportsOptionalAPIKey {
		titleKey := "webSearchSettings.apiKeyLabel"
		if !info.RequiresAPIKey {
			titleKey = "webSearchSettings.apiKeyOptionalLabel"
		}
		s.Set("api_key", &configschema.Schema{
			Type:   configschema.TypeString,
			Secret: true,
			Widget: "password",
			Title:  "API key",
			I18nKeys: map[string]string{
				"title":       titleKey,
				"placeholder": "webSearchSettings.apiKeyPlaceholder",
			},
			Group: webSearchGroupCredentials,
			Order: 20,
		}, info.RequiresAPIKey)
	}
	if info.RequiresEngineID {
		s.Set("engine_id", &configschema.Schema{
			Type:     configschema.TypeString,
			Title:    "Search engine ID",
			I18nKeys: map[string]string{"title": "webSearchSettings.engineIdLabel"},
			Group:    webSearchGroupCredentials,
			Order:    30,
		}, true)
	}
	if len(info.ConfigFields) > 0 {
		extra := configschema.Object()
		extra.Group = webSearchGroupCredentials
		extra.Order = 40
		for i, f := range info.ConfigFields {
			extra.Set(f.Key, webSearchConfigFieldSchema(f, i), f.Required)
		}
		s.Set("extra_config", extra, false)
	}
	if info.SupportsProxy {
		s.Set("proxy_url", &configschema.Schema{
			Type:        configschema.TypeString,
			Format:      "uri",
			Title:       "HTTP proxy",
			Description: "Leave empty to use the HTTP(S)_PROXY environment variables.",
			I18nKeys: map[string]string{
				"title":       "webSearchSettings.proxyUrlLabel",
				"placeholder": "webSearchSettings.proxyUrlPlaceholder",
				"description": "webSearchSettings.proxyUrlHelp",
			},
			Group: webSearchGroupOptions,
			Order: 50,
		}, false)
	}
	return s
}

func webSearchConfigFieldSchema(f WebSearchProviderConfigField, order int) *configschema.Schema {
	prop := &configschema.Schema{
		Type:        configschema.TypeString,
		Title:       f.Label,
		Description: f.Description,
		Widget:      f.Type,
		Order:       order,
	}
	if f.Default != "" {
		prop.Default = f.Default
	}
	prop.I18nKeys = i18nKeys(map[string]string{"title": f.LabelKey, "description": f.DescriptionKey})
	for _, o := range f.Options {
		prop.OneOf = append(prop.OneOf, &configschema.Schema{
			Const:    o.Value,
			Title:    o.Label,
			I18nKeys: i18nKeys(map[string]string{"title": o.LabelKey}),
		})
	}
	return prop
}

// i18nKeys drops empty keys and returns nil when nothing is left.
func i18nKeys(keys map[string]string) map[string]string {
	for k, v := range keys {
		if v == "" {
			delete(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return keys
}
