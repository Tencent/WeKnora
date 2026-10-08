package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ImageVectorRepository owns durable image jobs and the image projection they reference.
type ImageVectorRepository struct{ db *gorm.DB }

// NewImageVectorRepository binds jobs to the application database.
func NewImageVectorRepository(db *gorm.DB) *ImageVectorRepository {
	return &ImageVectorRepository{db: db}
}

// ImageVectorSource is one original image occurrence per document, irrespective
// of how many text/caption/OCR chunks project the same address.
type ImageVectorSource struct {
	KnowledgeID string
	ChunkID     string
	ImageURL    string
	Caption     string
	OCRText     string
}

func (r *ImageVectorRepository) sources(ctx context.Context, tenant uint64, kb string) *gorm.DB {
	return r.db.WithContext(ctx).Table("chunk_images i").
		Joins("JOIN knowledges k ON k.id = i.knowledge_id AND k.tenant_id = i.tenant_id "+
			"AND k.knowledge_base_id = i.knowledge_base_id AND k.deleted_at IS NULL").
		Where("i.tenant_id = ? AND i.knowledge_base_id = ? AND i.chunk_type <> ? AND i.is_enabled = ?",
			tenant, kb, types.ChunkTypeImageVector, true).
		Where("i.url <> '' OR i.original_url <> ''")
}

// Sources lists original image occurrences in stable document/address order.
func (r *ImageVectorRepository) Sources(
	ctx context.Context, tenant uint64, kb, knowledge string, offset, limit int,
) ([]ImageVectorSource, error) {
	q := r.sources(ctx, tenant, kb)
	if knowledge != "" {
		q = q.Where("i.knowledge_id = ?", knowledge)
	}
	var rows []ImageVectorSource
	err := q.Select("i.knowledge_id, MIN(i.chunk_id) AS chunk_id, i.image_key AS image_url, " +
		"MAX(i.caption) AS caption, MAX(i.ocr_text) AS ocr_text").
		Group("i.knowledge_id, i.image_key").Order("i.knowledge_id, i.image_key").
		Offset(offset).Limit(limit).Scan(&rows).Error
	return rows, err
}

// Source resolves live source metadata while requiring the original chunk to remain live.
func (r *ImageVectorRepository) Source(ctx context.Context, job *types.ImageVectorJob) (*ImageVectorSource, error) {
	var row ImageVectorSource
	err := r.sources(ctx, job.TenantID, job.KnowledgeBaseID).
		Where("i.knowledge_id = ? AND i.image_key = ?", job.KnowledgeID, job.ImageURL).
		Where("EXISTS (SELECT 1 FROM chunks original WHERE original.id = ? AND original.deleted_at IS NULL "+
			"AND original.is_enabled = ?)", job.SourceChunkID, true).
		Select("i.knowledge_id, MIN(i.chunk_id) AS chunk_id, i.image_key AS image_url, " +
			"MAX(i.caption) AS caption, MAX(i.ocr_text) AS ocr_text").
		Group("i.knowledge_id, i.image_key").Take(&row).Error
	return &row, err
}

// Ensure never resets a live lease. It invalidates completed jobs when their
// source was reparsed (vector chunk removed) or the encoding settings changed.
func (r *ImageVectorRepository) Ensure(ctx context.Context, job *types.ImageVectorJob) (bool, error) {
	now := time.Now()
	job.CreatedAt, job.UpdatedAt = now, now
	job.LeaseUntil = time.Unix(0, 0)
	db := r.db.WithContext(ctx)
	// Adopt a legacy vector chunk so upgrading and backfilling do not double-index an image.
	var legacy struct{ ChunkID string }
	if err := db.Table("chunk_images").Select("chunk_id").Where(
		"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? AND image_key = ? AND chunk_type = ?",
		job.TenantID, job.KnowledgeBaseID, job.KnowledgeID, job.ImageURL, types.ChunkTypeImageVector,
	).Order("chunk_id").Limit(1).Scan(&legacy).Error; err != nil {
		return false, err
	}
	if legacy.ChunkID != "" {
		job.ChunkID = legacy.ChunkID
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(job).Error; err != nil {
		return false, err
	}
	res := db.Model(&types.ImageVectorJob{}).Where("id = ? AND lease_until < ?", job.ID, now).
		Where(`fingerprint <> ? OR reason IN ('configuration_changed','source_removed')
 OR NOT EXISTS (SELECT 1 FROM chunks s WHERE s.id = image_vector_jobs.source_chunk_id AND s.deleted_at IS NULL)
 OR (status = 'completed' AND NOT EXISTS (SELECT 1 FROM chunks c
 WHERE c.id = image_vector_jobs.chunk_id AND c.deleted_at IS NULL
 AND c.is_enabled = TRUE AND c.status = ?))`, job.Fingerprint,
			types.ChunkStatusIndexed).
		Updates(map[string]any{
			"fingerprint": job.Fingerprint, "source_chunk_id": job.SourceChunkID,
			"status":     gorm.Expr("CASE WHEN reason = 'scanned_page' THEN 'skipped' ELSE 'pending' END"),
			"reason":     gorm.Expr("CASE WHEN reason = 'scanned_page' THEN reason ELSE '' END"),
			"updated_at": now,
		})
	if res.Error != nil {
		return false, res.Error
	}
	var current types.ImageVectorJob
	if err := db.First(&current, "id = ?", job.ID).Error; err != nil {
		return false, err
	}
	return current.Status != "completed" && current.Status != "skipped" && current.LeaseUntil.Before(now), nil
}

// Get loads a job within its execution tenant.
func (r *ImageVectorRepository) Get(ctx context.Context, tenant uint64, id string) (*types.ImageVectorJob, error) {
	var job types.ImageVectorJob
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenant).First(&job).Error
	return &job, err
}

// Claim acquires a bounded lease atomically across workers.
func (r *ImageVectorRepository) Claim(ctx context.Context, job *types.ImageVectorJob, token string) (bool, error) {
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&types.ImageVectorJob{}).
		Where("id = ? AND fingerprint = ? AND lease_until < ? AND status NOT IN ?",
			job.ID, job.Fingerprint, now, []string{"completed", "skipped"}).
		Updates(map[string]any{
			"status": "running", "lease_token": token, "lease_until": now.Add(10 * time.Minute),
			"updated_at": now,
		})
	return res.RowsAffected == 1, res.Error
}

// Finish records an outcome only for the worker that still owns the lease.
func (r *ImageVectorRepository) Finish(
	ctx context.Context, job *types.ImageVectorJob, token, status, reason string,
) error {
	return r.db.WithContext(ctx).Model(&types.ImageVectorJob{}).Where("id = ? AND lease_token = ?", job.ID, token).
		Updates(map[string]any{
			"status": status, "reason": reason, "lease_token": "",
			"lease_until": time.Unix(0, 0), "updated_at": time.Now(),
		}).Error
}

// Coverage reports index coverage for the current encoding configuration.
func (r *ImageVectorRepository) Coverage(
	ctx context.Context, tenant uint64, kb, fingerprint string,
) (*types.ImageVectorCoverage, error) {
	source := r.sources(ctx, tenant, kb).Select("i.knowledge_id, i.image_key").Group("i.knowledge_id, i.image_key")
	var counts []struct {
		State string
		Count int64
	}
	state := `CASE WHEN j.id IS NULL OR j.fingerprint <> ? THEN 'missing'
 WHEN j.status = 'completed' AND c.id IS NULL THEN 'missing'
 WHEN j.status = 'running' AND j.lease_until < ? THEN 'failed' ELSE j.status END`
	err := r.db.WithContext(ctx).Table("(?) AS s", source).
		Joins("LEFT JOIN image_vector_jobs j ON j.tenant_id = ? AND j.knowledge_base_id = ? "+
			"AND j.knowledge_id = s.knowledge_id AND j.image_url = s.image_key", tenant, kb).
		Joins("LEFT JOIN chunks c ON c.id = j.chunk_id AND c.deleted_at IS NULL "+
			"AND c.is_enabled = TRUE AND c.status = ?",
			types.ChunkStatusIndexed).
		Select(state+" AS state, COUNT(*) AS count", fingerprint, time.Now()).Group("state").Scan(&counts).Error
	if err != nil {
		return nil, err
	}
	out := &types.ImageVectorCoverage{}
	for _, c := range counts {
		out.Total += c.Count
		switch c.State {
		case "completed":
			out.Completed += c.Count
		case "pending", "running":
			out.Pending += c.Count
		case "failed":
			out.Failed += c.Count
		case "skipped":
			out.Skipped += c.Count
		default:
			out.Missing += c.Count
		}
	}
	return out, nil
}

// SaveVectorChunk uses an upsert without replacing generated seq_id. A retry
// updates the same vector chunk instead of accumulating copies.
func (r *ImageVectorRepository) SaveVectorChunk(
	ctx context.Context, job *types.ImageVectorJob, chunk *types.Chunk,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scoped := &ImageVectorRepository{db: tx}
		if _, err := scoped.Source(ctx, job); err != nil {
			return err
		}
		var old types.Chunk
		err := tx.Unscoped().First(&old, "id = ?", chunk.ID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			enabled := chunk.IsEnabled
			if err := NewChunkRepository(tx).CreateChunks(ctx, []*types.Chunk{chunk}); err != nil {
				return err
			}
			// GORM applies default:true on insert even when a bool is false.
			chunk.IsEnabled = enabled
			return tx.Model(&types.Chunk{}).Where("id = ?", chunk.ID).Update("is_enabled", enabled).Error
		}
		if err != nil {
			return err
		}
		return tx.Unscoped().Model(&types.Chunk{}).Where("id = ?", chunk.ID).Updates(map[string]any{
			"content": chunk.Content, "image_info": chunk.ImageInfo, "parent_chunk_id": chunk.ParentChunkID,
			"is_enabled": chunk.IsEnabled, "status": chunk.Status, "updated_at": chunk.UpdatedAt, "deleted_at": nil,
			"source_locators": chunk.SourceLocators, "tag_id": chunk.TagID,
		}).Error
	})
}

// RefreshMetadata attaches late VLM output without changing or recomputing the image vector.
func (r *ImageVectorRepository) RefreshMetadata(
	ctx context.Context, tenant uint64, kb, knowledge string, info types.ImageInfo,
) error {
	key := info.URL
	if key == "" {
		key = info.OriginalURL
	}
	raw, err := json.Marshal([]types.ImageInfo{info})
	if err != nil {
		return err
	}
	content := strings.TrimSpace(info.Caption)
	if content == "" {
		content = strings.TrimSpace(info.OCRText)
	}
	if content == "" {
		return nil
	}
	ids := r.db.Table("chunk_images").Select("chunk_id").Where(
		"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? AND image_key = ? AND chunk_type = ?",
		tenant, kb, knowledge, key, types.ChunkTypeImageVector)
	return r.db.WithContext(ctx).Model(&types.Chunk{}).Where("id IN (?)", ids).
		Updates(map[string]any{"content": content, "image_info": string(raw)}).Error
}
