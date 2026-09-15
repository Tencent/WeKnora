package repository

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type memoryRepository struct {
	db *gorm.DB
	// Whether the database can rank episode vectors itself. Probed once, on
	// first use, because it depends on a migration that is conditional on
	// pgvector being installed and so cannot be decided from the dialect
	// alone.
	episodeVectorOnce   sync.Once
	episodeVectorColumn bool
}

// NewMemoryRepository creates the long-term memory repository.
func NewMemoryRepository(db *gorm.DB) interfaces.MemoryRepository {
	return &memoryRepository{db: db}
}

// scoped starts every query already filtered by workspace and subject. All
// reads and writes go through it so a missing scope predicate is impossible.
func (r *memoryRepository) scoped(ctx context.Context, scope interfaces.MemoryScope) *gorm.DB {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID)
}

func (r *memoryRepository) GetSubject(
	ctx context.Context, scope interfaces.MemoryScope,
) (*types.MemorySubject, error) {
	var subject types.MemorySubject
	err := r.scoped(ctx, scope).First(&subject).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &subject, nil
}

func (r *memoryRepository) EnsureSubject(
	ctx context.Context, scope interfaces.MemoryScope,
) (*types.MemorySubject, error) {
	subject := &types.MemorySubject{
		ID:        uuid.New().String(),
		TenantID:  scope.TenantID,
		SubjectID: scope.SubjectID,
		Enabled:   true,
	}
	// DoNothing plus a re-read keeps concurrent first turns from racing into a
	// unique-violation. The insert is a no-op whenever the row already exists.
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "subject_id"}},
			DoNothing: true,
		}).
		Create(subject).Error
	if err != nil {
		return nil, err
	}
	existing, err := r.GetSubject(ctx, scope)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, errors.New("memory subject vanished after upsert")
	}
	return existing, nil
}

func (r *memoryRepository) UpdateSubjectEnabled(
	ctx context.Context, scope interfaces.MemoryScope, enabled bool,
) error {
	if _, err := r.EnsureSubject(ctx, scope); err != nil {
		return err
	}
	return r.scoped(ctx, scope).
		Model(&types.MemorySubject{}).
		Updates(map[string]interface{}{"enabled": enabled, "updated_at": time.Now()}).Error
}

func (r *memoryRepository) MarkForcedConsolidated(
	ctx context.Context, scope interfaces.MemoryScope,
) error {
	now := time.Now()
	return r.scoped(ctx, scope).
		Model(&types.MemorySubject{}).
		Updates(map[string]interface{}{"forced_consolidated_at": now, "updated_at": now}).Error
}
