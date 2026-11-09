package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateKnowledgeSourceIfCurrent changes only source-file fields of the same
// document version that the caller read. Folder and KB changes have their own
// APIs; source replacement and compensation must preserve the current location.
// FilePath is the replacement attempt's unique storage token, so a compensation
// cannot overwrite a newer replacement or a completed cross-KB transfer.
func (r *knowledgeRepository) UpdateKnowledgeSourceIfCurrent(ctx context.Context, before *types.Knowledge,
	values map[string]interface{},
) (*types.Knowledge, error) {
	if before == nil || before.ID == "" || before.TenantID == 0 || before.KnowledgeBaseID == "" {
		return nil, ErrKnowledgeFolderChanged
	}
	allowed := map[string]bool{
		"title": true, "file_name": true, "file_type": true, "file_size": true,
		"file_hash": true, "file_path": true, "metadata": true, "parse_status": true,
		"enable_status": true, "error_message": true, "updated_at": true,
	}
	updates := make(map[string]interface{}, len(values))
	for field, value := range values {
		if !allowed[field] {
			return nil, fmt.Errorf("source replacement cannot update column %q", field)
		}
		updates[field] = value
	}
	if value, ok := updates["error_message"].(string); ok {
		updates["error_message"] = common.CleanInvalidUTF8(value)
	}
	var current types.Knowledge
	err := r.withKnowledgeFolderMutation(ctx, before.TenantID, before.KnowledgeBaseID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?",
				before.ID, before.TenantID, before.KnowledgeBaseID).
			Take(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrKnowledgeFolderChanged
			}
			return err
		}
		if current.FilePath != before.FilePath || current.FileHash != before.FileHash ||
			current.ParseStatus == types.ParseStatusDeleting {
			return ErrKnowledgeFolderChanged
		}
		if err := access.RejectMovingKnowledge(&current); err != nil {
			return err
		}
		if status, ok := updates["parse_status"].(string); ok && status == types.ParseStatusPending {
			if current.ParseStatus == types.ParseStatusPending || current.ParseStatus == types.ParseStatusProcessing ||
				current.ParseStatus == types.ParseStatusFinalizing || current.ParseStatus != before.ParseStatus {
				return ErrKnowledgeFolderChanged
			}
		}
		currentToken, err := knowledgeReparseToken(current.Metadata)
		if err != nil {
			return err
		}
		beforeToken, err := knowledgeReparseToken(before.Metadata)
		if err != nil {
			return err
		}
		if currentToken != beforeToken {
			return ErrKnowledgeFolderChanged
		}
		if metadata, ok := updates["metadata"].(types.JSON); ok {
			fields, err := metadata.Map()
			if err != nil {
				return err
			}
			if fields == nil {
				fields = map[string]interface{}{}
			}
			// The current attempt remains the owner even when source-file
			// compensation restores older user metadata.
			if currentToken == "" {
				delete(fields, types.KnowledgeReparseMetadataKey)
			} else {
				fields[types.KnowledgeReparseMetadataKey] = currentToken
			}
			encoded, err := json.Marshal(fields)
			if err != nil {
				return err
			}
			updates["metadata"] = types.JSON(encoded)
		}
		if len(updates) == 0 {
			return nil
		}
		query := tx.Model(&types.Knowledge{}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? "+
				"AND file_path = ? AND COALESCE(file_hash, '') = ?",
				before.ID, before.TenantID, before.KnowledgeBaseID, before.FilePath, before.FileHash)
		// The server claim token (checked under this row lock above) identifies
		// the attempt even if a folder rename changed updated_at. A cancel,
		// newer claim or different source must still reject compensation.
		// Legacy callers without a claim retain the strict timestamp guard.
		if status, ok := updates["parse_status"].(string); ok && status == types.ParseStatusFailed {
			query = query.Where("parse_status = ?", before.ParseStatus)
			if beforeToken == "" {
				query = query.Where("updated_at = ?", before.UpdatedAt)
			}
		}
		result := query.Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrKnowledgeFolderChanged
		}
		return tx.Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?",
			before.ID, before.TenantID, before.KnowledgeBaseID).
			Take(&current).Error
	})
	if err != nil {
		return nil, err
	}
	return &current, nil
}

// ClaimKnowledgeForReparse owns this source version before any external cleanup.
// A normal reparse cannot take over an already active attempt. Only a replace
// carrying its just-committed pending source may admit pending here.
func (r *knowledgeRepository) ClaimKnowledgeForReparse(
	ctx context.Context, before *types.Knowledge, allowPending bool,
) (*types.Knowledge, error) {
	if before == nil || before.ID == "" || before.TenantID == 0 || before.KnowledgeBaseID == "" {
		return nil, ErrKnowledgeFolderChanged
	}
	switch before.ParseStatus {
	case types.ParseStatusCompleted, types.ParseStatusFailed, types.ParseStatusCancelled:
	case types.ManualKnowledgeStatusDraft:
		// A manual draft is idle and may be selected by bulk reparse. It
		// still must pass the exact source/KB/version CAS below.
		if !before.IsManual() {
			return nil, ErrKnowledgeFolderChanged
		}
	case types.ParseStatusPending:
		if !allowPending {
			return nil, ErrKnowledgeFolderChanged
		}
	default:
		return nil, ErrKnowledgeFolderChanged
	}
	var claimed types.Knowledge
	err := r.withKnowledgeFolderMutation(ctx, before.TenantID, before.KnowledgeBaseID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?",
				before.ID, before.TenantID, before.KnowledgeBaseID).
			Take(&claimed).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrKnowledgeFolderChanged
			}
			return err
		}
		if err := access.RejectMovingKnowledge(&claimed); err != nil {
			return err
		}
		fields, err := before.Metadata.Map()
		if err != nil {
			return err
		}
		if fields == nil {
			fields = map[string]interface{}{}
		}
		fields[types.KnowledgeReparseMetadataKey] = uuid.NewString()
		encoded, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		metadata := types.JSON(encoded)
		result := tx.Model(&types.Knowledge{}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? "+
				"AND COALESCE(file_path, '') = ? AND COALESCE(file_hash, '') = ? "+
				"AND updated_at = ? AND parse_status = ?",
				before.ID, before.TenantID, before.KnowledgeBaseID, before.FilePath, before.FileHash,
				before.UpdatedAt, before.ParseStatus).
			Updates(map[string]interface{}{
				"parse_status": types.ParseStatusProcessing, "enable_status": "disabled", "metadata": metadata,
				"error_message": "", "updated_at": time.Now().UTC().Truncate(time.Microsecond),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrKnowledgeFolderChanged
		}
		return tx.Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?",
			before.ID, before.TenantID, before.KnowledgeBaseID).
			Take(&claimed).Error
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

// FailKnowledgeReparseClaim releases only this still-current cleanup claim as
// failed. It never re-enables transfer by restoring completed after partial IO.
func (r *knowledgeRepository) FailKnowledgeReparseClaim(
	ctx context.Context, claim *types.Knowledge, message string,
) (*types.Knowledge, error) {
	if claim == nil || claim.ID == "" || claim.TenantID == 0 || claim.KnowledgeBaseID == "" {
		return nil, ErrKnowledgeFolderChanged
	}
	claimToken, err := knowledgeReparseToken(claim.Metadata)
	if err != nil {
		return nil, err
	}
	if claimToken == "" {
		return nil, ErrKnowledgeFolderChanged
	}
	var failed types.Knowledge
	err = r.withKnowledgeFolderMutation(ctx, claim.TenantID, claim.KnowledgeBaseID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?",
				claim.ID, claim.TenantID, claim.KnowledgeBaseID).
			Take(&failed).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrKnowledgeFolderChanged
			}
			return err
		}
		currentToken, err := knowledgeReparseToken(failed.Metadata)
		if err != nil {
			return err
		}
		if currentToken != claimToken || failed.FilePath != claim.FilePath || failed.FileHash != claim.FileHash ||
			failed.ParseStatus != types.ParseStatusProcessing {
			return ErrKnowledgeFolderChanged
		}
		// A folder rename may update updated_at without changing attempt
		// ownership. The row lock plus token check guards this update instead.
		result := tx.Model(&types.Knowledge{}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? "+
				"AND COALESCE(file_path, '') = ? AND COALESCE(file_hash, '') = ? AND parse_status = ?",
				claim.ID, claim.TenantID, claim.KnowledgeBaseID, claim.FilePath, claim.FileHash,
				types.ParseStatusProcessing).
			Updates(map[string]interface{}{
				"parse_status": types.ParseStatusFailed, "enable_status": "disabled",
				"error_message": common.CleanInvalidUTF8(message),
				"updated_at":    time.Now().UTC().Truncate(time.Microsecond),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrKnowledgeFolderChanged
		}
		return tx.Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?",
			claim.ID, claim.TenantID, claim.KnowledgeBaseID).
			Take(&failed).Error
	})
	if err != nil {
		return nil, err
	}
	return &failed, nil
}
