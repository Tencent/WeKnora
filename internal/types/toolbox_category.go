package types

import "time"

// Resource types supported by toolbox tag assignments.
const (
	ToolboxResourceSkill      = "skill"
	ToolboxResourceMCPService = "mcp_service"
)

// ToolboxCategory is one workspace-owned tag shared by Skills and
// MCP services.
type ToolboxCategory struct {
	ID        string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64    `json:"-" gorm:"not null;index"`
	Name      string    `json:"name" gorm:"type:varchar(64);not null"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName returns the workspace tag table.
func (*ToolboxCategory) TableName() string { return "toolbox_categories" }

// ToolboxCategorySkill joins a tag to a workspace skill catalog
// definition. Installations on individual sandboxes inherit this association.
type ToolboxCategorySkill struct {
	CategoryID     string    `gorm:"type:varchar(36);primaryKey"`
	SkillCatalogID string    `gorm:"type:varchar(36);primaryKey"`
	CreatedAt      time.Time `gorm:"autoCreateTime"`
}

// TableName returns the Skill tag relation table.
func (*ToolboxCategorySkill) TableName() string { return "toolbox_category_skills" }

// ToolboxCategoryMCPService joins a tag to an MCP service.
type ToolboxCategoryMCPService struct {
	CategoryID   string    `gorm:"type:varchar(36);primaryKey"`
	MCPServiceID string    `gorm:"type:varchar(36);primaryKey"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
}

// TableName returns the MCP service tag relation table.
func (*ToolboxCategoryMCPService) TableName() string {
	return "toolbox_category_mcp_services"
}
