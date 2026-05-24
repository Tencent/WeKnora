package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// systemSettingRepository implements interfaces.SystemSettingRepository
// against the system_settings table (migration 000053). The table is
// system-scoped (no tenant_id column) and intentionally tiny — single-
// digit rows in P1 — so List does not paginate.
type systemSettingRepository struct {
	db *gorm.DB
}

// NewSystemSettingRepository wires the repo into the dig container.
// Receives the shared *gorm.DB; no other deps.
func NewSystemSettingRepository(db *gorm.DB) interfaces.SystemSettingRepository {
	return &systemSettingRepository{db: db}
}

// Get fetches a system setting by key. Returns (nil, nil) when the row
// does not exist — the resolver service treats "missing" as "fall back
// to ENV / default", so a 404 here is a normal control-flow signal,
// not an error. Real DB errors (connection lost, etc.) surface up.
func (r *systemSettingRepository) Get(ctx context.Context, key string) (*types.SystemSetting, error) {
	var s types.SystemSetting
	err := r.db.WithContext(ctx).Where("key = ?", key).First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// List returns every system_settings row, ordered by category then key
// for stable management-UI rendering. No pagination — see type comment.
func (r *systemSettingRepository) List(ctx context.Context) ([]*types.SystemSetting, error) {
	var rows []*types.SystemSetting
	err := r.db.WithContext(ctx).Order("category ASC, key ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// Upsert writes the row keyed by Key. We use ON CONFLICT (key) DO UPDATE
// rather than the naive Save() because (a) the natural key is `key`, not
// `id`, and (b) the seeded migration row already has an id; a Save with
// id=0 would re-insert and trip the UNIQUE constraint. Updating only
// the mutable columns prevents the migration's seeded id/created_at
// from being overwritten.
func (r *systemSettingRepository) Upsert(ctx context.Context, s *types.SystemSetting) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"value",
				"value_type",
				"category",
				"description",
				"is_secret",
				"requires_restart",
				"last_modified_by",
				"updated_at",
			}),
		}).
		Create(s).Error
}
