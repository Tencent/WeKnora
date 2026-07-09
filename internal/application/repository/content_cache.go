package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---- VLMCacheRepo ----

// VLMCacheRepo persists canonical OCR/Caption results keyed by
// (image_hash, model_id, prompt_version, prompt_kind). Content-addressed:
// no tenant_id — the same image bytes + model + prompt yield the same
// canonical text regardless of owner.
type VLMCacheRepo interface {
	Get(ctx context.Context, imageHash, modelID, promptVersion, promptKind string) (string, bool, error)
	Put(ctx context.Context, row *types.VLMCache) error
	Delete(ctx context.Context, imageHash, modelID, promptVersion, promptKind string) error
}

type vlmCacheRepo struct{ db *gorm.DB }

func NewVLMCacheRepo(db *gorm.DB) VLMCacheRepo { return &vlmCacheRepo{db: db} }

func (r *vlmCacheRepo) Get(ctx context.Context, imageHash, modelID, promptVersion, promptKind string) (string, bool, error) {
	var row types.VLMCache
	err := r.db.WithContext(ctx).Where(
		"image_hash = ? AND model_id = ? AND prompt_version = ? AND prompt_kind = ?",
		imageHash, modelID, promptVersion, promptKind,
	).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	return row.OutputText, true, nil
}

func (r *vlmCacheRepo) Put(ctx context.Context, row *types.VLMCache) error {
	// ON CONFLICT DO NOTHING: the cold-cache race winner persists; the loser
	// discards its result and behaves as if it had a hit by reading back.
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(row).Error
}

func (r *vlmCacheRepo) Delete(ctx context.Context, imageHash, modelID, promptVersion, promptKind string) error {
	return r.db.WithContext(ctx).Where(
		"image_hash = ? AND model_id = ? AND prompt_version = ? AND prompt_kind = ?",
		imageHash, modelID, promptVersion, promptKind,
	).Delete(&types.VLMCache{}).Error
}

// ---- EmbeddingCacheRepo ----

// EmbeddingCacheRepo stores vectors keyed by (text_hash, model_id, dim).
// The key excludes doc_id/chunk_id so identical text dedups cross-doc.
// Vector is serialized as a JSON array of floats for DB portability.
type EmbeddingCacheRepo interface {
	// GetBatch returns a map of text_hash -> vector (as []float32) for the
	// keys that hit. Misses are simply absent from the map.
	GetBatch(ctx context.Context, textHashes []string, modelID string, dim int) (map[string][]float32, error)
	Put(ctx context.Context, rows []types.EmbeddingCache) error
}

type embeddingCacheRepo struct{ db *gorm.DB }

func NewEmbeddingCacheRepo(db *gorm.DB) EmbeddingCacheRepo { return &embeddingCacheRepo{db: db} }

func (r *embeddingCacheRepo) GetBatch(ctx context.Context, textHashes []string, modelID string, dim int) (map[string][]float32, error) {
	out := make(map[string][]float32, len(textHashes))
	if len(textHashes) == 0 {
		return out, nil
	}
	// Batch in chunks of 500 to avoid MySQL "too many placeholders" (1390).
	for i := 0; i < len(textHashes); i += 500 {
		end := i + 500
		if end > len(textHashes) {
			end = len(textHashes)
		}
		batch := textHashes[i:end]
		var got []types.EmbeddingCache
		err := r.db.WithContext(ctx).Where(
			"text_hash IN ? AND model_id = ? AND dimension = ?",
			batch, modelID, dim,
		).Find(&got).Error
		if err != nil {
			return nil, fmt.Errorf("embedding cache batch lookup: %w", err)
		}
		for _, row := range got {
			var vec []float32
			if err := json.Unmarshal([]byte(row.Vector), &vec); err != nil {
				// Corrupt row — skip it; the caller will treat it as a miss
				// and recompute, overwriting the bad row on the next Put.
				continue
			}
			out[row.TextHash] = vec
		}
	}
	return out, nil
}

func (r *embeddingCacheRepo) Put(ctx context.Context, rows []types.EmbeddingCache) error {
	if len(rows) == 0 {
		return nil
	}
	// Batch insert with ON CONFLICT DO NOTHING — concurrent cold-cache
	// writers don't clobber each other; the winner's row survives.
	for i := 0; i < len(rows); i += 500 {
		end := i + 500
		if end > len(rows) {
			end = len(rows)
		}
		if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
			DoNothing: true,
		}).Create(rows[i:end]).Error; err != nil {
			return fmt.Errorf("embedding cache batch put: %w", err)
		}
	}
	return nil
}

// ---- WikiMapCacheRepo ----

// WikiMapCacheRepo stores the complete per-doc map output (entities,
// concepts, summary, citations, new-slugs) as an opaque JSON payload.
type WikiMapCacheRepo interface {
	Get(ctx context.Context, docContentHash, granularity, synthesisModelID, promptVersion string) (string, bool, error)
	Put(ctx context.Context, row *types.WikiMapCache) error
}

type wikiMapCacheRepo struct{ db *gorm.DB }

func NewWikiMapCacheRepo(db *gorm.DB) WikiMapCacheRepo { return &wikiMapCacheRepo{db: db} }

func (r *wikiMapCacheRepo) Get(ctx context.Context, docContentHash, granularity, synthesisModelID, promptVersion string) (string, bool, error) {
	var row types.WikiMapCache
	err := r.db.WithContext(ctx).Where(
		"doc_content_hash = ? AND granularity = ? AND synthesis_model_id = ? AND prompt_version = ?",
		docContentHash, granularity, synthesisModelID, promptVersion,
	).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	return row.Payload, true, nil
}

func (r *wikiMapCacheRepo) Put(ctx context.Context, row *types.WikiMapCache) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(row).Error
}

// ---- GraphChunkCacheRepo ----

type GraphChunkCacheRepo interface {
	Get(ctx context.Context, chunkContentHash, extractConfigHash, chatModelID, promptVersion string) (string, bool, error)
	Put(ctx context.Context, row *types.GraphChunkCache) error
}

type graphChunkCacheRepo struct{ db *gorm.DB }

func NewGraphChunkCacheRepo(db *gorm.DB) GraphChunkCacheRepo {
	return &graphChunkCacheRepo{db: db}
}

func (r *graphChunkCacheRepo) Get(ctx context.Context, chunkContentHash, extractConfigHash, chatModelID, promptVersion string) (string, bool, error) {
	var row types.GraphChunkCache
	err := r.db.WithContext(ctx).Where(
		"chunk_content_hash = ? AND extract_config_hash = ? AND chat_model_id = ? AND prompt_version = ?",
		chunkContentHash, extractConfigHash, chatModelID, promptVersion,
	).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	return row.Payload, true, nil
}

func (r *graphChunkCacheRepo) Put(ctx context.Context, row *types.GraphChunkCache) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(row).Error
}

// ---- ParseProductCacheRepo ----

type ParseProductCacheRepo interface {
	Get(ctx context.Context, fileHash, parserEngine, parserConfigHash, renderConfigHash string) (string, bool, error)
	Put(ctx context.Context, row *types.ParseProductCache) error
}

type parseProductCacheRepo struct{ db *gorm.DB }

func NewParseProductCacheRepo(db *gorm.DB) ParseProductCacheRepo {
	return &parseProductCacheRepo{db: db}
}

func (r *parseProductCacheRepo) Get(ctx context.Context, fileHash, parserEngine, parserConfigHash, renderConfigHash string) (string, bool, error) {
	var row types.ParseProductCache
	err := r.db.WithContext(ctx).Where(
		"file_hash = ? AND parser_engine = ? AND parser_config_hash = ? AND render_config_hash = ?",
		fileHash, parserEngine, parserConfigHash, renderConfigHash,
	).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	return row.Payload, true, nil
}

func (r *parseProductCacheRepo) Put(ctx context.Context, row *types.ParseProductCache) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(row).Error
}

// ---- SummaryCacheRepo ----

// SummaryCacheRepo persists LLM-generated document summaries keyed by
// (doc_content_hash, model_id, prompt_version, config_hash).
type SummaryCacheRepo interface {
	Get(ctx context.Context, docContentHash, modelID, promptVersion, configHash string) (string, bool, error)
	Put(ctx context.Context, row *types.SummaryCache) error
}

type summaryCacheRepo struct{ db *gorm.DB }

func NewSummaryCacheRepo(db *gorm.DB) SummaryCacheRepo { return &summaryCacheRepo{db: db} }

func (r *summaryCacheRepo) Get(ctx context.Context, docContentHash, modelID, promptVersion, configHash string) (string, bool, error) {
	var row types.SummaryCache
	err := r.db.WithContext(ctx).Where(
		"doc_content_hash = ? AND model_id = ? AND prompt_version = ? AND config_hash = ?",
		docContentHash, modelID, promptVersion, configHash,
	).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	return row.Summary, true, nil
}

func (r *summaryCacheRepo) Put(ctx context.Context, row *types.SummaryCache) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(row).Error
}

// ---- QuestionCacheRepo ----

// QuestionCacheRepo persists LLM-generated chunk questions keyed by
// (chunk_content_hash, model_id, prompt_version, config_hash).
type QuestionCacheRepo interface {
	Get(ctx context.Context, chunkContentHash, modelID, promptVersion, configHash string) (string, bool, error)
	Put(ctx context.Context, row *types.QuestionCache) error
}

type questionCacheRepo struct{ db *gorm.DB }

func NewQuestionCacheRepo(db *gorm.DB) QuestionCacheRepo { return &questionCacheRepo{db: db} }

func (r *questionCacheRepo) Get(ctx context.Context, chunkContentHash, modelID, promptVersion, configHash string) (string, bool, error) {
	var row types.QuestionCache
	err := r.db.WithContext(ctx).Where(
		"chunk_content_hash = ? AND model_id = ? AND prompt_version = ? AND config_hash = ?",
		chunkContentHash, modelID, promptVersion, configHash,
	).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	return row.Payload, true, nil
}

func (r *questionCacheRepo) Put(ctx context.Context, row *types.QuestionCache) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(row).Error
}
