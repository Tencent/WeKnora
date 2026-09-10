package invoke

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/provider"
)

// Adapter contract (design §6.2): one facet per interface, three interlocking
// locks keep the declared capability shards and the implemented facets from
// drifting:
//  1. compile-time: `var _ ChatAdapter = (*someAdapter)(nil)` in adapter files;
//  2. registration: Registry.Register validates shard set == facet set and
//     fails fast;
//  3. dispatch: entry type assertions fall back to ErrUnsupportedType.
type Adapter interface {
	Provider() string
	Capabilities() provider.Capabilities
}

// ChatAdapter serves the chat facet (VLM rides along via InputModalities).
type ChatAdapter interface {
	Adapter
	BuildChatRequest(ep Endpoint, model string, opts *ChatOptions) (*Request, error)
	ParseChatResponse(status int, header http.Header, body []byte) (*ChatResponse, error)
	// TranslateStreamEvent bridges one demuxed wire chunk to the internal
	// standard StreamEvent (design §6.2: stream semantics belong to the
	// adapter). state carries cross-chunk scratch (tool-call assembly, finish
	// tracking); stream.go ships the openai-shape default for embedding.
	TranslateStreamEvent(state *StreamBridgeState, chunk StreamChunk) (*StreamEvent, error)
}

// EmbeddingAdapter serves the embedding facet.
type EmbeddingAdapter interface {
	Adapter
	BuildEmbeddingRequest(ep Endpoint, model string, opts *EmbeddingOptions) (*Request, error)
	ParseEmbeddingResponse(status int, header http.Header, body []byte) (*EmbeddingResponse, error)
}

// RerankAdapter serves the rerank facet.
type RerankAdapter interface {
	Adapter
	BuildRerankRequest(ep Endpoint, model string, opts *RerankOptions) (*Request, error)
	ParseRerankResponse(status int, header http.Header, body []byte) (*RerankResponse, error)
}

// ASRAdapter serves the ASR facet.
type ASRAdapter interface {
	Adapter
	BuildASRRequest(ep Endpoint, model string, opts *ASROptions) (*Request, error)
	ParseASRResponse(status int, header http.Header, body []byte) (*ASRResponse, error)
}

// ListModelsAdapter is the OPTIONAL fifth facet: providers without a list
// endpoint (e.g. azure deployment mode) do not implement it.
type ListModelsAdapter interface {
	Adapter
	BuildListRequest(ep Endpoint) (*Request, error)
	ParseListResponse(status int, header http.Header, body []byte) ([]RemoteModel, error)
}

// Registry holds adapters by provider name and enforces the registration-time
// lock. A zero Registry is usable; an empty registry makes every entry call
// fail with an explicit error (never a panic).
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// Default is the process-wide registry the entry functions dispatch through.
var Default = &Registry{adapters: make(map[string]Adapter)}

// Register adds an adapter to the registry after validating the three-lock #2
// invariant: declared capability shards must equal implemented facets.
func (r *Registry) Register(a Adapter) error {
	if a == nil {
		return fmt.Errorf("invoke: register nil adapter")
	}
	name := a.Provider()
	if name == "" {
		return fmt.Errorf("invoke: adapter %T has empty provider name", a)
	}
	if err := validateFacetCoverage(a); err != nil {
		return fmt.Errorf("invoke: adapter %s: %w", name, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapters == nil {
		r.adapters = make(map[string]Adapter)
	}
	if _, exists := r.adapters[name]; exists {
		return fmt.Errorf("invoke: adapter %s already registered", name)
	}
	r.adapters[name] = a
	return nil
}

// Get returns the adapter for a provider name.
func (r *Registry) Get(name string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[name]
	return a, ok
}

// Len reports how many adapters are registered (0 = empty registry; entry
// calls fail with an explicit error).
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.adapters)
}

// validateFacetCoverage implements registration lock #2: each non-nil
// capability shard must have a matching facet implementation and vice versa.
// The ListModels facet binds to Common.ModelListing.Supported.
func validateFacetCoverage(a Adapter) error {
	caps := a.Capabilities()
	if _, ok := a.(ChatAdapter); ok != (caps.Chat != nil) {
		return fmt.Errorf("chat facet implementation (%v) mismatches Chat capability shard (nil=%v)",
			ok, caps.Chat == nil)
	}
	if _, ok := a.(EmbeddingAdapter); ok != (caps.Embedding != nil) {
		return fmt.Errorf("embedding facet implementation (%v) mismatches Embedding capability shard (nil=%v)",
			ok, caps.Embedding == nil)
	}
	if _, ok := a.(RerankAdapter); ok != (caps.Rerank != nil) {
		return fmt.Errorf("rerank facet implementation (%v) mismatches Rerank capability shard (nil=%v)",
			ok, caps.Rerank == nil)
	}
	if _, ok := a.(ASRAdapter); ok != (caps.ASR != nil) {
		return fmt.Errorf("asr facet implementation (%v) mismatches ASR capability shard (nil=%v)",
			ok, caps.ASR == nil)
	}
	_, lists := a.(ListModelsAdapter)
	if lists != caps.Common.ModelListing.Supported {
		return fmt.Errorf("list-models facet implementation (%v) mismatches Common.ModelListing.Supported (%v)",
			lists, caps.Common.ModelListing.Supported)
	}
	return nil
}
