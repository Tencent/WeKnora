package postgres

import "time"

// tagCategoryModel maps to tag_categories table.
type tagCategoryModel struct {
	ID          int64     `gorm:"column:id;primaryKey"`
	Name        string    `gorm:"column:name"
	Description string    `gorm:"column:description"
	Color       string    `gorm:"column:color"
	CreatedBy   *int64    `gorm:"column:created_by"
	CreatedAt   time.Time `gorm:"column:created_at"
	UpdatedAt   time.Time `gorm:"column:updated_at"
}

func (tagCategoryModel) TableName() string { return "tag_categories" }

// tagModel maps to tags table.
type tagModel struct {
	ID          int64     `gorm:"column:id;primaryKey"`
	CategoryID  int64     `gorm:"column:category_id"
	Name        string    `gorm:"column:name"
	Value       string    `gorm:"column:value"
	Weight      float64   `gorm:"column:weight"
	Description string    `gorm:"column:description"
	CreatedBy   *int64    `gorm:"column:created_by"
	CreatedAt   time.Time `gorm:"column:created_at"
	UpdatedAt   time.Time `gorm:"column:updated_at"
}

func (tagModel) TableName() string { return "tags" }

// documentTagModel maps to document_tags table.
type documentTagModel struct {
	ID         int64      `gorm:"column:id;primaryKey"`
	DocumentID string     `gorm:"column:document_id"
	TagID      int64      `gorm:"column:tag_id"
	Weight     *float64   `gorm:"column:weight"
	CreatedBy  *int64     `gorm:"column:created_by"
	CreatedAt  time.Time  `gorm:"column:created_at"
	UpdatedAt  time.Time  `gorm:"column:updated_at"
	Tag        *tagModel  `gorm:"foreignKey:TagID"
}

func (documentTagModel) TableName() string { return "document_tags" }

// virtualKBModel maps to virtual_knowledge_bases table.
type virtualKBModel struct {
	ID          int64     `gorm:"column:id;primaryKey"`
	Name        string    `gorm:"column:name"
	Description string    `gorm:"column:description"
	CreatedBy   *int64    `gorm:"column:created_by"
	Config      any       `gorm:"column:config"
	CreatedAt   time.Time `gorm:"column:created_at"
	UpdatedAt   time.Time `gorm:"column:updated_at"
	Filters     []virtualKBFilterModel `gorm:"foreignKey:VirtualKBID"`
}

func (virtualKBModel) TableName() string { return "virtual_knowledge_bases" }

// virtualKBFilterModel maps to virtual_kb_filters table.
type virtualKBFilterModel struct {
	ID            int64     `gorm:"column:id;primaryKey"`
	VirtualKBID   int64     `gorm:"column:virtual_kb_id"
	TagCategoryID int64     `gorm:"column:tag_category_id"
	Operator      string    `gorm:"column:operator"
	Weight        float64   `gorm:"column:weight"
	TagIDs        []int64   `gorm:"column:tag_ids;type:integer[]"
	CreatedAt     time.Time `gorm:"column:created_at"
	UpdatedAt     time.Time `gorm:"column:updated_at"
}

func (virtualKBFilterModel) TableName() string { return "virtual_kb_filters" }
