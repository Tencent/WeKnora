package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type temporaryDocumentRepository struct{ db *gorm.DB }

func NewTemporaryDocumentRepository(db *gorm.DB) interfaces.TemporaryDocumentRepository {
	return &temporaryDocumentRepository{db: db}
}

func (r *temporaryDocumentRepository) Create(ctx context.Context, document *types.TemporaryDocument) error {
	return r.db.WithContext(ctx).Create(document).Error
}

func (r *temporaryDocumentRepository) GetByID(ctx context.Context, tenantID uint64, documentID string) (*types.TemporaryDocument, error) {
	var document types.TemporaryDocument
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, documentID).First(&document).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &document, err
}

func (r *temporaryDocumentRepository) GetScoped(ctx context.Context, tenantID uint64, sessionID, documentID string) (*types.TemporaryDocument, error) {
	var document types.TemporaryDocument
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND session_id = ? AND id = ?", tenantID, sessionID, documentID).First(&document).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &document, err
}

func (r *temporaryDocumentRepository) ListScoped(ctx context.Context, tenantID uint64, sessionID string) ([]*types.TemporaryDocument, error) {
	var documents []*types.TemporaryDocument
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND session_id = ?", tenantID, sessionID).Order("created_at ASC").Find(&documents).Error
	return documents, err
}

func (r *temporaryDocumentRepository) MarkProcessing(ctx context.Context, tenantID uint64, documentID string, startedAt time.Time) error {
	return r.db.WithContext(ctx).Model(&types.TemporaryDocument{}).
		Where("tenant_id = ? AND id = ?", tenantID, documentID).
		Updates(map[string]interface{}{"status": types.TemporaryDocumentStatusProcessing, "started_at": startedAt, "error_message": ""}).Error
}

func (r *temporaryDocumentRepository) MarkReady(ctx context.Context, tenantID uint64, documentID, content string, chunks, imageRefs, metadata types.JSON, tokenCount, chunkCount int, readyAt time.Time) error {
	return r.db.WithContext(ctx).Model(&types.TemporaryDocument{}).
		Where("tenant_id = ? AND id = ?", tenantID, documentID).
		Updates(map[string]interface{}{
			"status": types.TemporaryDocumentStatusReady, "content": content, "chunks": chunks,
			"image_refs": imageRefs, "metadata": metadata, "token_count": tokenCount,
			"chunk_count": chunkCount, "ready_at": readyAt, "error_message": "",
		}).Error
}

func (r *temporaryDocumentRepository) MarkFailed(ctx context.Context, tenantID uint64, documentID, message string) error {
	return r.db.WithContext(ctx).Model(&types.TemporaryDocument{}).
		Where("tenant_id = ? AND id = ?", tenantID, documentID).
		Updates(map[string]interface{}{"status": types.TemporaryDocumentStatusFailed, "error_message": message}).Error
}

func (r *temporaryDocumentRepository) DeleteScoped(ctx context.Context, tenantID uint64, sessionID, documentID string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND session_id = ? AND id = ?", tenantID, sessionID, documentID).Delete(&types.TemporaryDocument{}).Error
}

func (r *temporaryDocumentRepository) ListExpired(ctx context.Context, before time.Time, limit int) ([]*types.TemporaryDocument, error) {
	var documents []*types.TemporaryDocument
	err := r.expiredQuery(ctx, before, limit).Find(&documents).Error
	return documents, err
}

func (r *temporaryDocumentRepository) expiredQuery(ctx context.Context, before time.Time, limit int) *gorm.DB {
	// Sending an attachment makes it part of durable conversation history.
	// Keep the parsed document and source while any live session references it,
	// including forks whose document row still belongs to the original session.
	// Filter before LIMIT so retained uploads cannot starve the cleanup queue.
	query := r.db.WithContext(ctx).Model(&types.TemporaryDocument{}).
		Where("expires_at <= ?", before).Order("expires_at ASC").Limit(limit)
	if r.db.Name() == "sqlite" {
		// SQLite cannot index JSON array membership. Materialize the live
		// tenant/document keys once, rather than expanding every message again
		// for each expired document. A scalar key permits a single indexed IN
		// lookup; IS NOT NULL avoids NOT IN's NULL trap. Tenant IDs are numeric,
		// so the ':' separator cannot collide across tenants.
		referenced := r.db.Table("messages m").
			Select("CAST(s.tenant_id AS TEXT) || ':' || json_extract(a.value, '$.id')").
			Joins("JOIN sessions s ON s.id = m.session_id").
			Joins("JOIN json_each(CAST(m.attachments AS TEXT)) a").
			Where("m.role = 'user' AND m.deleted_at IS NULL AND s.deleted_at IS NULL").
			Where("json_extract(a.value, '$.id') IS NOT NULL")
		return query.Where("(CAST(temporary_documents.tenant_id AS TEXT) || ':' || temporary_documents.id) NOT IN (?)",
			referenced)
	}
	// PostgreSQL uses the partial GIN attachments index for this containment
	// predicate; its live-user predicate must match the index predicate.
	containsAttachment := "m.attachments @> jsonb_build_array(jsonb_build_object('id', temporary_documents.id))"
	switch r.db.Name() {
	case "mysql":
		containsAttachment = "JSON_CONTAINS(m.attachments, JSON_OBJECT('id', temporary_documents.id))"
	}
	referenced := r.db.Table("messages m").Select("1").
		Joins("JOIN sessions s ON s.id = m.session_id").
		Where("m.role = 'user'").
		Where("s.tenant_id = temporary_documents.tenant_id AND s.deleted_at IS NULL AND m.deleted_at IS NULL").
		Where(containsAttachment)
	return query.Where("NOT EXISTS (?)", referenced)
}
