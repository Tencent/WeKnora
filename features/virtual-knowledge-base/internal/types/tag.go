package types

import "time"

// Tag describes metadata applied to documents.
type Tag struct {
	ID          int64     `json:"id"`
	CategoryID  int64     `json:"category_id"`
	Name        string    `json:"name"`
	Value       string    `json:"value"`
	Weight      float64   `json:"weight"`
	Description string    `json:"description"`
	CreatedBy   int64     `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
