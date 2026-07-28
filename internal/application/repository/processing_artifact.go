package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const processingArtifactBatchSize = 500

type processingArtifactRepository struct {
	db *gorm.DB
}

func NewProcessingArtifactRepository(db *gorm.DB) interfaces.ProcessingArtifactRepository {
	return &processingArtifactRepository{db: db}
}

func (r *processingArtifactRepository) Get(
	ctx context.Context,
	key types.ProcessingArtifactLookup,
) (*types.ProcessingArtifact, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	var artifact types.ProcessingArtifact
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND stage = ? AND key_version = ? AND artifact_key = ?",
			key.TenantID,
			key.Stage,
			key.KeyVersion,
			key.ArtifactKey,
		).
		Take(&artifact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, types.ErrProcessingArtifactNotFound
	}
	if err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (r *processingArtifactRepository) BatchGet(
	ctx context.Context,
	keys []types.ProcessingArtifactLookup,
) (map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, error) {
	result := make(map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, len(keys))
	groups, err := groupProcessingArtifactKeys(keys)
	if err != nil {
		return nil, err
	}

	for group, artifactKeys := range groups {
		for start := 0; start < len(artifactKeys); start += processingArtifactBatchSize {
			end := min(start+processingArtifactBatchSize, len(artifactKeys))
			var artifacts []*types.ProcessingArtifact
			err := r.db.WithContext(ctx).
				Where(
					"tenant_id = ? AND stage = ? AND key_version = ?",
					group.tenantID,
					group.stage,
					group.keyVersion,
				).
				Where("artifact_key IN ?", artifactKeys[start:end]).
				Find(&artifacts).Error
			if err != nil {
				return nil, err
			}
			for _, artifact := range artifacts {
				result[artifact.Lookup()] = artifact
			}
		}
	}
	return result, nil
}

func (r *processingArtifactRepository) PutIfAbsent(
	ctx context.Context,
	candidate *types.ProcessingArtifact,
) (*types.ProcessingArtifact, bool, error) {
	if candidate == nil {
		return nil, false, errors.New("processing artifact candidate must not be nil")
	}
	if err := candidate.Validate(); err != nil {
		return nil, false, err
	}

	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "stage"},
			{Name: "key_version"},
			{Name: "artifact_key"},
		},
		DoNothing: true,
	}).Create(candidate)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return candidate, true, nil
	}

	winner, err := r.Get(ctx, candidate.Lookup())
	if err != nil {
		return nil, false, fmt.Errorf("load processing artifact conflict winner: %w", err)
	}
	return winner, false, nil
}

func (r *processingArtifactRepository) PutManyIfAbsent(
	ctx context.Context,
	candidates []*types.ProcessingArtifact,
) (map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, error) {
	if len(candidates) == 0 {
		return map[types.ProcessingArtifactLookup]*types.ProcessingArtifact{}, nil
	}
	keys := make([]types.ProcessingArtifactLookup, 0, len(candidates))
	unique := make(map[types.ProcessingArtifactLookup]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			return nil, errors.New("processing artifact candidate must not be nil")
		}
		if err := candidate.Validate(); err != nil {
			return nil, err
		}
		key := candidate.Lookup()
		if _, exists := unique[key]; exists {
			return nil, fmt.Errorf("duplicate processing artifact candidate %v", key)
		}
		unique[key] = struct{}{}
		keys = append(keys, key)
	}

	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "stage"},
			{Name: "key_version"},
			{Name: "artifact_key"},
		},
		DoNothing: true,
	}).CreateInBatches(candidates, processingArtifactBatchSize).Error
	if err != nil {
		return nil, err
	}
	return r.BatchGet(ctx, keys)
}

func (r *processingArtifactRepository) DeleteCorrupt(
	ctx context.Context,
	key types.ProcessingArtifactLookup,
	observedChecksum string,
) error {
	if err := key.Validate(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND stage = ? AND key_version = ? AND artifact_key = ? AND payload_checksum = ?",
			key.TenantID,
			key.Stage,
			key.KeyVersion,
			key.ArtifactKey,
			observedChecksum,
		).
		Delete(&types.ProcessingArtifact{}).Error
}

func (r *processingArtifactRepository) TouchHits(
	ctx context.Context,
	keys []types.ProcessingArtifactLookup,
) error {
	groups, err := groupProcessingArtifactKeys(keys)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for group, artifactKeys := range groups {
		for start := 0; start < len(artifactKeys); start += processingArtifactBatchSize {
			end := min(start+processingArtifactBatchSize, len(artifactKeys))
			err := r.db.WithContext(ctx).
				Model(&types.ProcessingArtifact{}).
				Where(
					"tenant_id = ? AND stage = ? AND key_version = ?",
					group.tenantID,
					group.stage,
					group.keyVersion,
				).
				Where("artifact_key IN ?", artifactKeys[start:end]).
				Updates(map[string]any{
					"hit_count":   gorm.Expr("hit_count + 1"),
					"last_hit_at": now,
				}).Error
			if err != nil {
				return err
			}
		}
	}
	return nil
}

type processingArtifactKeyGroup struct {
	tenantID   uint64
	stage      string
	keyVersion uint16
}

func groupProcessingArtifactKeys(
	keys []types.ProcessingArtifactLookup,
) (map[processingArtifactKeyGroup][]string, error) {
	sets := make(map[processingArtifactKeyGroup]map[string]struct{})
	for _, key := range keys {
		if err := key.Validate(); err != nil {
			return nil, err
		}
		group := processingArtifactKeyGroup{
			tenantID:   key.TenantID,
			stage:      key.Stage,
			keyVersion: key.KeyVersion,
		}
		if sets[group] == nil {
			sets[group] = make(map[string]struct{})
		}
		sets[group][key.ArtifactKey] = struct{}{}
	}

	groups := make(map[processingArtifactKeyGroup][]string, len(sets))
	for group, set := range sets {
		values := make([]string, 0, len(set))
		for value := range set {
			values = append(values, value)
		}
		sort.Strings(values)
		groups[group] = values
	}
	return groups, nil
}
