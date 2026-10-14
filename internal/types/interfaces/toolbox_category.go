package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ToolboxCategoryRepository persists workspace tags and their
// Skill/MCP many-to-many relations.
type ToolboxCategoryRepository interface {
	List(context.Context, uint64) ([]*types.ToolboxCategory, error)
	GetByID(context.Context, uint64, string) (*types.ToolboxCategory, error)
	GetByName(context.Context, uint64, string) (*types.ToolboxCategory, error)
	Create(context.Context, *types.ToolboxCategory) error
	Update(context.Context, *types.ToolboxCategory) error
	Delete(context.Context, uint64, string) error
	ListByResourceIDs(
		context.Context, uint64, string, []string,
	) (map[string][]types.ToolboxCategory, error)
	ReplaceResourceCategories(
		context.Context, uint64, string, string, []string,
	) error
}

// ToolboxCategoryService owns category validation, tenant isolation and
// resource association writes.
type ToolboxCategoryService interface {
	List(context.Context, uint64) ([]*types.ToolboxCategory, error)
	Create(context.Context, uint64, string) (*types.ToolboxCategory, error)
	Update(context.Context, uint64, string, string) (*types.ToolboxCategory, error)
	Delete(context.Context, uint64, string) error
	ListByResourceIDs(
		context.Context, uint64, string, []string,
	) (map[string][]types.ToolboxCategory, error)
	ReplaceSkillCategories(
		context.Context, uint64, string, []string,
	) ([]types.ToolboxCategory, error)
	ReplaceMCPServiceCategories(
		context.Context, uint64, string, []string,
	) ([]types.ToolboxCategory, error)
}
