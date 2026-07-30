package mysql

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type mysqlEmbedding struct {
	ID              uint      `gorm:"primarykey;autoIncrement"`
	CreatedAt       time.Time `gorm:"column:created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at"`
	SourceID        string    `gorm:"column:source_id;not null;uniqueIndex:idx_mysql_emb_source"`
	SourceType      int       `gorm:"column:source_type;not null;uniqueIndex:idx_mysql_emb_source"`
	ChunkID         string    `gorm:"column:chunk_id;index"`
	KnowledgeID     string    `gorm:"column:knowledge_id;index"`
	KnowledgeBaseID string    `gorm:"column:knowledge_base_id;index"`
	TagID           string    `gorm:"column:tag_id;index"`
	Content         string    `gorm:"column:content;not null"`
	Dimension       int       `gorm:"column:dimension;not null"`
	Embedding       []byte    `gorm:"column:embedding;type:longblob"`
	IsEnabled       *bool     `gorm:"column:is_enabled;default:true;index"`
}

func (mysqlEmbedding) TableName() string { return "mysql_embeddings" }

type mysqlRepository struct {
	db *gorm.DB
}

func NewMySQLRetrieveEngineRepository(db *gorm.DB) interfaces.RetrieveEngineRepository {
	logger.GetLogger(context.Background()).Info("[MySQL] Initializing MySQL retriever engine repository")
	if err := db.AutoMigrate(&mysqlEmbedding{}); err != nil {
		logger.GetLogger(context.Background()).Errorf("[MySQL] AutoMigrate failed: %v", err)
	}
	return &mysqlRepository{db: db}
}

func (r *mysqlRepository) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (r *mysqlRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

func floatsToBytes(f []float32) []byte {
	b := make([]byte, len(f)*4)
	for i, v := range f {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(v))
	}
	return b
}

func bytesToFloats(b []byte) []float32 {
	floats := make([]float32, len(b)/4)
	for i := range floats {
		floats[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return floats
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func toDBVectorEmbedding(info *types.IndexInfo, params map[string]any) *mysqlEmbedding {
	m := &mysqlEmbedding{
		SourceID:        info.SourceID,
		SourceType:      int(info.SourceType),
		ChunkID:         info.ChunkID,
		KnowledgeID:     info.KnowledgeID,
		KnowledgeBaseID: info.KnowledgeBaseID,
		TagID:           info.TagID,
		Content:         common.CleanInvalidUTF8(info.Content),
	}
	if params != nil {
		if em, ok := params["embedding"].(map[string][]float32); ok {
			if emb, ok := em[info.SourceID]; ok {
				m.Embedding = floatsToBytes(emb)
				m.Dimension = len(emb)
			}
		}
	}
	// Set is_enabled from info, with override from additionalParams
	isEnabled := info.IsEnabled
	if params != nil {
		if chunkEnabledMap, ok := params["chunk_enabled"].(map[string]bool); ok {
			if enabled, exists := chunkEnabledMap[info.ChunkID]; exists {
				isEnabled = enabled
			}
		}
	}
	m.IsEnabled = &isEnabled
	return m
}

func fromDBEmbedding(e *mysqlEmbedding, score float64, mt types.MatchType) *types.IndexWithScore {
	return &types.IndexWithScore{
		ID:              strconv.FormatUint(uint64(e.ID), 10),
		SourceID:        e.SourceID,
		SourceType:      types.SourceType(e.SourceType),
		ChunkID:         e.ChunkID,
		KnowledgeID:     e.KnowledgeID,
		KnowledgeBaseID: e.KnowledgeBaseID,
		TagID:           e.TagID,
		Content:         e.Content,
		Score:           score,
		MatchType:       mt,
	}
}

func (r *mysqlRepository) EstimateStorageSize(ctx context.Context, list []*types.IndexInfo, params map[string]any) int64 {
	var total int64
	for _, info := range list {
		emb := toDBVectorEmbedding(info, params)
		total += int64(len(emb.Content)) + int64(emb.Dimension*4) + 200
	}
	return total
}

func (r *mysqlRepository) Save(ctx context.Context, info *types.IndexInfo, params map[string]any) error {
	return r.db.WithContext(ctx).Create(toDBVectorEmbedding(info, params)).Error
}

func (r *mysqlRepository) BatchSave(ctx context.Context, list []*types.IndexInfo, params map[string]any) error {
	if len(list) == 0 {
		return nil
	}
	embeddings := make([]*mysqlEmbedding, len(list))
	for i := range list {
		embeddings[i] = toDBVectorEmbedding(list[i], params)
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(embeddings).Error
}

func (r *mysqlRepository) DeleteByChunkIDList(ctx context.Context, ids []string, dim int, kt string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Where("chunk_id IN ?", ids).Delete(&mysqlEmbedding{}).Error
}

func (r *mysqlRepository) DeleteBySourceIDList(ctx context.Context, ids []string, dim int, kt string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Where("source_id IN ?", ids).Delete(&mysqlEmbedding{}).Error
}

func (r *mysqlRepository) DeleteByKnowledgeIDList(ctx context.Context, ids []string, dim int, kt string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Where("knowledge_id IN ?", ids).Delete(&mysqlEmbedding{}).Error
}

func (r *mysqlRepository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	switch params.RetrieverType {
	case types.KeywordsRetrieverType:
		return r.KeywordsRetrieve(ctx, params)
	case types.VectorRetrieverType:
		return r.VectorRetrieve(ctx, params)
	}
	return nil, errors.New("invalid retriever type")
}

func (r *mysqlRepository) KeywordsRetrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	logger.GetLogger(ctx).Infof("[MySQL] Keywords retrieval: query=%s, topK=%d", params.Query, params.TopK)
	db := r.db.WithContext(ctx).Table("mysql_embeddings")
	if len(params.KnowledgeBaseIDs) > 0 {
		db = db.Where("knowledge_base_id IN ?", params.KnowledgeBaseIDs)
	}
	if len(params.KnowledgeIDs) > 0 {
		db = db.Where("knowledge_id IN ?", params.KnowledgeIDs)
	}
	if len(params.TagIDs) > 0 {
		db = db.Where("tag_id IN ?", params.TagIDs)
	}
	db = db.Where("is_enabled IS NULL OR is_enabled = ?", true)

	var list []mysqlEmbedding
	// Try FULLTEXT first, fallback to LIKE
	err := db.Where("MATCH(content) AGAINST(? IN BOOLEAN MODE)", params.Query).
		Limit(int(params.TopK)).Find(&list).Error
	if err != nil || len(list) == 0 {
		db2 := r.db.WithContext(ctx).Table("mysql_embeddings").
			Where("is_enabled IS NULL OR is_enabled = ?", true)
		if len(params.KnowledgeBaseIDs) > 0 {
			db2 = db2.Where("knowledge_base_id IN ?", params.KnowledgeBaseIDs)
		}
		if len(params.KnowledgeIDs) > 0 {
			db2 = db2.Where("knowledge_id IN ?", params.KnowledgeIDs)
		}
		if len(params.TagIDs) > 0 {
			db2 = db2.Where("tag_id IN ?", params.TagIDs)
		}
		err = db2.Where("content LIKE ?", "%"+params.Query+"%").Limit(int(params.TopK)).Find(&list).Error
	}
	if err != nil {
		return nil, err
	}
	results := make([]*types.IndexWithScore, len(list))
	for i := range list {
		results[i] = fromDBEmbedding(&list[i], 1.0, types.MatchTypeKeywords)
	}
	return []*types.RetrieveResult{{
		Results:             results,
		RetrieverEngineType: types.PostgresRetrieverEngineType,
		RetrieverType:       types.KeywordsRetrieverType,
	}}, nil
}

func (r *mysqlRepository) VectorRetrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	logger.GetLogger(ctx).Infof("[MySQL] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		len(params.Embedding), params.TopK, params.Threshold)

	queryEmb := params.Embedding
	db := r.db.WithContext(ctx).Table("mysql_embeddings").
		Where("dimension = ?", len(queryEmb)).
		Where("is_enabled IS NULL OR is_enabled = ?", true)

	if len(params.KnowledgeBaseIDs) > 0 {
		db = db.Where("knowledge_base_id IN ?", params.KnowledgeBaseIDs)
	}
	if len(params.KnowledgeIDs) > 0 {
		db = db.Where("knowledge_id IN ?", params.KnowledgeIDs)
	}
	if len(params.TagIDs) > 0 {
		db = db.Where("tag_id IN ?", params.TagIDs)
	}

	fetchLimit := int(params.TopK) * 5
	if fetchLimit < 100 {
		fetchLimit = 100
	}
	if fetchLimit > 500 {
		fetchLimit = 500
	}

	var candidates []mysqlEmbedding
	if err := db.Limit(fetchLimit).Find(&candidates).Error; err != nil {
		return nil, err
	}

	type scored struct {
		e *mysqlEmbedding
		s float64
	}
	scored := make([]scored, 0, len(candidates))
	for i := range candidates {
		if len(candidates[i].Embedding) == 0 {
			continue
		}
		sim := cosineSimilarity(queryEmb, bytesToFloats(candidates[i].Embedding))
		if sim >= float64(params.Threshold) {
			scored = append(scored, scored{e: &candidates[i], s: sim})
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].s > scored[j].s })
	if len(scored) > int(params.TopK) {
		scored = scored[:params.TopK]
	}

	results := make([]*types.IndexWithScore, len(scored))
	for i := range scored {
		results[i] = fromDBEmbedding(scored[i].e, scored[i].s, types.MatchTypeEmbedding)
	}
	logger.GetLogger(ctx).Infof("[MySQL] Vector retrieval found %d results", len(results))
	return []*types.RetrieveResult{{
		Results:             results,
		RetrieverEngineType: types.PostgresRetrieverEngineType,
		RetrieverType:       types.VectorRetrieverType,
	}}, nil
}

func (r *mysqlRepository) CopyIndices(ctx context.Context,
	srcKBID string, kbMap map[string]string, chunkMap map[string]string,
	dstKBID string, dim int, kt string,
) error {
	if len(chunkMap) == 0 {
		return nil
	}
	batchSize := 500
	offset := 0
	total := 0
	for {
		var src []*mysqlEmbedding
		if err := r.db.WithContext(ctx).Where("knowledge_base_id = ?", srcKBID).
			Limit(batchSize).Offset(offset).Find(&src).Error; err != nil {
			return err
		}
		if len(src) == 0 {
			break
		}
		dst := make([]*mysqlEmbedding, 0, len(src))
		for _, sv := range src {
			tc, ok := chunkMap[sv.ChunkID]
			if !ok {
				continue
			}
			tk, ok := kbMap[sv.KnowledgeID]
			if !ok {
				continue
			}
			sid := tc
			if sv.SourceID != sv.ChunkID {
				if strings.HasPrefix(sv.SourceID, sv.ChunkID+"-") {
					sid = tc + "-" + strings.TrimPrefix(sv.SourceID, sv.ChunkID+"-")
				} else {
					sid = uuid.New().String()
				}
			}
			dst = append(dst, &mysqlEmbedding{
				Content: sv.Content, SourceID: sid, SourceType: sv.SourceType,
				ChunkID: tc, KnowledgeID: tk, KnowledgeBaseID: dstKBID,
				Dimension: sv.Dimension, Embedding: sv.Embedding, IsEnabled: sv.IsEnabled,
			})
		}
		if len(dst) > 0 {
			if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
				Create(dst).Error; err != nil {
				return err
			}
			total += len(dst)
		}
		offset += batchSize
		if len(src) < batchSize {
			break
		}
	}
	logger.GetLogger(ctx).Infof("[MySQL] CopyIndices done: %d", total)
	return nil
}

func (r *mysqlRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, m map[string]bool) error {
	if len(m) == 0 {
		return nil
	}
	var on, off []string
	for id, en := range m {
		if en {
			on = append(on, id)
		} else {
			off = append(off, id)
		}
	}
	if len(on) > 0 {
		if err := r.db.WithContext(ctx).Model(&mysqlEmbedding{}).
			Where("chunk_id IN ?", on).Update("is_enabled", true).Error; err != nil {
			return err
		}
	}
	if len(off) > 0 {
		if err := r.db.WithContext(ctx).Model(&mysqlEmbedding{}).
			Where("chunk_id IN ?", off).Update("is_enabled", false).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *mysqlRepository) BatchUpdateChunkTagID(ctx context.Context, m map[string]string) error {
	if len(m) == 0 {
		return nil
	}
	groups := make(map[string][]string)
	for id, tag := range m {
		groups[tag] = append(groups[tag], id)
	}
	for tag, ids := range groups {
		if err := r.db.WithContext(ctx).Model(&mysqlEmbedding{}).
			Where("chunk_id IN ?", ids).Update("tag_id", tag).Error; err != nil {
			return err
		}
	}
	return nil
}
