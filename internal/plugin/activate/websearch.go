package activate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	infra_web_search "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// WebSearch registers code plugins' web search providers as provider types.
// A plugin provider stores its configuration in the same parameters as a
// builtin one: the secret api_key and string fields under extra_config, so
// encryption, redaction and the credentials endpoint work unchanged.
type WebSearch struct {
	iv       *Invoker
	registry *infra_web_search.Registry

	mu         sync.Mutex
	registered map[string][]string // plugin ID → provider type IDs
}

// NewWebSearch creates the web search activator.
func NewWebSearch(iv *Invoker, registry *infra_web_search.Registry) *WebSearch {
	return &WebSearch{iv: iv, registry: registry, registered: map[string][]string{}}
}

// Name implements reconcile.Activator.
func (a *WebSearch) Name() string { return "webSearch" }

// Activate implements reconcile.Activator: all of a plugin's providers or
// none.
func (a *WebSearch) Activate(_ context.Context, l *reconcile.Loaded) error {
	var ids []string
	for _, c := range l.Manifest.Contributes[manifest.PointWebSearch] {
		id := manifest.QualifiedID(l.Manifest.ID, c.ID)
		pt, err := webSearchType(l, c, id)
		if err == nil {
			m, local := l.Manifest, c.ID
			err = a.registry.RegisterPlugin(
				pt,
				func(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
					return &remoteSearch{
						iv:       a.iv,
						m:        m,
						local:    local,
						typeID:   id,
						instance: webSearchInstance(params),
					}, nil
				},
			)
		}
		if err != nil {
			for _, done := range ids {
				a.registry.Unregister(done)
			}
			return fmt.Errorf("webSearch %s: %w", c.ID, err)
		}
		ids = append(ids, id)
	}
	a.mu.Lock()
	a.registered[l.Manifest.ID] = ids
	a.mu.Unlock()
	return nil
}

// Deactivate implements reconcile.Activator.
func (a *WebSearch) Deactivate(_ context.Context, pluginID string) error {
	a.mu.Lock()
	ids := a.registered[pluginID]
	delete(a.registered, pluginID)
	a.mu.Unlock()
	for _, id := range ids {
		a.registry.Unregister(id)
	}
	return nil
}

// webSearchType describes a plugin provider for the type list.
func webSearchType(l *reconcile.Loaded, c manifest.Contribution, id string) (infra_web_search.PluginType, error) {
	src, err := instanceSchema(l.Package, c)
	if err != nil {
		return infra_web_search.PluginType{}, err
	}
	schema, requiresKey, err := webSearchSchema(src)
	if err != nil {
		return infra_web_search.PluginType{}, err
	}
	icon, err := iconDataURI(l.Package, c.Icon)
	if err != nil {
		return infra_web_search.PluginType{}, err
	}
	info := types.WebSearchProviderTypeInfo{
		ID: id, Name: c.Name.Default, Names: c.Name.Locales, Description: c.Description.Default,
		RequiresAPIKey: requiresKey, Icon: icon, PluginID: l.Manifest.ID, ConfigSchema: schema,
		PluginVersion: l.Manifest.Version, Editor: c.Editor,
	}
	validate := func(params types.WebSearchProviderParameters) error {
		value := map[string]any{"api_key": params.APIKey}
		extra := map[string]any{}
		for k, v := range params.ExtraConfig {
			extra[k] = v
		}
		value["extra_config"] = extra
		if errs := configschema.Validate(schema, value); len(errs) > 0 {
			return errs
		}
		return nil
	}
	return infra_web_search.PluginType{Info: info, Validate: validate}, nil
}

// webSearchSchema maps a plugin's instance schema onto the provider
// parameters: api_key stays top-level (the only secret), everything else
// moves under extra_config, which stores strings.
func webSearchSchema(src *configschema.Schema) (*configschema.Schema, bool, error) {
	out := configschema.Object()
	if src == nil {
		return out, false, nil
	}
	required := map[string]bool{}
	for _, k := range src.Required {
		required[k] = true
	}
	extra := &configschema.Schema{
		Type: configschema.TypeObject, Group: "credentials", Order: 40, Properties: map[string]*configschema.Schema{},
	}
	var keys []string
	for k := range src.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	requiresKey := false
	for _, k := range keys {
		prop := *src.Properties[k]
		if k == "api_key" {
			prop.Type, prop.Secret, prop.Group = configschema.TypeString, true, "credentials"
			out.Set("api_key", &prop, required[k])
			requiresKey = required[k]
			continue
		}
		if prop.Secret {
			return nil, false, fmt.Errorf("instanceSchema field %s is secret; only api_key may be", k)
		}
		if prop.Type != "" && prop.Type != configschema.TypeString {
			return nil, false, fmt.Errorf(
				"instanceSchema field %s must be a string (web search settings are stored as strings)",
				k,
			)
		}
		prop.Type, prop.Group = configschema.TypeString, ""
		extra.Set(k, &prop, required[k])
	}
	if len(extra.Properties) > 0 {
		out.Set("extra_config", extra, len(extra.Required) > 0)
	}
	return out, requiresKey, nil
}

// webSearchInstance is what the plugin receives as config.instance.
func webSearchInstance(params types.WebSearchProviderParameters) map[string]any {
	out := map[string]any{}
	if params.APIKey != "" {
		out["api_key"] = params.APIKey
	}
	for k, v := range params.ExtraConfig {
		out[k] = v
	}
	return out
}

// instanceSchema reads a contribution's instanceSchema file (JSON or YAML).
func instanceSchema(p *pkg.Package, c manifest.Contribution) (*configschema.Schema, error) {
	if c.InstanceSchema == "" || p == nil {
		return nil, nil
	}
	raw, ok := p.ReadFile(c.InstanceSchema)
	if !ok {
		return nil, fmt.Errorf("instanceSchema %s is not in the package", c.InstanceSchema)
	}
	var v any
	if err := yaml.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("instanceSchema %s: %w", c.InstanceSchema, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	s, err := configschema.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("instanceSchema %s: %w", c.InstanceSchema, err)
	}
	return s, nil
}

// maxIconBytes caps an icon inlined into type listings.
const maxIconBytes = 64 << 10

// iconDataURI inlines a package icon (SVG or PNG) as a data: URI.
func iconDataURI(p *pkg.Package, file string) (string, error) {
	if file == "" || p == nil {
		return "", nil
	}
	b, ok := p.ReadFile(file)
	if !ok {
		return "", fmt.Errorf("icon %s is not in the package", file)
	}
	if len(b) > maxIconBytes {
		return "", fmt.Errorf("icon %s is over %d bytes", file, maxIconBytes)
	}
	var mime string
	switch strings.ToLower(path.Ext(file)) {
	case ".svg":
		mime = "image/svg+xml"
	case ".png":
		mime = "image/png"
	default:
		return "", fmt.Errorf("icon %s must be .svg or .png", file)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}

// remoteSearch is one configured plugin provider.
type remoteSearch struct {
	iv       *Invoker
	m        *manifest.Manifest
	local    string
	typeID   string
	instance map[string]any
}

// Name implements interfaces.WebSearchProvider.
func (r *remoteSearch) Name() string { return r.typeID }

// Search implements interfaces.WebSearchProvider.
func (r *remoteSearch) Search(
	ctx context.Context, query string, maxResults int, includeDate bool,
) ([]*types.WebSearchResult, error) {
	return r.SearchWithFilters(ctx, query, maxResults, includeDate, types.WebSearchFilters{})
}

// SearchWithFilters implements interfaces.FilteredWebSearchProvider; the
// plugin must fail rather than ignore a filter it cannot honour.
func (r *remoteSearch) SearchWithFilters(
	ctx context.Context, query string, maxResults int, includeDate bool, filters types.WebSearchFilters,
) ([]*types.WebSearchResult, error) {
	ctx, cancel := withDefaultTimeout(ctx, searchTimeout)
	defer cancel()
	in := pluginapi.SearchInput{
		Query: query, MaxResults: maxResults, IncludeDate: includeDate,
		Region: filters.Country, Freshness: filters.Freshness,
	}
	var out pluginapi.SearchOutput
	if err := r.iv.Call(ctx, r.m, pluginapi.SearchPath(r.local), r.instance, in, &out); err != nil {
		return nil, err
	}
	results := make([]*types.WebSearchResult, 0, len(out.Results))
	for _, h := range out.Results {
		results = append(results, &types.WebSearchResult{
			Title: h.Title, URL: h.URL, Snippet: h.Snippet, Content: h.Content,
			Source: r.typeID, Age: h.Age, PublishedAt: h.PublishedAt,
		})
	}
	return results, nil
}
