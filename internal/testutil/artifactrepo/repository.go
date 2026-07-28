// Package artifactrepo provides a concurrency-safe in-memory artifact
// repository for model-package tests without importing the application
// repository layer back into those model packages.
package artifactrepo

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
)

type Repository struct {
	mu     sync.Mutex
	values map[types.ProcessingArtifactLookup]*types.ProcessingArtifact
}

func New() *Repository {
	return &Repository{
		values: make(map[types.ProcessingArtifactLookup]*types.ProcessingArtifact),
	}
}

func (r *Repository) Get(
	ctx context.Context,
	key types.ProcessingArtifactLookup,
) (*types.ProcessingArtifact, error) {
	values, err := r.BatchGet(ctx, []types.ProcessingArtifactLookup{key})
	if err != nil {
		return nil, err
	}
	value, found := values[key]
	if !found {
		return nil, types.ErrProcessingArtifactNotFound
	}
	return value, nil
}

func (r *Repository) BatchGet(
	_ context.Context,
	keys []types.ProcessingArtifactLookup,
) (map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, len(keys))
	for _, key := range keys {
		if value := r.values[key]; value != nil {
			result[key] = clone(value)
		}
	}
	return result, nil
}

func (r *Repository) PutIfAbsent(
	_ context.Context,
	candidate *types.ProcessingArtifact,
) (*types.ProcessingArtifact, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := candidate.Lookup()
	if winner := r.values[key]; winner != nil {
		return clone(winner), false, nil
	}
	r.values[key] = clone(candidate)
	return clone(candidate), true, nil
}

func (r *Repository) PutManyIfAbsent(
	ctx context.Context,
	candidates []*types.ProcessingArtifact,
) (map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, error) {
	for _, candidate := range candidates {
		if _, _, err := r.PutIfAbsent(ctx, candidate); err != nil {
			return nil, err
		}
	}
	keys := make([]types.ProcessingArtifactLookup, len(candidates))
	for index, candidate := range candidates {
		keys[index] = candidate.Lookup()
	}
	return r.BatchGet(ctx, keys)
}

func (r *Repository) DeleteCorrupt(
	_ context.Context,
	key types.ProcessingArtifactLookup,
	observedChecksum string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if value := r.values[key]; value != nil && value.PayloadChecksum == observedChecksum {
		delete(r.values, key)
	}
	return nil
}

func (r *Repository) TouchHits(context.Context, []types.ProcessingArtifactLookup) error {
	return nil
}

func (r *Repository) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.values)
}

func clone(value *types.ProcessingArtifact) *types.ProcessingArtifact {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Payload = append([]byte(nil), value.Payload...)
	return &copy
}
