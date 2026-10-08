package runtime

import (
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/models"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/models/internal/configcopy"
	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/types"
)

// Provider combines a provider definition with a catalog from the same runtime
// generation. Public queries return owned definitions; the catalog is immutable.
type Provider struct {
	*providers.Definition
	catalog *catalog.Catalog
}

// Models returns owned entries from this view, including hidden rules.
func (p *Provider) Models() []models.ModelSpec { return p.catalog.Models() }

// ModelsByType returns visible entries for the requested capability.
func (p *Provider) ModelsByType(kind types.ModelType) []models.ModelSpec {
	return p.catalog.ModelsByType(kind)
}

// FindModel matches an exact ID, alias or family within this view.
func (p *Provider) FindModel(name string, kind types.ModelType) (models.ModelSpec, bool) {
	return p.catalog.FindModel(name, kind)
}

// Resolve uses this view's definition and catalog even after the runtime reloads.
func (p *Provider) Resolve(ref Ref) (*Resolved, error) {
	return resolveWithVendor(ref, p)
}

func (p *Provider) clone() *Provider {
	return &Provider{Definition: configcopy.Clone(p.Definition), catalog: p.catalog}
}

// Runtime owns provider definitions, model catalogs and deployment overrides.
// Separate instances can be configured without changing any other instance.
type Runtime struct {
	mu        sync.RWMutex
	providers map[string]*Provider
	builtins  map[string]*Provider
}

// New explicitly composes fresh built-in definitions and independent catalogs.
func New() *Runtime {
	rt := &Runtime{providers: map[string]*Provider{}, builtins: map[string]*Provider{}}
	for _, definition := range providers.Builtins() {
		rt.Register(definition, catalog.BuiltinModels(definition.ID)...)
	}
	return rt
}

// Default is the application runtime used by the existing model factories.
// New is available to callers that need an isolated configuration.
func Default() *Runtime { return sharedRuntime }

var sharedRuntime = New()

// Register adds an owned built-in definition and its independently supplied
// catalog. Deployment overrides never mutate this baseline.
func (rt *Runtime) Register(definition *providers.Definition, entries ...models.ModelSpec) {
	if definition == nil || definition.ID == "" {
		panic("runtime: provider without id")
	}
	p := &Provider{Definition: configcopy.Clone(definition), catalog: catalog.New(entries)}
	normalizeProvider(p)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.providers[p.ID] = p
	rt.builtins[p.ID] = p.clone()
}

func normalizeProvider(p *Provider) {
	if p.API == "" {
		p.API = api.APIOpenAICompletions
	}
	if p.Auth == "" {
		p.Auth = providers.AuthBearer
	}
	if p.RerankAPI == "" && p.SupportsType(types.ModelTypeRerank) {
		p.RerankAPI = api.RerankCohere
	}
	if p.EmbeddingAPI == "" && p.SupportsType(types.ModelTypeEmbedding) {
		p.EmbeddingAPI = api.EmbeddingOpenAI
	}
	if p.TranscriptionAPI == "" && p.SupportsType(types.ModelTypeASR) {
		p.TranscriptionAPI = api.TranscriptionOpenAI
	}
	entries := p.Models()
	for i := range entries {
		if entries[i].Type == "" {
			entries[i].Type = types.ModelTypeKnowledgeQA
		}
		if entries[i].API == "" && entries[i].Type == types.ModelTypeKnowledgeQA {
			entries[i].API = p.API
		}
	}
	p.catalog = catalog.New(entries)
}

// Get returns an owned provider view from the current generation.
func (rt *Runtime) Get(id string) (*Provider, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	p, ok := rt.providers[strings.ToLower(strings.TrimSpace(id))]
	if !ok {
		return nil, false
	}
	return p.clone(), true
}

// selectProvider retains one immutable generation for the entire resolution.
// Its result stays private; Resolve copies the connection definition before
// returning it to callers, without copying the full model catalog.
func (rt *Runtime) selectProvider(id, baseURL string) *Provider {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		id = detectByURL(rt.providers, baseURL)
	}
	if p, ok := rt.providers[id]; ok {
		return p
	}
	return rt.providers[providers.GenericID]
}

func detectByURL(definitions map[string]*Provider, baseURL string) string {
	id, bestLen, bestOrder := providers.GenericID, 0, int(^uint(0)>>1)
	lower := strings.ToLower(baseURL)
	for candidate, p := range definitions {
		for _, pattern := range p.URLPatterns {
			if pattern != "" && strings.Contains(lower, strings.ToLower(pattern)) &&
				(len(pattern) > bestLen || len(pattern) == bestLen &&
					(p.Order < bestOrder || p.Order == bestOrder && candidate < id)) {
				id, bestLen, bestOrder = candidate, len(pattern), p.Order
			}
		}
	}
	return id
}

// DetectByURL applies legacy provider inference without exposing registry state.
func (rt *Runtime) DetectByURL(baseURL string) string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return detectByURL(rt.providers, baseURL)
}

// List returns owned provider views from one generation, ordered by Order and ID.
func (rt *Runtime) List() []*Provider {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make([]*Provider, 0, len(rt.providers))
	for _, p := range rt.providers {
		out = append(out, p.clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ListByType returns owned views supporting the requested capability.
func (rt *Runtime) ListByType(kind types.ModelType) []*Provider {
	out := make([]*Provider, 0)
	for _, p := range rt.List() {
		if p.SupportsType(kind) {
			out = append(out, p)
		}
	}
	return out
}

func cloneProviders(in map[string]*Provider) map[string]*Provider {
	out := make(map[string]*Provider, len(in))
	for id, p := range in {
		out[id] = p.clone()
	}
	return out
}

// Register adds a built-in definition and separate catalog to the default runtime.
func Register(definition *providers.Definition, entries ...models.ModelSpec) {
	Default().Register(definition, entries...)
}

// Get queries the default runtime.
func Get(id string) (*Provider, bool) { return Default().Get(id) }

// List queries the default runtime.
func List() []*Provider { return Default().List() }

// ListByType queries the default runtime for one capability.
func ListByType(kind types.ModelType) []*Provider { return Default().ListByType(kind) }

// DetectByURL infers a legacy provider using the default runtime.
func DetectByURL(baseURL string) string { return Default().DetectByURL(baseURL) }
