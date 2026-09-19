package pluginapi

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

// RetrieverProvider is what a plugin author supplies: static metadata plus the
// factory that opens a backend store. The SDK takes care of gRPC serving,
// store_handle generation, and the in-process session map.
type RetrieverProvider struct {
	PluginID       string
	EngineType     string
	Capabilities   []string // "vector", "keywords", "filter", ...
	ScoreSemantics string   // similarity_higher_better | distance_lower_better | rank_only
	Open           func(ctx context.Context, config RetrieverStoreConfig) (RetrieverBackend, error)
	OnHealth       func(context.Context) HealthResponse
}

// RetrieverStoreConfig is the configuration passed to Open. The host hands the
// plugin the raw store config; the plugin interprets it (connection, index,
// dimensions, ...) without the host understanding any backend specifics.
type RetrieverStoreConfig struct {
	Settings    map[string]any
	Credentials map[string]any
	IndexConfig map[string]any
}

// RetrieverBackend is one opened store instance. The plugin author implements
// this thin interface; all backend internals (collections, indexes, retries,
// transactions, dimensions) stay inside the implementation.
type RetrieverBackend interface {
	BatchPut(ctx context.Context, records []RetrieverRecord) error
	// Search returns hits for a query. The host re-sorts results (score
	// descending, then RecordID ascending) before RRF fusion, so the returned
	// order is not load-bearing; still, returning hits in score-descending
	// order is the recommended convention. For keyword-only backends with no
	// meaningful score (e.g. Qdrant scroll), return score 0 and rely on the
	// host's deterministic tiebreaker.
	Search(ctx context.Context, req RetrieverSearchRequest) ([]RetrieverHit, error)
	Delete(ctx context.Context, recordIDs []string, filter map[string][]string) error
	Patch(ctx context.Context, recordIDs []string, filter map[string][]string, patch map[string]string) error
	Close(ctx context.Context) error
}

// RetrieverRecord is one unit written to a store. Embedding is strongly typed
// and computed host-side; Metadata carries filter fields (enabled, tag_id, ...)
// as strings.
type RetrieverRecord struct {
	RecordID  string
	Content   string
	Embedding []float32
	Metadata  map[string]string
}

// RetrieverSearchRequest is a query against one store. Filter values are lists:
// a field matches when its value is in the list.
type RetrieverSearchRequest struct {
	Query         string
	Embedding     []float32
	Filter        map[string][]string
	TopK          int
	Threshold     float64
	RetrieverType string // "vector" or "keywords"
}

// RetrieverHit is one search result.
type RetrieverHit struct {
	RecordID string
	Score    float64
	Metadata map[string]string
}

// RetrieverHandler implements RetrieverPluginServer plus PluginControlServer.
// It manages multiple store sessions addressed by opaque store_handle values.
// Session map locking is kept to map operations only; backend connections are
// established outside the lock.
type RetrieverHandler struct {
	provider RetrieverProvider

	mu       sync.RWMutex
	sessions map[string]RetrieverBackend
}

var retrieverHandleCounter uint64

// NewRetrieverHandler builds a handler from a provider.
func NewRetrieverHandler(provider RetrieverProvider) *RetrieverHandler {
	return &RetrieverHandler{
		provider: provider,
		sessions: make(map[string]RetrieverBackend),
	}
}

func (h *RetrieverHandler) Handshake(context.Context, *pluginproto.HandshakeRequest) (*pluginproto.HandshakeResponse, error) {
	return EncodeHandshake(HandshakeResponse{
		ProtocolVersion: ProtocolVersionV1,
		PluginID:        h.provider.PluginID,
		Capabilities:    h.provider.Capabilities,
		ExtensionType:   ExtensionTypeRetriever,
		Services:        []string{ExtensionTypeRetriever},
	}), nil
}

// Health reports only whether the plugin process itself is healthy. It must not
// aggregate per-store backend health, otherwise one bad store would restart the
// whole runtime and tear down healthy stores.
func (h *RetrieverHandler) Health(ctx context.Context, _ *pluginproto.HealthRequest) (*pluginproto.HealthResponse, error) {
	v := HealthResponse{State: "running"}
	if h.provider.OnHealth != nil {
		v = h.provider.OnHealth(ctx)
	}
	return EncodeHealth(v), nil
}

func (h *RetrieverHandler) Describe(context.Context, *pluginproto.RetrieverDescribeRequest) (*pluginproto.RetrieverDescribeResponse, error) {
	return &pluginproto.RetrieverDescribeResponse{
		EngineType:     h.provider.EngineType,
		Capabilities:   h.provider.Capabilities,
		ScoreSemantics: h.provider.ScoreSemantics,
	}, nil
}

func (h *RetrieverHandler) OpenStore(ctx context.Context, v *pluginproto.RetrieverOpenStoreRequest) (*pluginproto.RetrieverOpenStoreResponse, error) {
	config := decodeRetrieverStoreConfig(v.GetConfig())
	// Establish the backend outside the session lock; a slow connect must not
	// block other stores.
	backend, err := h.provider.Open(ctx, config)
	if err != nil {
		return &pluginproto.RetrieverOpenStoreResponse{Error: err.Error()}, nil
	}
	handle := nextRetrieverHandle()
	h.mu.Lock()
	h.sessions[handle] = backend
	h.mu.Unlock()
	return &pluginproto.RetrieverOpenStoreResponse{StoreHandle: handle}, nil
}

func (h *RetrieverHandler) CloseStore(ctx context.Context, v *pluginproto.RetrieverCloseStoreRequest) (*pluginproto.RetrieverResponse, error) {
	h.mu.Lock()
	backend, ok := h.sessions[v.GetStoreHandle()]
	if ok {
		delete(h.sessions, v.GetStoreHandle())
	}
	h.mu.Unlock()
	if !ok {
		return &pluginproto.RetrieverResponse{Error: "unknown store_handle"}, nil
	}
	return &pluginproto.RetrieverResponse{Error: errorString(backend.Close(ctx))}, nil
}

func (h *RetrieverHandler) BatchPut(ctx context.Context, v *pluginproto.RetrieverBatchPutRequest) (*pluginproto.RetrieverBatchPutResponse, error) {
	backend, err := h.lookup(v.GetStoreHandle())
	if err != nil {
		return &pluginproto.RetrieverBatchPutResponse{Error: err.Error()}, nil
	}
	records := make([]RetrieverRecord, 0, len(v.GetRecords()))
	for _, r := range v.GetRecords() {
		records = append(records, RetrieverRecord{
			RecordID:  r.GetRecordId(),
			Content:   r.GetContent(),
			Embedding: r.GetEmbedding(),
			Metadata:  r.GetMetadata(),
		})
	}
	return &pluginproto.RetrieverBatchPutResponse{Error: errorString(backend.BatchPut(ctx, records))}, nil
}

func (h *RetrieverHandler) Search(ctx context.Context, v *pluginproto.RetrieverSearchRequest) (*pluginproto.RetrieverSearchResponse, error) {
	backend, err := h.lookup(v.GetStoreHandle())
	if err != nil {
		return &pluginproto.RetrieverSearchResponse{Error: err.Error()}, nil
	}
	hits, err := backend.Search(ctx, RetrieverSearchRequest{
		Query:         v.GetQuery(),
		Embedding:     v.GetEmbedding(),
		Filter:        stringListMapToSlice(v.GetFilter()),
		TopK:          int(v.GetTopK()),
		Threshold:     v.GetThreshold(),
		RetrieverType: v.GetRetrieverType(),
	})
	out := &pluginproto.RetrieverSearchResponse{Error: errorString(err)}
	for _, hit := range hits {
		out.Hits = append(out.Hits, &pluginproto.RetrieverHit{
			RecordId: hit.RecordID,
			Score:    hit.Score,
			Metadata: hit.Metadata,
		})
	}
	return out, nil
}

func (h *RetrieverHandler) Delete(ctx context.Context, v *pluginproto.RetrieverDeleteRequest) (*pluginproto.RetrieverResponse, error) {
	backend, err := h.lookup(v.GetStoreHandle())
	if err != nil {
		return &pluginproto.RetrieverResponse{Error: err.Error()}, nil
	}
	return &pluginproto.RetrieverResponse{Error: errorString(backend.Delete(ctx, v.GetRecordIds(), stringListMapToSlice(v.GetFilter())))}, nil
}

func (h *RetrieverHandler) Patch(ctx context.Context, v *pluginproto.RetrieverPatchRequest) (*pluginproto.RetrieverResponse, error) {
	backend, err := h.lookup(v.GetStoreHandle())
	if err != nil {
		return &pluginproto.RetrieverResponse{Error: err.Error()}, nil
	}
	return &pluginproto.RetrieverResponse{Error: errorString(backend.Patch(ctx, v.GetRecordIds(), stringListMapToSlice(v.GetFilter()), v.GetPatch()))}, nil
}

func (h *RetrieverHandler) lookup(handle string) (RetrieverBackend, error) {
	h.mu.RLock()
	backend, ok := h.sessions[handle]
	h.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown store_handle")
	}
	return backend, nil
}

func nextRetrieverHandle() string {
	n := atomic.AddUint64(&retrieverHandleCounter, 1)
	return fmt.Sprintf("store_%d", n)
}

func decodeRetrieverStoreConfig(v *structpb.Struct) RetrieverStoreConfig {
	if v == nil {
		return RetrieverStoreConfig{}
	}
	m := v.AsMap()
	return RetrieverStoreConfig{
		Settings:    subMap(m, "settings"),
		Credentials: subMap(m, "credentials"),
		IndexConfig: subMap(m, "index_config"),
	}
}

func subMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func stringListMapToSlice(m map[string]*pluginproto.RetrieverStringList) map[string][]string {
	if m == nil {
		return nil
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		if v != nil {
			out[k] = v.GetValues()
		}
	}
	return out
}

// ServeRetriever serves a retriever plugin: it registers PluginControl plus the
// RetrieverPlugin business service on the same gRPC server.
func ServeRetriever(ctx context.Context, address string, provider RetrieverProvider, opts ...grpc.ServerOption) error {
	handler := NewRetrieverHandler(provider)
	return servePlugin(ctx, address, opts, func(s *grpc.Server) {
		RegisterPluginControlServer(s, handler)
		RegisterRetrieverPluginServer(s, handler)
	})
}

var _ RetrieverPluginServer = (*RetrieverHandler)(nil)
