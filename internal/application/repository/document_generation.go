package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ interfaces.DocumentGenerationRepository = (*taskPendingOpsRepository)(nil)

type documentGeneration struct {
	TenantID    uint64 `gorm:"primaryKey;autoIncrement:false"`
	KnowledgeID string `gorm:"primaryKey"`
	ScopeID     string
	Token       string
	Advanced    bool
}

// TableName binds persistent producer tombstones to their independent table.
func (documentGeneration) TableName() string { return "document_generations" }

func documentGenerationOwner(tx *gorm.DB, tenantID uint64, knowledgeID, kbID string) (bool, error) {
	if tenantID == 0 || knowledgeID == "" || kbID == "" {
		return false, errors.New("document generation: owner is required")
	}
	queries := []*gorm.DB{
		tx.Model(&types.Tenant{}).Select("id").Where("id=?", tenantID),
		tx.Model(&types.KnowledgeBase{}).Select("id").Where("id=? AND tenant_id=?", kbID, tenantID),
		tx.Model(&types.Knowledge{}).Select("id").
			Where("id=? AND tenant_id=? AND knowledge_base_id=?", knowledgeID, tenantID, kbID),
	}
	for _, query := range queries {
		if tx.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "SHARE"})
		}
		var row map[string]any
		if err := query.Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}

func initialDocumentGeneration(tx *gorm.DB, tenantID uint64, knowledgeID, kbID string) (string, error) {
	var generation documentGeneration
	err := tx.Where("tenant_id=? AND knowledge_id=?", tenantID, knowledgeID).Take(&generation).Error
	if err == nil {
		if generation.Advanced || generation.ScopeID != kbID {
			return "", nil
		}
		return generation.Token, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	generation = documentGeneration{
		TenantID: tenantID, KnowledgeID: knowledgeID, ScopeID: kbID, Token: uuid.NewString(),
	}
	return generation.Token, tx.Create(&generation).Error
}

// InitialDocumentGeneration may create the initial token, but never adopts an advanced generation.
func (r *taskPendingOpsRepository) InitialDocumentGeneration(
	ctx context.Context, tenantID uint64, knowledgeID, kbID string,
) (string, error) {
	var token string
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		token = ""
		active, err := documentGenerationOwner(tx, tenantID, knowledgeID, kbID)
		if err != nil || !active {
			return err
		}
		token, err = initialDocumentGeneration(tx, tenantID, knowledgeID, kbID)
		return err
	})
	return token, err
}

// BeginDocumentGeneration advances ownership before a producer changes source or status.
// The tombstone persists through queue cleanup; execution leases remain independent.
func (r *taskPendingOpsRepository) BeginDocumentGeneration(
	ctx context.Context, tenantID uint64, knowledgeID, currentKB, targetKB string,
) (string, error) {
	if targetKB == "" {
		return "", errors.New("document generation: target scope is required")
	}
	var token string
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		token = ""
		active, err := documentGenerationOwner(tx, tenantID, knowledgeID, currentKB)
		if err != nil || !active {
			return err
		}
		var target types.KnowledgeBase
		query := tx.Model(&types.KnowledgeBase{}).Select("id").Where("id=? AND tenant_id=?", targetKB, tenantID)
		if tx.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "SHARE"})
		}
		if err := query.Take(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		generation := documentGeneration{
			TenantID: tenantID, KnowledgeID: knowledgeID, ScopeID: targetKB, Token: uuid.NewString(), Advanced: true,
		}
		conflict := clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "knowledge_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"scope_id", "token", "advanced"}),
		}
		if err := tx.Clauses(conflict).Create(&generation).Error; err != nil {
			return err
		}
		if err := documentRows(tx).Where("tenant_id=? AND dedup_key=?", tenantID, knowledgeID).
			Delete(&types.TaskPendingOp{}).Error; err != nil {
			return err
		}
		token = generation.Token
		return nil
	})
	return token, err
}

func matchDocumentGeneration(tx *gorm.DB, tenantID uint64, knowledgeID, token string) (*documentGeneration, error) {
	if token == "" {
		return nil, nil
	}
	var generation documentGeneration
	err := tx.Where("tenant_id=? AND knowledge_id=? AND token=?", tenantID, knowledgeID, token).Take(&generation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &generation, err
}

// CheckDocumentGeneration validates ownership without changing document timestamps.
func (r *taskPendingOpsRepository) CheckDocumentGeneration(
	ctx context.Context, tenantID uint64, knowledgeID, token string,
) (bool, error) {
	current := false
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		current = false
		generation, err := matchDocumentGeneration(tx, tenantID, knowledgeID, token)
		if err != nil || generation == nil {
			return err
		}
		var count int64
		err = tx.Model(&types.Knowledge{}).Where("id=? AND tenant_id=?", knowledgeID, tenantID).
			Where(`EXISTS (SELECT 1 FROM tenants t WHERE t.id=knowledges.tenant_id AND t.deleted_at IS NULL)
 AND EXISTS (SELECT 1 FROM knowledge_bases b WHERE b.id=knowledges.knowledge_base_id
 AND b.tenant_id=knowledges.tenant_id AND b.deleted_at IS NULL)`).Count(&count).Error
		current = count == 1
		return err
	})
	return current && err == nil, err
}

// UpdateDocumentGeneration applies an exact-token mutation without resurrecting missing documents.
func (r *taskPendingOpsRepository) UpdateDocumentGeneration(
	ctx context.Context, tenantID uint64, knowledgeID, token string, columns map[string]any,
) (bool, error) {
	updated := false
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		updated = false
		generation, err := matchDocumentGeneration(tx, tenantID, knowledgeID, token)
		if err != nil || generation == nil {
			return err
		}
		if target, ok := columns["knowledge_base_id"]; ok {
			if scope, valid := target.(string); !valid || scope != generation.ScopeID {
				return errors.New("document generation: target scope mismatch")
			}
		}
		if _, ok := columns["tenant_id"]; ok {
			return errors.New("document generation: tenant is immutable")
		}
		if _, ok := columns["id"]; ok {
			return errors.New("document generation: knowledge ID is immutable")
		}
		res := tx.Model(&types.Knowledge{}).Where("id=? AND tenant_id=?", knowledgeID, tenantID).Updates(columns)
		updated = res.RowsAffected == 1
		return res.Error
	})
	return updated && err == nil, err
}

// SaveDocumentGeneration retains ordinary Save omission rules but never performs an insert fallback.
func (r *taskPendingOpsRepository) SaveDocumentGeneration(
	ctx context.Context, token string, knowledge *types.Knowledge,
) (bool, error) {
	if knowledge == nil {
		return false, errors.New("document generation: nil knowledge")
	}
	updated := false
	var savedUpdatedAt time.Time
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		updated = false
		generation, err := matchDocumentGeneration(tx, knowledge.TenantID, knowledge.ID, token)
		if err != nil || generation == nil {
			return err
		}
		if knowledge.KnowledgeBaseID != generation.ScopeID {
			return errors.New("document generation: target scope mismatch")
		}
		copyKnowledge := *knowledge
		copyKnowledge.ErrorMessage = common.CleanInvalidUTF8(copyKnowledge.ErrorMessage)
		omit := append([]string{}, omitFieldsOnUpdate...)
		omit = append(omit, "ID", "TenantID")
		if knowledge.CustomMetadata == nil {
			omit = append(omit, "custom_metadata")
		}
		res := tx.Model(&types.Knowledge{}).Where("id=? AND tenant_id=?", knowledge.ID, knowledge.TenantID).
			Select("*").Omit(omit...).Updates(&copyKnowledge)
		updated = res.RowsAffected == 1
		if res.Error != nil || !updated {
			return res.Error
		}
		var stored struct{ UpdatedAt time.Time }
		if err := tx.Model(&types.Knowledge{}).Select("updated_at").
			Where("id=? AND tenant_id=?", knowledge.ID, knowledge.TenantID).Take(&stored).Error; err != nil {
			return err
		}
		savedUpdatedAt = stored.UpdatedAt
		return nil
	})
	if err == nil && updated {
		knowledge.UpdatedAt = savedUpdatedAt
		knowledge.ErrorMessage = common.CleanInvalidUTF8(knowledge.ErrorMessage)
	}
	return updated && err == nil, err
}

// FailLegacyDocumentGeneration cannot let an archived transport task mutate an advanced producer generation.
func (r *taskPendingOpsRepository) FailLegacyDocumentGeneration(
	ctx context.Context, payload *types.DocumentProcessPayload, lastError string,
) (bool, error) {
	if payload == nil {
		return false, errors.New("document generation: nil payload")
	}
	updated := false
	err := r.documentTransaction(ctx, func(tx *gorm.DB) error {
		updated = false
		active, err := documentGenerationOwner(tx, payload.TenantID, payload.KnowledgeID, payload.KnowledgeBaseID)
		if err != nil || !active {
			return err
		}
		token, err := initialDocumentGeneration(tx, payload.TenantID, payload.KnowledgeID, payload.KnowledgeBaseID)
		if err != nil || token == "" {
			return err
		}
		// An admitted copy owns completion even if this legacy transport failed.
		// Claimed rows remain pending until their execution settles.
		var pending int64
		if err := documentRows(tx).Where("tenant_id=? AND dedup_key=? AND scope_id=? AND generation=?",
			payload.TenantID, payload.KnowledgeID, payload.KnowledgeBaseID, token).Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return nil
		}
		query := tx.Model(&types.Knowledge{}).Where("id=? AND tenant_id=? AND knowledge_base_id=?",
			payload.KnowledgeID, payload.TenantID, payload.KnowledgeBaseID).
			Where("parse_status IN ?", []string{types.ParseStatusPending, types.ParseStatusProcessing})
		if payload.Attempt > 0 {
			query = query.Where(`NOT EXISTS (SELECT 1 FROM knowledge_processing_spans s
 WHERE s.knowledge_id=knowledges.id AND s.attempt>?)`, payload.Attempt)
		}
		res := query.Updates(map[string]any{
			"parse_status": types.ParseStatusFailed, "error_message": lastError, "updated_at": time.Now(),
		})
		updated = res.RowsAffected == 1
		return res.Error
	})
	return updated && err == nil, err
}

func pendingDocumentGeneration(tx *gorm.DB, id int64) (string, error) {
	var token string
	err := tx.Raw(`SELECT generation FROM task_pending_ops WHERE id=?`, id).Scan(&token).Error
	return token, err
}
