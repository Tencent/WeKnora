package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// promptTemplateRepository is the GORM-backed implementation of
// interfaces.PromptTemplateRepository. The store is global (no tenant
// scoping) — see migration 000039 for the full rationale.
type promptTemplateRepository struct {
	db *gorm.DB
}

// NewPromptTemplateRepository wires the repository against a *gorm.DB.
func NewPromptTemplateRepository(db *gorm.DB) interfaces.PromptTemplateRepository {
	return &promptTemplateRepository{db: db}
}

// List returns every template, ordered by (category, id) for deterministic
// output (handy for tests / diffing seed snapshots).
func (r *promptTemplateRepository) List(ctx context.Context) ([]*types.PromptTemplateRecord, error) {
	var rows []*types.PromptTemplateRecord
	if err := r.db.WithContext(ctx).
		Order("category ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListByCategory narrows List to a single category. Caller is expected to
// have validated the value already; an unknown category just returns an empty
// slice (no error).
func (r *promptTemplateRepository) ListByCategory(ctx context.Context, category string) ([]*types.PromptTemplateRecord, error) {
	var rows []*types.PromptTemplateRecord
	if err := r.db.WithContext(ctx).
		Where("category = ?", category).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Get returns a single template by composite key. (nil, nil) on miss so
// callers don't have to import gorm.ErrRecordNotFound.
func (r *promptTemplateRepository) Get(ctx context.Context, category, id string) (*types.PromptTemplateRecord, error) {
	var row types.PromptTemplateRecord
	if err := r.db.WithContext(ctx).
		Where("category = ? AND id = ?", category, id).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// Upsert inserts or updates the template by (category, id). version is
// incremented atomically so concurrent writers always see a monotonic value.
// updated_at is refreshed via the standard GORM mechanism.
func (r *promptTemplateRepository) Upsert(ctx context.Context, t *types.PromptTemplateRecord) error {
	if t == nil {
		return errors.New("prompt template: nil record")
	}
	if t.Version <= 0 {
		t.Version = 1
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now

	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			// Match on the composite primary key so the row is updated in
			// place rather than duplicated.
			Columns: []clause.Column{{Name: "category"}, {Name: "id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"name":           t.Name,
				"description":    t.Description,
				"content":        t.Content,
				"user_prompt":    t.UserPrompt,
				"has_kb":         t.HasKB,
				"has_web_search": t.HasWebSearch,
				"is_default":     t.IsDefault,
				"mode":           t.Mode,
				"i18n":           t.I18n,
				// Bump version on every conflicting write so we can spot
				// "this row has been edited" without reading the content.
				"version":    gorm.Expr("prompt_templates.version + 1"),
				"updated_at": now,
			}),
		}).
		Create(t).Error
}

// ExistingIDs returns a category→id→{} map of every row's key. Used by the
// YAML seeding step; chosen over List() because we only need the keys and
// want to avoid materialising large content fields.
func (r *promptTemplateRepository) ExistingIDs(ctx context.Context) (map[string]map[string]struct{}, error) {
	type keyOnly struct {
		Category string
		ID       string
	}
	var keys []keyOnly
	if err := r.db.WithContext(ctx).
		Model(&types.PromptTemplateRecord{}).
		Select("category", "id").
		Find(&keys).Error; err != nil {
		return nil, err
	}
	out := make(map[string]map[string]struct{}, len(types.AllPromptTemplateCategories))
	for _, k := range keys {
		bucket, ok := out[k.Category]
		if !ok {
			bucket = make(map[string]struct{})
			out[k.Category] = bucket
		}
		bucket[k.ID] = struct{}{}
	}
	return out, nil
}

// MaxUpdatedAt returns the latest updated_at value, or zero Time when the
// table is empty.
func (r *promptTemplateRepository) MaxUpdatedAt(ctx context.Context) (time.Time, error) {
	var ts *time.Time
	if err := r.db.WithContext(ctx).
		Model(&types.PromptTemplateRecord{}).
		Select("MAX(updated_at)").
		Scan(&ts).Error; err != nil {
		return time.Time{}, err
	}
	if ts == nil {
		return time.Time{}, nil
	}
	return ts.UTC(), nil
}
