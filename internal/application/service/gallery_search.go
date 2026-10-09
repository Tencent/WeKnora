package service

import (
	"context"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// GallerySearchLimit bounds the ranked result set, not the assets considered.
const GallerySearchLimit = 200

// GallerySearchService searches image evidence without document expansion.
type GallerySearchService struct {
	images    *ImageVectorService
	knowledge interfaces.KnowledgeRepository
}

// NewGallerySearchService reuses the KB's embedding and retrieval configuration.
func NewGallerySearchService(
	images *ImageVectorService, knowledge interfaces.KnowledgeRepository,
) *GallerySearchService {
	return &GallerySearchService{images: images, knowledge: knowledge}
}

// Search filters all gallery assets before retrieval, reduces caption/OCR/image
// hits to image identities, and paginates a bounded relevance-ordered result set.
func (s *GallerySearchService) Search(ctx context.Context, kbID string, page *types.Pagination,
	filter *types.ImageListFilter,
) (*types.PageResult, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if filter == nil || strings.TrimSpace(filter.Keyword) == "" {
		return nil, false, apperrors.NewBadRequestError("image search query is required")
	}
	query := strings.TrimSpace(filter.Keyword)
	if len([]rune(query)) > 2000 {
		return nil, false, apperrors.NewBadRequestError("image search query exceeds 2000 characters")
	}
	kb, model, fingerprint, err := s.images.configuration(ctx, kbID)
	if err != nil {
		return nil, false, err
	}
	if !kb.IsImageVectorEnabled() {
		return nil, false, apperrors.NewBadRequestError("enable image vector indexing to use semantic image search")
	}
	if _, ok := embedding.AsImageEmbedder(model); !ok {
		return nil, false, apperrors.NewBadRequestError("image search requires an image-capable embedding model")
	}
	vector, err := model.Embed(types.WithEmbedQuery(ctx), query)
	if err != nil {
		return nil, false, err
	}
	if len(vector) == 0 || (model.GetDimensions() > 0 && len(vector) != model.GetDimensions()) {
		return nil, false, apperrors.NewInternalServerError("invalid query embedding dimensions")
	}
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, false, apperrors.NewInternalServerError("invalid query embedding values")
		}
	}
	tenant, err := s.images.tenants.GetTenantByID(ctx, kb.TenantID)
	if err != nil {
		return nil, false, err
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	engine, err := retriever.CreateRetrieveEngineForKB(ctx, s.images.engines, s.images.ownership,
		kb.TenantID, kb.VectorStoreID)
	if err != nil {
		return nil, false, err
	}
	q := buildImageAssetQuery(filter, &types.Pagination{})
	q.Keyword, q.Offset, q.Limit = "", 0, 100
	// Disabled assets are browseable in lexical mode but never query evidence.
	enabled := true
	if filter.IsEnabled != nil && !*filter.IsEnabled {
		return types.NewPageResult(0, page, []types.ImageAsset{}), false, nil
	}
	q.IsEnabled = &enabled
	var ranked []types.ImageAsset
	truncated := false
	for ; ; q.Offset += q.Limit {
		rows, _, err := s.images.chunks.ListImageAssets(ctx, kb.TenantID, kbID, q)
		if err != nil {
			return nil, false, err
		}
		assets := imageAssetsFromRows(rows)
		keys := make([]string, 0, len(assets))
		byKey := make(map[string]int, len(assets))
		for i, asset := range assets {
			key := asset.URL
			if key == "" {
				key = asset.OriginalURL
			}
			keys = append(keys, key)
			byKey[key] = i
		}
		indices, err := s.images.jobs.SearchIndices(ctx, kb.TenantID, kbID, fingerprint, keys)
		if err != nil {
			return nil, false, err
		}
		byChunk := make(map[string][]string, len(indices))
		var ids []string
		for _, index := range indices {
			if _, exists := byChunk[index.ChunkID]; !exists {
				ids = append(ids, index.ChunkID)
			}
			byChunk[index.ChunkID] = append(byChunk[index.ChunkID], index.ImageKey)
		}
		best := map[string]float64{}
		for batch := range slices.Chunk(ids, 256) {
			result, err := engine.Retrieve(ctx, []types.RetrieveParams{{
				Query: query, Embedding: vector, KnowledgeBaseIDs: []string{kbID}, ChunkIDs: batch,
				RetrieverType: types.VectorRetrieverType, TopK: len(batch),
			}})
			if err != nil {
				return nil, false, err
			}
			for _, set := range result {
				if set == nil {
					continue
				}
				for _, hit := range set.Results {
					if hit == nil || math.IsNaN(hit.Score) || math.IsInf(hit.Score, 0) {
						continue
					}
					// Keep the repository allow-list as a final guard too.
					if hit.KnowledgeBaseID != kbID || !slices.Contains(batch, hit.ChunkID) {
						continue
					}
					for _, key := range byChunk[hit.ChunkID] {
						if previous, exists := best[key]; !exists || hit.Score > previous {
							best[key] = hit.Score
						}
					}
				}
			}
		}
		for key, score := range best {
			asset := assets[byKey[key]]
			asset.Relevance = score
			ranked = append(ranked, asset)
		}
		sortGalleryMatches(ranked)
		if len(ranked) > GallerySearchLimit {
			truncated = true
			ranked = ranked[:GallerySearchLimit]
		}
		if len(rows) < q.Limit {
			break
		}
	}
	start := min(max(page.Offset(), 0), len(ranked))
	end := min(start+page.GetPageSize(), len(ranked))
	items := append([]types.ImageAsset{}, ranked[start:end]...)
	names := resolveImageAssetSourceNames(ctx, s.knowledge, kb.TenantID, items)
	for i := range items {
		items[i].SourceName = names[items[i].KnowledgeID]
	}
	return types.NewPageResult(int64(len(ranked)), page, items), truncated, nil
}

func sortGalleryMatches(assets []types.ImageAsset) {
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].Relevance != assets[j].Relevance {
			return assets[i].Relevance > assets[j].Relevance
		}
		return assets[i].ID < assets[j].ID
	})
}
