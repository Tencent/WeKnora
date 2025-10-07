package types

import "time"

// VirtualKB represents a virtual knowledge base definition.
type VirtualKB struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Filters     []VirtualKBFilter `json:"filters"`
	Config      map[string]any    `json:"config"`
	CreatedBy   int64             `json:"created_by"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// VirtualKBFilter describes a single tag-based rule.
type VirtualKBFilter struct {
	ID            int64   `json:"id"`
	VirtualKBID   int64   `json:"virtual_kb_id"`
	TagCategoryID int64   `json:"tag_category_id"`
	TagIDs        []int64 `json:"tag_ids"`
	Operator      string  `json:"operator"`
	Weight        float64 `json:"weight"`
}
