package repository

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm/clause"
)

const knowledgeFolderNamesBatchSize = 200

// CreateIfAbsent inserts without aborting the transaction on uniqueness conflicts.
func (r *knowledgeFolderTreeRepository) CreateIfAbsent(
	ctx context.Context,
	folder *types.KnowledgeFolder,
) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("%w: context is nil", ErrKnowledgeFolderInvalid)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := types.ValidateKnowledgeFolderStructure(folder); err != nil {
		return false, fmt.Errorf("%w: %v", ErrKnowledgeFolderInvalid, err)
	}

	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(folder)
	if result.Error != nil {
		return false, result.Error
	}
	switch result.RowsAffected {
	case 1:
		return true, nil
	case 0:
		return false, nil
	default:
		return false, fmt.Errorf(
			"%w: create-if-absent affected %d rows",
			ErrKnowledgeFolderDataIntegrity,
			result.RowsAffected,
		)
	}
}

func (r *knowledgeFolderReader) ListByParentAndNames(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parentID string,
	names []string,
) ([]*types.KnowledgeFolder, error) {
	uniqueNames := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		uniqueNames = append(uniqueNames, name)
	}
	if len(uniqueNames) == 0 {
		return []*types.KnowledgeFolder{}, nil
	}

	folders := make([]*types.KnowledgeFolder, 0)
	for start := 0; start < len(uniqueNames); start += knowledgeFolderNamesBatchSize {
		end := start + knowledgeFolderNamesBatchSize
		if end > len(uniqueNames) {
			end = len(uniqueNames)
		}

		var batch []*types.KnowledgeFolder
		if err := r.db.WithContext(ctx).
			Where(
				"tenant_id = ? AND knowledge_base_id = ? AND parent_id = ? AND name IN ?",
				tenantID,
				kbID,
				parentID,
				uniqueNames[start:end],
			).
			Order("name ASC").
			Order("id ASC").
			Find(&batch).Error; err != nil {
			return nil, err
		}
		folders = append(folders, batch...)
	}
	return folders, nil
}
