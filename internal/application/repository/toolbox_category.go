package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type toolboxCategoryRepository struct{ db *gorm.DB }

// ErrToolboxCategoryNameExists identifies a duplicate tag name within a workspace.
var ErrToolboxCategoryNameExists = errors.New("toolbox tag name already exists")

func (r *toolboxCategoryRepository) categoryWriteError(err error) error {
	if err == nil {
		return nil
	}
	if translator, ok := r.db.Dialector.(gorm.ErrorTranslator); ok &&
		errors.Is(translator.Translate(err), gorm.ErrDuplicatedKey) {
		return ErrToolboxCategoryNameExists
	}
	return err
}

// NewToolboxCategoryRepository creates the workspace tag repository.
func NewToolboxCategoryRepository(db *gorm.DB) interfaces.ToolboxCategoryRepository {
	return &toolboxCategoryRepository{db: db}
}

func (r *toolboxCategoryRepository) List(
	ctx context.Context, tenantID uint64,
) ([]*types.ToolboxCategory, error) {
	var categories []*types.ToolboxCategory
	err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("name ASC, id ASC").
		Find(&categories).Error
	return categories, err
}

func (r *toolboxCategoryRepository) GetByID(
	ctx context.Context, tenantID uint64, id string,
) (*types.ToolboxCategory, error) {
	var category types.ToolboxCategory
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		First(&category).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &category, err
}

func (r *toolboxCategoryRepository) GetByName(
	ctx context.Context, tenantID uint64, name string,
) (*types.ToolboxCategory, error) {
	var category types.ToolboxCategory
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND name = ?", tenantID, name).
		First(&category).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &category, err
}

func (r *toolboxCategoryRepository) Create(
	ctx context.Context, category *types.ToolboxCategory,
) error {
	return r.categoryWriteError(r.db.WithContext(ctx).Create(category).Error)
}

func (r *toolboxCategoryRepository) Update(
	ctx context.Context, category *types.ToolboxCategory,
) error {
	return r.categoryWriteError(r.db.WithContext(ctx).
		Model(&types.ToolboxCategory{}).
		Where("tenant_id = ? AND id = ?", category.TenantID, category.ID).
		Updates(map[string]any{"name": category.Name, "updated_at": category.UpdatedAt}).Error)
}

func (r *toolboxCategoryRepository) Delete(
	ctx context.Context, tenantID uint64, id string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&types.ToolboxCategory{}).Error
}

func toolboxRelation(resourceType string) (table, resourceColumn string, err error) {
	switch resourceType {
	case types.ToolboxResourceSkill:
		return "toolbox_category_skills", "skill_catalog_id", nil
	case types.ToolboxResourceMCPService:
		return "toolbox_category_mcp_services", "mcp_service_id", nil
	default:
		return "", "", fmt.Errorf("unsupported toolbox resource type %q", resourceType)
	}
}

func (r *toolboxCategoryRepository) ListByResourceIDs(
	ctx context.Context,
	tenantID uint64,
	resourceType string,
	resourceIDs []string,
) (map[string][]types.ToolboxCategory, error) {
	out := make(map[string][]types.ToolboxCategory, len(resourceIDs))
	if len(resourceIDs) == 0 {
		return out, nil
	}
	for _, resourceID := range resourceIDs {
		out[resourceID] = []types.ToolboxCategory{}
	}
	table, resourceColumn, err := toolboxRelation(resourceType)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ResourceID string    `gorm:"column:resource_id"`
		ID         string    `gorm:"column:id"`
		Name       string    `gorm:"column:name"`
		CreatedAt  time.Time `gorm:"column:created_at"`
		UpdatedAt  time.Time `gorm:"column:updated_at"`
	}
	err = r.db.WithContext(ctx).
		Table(table+" AS rel").
		Select("rel."+resourceColumn+" AS resource_id, c.id, c.name, c.created_at, c.updated_at").
		Joins("JOIN toolbox_categories AS c ON c.id = rel.category_id").
		Where("c.tenant_id = ? AND rel."+resourceColumn+" IN ?", tenantID, resourceIDs).
		Order("c.name ASC, c.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ResourceID] = append(out[row.ResourceID], types.ToolboxCategory{
			ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	return out, nil
}

func (r *toolboxCategoryRepository) ReplaceResourceCategories(
	ctx context.Context,
	tenantID uint64,
	resourceType string,
	resourceID string,
	categoryIDs []string,
) error {
	table, resourceColumn, err := toolboxRelation(resourceType)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock even when the relation set is empty. Locking existing relation
		// rows cannot serialize concurrent DELETE + INSERT replacements.
		// Include the tenant so shared built-in MCP definitions stay independent.
		if tx.Name() == "postgres" {
			key := fmt.Sprintf("toolbox_categories:%d:%s:%s", tenantID, resourceType, resourceID)
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error; err != nil {
				return err
			}
		}
		deleteSQL := "DELETE FROM " + table + " WHERE " + resourceColumn +
			" = ? AND category_id IN (SELECT id FROM toolbox_categories WHERE tenant_id = ?)"
		if err := tx.Exec(deleteSQL, resourceID, tenantID).Error; err != nil {
			return err
		}
		if len(categoryIDs) == 0 {
			return nil
		}
		switch resourceType {
		case types.ToolboxResourceSkill:
			rows := make([]types.ToolboxCategorySkill, 0, len(categoryIDs))
			for _, categoryID := range categoryIDs {
				rows = append(rows, types.ToolboxCategorySkill{
					CategoryID: categoryID, SkillCatalogID: resourceID,
				})
			}
			return tx.Create(&rows).Error
		case types.ToolboxResourceMCPService:
			rows := make([]types.ToolboxCategoryMCPService, 0, len(categoryIDs))
			for _, categoryID := range categoryIDs {
				rows = append(rows, types.ToolboxCategoryMCPService{
					CategoryID: categoryID, MCPServiceID: resourceID,
				})
			}
			return tx.Create(&rows).Error
		default:
			return fmt.Errorf("unsupported toolbox resource type %q", resourceType)
		}
	})
}
