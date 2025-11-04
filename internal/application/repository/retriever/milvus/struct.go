package repomilvus

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

const EmbeddingsCollectionNamePrefix string = "embeddings"
const EmbeddingsDefaultDimension int = 1536

var EmbeddingsCollectionDefaultName string = EmbeddingsCollectionNamePrefix + strconv.Itoa(EmbeddingsDefaultDimension)

var OutputFields = []string{"chunk_id", "content", "source_type", "source_id", "knowledge_base_id", "knowledge_id"}

type MilvusChunkVector struct {
	ChunkID         string    `json:"chunk_id"`
	Vector          []float32 `json:"vector"`
	Content         string    `json:"content"`
	SourceType      int32     `json:"source_type"`
	SourceID        string    `json:"source_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	KnowledgeID     string    `json:"knowledge_id"`
}

func NewMilvusVectorFromIndexInfo(indexInfo *types.IndexInfo, additionalParams map[string]any) *MilvusChunkVector {
	mcv := &MilvusChunkVector{
		Content:         indexInfo.Content,
		SourceType:      int32(indexInfo.SourceType),
		ChunkID:         indexInfo.ChunkID,
		KnowledgeBaseID: indexInfo.KnowledgeBaseID,
		KnowledgeID:     indexInfo.KnowledgeID,
	}

	if additionalParams != nil && slices.Contains(slices.Collect(maps.Keys(additionalParams)), "embedding") {
		if embeddingMap, ok := additionalParams["embedding"].(map[string][]float32); ok {
			mcv.Vector = embeddingMap[indexInfo.SourceID]
		}
	}
	return mcv
}

func ToInsertOption(collectionName string, vecs []*MilvusChunkVector) milvusclient.InsertOption {
	var opt = milvusclient.NewColumnBasedInsertOption(collectionName)

	dim := len(vecs[0].Vector)
	vectors := make([][]float32, len(vecs))
	for i, v := range vecs {
		vectors[i] = v.Vector
	}
	opt.WithFloatVectorColumn("vector", dim, vectors)
	contents := make([]string, len(vecs))
	sourceTypes := make([]int32, len(vecs))
	chunkIDs := make([]string, len(vecs))
	knowledgeBaseIDs := make([]string, len(vecs))
	knowledgeIDs := make([]string, len(vecs))
	for i, v := range vecs {
		contents[i] = v.Content
		sourceTypes[i] = int32(v.SourceType)
		chunkIDs[i] = v.ChunkID
		knowledgeBaseIDs[i] = v.KnowledgeBaseID
		knowledgeIDs[i] = v.KnowledgeID
	}
	opt.WithVarcharColumn("chunk_id", chunkIDs)
	opt.WithVarcharColumn("content", contents)
	opt.WithInt32Column("source_type", sourceTypes)
	opt.WithVarcharColumn("source_id", chunkIDs)
	opt.WithVarcharColumn("knowledge_base_id", knowledgeBaseIDs)
	opt.WithVarcharColumn("knowledge_id", knowledgeIDs)
	return opt
}

func ToDeleteOptionByChunkIds(collectionName string, chunkIDList []string) milvusclient.DeleteOption {
	return milvusclient.NewDeleteOption(collectionName).WithStringIDs("chunk_id", chunkIDList)
}

func ToDeleteOptionByKnowledgeIds(collectionName string, knowledgeIdList []string) milvusclient.DeleteOption {
	return milvusclient.NewDeleteOption(collectionName).WithStringIDs("knowledge_id", knowledgeIdList)
}

func ToSearchOptionByKeywordsRetrieve(collectionName string, params types.RetrieveParams) milvusclient.SearchOption {
	annSearchParams := index.NewCustomAnnParam()
	annSearchParams.WithExtraParam("drop_ratio_search", 0.1)
	var opt = milvusclient.NewSearchOption(
		collectionName,
		params.TopK,
		[]entity.Vector{entity.Text(params.Query)})
	opt.WithConsistencyLevel(entity.ClStrong).
		WithANNSField("sparse").
		WithAnnParam(annSearchParams).
		WithOutputFields(OutputFields...)
	if len(params.KnowledgeBaseIDs) > 0 {
		opt.WithFilter(FilterWithStringIds("knowledge_base_id", params.KnowledgeBaseIDs))
	}
	return opt
}

func ToSearchOptionByVectorRetrieve(collectionName string, params types.RetrieveParams) milvusclient.SearchOption {
	var opt = milvusclient.NewSearchOption(
		collectionName,
		params.TopK,
		[]entity.Vector{entity.FloatVector(params.Embedding)})

	opt.WithANNSField("vector").
		WithOutputFields(OutputFields...)
	if len(params.KnowledgeBaseIDs) > 0 {
		opt.WithFilter(FilterWithStringIds("knowledge_base_id", params.KnowledgeBaseIDs))
	}
	return opt
}

func FilterWithStringIds(name string, ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf(`"%s"`, id)
	}
	return fmt.Sprintf("%s in [%s]", name, strings.Join(quoted, ", "))
}

func ToRetrieveResult(rs milvusclient.ResultSet, matchType types.MatchType) []*types.IndexWithScore {
	chunkIds := rs.GetColumn("chunk_id").FieldData().GetScalars().GetStringData().Data
	contents := rs.GetColumn("content").FieldData().GetScalars().GetStringData().Data
	sourceTypes := rs.GetColumn("source_type").FieldData().GetScalars().GetIntData().Data
	sourceIds := rs.GetColumn("source_id").FieldData().GetScalars().GetStringData().Data
	knowledgeBaseIds := rs.GetColumn("knowledge_base_id").FieldData().GetScalars().GetStringData().Data
	knowledgeIds := rs.GetColumn("knowledge_id").FieldData().GetScalars().GetStringData().Data
	scores := rs.Scores
	results := make([]*types.IndexWithScore, rs.Len())
	for i := 0; i < rs.Len(); i++ {
		results[i] = &types.IndexWithScore{
			ChunkID:         chunkIds[i],
			Content:         contents[i],
			SourceID:        sourceIds[i],
			SourceType:      types.SourceType(sourceTypes[i]),
			KnowledgeID:     knowledgeIds[i],
			KnowledgeBaseID: knowledgeBaseIds[i],
			Score:           float64(scores[i]),
			MatchType:       matchType,
		}
	}
	return results
}

func ToInsertOptionFromResultSet(ctx context.Context, collectionName string, rs milvusclient.ResultSet, targetKnowledgeBaseID string, sourceToTargetKBIDMap map[string]string, sourceToTargetChunkIDMap map[string]string) milvusclient.InsertOption {
	n := rs.Len()

	vectorField := rs.GetColumn("vector").FieldData().GetVectors()
	dim := int(vectorField.GetDim())
	allVectors := vectorField.GetFloatVector().GetData()

	chunkIds := rs.GetColumn("chunk_id").FieldData().GetScalars().GetStringData().Data
	contents := rs.GetColumn("content").FieldData().GetScalars().GetStringData().Data
	sourceTypes := rs.GetColumn("source_type").FieldData().GetScalars().GetIntData().Data
	sourceIds := rs.GetColumn("source_id").FieldData().GetScalars().GetStringData().Data
	knowledgeIds := rs.GetColumn("knowledge_id").FieldData().GetScalars().GetStringData().Data

	validVectors := make([][]float32, 0, n)
	validChunkIds := make([]string, 0, n)
	validKnowledgeIds := make([]string, 0, n)
	validKnowledgeBaseIds := make([]string, 0, n)
	validContents := make([]string, 0, n)
	validSourceTypes := make([]int32, 0, n)
	validSourceIds := make([]string, 0, n)
	for i := 0; i < n; i++ {
		chunkID := chunkIds[i]
		knowledgeID := knowledgeIds[i]

		targetChunkID, ok := sourceToTargetChunkIDMap[chunkID]
		if !ok {
			logger.GetLogger(ctx).Warnf("[Milvus] Source chunk %s not found in target chunk mapping, skipping", chunkID)
			continue
		}
		targetKnowledgeID, ok := sourceToTargetKBIDMap[knowledgeID]
		if ok {
			logger.GetLogger(ctx).Warnf("[Milvus] Skip vector: knowledge %s not found in target mapping", knowledgeID)
			continue
		}

		start := i * dim
		end := start + dim
		vectorSlice := allVectors[start:end]

		validVectors = append(validVectors, vectorSlice)
		validChunkIds = append(validChunkIds, targetChunkID)
		validKnowledgeIds = append(validKnowledgeIds, targetKnowledgeID)
		validKnowledgeBaseIds = append(validKnowledgeBaseIds, targetKnowledgeBaseID)
		validContents = append(validContents, contents[i])
		validSourceTypes = append(validSourceTypes, sourceTypes[i])
		validSourceIds = append(validSourceIds, sourceIds[i])
	}

	opt := milvusclient.NewColumnBasedInsertOption(collectionName)
	opt.WithFloatVectorColumn("vector", dim, validVectors)
	opt.WithVarcharColumn("chunk_id", validChunkIds)
	opt.WithVarcharColumn("content", validContents)
	opt.WithInt32Column("source_type", validSourceTypes)
	opt.WithVarcharColumn("source_id", validSourceIds)
	opt.WithVarcharColumn("knowledge_base_id", validKnowledgeBaseIds)
	opt.WithVarcharColumn("knowledge_id", validKnowledgeIds)
	return opt
}
