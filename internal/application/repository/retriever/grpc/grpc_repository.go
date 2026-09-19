package grpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/Tencent/WeKnora/internal/plugin"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

// ErrCopyIndicesUnsupported is returned by CopyIndices when the backing plugin
// has not declared the copy_indices capability. It is a typed sentinel so the
// host can detect it and fall back to re-embedding instead of assuming success.
var ErrCopyIndicesUnsupported = errors.New("retriever backend does not support copy indices")

// session is an opened store session: a client paired with its opaque handle.
type session struct {
	client pluginapi.RetrieverPluginClient
	handle string
}

// GRPCRetrieverRepository implements interfaces.RetrieveEngineRepository for an
// external retriever plugin. It maps the host's internal repository contract
// onto the plugin's minimal protocol (BatchPut/Search/Delete/Patch), and owns a
// self-healing store session: it re-opens the session lazily after the plugin
// runtime restarts (generation changes), coalescing concurrent re-opens with
// singleflight.
type GRPCRetrieverRepository struct {
	engineType   types.RetrieverEngineType
	support      []types.RetrieverType
	capabilities []string

	openSession func(ctx context.Context, config pluginapi.RetrieverStoreConfig) (pluginapi.RetrieverPluginClient, string, uint64, error)
	genValid    func(generation uint64) bool
	config      pluginapi.RetrieverStoreConfig

	mu         sync.RWMutex
	sess       *session
	generation uint64
	openGroup  singleflight.Group
}

// NewGRPCRetrieverRepository builds a repository from a provider registration
// and a store config.
func NewGRPCRetrieverRepository(info plugin.RetrieverProviderInfo, config pluginapi.RetrieverStoreConfig, support []types.RetrieverType) *GRPCRetrieverRepository {
	return &GRPCRetrieverRepository{
		engineType:   info.EngineType,
		support:      support,
		capabilities: info.Capabilities,
		openSession:  info.OpenSession,
		genValid:     info.GenerationValid,
		config:       config,
	}
}

// ensureSession returns the current session, re-opening it (coalesced via
// singleflight) when the bound runtime generation is no longer valid.
func (r *GRPCRetrieverRepository) ensureSession(ctx context.Context) (*session, error) {
	r.mu.RLock()
	if r.sess != nil && r.genValid != nil && r.genValid(r.generation) {
		s := r.sess
		r.mu.RUnlock()
		return s, nil
	}
	r.mu.RUnlock()

	v, err, _ := r.openGroup.Do("store", func() (any, error) {
		client, handle, generation, err := r.openSession(ctx, r.config)
		if err != nil {
			return nil, err
		}
		// openSession 在返回前已释放调用租约，runtime 可能在此期间重启，使
		// 返回的 store_handle 属于已失效的 generation。发布前复核一次：失效则
		// 关闭旧 handle 并报错，下次调用会重新打开（懒重建自愈）。
		if r.genValid != nil && !r.genValid(generation) {
			_, _ = client.CloseStore(context.Background(), &pluginproto.RetrieverCloseStoreRequest{StoreHandle: handle})
			return nil, errors.New("retriever runtime restarted during session open; retry")
		}
		s := &session{client: client, handle: handle}
		r.mu.Lock()
		r.sess = s
		r.generation = generation
		r.mu.Unlock()
		return s, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*session), nil
}

func (r *GRPCRetrieverRepository) Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error {
	return r.BatchSave(ctx, []*types.IndexInfo{indexInfo}, params)
}

func (r *GRPCRetrieverRepository) BatchSave(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) error {
	if len(indexInfoList) == 0 {
		return nil
	}
	s, err := r.ensureSession(ctx)
	if err != nil {
		return err
	}
	records := make([]*pluginproto.RetrieverRecord, 0, len(indexInfoList))
	for _, info := range indexInfoList {
		emb, _ := embeddingFor(params, info.SourceID)
		records = append(records, &pluginproto.RetrieverRecord{
			RecordId:  stableRecordID(info),
			Content:   info.Content,
			Embedding: emb,
			Metadata:  indexInfoMetadata(info),
		})
	}
	resp, err := s.client.BatchPut(ctx, &pluginproto.RetrieverBatchPutRequest{StoreHandle: s.handle, Records: records})
	if err != nil {
		return err
	}
	if resp.GetError() != "" {
		return errors.New(resp.GetError())
	}
	return nil
}

func (r *GRPCRetrieverRepository) EstimateStorageSize(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) int64 {
	return EstimateGenericRetrieverStorage(indexInfoList, params)
}

func (r *GRPCRetrieverRepository) DeleteByChunkIDList(ctx context.Context, indexIDList []string, dimension int, knowledgeType string) error {
	return r.deleteByFilter(ctx, "chunk_id", indexIDList)
}

func (r *GRPCRetrieverRepository) DeleteBySourceIDList(ctx context.Context, sourceIDList []string, dimension int, knowledgeType string) error {
	return r.deleteByFilter(ctx, "source_id", sourceIDList)
}

func (r *GRPCRetrieverRepository) DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, dimension int, knowledgeType string) error {
	return r.deleteByFilter(ctx, "knowledge_id", knowledgeIDList)
}

func (r *GRPCRetrieverRepository) deleteByFilter(ctx context.Context, key string, values []string) error {
	if len(values) == 0 {
		return nil
	}
	s, err := r.ensureSession(ctx)
	if err != nil {
		return err
	}
	resp, err := s.client.Delete(ctx, &pluginproto.RetrieverDeleteRequest{
		StoreHandle: s.handle,
		Filter:      map[string]*pluginproto.RetrieverStringList{key: {Values: values}},
	})
	if err != nil {
		return err
	}
	if resp.GetError() != "" {
		return errors.New(resp.GetError())
	}
	return nil
}

func (r *GRPCRetrieverRepository) CopyIndices(
	ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	// Copy is a future capability; the minimal protocol does not expose it.
	return ErrCopyIndicesUnsupported
}

func (r *GRPCRetrieverRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	enabled := make([]string, 0)
	disabled := make([]string, 0)
	for chunkID, on := range chunkStatusMap {
		if on {
			enabled = append(enabled, chunkID)
		} else {
			disabled = append(disabled, chunkID)
		}
	}
	if err := r.patchByFilter(ctx, enabled, map[string]string{"enabled": "true"}); err != nil {
		return err
	}
	return r.patchByFilter(ctx, disabled, map[string]string{"enabled": "false"})
}

func (r *GRPCRetrieverRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	groups := make(map[string][]string)
	for chunkID, tagID := range chunkTagMap {
		groups[tagID] = append(groups[tagID], chunkID)
	}
	for tagID, chunkIDs := range groups {
		if err := r.patchByFilter(ctx, chunkIDs, map[string]string{"tag_id": tagID}); err != nil {
			return err
		}
	}
	return nil
}

func (r *GRPCRetrieverRepository) patchByFilter(ctx context.Context, chunkIDs []string, patch map[string]string) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	s, err := r.ensureSession(ctx)
	if err != nil {
		return err
	}
	resp, err := s.client.Patch(ctx, &pluginproto.RetrieverPatchRequest{
		StoreHandle: s.handle,
		Filter:      map[string]*pluginproto.RetrieverStringList{"chunk_id": {Values: chunkIDs}},
		Patch:       patch,
	})
	if err != nil {
		return err
	}
	if resp.GetError() != "" {
		return errors.New(resp.GetError())
	}
	return nil
}

func (r *GRPCRetrieverRepository) EngineType() types.RetrieverEngineType {
	return r.engineType
}

func (r *GRPCRetrieverRepository) Support() []types.RetrieverType {
	return r.support
}

// Close closes the current store session (if any), releasing the plugin
// backend's resources (connections, caches). Safe to call multiple times; the
// engine registry invokes it when a VectorStore is unregistered. After Close,
// the next operation lazily re-opens a session via ensureSession.
func (r *GRPCRetrieverRepository) Close(ctx context.Context) error {
	r.mu.Lock()
	s := r.sess
	r.sess = nil
	r.mu.Unlock()
	if s == nil {
		return nil
	}
	resp, err := s.client.CloseStore(ctx, &pluginproto.RetrieverCloseStoreRequest{StoreHandle: s.handle})
	if err != nil {
		return err
	}
	if resp.GetError() != "" {
		return errors.New(resp.GetError())
	}
	return nil
}

func (r *GRPCRetrieverRepository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	s, err := r.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	// The host already fans out by retriever type (vector vs keywords) before
	// calling Retrieve and merges results with RRF upstream. Executing one
	// search per declared capability here would duplicate calls and pollute the
	// fusion inputs, so we honor params.RetrieverType exactly.
	rt := params.RetrieverType
	if rt == "" {
		if len(r.support) == 0 {
			return nil, nil
		}
		rt = r.support[0]
	}
	resp, err := s.client.Search(ctx, &pluginproto.RetrieverSearchRequest{
		StoreHandle:   s.handle,
		Query:         params.Query,
		Embedding:     params.Embedding,
		Filter:        buildFilter(params),
		TopK:          int32(params.TopK),
		Threshold:     params.Threshold,
		RetrieverType: string(rt),
	})
	if err != nil {
		return nil, err
	}
	if resp.GetError() != "" {
		return nil, errors.New(resp.GetError())
	}
	// 宿主把检索类型映射为 MatchType，而不是让插件返回 WeKnora 内部枚举。
	matchType := types.MatchTypeEmbedding
	if rt == types.KeywordsRetrieverType {
		matchType = types.MatchTypeKeywords
	}
	hits := make([]*types.IndexWithScore, 0, len(resp.GetHits()))
	for _, hit := range resp.GetHits() {
		hits = append(hits, hitToIndexWithScore(hit, matchType))
	}
	// Sort deterministically: score descending, then ID ascending as a
	// tiebreaker. RRF fusion upstream derives rank from slice position (see
	// fuseWithRRF), so the plugin's return order must not leak into the final
	// ranking. Sorting here makes the contract independent of plugin behavior,
	// including keyword engines that return no meaningful scores (score == 0
	// for all hits) — the ID tiebreaker keeps the rank stable.
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	return []*types.RetrieveResult{{
		RetrieverEngineType: r.engineType,
		RetrieverType:       rt,
		Results:             hits,
	}}, nil
}

// EstimateGenericRetrieverStorage is a host-side conservative storage estimate
// for external retriever plugins. It never returns zero and accounts for
// content bytes plus vector bytes (float32 = 4 bytes per dimension) plus a
// fixed per-record metadata/index overhead.
func EstimateGenericRetrieverStorage(indexInfoList []*types.IndexInfo, params map[string]any) int64 {
	const metadataOverhead = 256
	var total int64
	for _, info := range indexInfoList {
		total += int64(len(info.Content))
		if vec, ok := embeddingFor(params, info.SourceID); ok {
			total += int64(len(vec) * 4)
		}
		total += metadataOverhead
	}
	return total
}

func embeddingFor(params map[string]any, sourceID string) ([]float32, bool) {
	if params == nil {
		return nil, false
	}
	raw, present := params["embedding"]
	if !present {
		return nil, false
	}
	em, ok := raw.(map[string][]float32)
	if !ok {
		// 类型不匹配会被调用方当作"无 embedding"，使存储估算偏小且无告警；
		// 记录诊断，便于发现上游把 embedding 传成了其他类型。
		log.Printf("[retriever] embedding param has unexpected type %T; storage estimate will be underestimated", raw)
		return nil, false
	}
	v, ok := em[sourceID]
	return v, ok
}

func indexInfoMetadata(info *types.IndexInfo) map[string]string {
	return map[string]string{
		"source_id":         info.SourceID,
		"source_type":       fmt.Sprintf("%d", info.SourceType),
		"chunk_id":          info.ChunkID,
		"knowledge_id":      info.KnowledgeID,
		"knowledge_base_id": info.KnowledgeBaseID,
		"knowledge_type":    info.KnowledgeType,
		"tag_id":            info.TagID,
		"is_enabled":        boolString(info.IsEnabled),
		"is_recommended":    boolString(info.IsRecommended),
	}
}

// stableRecordID derives a deterministic record ID from the index info so a
// retried write of the same chunk produces the same record ID, letting the
// plugin backend upsert (replace) instead of appending a duplicate vector.
func stableRecordID(info *types.IndexInfo) string {
	if info.ChunkID != "" {
		return info.ChunkID
	}
	if info.SourceID != "" {
		return info.SourceID
	}
	return info.ID
}

func hitToIndexWithScore(hit *pluginproto.RetrieverHit, matchType types.MatchType) *types.IndexWithScore {
	meta := hit.GetMetadata()
	if meta == nil {
		meta = map[string]string{}
	}
	return &types.IndexWithScore{
		ID:              hit.GetRecordId(),
		SourceID:        meta["source_id"],
		SourceType:      types.SourceType(atoi(meta["source_type"])),
		ChunkID:         meta["chunk_id"],
		KnowledgeID:     meta["knowledge_id"],
		KnowledgeBaseID: meta["knowledge_base_id"],
		TagID:           meta["tag_id"],
		Score:           hit.GetScore(),
		IsEnabled:       meta["is_enabled"] == "true",
		MatchType:       matchType,
	}
}

func buildFilter(params types.RetrieveParams) map[string]*pluginproto.RetrieverStringList {
	filter := map[string]*pluginproto.RetrieverStringList{}
	if len(params.KnowledgeBaseIDs) > 0 {
		filter["knowledge_base_id"] = &pluginproto.RetrieverStringList{Values: params.KnowledgeBaseIDs}
	}
	if len(params.KnowledgeIDs) > 0 {
		filter["knowledge_id"] = &pluginproto.RetrieverStringList{Values: params.KnowledgeIDs}
	}
	if len(params.TagIDs) > 0 {
		filter["tag_id"] = &pluginproto.RetrieverStringList{Values: params.TagIDs}
	}
	if len(params.ExcludeKnowledgeIDs) > 0 {
		filter["exclude_knowledge_id"] = &pluginproto.RetrieverStringList{Values: params.ExcludeKnowledgeIDs}
	}
	if len(params.ExcludeChunkIDs) > 0 {
		filter["exclude_chunk_id"] = &pluginproto.RetrieverStringList{Values: params.ExcludeChunkIDs}
	}
	if len(filter) == 0 {
		return nil
	}
	return filter
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func atoi(s string) int {
	var n int
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

var _ interfaces.RetrieveEngineRepository = (*GRPCRetrieverRepository)(nil)
