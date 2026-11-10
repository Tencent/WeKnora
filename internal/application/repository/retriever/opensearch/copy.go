package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	osapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// copyBatchSize is the pagination size for the source scan. Kept under the
// BatchSave per-call cap so each copied page is a single bulk request.
const copyBatchSize = 500

// copySourceDoc is the full _source read during CopyIndices — it includes the
// embedding vector and is_recommended, which the retrieve-path hit struct
// omits because retrieval does not need them.
type copySourceDoc struct {
	Content         string    `json:"content"`
	SourceID        string    `json:"source_id"`
	SourceType      int       `json:"source_type"`
	ChunkID         string    `json:"chunk_id"`
	KnowledgeID     string    `json:"knowledge_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	TagID           string    `json:"tag_id"`
	IsEnabled       bool      `json:"is_enabled"`
	IsRecommended   bool      `json:"is_recommended"`
	Embedding       []float32 `json:"embedding"`
}

// transformSourceID mirrors the sibling drivers' source_id remap:
//   - regular chunk (source_id == chunk_id) → target chunk id
//   - generated question (source_id == "<chunk>-<q>") → "<targetChunk>-<q>"
//   - anything else → a fresh uuid
func transformSourceID(sourceID, chunkID, targetChunkID string) string {
	switch {
	case sourceID == chunkID:
		return targetChunkID
	case strings.HasPrefix(sourceID, chunkID+"-"):
		return targetChunkID + "-" + strings.TrimPrefix(sourceID, chunkID+"-")
	default:
		return uuid.New().String()
	}
}

// CopyIndices copies all docs of one knowledge base into another (within the
// same store) by scanning the source and re-saving via BatchSave — mirroring
// the Elasticsearch / Qdrant drivers (search→BatchSave), which yields the
// source_id transformation and dim/keyword routing for free. Runs
// synchronously and paginates; the large-batch background-task path is a
// later change.
//
// NOTE: from/size pagination is bounded by the index's max_result_window
// (default 10000). Copies larger than that require the scroll-based async
// path (a later change).
func (r *Repository) CopyIndices(
	ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string, // keyed by source knowledge_id (mirrors sibling drivers)
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	if len(sourceToTargetChunkIDMap) == 0 {
		log.Warn("[OpenSearch] CopyIndices: empty chunk mapping, skipping")
		return nil
	}
	if dimension <= 0 {
		return fmt.Errorf("opensearch: CopyIndices requires dim > 0, got %d: %w",
			dimension, ErrDimensionMismatch)
	}
	if err := r.ensureReady(ctx, dimension); err != nil {
		return err
	}
	alias := r.indexAlias(dimension)

	var total int64
	for from := 0; ; from += copyBatchSize {
		docs, err := r.copyScanBatch(ctx, alias, sourceKnowledgeBaseID, from, copyBatchSize)
		if err != nil {
			return err
		}
		if len(docs) == 0 {
			break
		}
		infos := make([]*types.IndexInfo, 0, len(docs))
		embMap := make(map[string][]float32, len(docs))
		enabledMap := make(map[string]bool, len(docs))
		for i := range docs {
			d := &docs[i]
			targetChunkID, ok := sourceToTargetChunkIDMap[d.ChunkID]
			if !ok {
				log.Warnf("[OpenSearch] CopyIndices: source chunk %s not mapped, skipping", d.ChunkID)
				continue
			}
			targetKnowledgeID, ok := sourceToTargetKBIDMap[d.KnowledgeID]
			if !ok {
				log.Warnf("[OpenSearch] CopyIndices: source knowledge %s not mapped, skipping", d.KnowledgeID)
				continue
			}
			targetSourceID := transformSourceID(d.SourceID, d.ChunkID, targetChunkID)
			if len(d.Embedding) > 0 {
				// BatchSave looks up embeddings by SourceID (lookupEmbedding),
				// so key by the target source id — not the chunk id, which is
				// the Elasticsearch driver's convention.
				embMap[targetSourceID] = d.Embedding
			}
			enabledMap[targetChunkID] = d.IsEnabled
			infos = append(infos, &types.IndexInfo{
				Content:         d.Content,
				SourceID:        targetSourceID,
				SourceType:      types.SourceType(d.SourceType),
				ChunkID:         targetChunkID,
				KnowledgeID:     targetKnowledgeID,
				KnowledgeBaseID: targetKnowledgeBaseID,
				KnowledgeType:   knowledgeType,
				TagID:           d.TagID,
				IsEnabled:       d.IsEnabled,
				IsRecommended:   d.IsRecommended,
			})
		}
		if len(infos) > 0 {
			params := map[string]any{
				"embedding":     embMap,
				"chunk_enabled": enabledMap,
			}
			if err := r.BatchSave(ctx, infos, params); err != nil {
				return fmt.Errorf("opensearch: CopyIndices batch save: %w", err)
			}
			total += int64(len(infos))
		}
		if len(docs) < copyBatchSize {
			break
		}
	}
	log.Infof("[OpenSearch] CopyIndices: copied %d docs (KB %s → %s, dim=%d)",
		total, sourceKnowledgeBaseID, targetKnowledgeBaseID, dimension)
	r.auditSink().EmitReindexExecuted(ctx, alias, alias, total)
	return nil
}

// copyScanBatch reads one page of docs belonging to sourceKB from the per-dim
// index, decoding the full _source (including the embedding vector).
func (r *Repository) copyScanBatch(
	ctx context.Context, index, sourceKB string, from, size int,
) ([]copySourceDoc, error) {
	body, err := json.Marshal(map[string]any{
		"from": from,
		"size": size,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"knowledge_base_id": sourceKB}},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("opensearch: marshal copy scan body: %w", err)
	}
	req := osapi.SearchReq{Indices: []string{index}, Body: bytes.NewReader(body)}
	resp, err := r.client.Search(ctx, &req)
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("opensearch: index %s missing: %w", index, ErrIndexNotFound)
		}
		return nil, wrapTransport(err)
	}
	defer drainAndClose(resp.Inspect().Response.Body)
	var parsed struct {
		Hits struct {
			Hits []struct {
				Source copySourceDoc `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Inspect().Response.Body, 64<<20)).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("opensearch: parse copy scan response: %w", ErrTransport)
	}
	out := make([]copySourceDoc, len(parsed.Hits.Hits))
	for i, h := range parsed.Hits.Hits {
		out[i] = h.Source
	}
	return out, nil
}
