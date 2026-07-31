package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrKnowledgeFolderMoveInvalid             = errors.New("invalid knowledge folder move")
	ErrKnowledgeFolderMoveKnowledgeNotFound   = errors.New("knowledge folder move knowledge not found")
	ErrKnowledgeFolderMoveConflict            = errors.New("knowledge folder move conflict")
	ErrKnowledgeFolderMoveDataIntegrity       = errors.New("knowledge folder move data integrity error")
	ErrKnowledgeFolderIndexPendingInvalid     = errors.New("invalid knowledge folder index pending row")
	ErrKnowledgeCrossKBMoveInvalid            = errors.New("invalid cross-knowledge-base move")
	ErrKnowledgeCrossKBMoveInvalidFolderState = errors.New("cross-knowledge-base move requires root folder state")
	ErrKnowledgeCrossKBMoveConflict           = errors.New("cross-knowledge-base move conflict")
)

type knowledgeFolderMoveWriteRepository struct {
	*knowledgeFolderReader
}

var _ interfaces.KnowledgeFolderMoveRepository = (*knowledgeFolderRepository)(nil)
var _ interfaces.KnowledgeFolderMoveTxRepository = (*knowledgeFolderMoveWriteRepository)(nil)
var _ interfaces.KnowledgeCrossKBMoveRepository = (*knowledgeRepository)(nil)

func newKnowledgeFolderMoveWriteRepository(db *gorm.DB) *knowledgeFolderMoveWriteRepository {
	return &knowledgeFolderMoveWriteRepository{
		knowledgeFolderReader: newKnowledgeFolderReader(db),
	}
}

// NewKnowledgeFolderMoveRepository creates the outer transaction capability
// without widening the existing folder tree repository interface.
func NewKnowledgeFolderMoveRepository(db *gorm.DB) interfaces.KnowledgeFolderMoveRepository {
	return newKnowledgeFolderRepository(db)
}

// LockKnowledgeForFolderMove locks active knowledge rows in deterministic
// tenant/knowledge-base/id order. The input is copied and deduplicated so the
// repository never mutates caller-owned slices.
func (r *knowledgeFolderMoveWriteRepository) LockKnowledgeForFolderMove(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	knowledgeIDs []string,
) ([]*types.Knowledge, error) {
	if ctx == nil || tenantID == 0 || kbID == "" || r == nil ||
		r.knowledgeFolderReader == nil || r.db == nil {
		return nil, ErrKnowledgeFolderMoveInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	orderedIDs, err := normalizeKnowledgeFolderMoveIDs(knowledgeIDs)
	if err != nil {
		return nil, err
	}

	query := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Select(
			"id",
			"tenant_id",
			"knowledge_base_id",
			"folder_id",
			"folder_version",
			"folder_indexed_version",
			"parse_status",
		).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id IN ?",
			tenantID,
			kbID,
			orderedIDs,
		).
		Order("tenant_id ASC").
		Order("knowledge_base_id ASC").
		Order("id ASC")
	if r.db.Dialector != nil && r.db.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}

	var knowledges []*types.Knowledge
	if err := query.Find(&knowledges).Error; err != nil {
		return nil, err
	}
	if len(knowledges) != len(orderedIDs) {
		return nil, ErrKnowledgeFolderMoveKnowledgeNotFound
	}
	return knowledges, nil
}

func normalizeKnowledgeFolderMoveIDs(knowledgeIDs []string) ([]string, error) {
	if len(knowledgeIDs) == 0 {
		return nil, ErrKnowledgeFolderMoveInvalid
	}
	seen := make(map[string]struct{}, len(knowledgeIDs))
	orderedIDs := make([]string, 0, len(knowledgeIDs))
	for _, knowledgeID := range knowledgeIDs {
		if knowledgeID == "" || knowledgeID != strings.TrimSpace(knowledgeID) {
			return nil, ErrKnowledgeFolderMoveInvalid
		}
		if _, exists := seen[knowledgeID]; exists {
			continue
		}
		seen[knowledgeID] = struct{}{}
		orderedIDs = append(orderedIDs, knowledgeID)
	}
	sort.Strings(orderedIDs)
	return orderedIDs, nil
}

// UpdateKnowledgeFolderForMove conditionally changes one active knowledge
// placement and increments the authoritative version in the database. The
// indexed checkpoint is intentionally untouched so the row becomes pending.
func (r *knowledgeFolderMoveWriteRepository) UpdateKnowledgeFolderForMove(
	ctx context.Context,
	update interfaces.KnowledgeFolderMoveUpdate,
) error {
	if ctx == nil || update.TenantID == 0 || update.KnowledgeBaseID == "" ||
		update.KnowledgeID == "" || r == nil || r.db == nil {
		return ErrKnowledgeFolderMoveInvalid
	}
	if update.ExpectedFolderVersion == 0 ||
		update.ExpectedFolderVersion >= uint64(math.MaxInt64) {
		return ErrKnowledgeFolderMoveDataIntegrity
	}
	if update.ExpectedFolderID == update.TargetFolderID {
		return ErrKnowledgeFolderMoveInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	updatedAt := update.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			update.TenantID,
			update.KnowledgeBaseID,
			update.KnowledgeID,
		).
		Where("parse_status <> ?", types.ParseStatusDeleting).
		Where(
			"folder_id = ? AND folder_version = ?",
			update.ExpectedFolderID,
			update.ExpectedFolderVersion,
		).
		Updates(map[string]interface{}{
			"folder_id":      update.TargetFolderID,
			"folder_version": gorm.Expr("folder_version + 1"),
			"updated_at":     updatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrKnowledgeFolderMoveConflict
	}
	return nil
}

// UpsertKnowledgeFolderIndexPending records the latest requested placement for
// one knowledge. A conflict preserves the existing row identity and creation
// timestamp, replacing only the latest-wins payload and update timestamp.
func (r *knowledgeFolderMoveWriteRepository) UpsertKnowledgeFolderIndexPending(
	ctx context.Context,
	pending *types.KnowledgeFolderIndexPending,
) error {
	if ctx == nil || pending == nil || pending.TenantID == 0 ||
		pending.KnowledgeBaseID == "" || pending.KnowledgeID == "" ||
		pending.RequestedVersion == 0 ||
		pending.RequestedVersion > uint64(math.MaxInt64) ||
		r == nil || r.db == nil {
		return ErrKnowledgeFolderIndexPendingInvalid
	}
	parsedID, err := uuid.Parse(pending.ID)
	if err != nil || parsedID.String() != pending.ID {
		return ErrKnowledgeFolderIndexPendingInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "knowledge_base_id"},
				{Name: "knowledge_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"target_folder_id",
				"requested_version",
				"updated_at",
			}),
		}).
		Create(pending).Error
}

// UpdateKnowledgeForCrossKBMove persists the target knowledge-base identity
// and reset folder state in one scoped UPDATE. It deliberately bypasses the
// generic update path, whose folder columns are protected from stale writes.
func (r *knowledgeRepository) UpdateKnowledgeForCrossKBMove(
	ctx context.Context,
	knowledge *types.Knowledge,
	sourceKnowledgeBaseID string,
) error {
	if ctx == nil || knowledge == nil || knowledge.TenantID == 0 ||
		knowledge.ID == "" || knowledge.KnowledgeBaseID == "" ||
		sourceKnowledgeBaseID == "" ||
		knowledge.KnowledgeBaseID == sourceKnowledgeBaseID ||
		r == nil || r.db == nil {
		return ErrKnowledgeCrossKBMoveInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if knowledge.FolderID != types.KnowledgeFolderRootID ||
		knowledge.FolderVersion != 1 ||
		knowledge.FolderIndexedVersion != 0 {
		return ErrKnowledgeCrossKBMoveInvalidFolderState
	}
	if knowledge.DeletedAt.Valid {
		return ErrKnowledgeCrossKBMoveInvalid
	}

	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where(
			"tenant_id = ? AND id = ? AND knowledge_base_id = ?",
			knowledge.TenantID,
			knowledge.ID,
			sourceKnowledgeBaseID,
		).
		Where("parse_status = ?", types.ParseStatusProcessing).
		Select("*").
		Omit("id", "deleted_at", "pending_subtasks_count").
		Updates(knowledge)
	if result.Error != nil {
		return fmt.Errorf("update knowledge for cross-knowledge-base move: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrKnowledgeCrossKBMoveConflict
	}
	return nil
}
