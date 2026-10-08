package qdrant

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/qdrant/go-client/qdrant"
)

// The name versions the tokenizer and scoring options. Changing them requires a backfill.
const bm25VectorName = "weknora_bm25_v1"

func bm25Config() *qdrant.SparseVectorConfig {
	return qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
		bm25VectorName: {Modifier: qdrant.Modifier_Idf.Enum()},
	})
}

func bm25Document(text string) *qdrant.Document {
	return &qdrant.Document{
		Text:  newQdrantValueMap(map[string]any{fieldContent: text})[fieldContent].GetStringValue(),
		Model: "Qdrant/bm25",
		Options: qdrant.NewValueMap(map[string]any{
			"tokenizer": "multilingual",
			// No language-specific stemming or stop words for mixed-language knowledge bases.
			"language": "none", "lowercase": true,
			"avg_len": 256, "k": 1.2, "b": 0.75,
		}),
	}
}

func (q *qdrantRepository) pointVectors(dense []float32, content string) *qdrant.Vectors {
	if !q.bm25 {
		return qdrant.NewVectors(dense...)
	}
	return qdrant.NewVectorsMap(map[string]*qdrant.Vector{
		"":             qdrant.NewVector(dense...),
		bm25VectorName: qdrant.NewVectorDocument(bm25Document(content)),
	})
}

func denseVectorData(vectors *qdrant.VectorsOutput) []float32 {
	vector := vectors.GetVector()
	if vector == nil {
		vector = vectors.GetVectors().GetVectors()[""]
	}
	return vector.GetDenseVector().GetData()
}

func collectionDimension(name, prefix string) (int, bool) {
	suffix, ok := strings.CutPrefix(name, prefix+"_")
	if !ok {
		return 0, false
	}
	dimension, err := strconv.Atoi(suffix)
	return dimension, err == nil && dimension > 0 && strconv.Itoa(dimension) == suffix
}

func (q *qdrantRepository) checkBM25Collection(ctx context.Context, name string, dimension int) error {
	info, err := q.client.GetCollectionInfo(ctx, name)
	if err != nil {
		return fmt.Errorf("inspect BM25 collection %s: %w", name, err)
	}
	params := info.GetConfig().GetParams()
	dense := params.GetVectorsConfig().GetParams()
	sparse := params.GetSparseVectorsConfig().GetMap()[bm25VectorName]
	if dense.GetSize() != uint64(dimension) || dense.GetDistance() != qdrant.Distance_Cosine ||
		sparse == nil || sparse.GetModifier() != qdrant.Modifier_Idf {
		return fmt.Errorf("collection %s is not compatible with BM25; migrate using cmd/qdrant-migrate", name)
	}
	return nil
}

func (q *qdrantRepository) bm25Retrieve(
	ctx context.Context, params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	if params.TopK <= 0 {
		return nil, fmt.Errorf("BM25 topK must be positive")
	}
	if strings.TrimSpace(params.Query) == "" {
		return buildRetrieveResult(nil, types.KeywordsRetrieverType), nil
	}
	collections, err := q.client.ListCollections(ctx)
	if err != nil {
		return nil, fmt.Errorf("list BM25 collections: %w", err)
	}
	var retrieved []*types.RetrieveResult
	var searchErr error
	limit := uint64(params.TopK)
	for _, name := range collections {
		if _, ok := collectionDimension(name, q.collectionBaseName); !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		using := bm25VectorName
		points, err := q.client.Query(ctx, &qdrant.QueryPoints{
			CollectionName: name,
			Query:          qdrant.NewQueryNearest(qdrant.NewVectorInputDocument(bm25Document(params.Query))),
			Using:          &using,
			Filter:         q.getBaseFilter(params),
			Limit:          &limit,
			WithPayload:    qdrant.NewWithPayload(true),
		})
		if err != nil {
			searchErr = errors.Join(searchErr,
				fmt.Errorf("BM25 search in %s (requires a migrated collection): %w", name, err))
			continue
		}
		results := make([]*types.IndexWithScore, 0, len(points))
		for _, point := range points {
			payload := point.Payload
			embedding := &QdrantVectorEmbeddingWithScore{
				QdrantVectorEmbedding: QdrantVectorEmbedding{
					Content:         payload[fieldContent].GetStringValue(),
					SourceID:        payload[fieldSourceID].GetStringValue(),
					SourceType:      int(payload[fieldSourceType].GetIntegerValue()),
					ChunkID:         payload[fieldChunkID].GetStringValue(),
					KnowledgeID:     payload[fieldKnowledgeID].GetStringValue(),
					KnowledgeBaseID: payload[fieldKnowledgeBaseID].GetStringValue(),
					TagID:           payload[fieldTagID].GetStringValue(),
				},
				Score: float64(point.Score),
			}
			results = append(results, fromQdrantVectorEmbedding(point.Id.GetUuid(), embedding, types.MatchTypeKeywords))
		}
		// Collection IDF statistics differ. Keep ranked lists separate for the existing
		// service-level normalization/RRF rather than comparing raw scores across collections.
		retrieved = append(retrieved, buildRetrieveResult(results, types.KeywordsRetrieverType)...)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(retrieved) == 0 {
		if searchErr != nil {
			return nil, searchErr
		}
		return buildRetrieveResult(nil, types.KeywordsRetrieverType), nil
	}
	for _, result := range retrieved {
		result.Error = searchErr
	}
	return retrieved, nil
}
