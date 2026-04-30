// Package interfaces defines the prompt template repository contract.
package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// PromptTemplateRepository defines the persistence contract for the
// prompt_templates table. Templates are append/upsert-only at the API level —
// removal is intentionally not exposed because the table is small, slow-
// changing config rather than transactional data; if you need to "reset to
// factory" simply Upsert with the YAML default content.
type PromptTemplateRepository interface {
	// List returns every template, sorted by (category, id) for stable
	// output. The slice is safe for the caller to mutate.
	List(ctx context.Context) ([]*types.PromptTemplateRecord, error)

	// ListByCategory returns templates within a single category. The caller
	// is responsible for validating the category against
	// types.AllPromptTemplateCategories.
	ListByCategory(ctx context.Context, category string) ([]*types.PromptTemplateRecord, error)

	// Get fetches a single template by composite key. Returns
	// (nil, nil) when not found, so callers can distinguish "missing" from
	// "DB error" without inspecting GORM's error sentinel.
	Get(ctx context.Context, category, id string) (*types.PromptTemplateRecord, error)

	// Upsert inserts or updates a template by (category, id). Implementations
	// should bump version and set updated_at on every call.
	Upsert(ctx context.Context, t *types.PromptTemplateRecord) error

	// ExistingIDs returns the set of (category, id) pairs already present
	// in the table. Used by the YAML seeding step to skip rows that have
	// already been inserted (or that the user has since modified).
	//
	// The returned map is keyed first by category, then by id, so callers
	// can do `if existing[cat][id] { skip }` without further allocation.
	ExistingIDs(ctx context.Context) (map[string]map[string]struct{}, error)

	// MaxUpdatedAt returns the latest updated_at among all rows. Returns the
	// zero Time when the table is empty. Useful for change detection in
	// future hot-reload pollers.
	MaxUpdatedAt(ctx context.Context) (time.Time, error)
}
