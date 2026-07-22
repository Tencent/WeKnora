package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

var (
	ErrKnowledgeFolderInvalid       = errors.New("invalid knowledge folder")
	ErrKnowledgeFolderNotFound      = errors.New("knowledge folder not found")
	ErrKnowledgeFolderNotEmpty      = errors.New("knowledge folder is not empty")
	ErrKnowledgeFolderConflict      = errors.New("knowledge folder conflict")
	ErrKnowledgeFolderDataIntegrity = errors.New("knowledge folder data integrity error")
)

type knowledgeFolderReader struct {
	db *gorm.DB
}

type knowledgeFolderRepository struct {
	*knowledgeFolderReader
	sqliteRetryWait knowledgeFolderSQLiteWaitFunc
}

type knowledgeFolderTreeRepository struct {
	*knowledgeFolderReader
}

var _ interfaces.KnowledgeFolderReader = (*knowledgeFolderReader)(nil)
var _ interfaces.KnowledgeFolderRepository = (*knowledgeFolderRepository)(nil)
var _ interfaces.KnowledgeFolderTreeRepository = (*knowledgeFolderTreeRepository)(nil)

func newKnowledgeFolderReader(db *gorm.DB) *knowledgeFolderReader {
	return &knowledgeFolderReader{db: db}
}

func newKnowledgeFolderRepository(db *gorm.DB) *knowledgeFolderRepository {
	return &knowledgeFolderRepository{knowledgeFolderReader: newKnowledgeFolderReader(db)}
}

func newKnowledgeFolderTreeRepository(db *gorm.DB) *knowledgeFolderTreeRepository {
	return &knowledgeFolderTreeRepository{knowledgeFolderReader: newKnowledgeFolderReader(db)}
}

// NewKnowledgeFolderRepository creates a knowledge folder repository.
func NewKnowledgeFolderRepository(db *gorm.DB) interfaces.KnowledgeFolderRepository {
	return newKnowledgeFolderRepository(db)
}

func (r *knowledgeFolderTreeRepository) Create(ctx context.Context, folder *types.KnowledgeFolder) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrKnowledgeFolderInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := types.ValidateKnowledgeFolderStructure(folder); err != nil {
		return fmt.Errorf("%w: %v", ErrKnowledgeFolderInvalid, err)
	}
	if err := r.db.WithContext(ctx).Create(folder).Error; err != nil {
		if IsKnowledgeFolderUniqueViolation(err) {
			return fmt.Errorf("%w: %w", ErrKnowledgeFolderConflict, err)
		}
		return err
	}
	return nil
}

func (r *knowledgeFolderReader) GetByID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderID string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			tenantID,
			kbID,
			folderID,
		).
		First(&folder).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeFolderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeFolderReader) GetByParentAndName(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parentID string,
	name string,
) (*types.KnowledgeFolder, error) {
	var folder types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND parent_id = ? AND name = ?",
			tenantID,
			kbID,
			parentID,
			name,
		).
		First(&folder).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeFolderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (r *knowledgeFolderReader) ListByParent(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parentID string,
	page *types.Pagination,
) ([]*types.KnowledgeFolder, int64, error) {
	if page == nil {
		page = &types.Pagination{}
	}

	scope := func(db *gorm.DB) *gorm.DB {
		return db.Where(
			"tenant_id = ? AND knowledge_base_id = ? AND parent_id = ?",
			tenantID,
			kbID,
			parentID,
		)
	}

	var total int64
	if err := scope(r.db.WithContext(ctx).Model(&types.KnowledgeFolder{})).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var folders []*types.KnowledgeFolder
	if err := scope(r.db.WithContext(ctx)).
		Order("sort_order ASC").
		Order("name ASC").
		Order("created_at ASC").
		Order("id ASC").
		Offset(page.Offset()).
		Limit(page.Limit()).
		Find(&folders).Error; err != nil {
		return nil, 0, err
	}
	return folders, total, nil
}

func (r *knowledgeFolderReader) CountKnowledgeByFolderIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
) (map[string]int64, error) {
	counts := make(map[string]int64, len(folderIDs))
	for _, folderID := range folderIDs {
		counts[folderID] = 0
	}
	if len(folderIDs) == 0 {
		return counts, nil
	}

	type countRow struct {
		FolderID string `gorm:"column:folder_id"`
		Count    int64  `gorm:"column:knowledge_count"`
	}
	var rows []countRow
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Select("folder_id, COUNT(*) AS knowledge_count").
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND folder_id IN ?",
			tenantID,
			kbID,
			folderIDs,
		).
		Group("folder_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.FolderID] = row.Count
	}
	return counts, nil
}

func (r *knowledgeFolderReader) FindParentIDsWithChildren(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parentIDs []string,
) (map[string]bool, error) {
	hasChildren := make(map[string]bool, len(parentIDs))
	for _, parentID := range parentIDs {
		hasChildren[parentID] = false
	}
	if len(parentIDs) == 0 {
		return hasChildren, nil
	}

	var foundParentIDs []string
	if err := r.db.WithContext(ctx).
		Model(&types.KnowledgeFolder{}).
		Distinct("parent_id").
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND parent_id IN ?",
			tenantID,
			kbID,
			parentIDs,
		).
		Pluck("parent_id", &foundParentIDs).Error; err != nil {
		return nil, err
	}
	for _, parentID := range foundParentIDs {
		hasChildren[parentID] = true
	}
	return hasChildren, nil
}

// DeleteEmpty soft-deletes a folder only when it has no active children or knowledge.
func (r *knowledgeFolderTreeRepository) DeleteEmpty(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderID string,
) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).
		Model(&types.KnowledgeFolder{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			tenantID,
			kbID,
			folderID,
		).
		Where(
			`NOT EXISTS (
				SELECT 1
				FROM knowledge_folders AS child
				WHERE child.tenant_id = ?
					AND child.knowledge_base_id = ?
					AND child.parent_id = knowledge_folders.id
					AND child.deleted_at IS NULL
			)`,
			tenantID,
			kbID,
		).
		Where(
			`NOT EXISTS (
				SELECT 1
				FROM knowledges AS knowledge
				WHERE knowledge.tenant_id = ?
					AND knowledge.knowledge_base_id = ?
					AND knowledge.folder_id = knowledge_folders.id
					AND knowledge.deleted_at IS NULL
			)`,
			tenantID,
			kbID,
		).
		Updates(map[string]interface{}{
			"deleted_at": now,
			"updated_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	if result.RowsAffected > 1 {
		return ErrKnowledgeFolderDataIntegrity
	}
	if _, err := r.GetByID(ctx, tenantID, kbID, folderID); err != nil {
		return err
	}
	return ErrKnowledgeFolderNotEmpty
}

// IsKnowledgeFolderUniqueViolation reports whether err is a database uniqueness violation.
func IsKnowledgeFolderUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}

	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique ||
			sqliteErr.ExtendedCode == sqlite3.ErrConstraintPrimaryKey
	}
	return false
}
