package types

import "time"

// DocumentTag links documents to tags.
type DocumentTag struct {
	ID         int64     `json:"id"`
	DocumentID string    `json:"document_id"`
	TagID      int64     `json:"tag_id"`
	Weight     *float64  `json:"weight"`
	CreatedBy  int64     `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
