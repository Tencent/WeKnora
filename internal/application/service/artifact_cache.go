package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const artifactLeaseDuration = 45 * time.Minute

// ArtifactCacheSpec contains every value that can influence a cached output.
// Callers should hash final rendered prompts/configs instead of relying on a
// manually maintained version string alone.
type ArtifactCacheSpec struct {
	TenantID          uint64
	Kind              types.ProcessingArtifactKind
	InputHash         string
	ModelFingerprint  string
	PromptFingerprint string
	ConfigFingerprint string
	SchemaVersion     string
}

func (s ArtifactCacheSpec) CacheKey() string {
	return hashFingerprint(
		string(s.Kind),
		s.SchemaVersion,
		s.InputHash,
		s.ModelFingerprint,
		s.PromptFingerprint,
		s.ConfigFingerprint,
	)
}

// ArtifactCache coordinates durable, cross-worker computation reuse. A nil
// repository degrades to direct computation for small unit-test harnesses.
type ArtifactCache struct {
	repo      interfaces.ProcessingArtifactRepository
	instance  string
	leaseTime time.Duration
}

func (c *ArtifactCache) acquire(
	ctx context.Context,
	spec ArtifactCacheSpec,
) (*types.ProcessingArtifact, bool, error) {
	if c == nil || c.repo == nil || spec.TenantID == 0 {
		return nil, true, nil
	}
	if spec.SchemaVersion == "" {
		spec.SchemaVersion = "v1"
	}
	now := time.Now().UTC()
	candidate := &types.ProcessingArtifact{
		TenantID:          spec.TenantID,
		Kind:              spec.Kind,
		CacheKey:          spec.CacheKey(),
		InputHash:         spec.InputHash,
		ModelFingerprint:  spec.ModelFingerprint,
		PromptFingerprint: spec.PromptFingerprint,
		ConfigFingerprint: spec.ConfigFingerprint,
		SchemaVersion:     spec.SchemaVersion,
	}
	return c.repo.Acquire(ctx, candidate, c.instance, now.Add(c.leaseTime))
}

func (c *ArtifactCache) markReadyJSON(
	ctx context.Context,
	artifact *types.ProcessingArtifact,
	value any,
) error {
	if c == nil || c.repo == nil || artifact == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	artifact.ResultJSON = types.JSON(encoded)
	artifact.ResultSize = int64(len(encoded))
	artifact.LeaseOwner = c.instance
	return c.repo.MarkReady(ctx, artifact)
}

func (c *ArtifactCache) markFailed(
	ctx context.Context,
	artifact *types.ProcessingArtifact,
	err error,
) {
	if c == nil || c.repo == nil || artifact == nil || err == nil {
		return
	}
	_ = c.repo.MarkFailed(ctx, artifact.ID, c.instance, err.Error())
}

func (c *ArtifactCache) decodeJSON(
	ctx context.Context,
	artifact *types.ProcessingArtifact,
	result any,
) error {
	if artifact == nil {
		return errors.New("processing artifact is nil")
	}
	if err := json.Unmarshal(artifact.ResultJSON, result); err != nil {
		return err
	}
	if c != nil && c.repo != nil {
		_ = c.repo.TouchHit(ctx, artifact.ID, time.Now().UTC())
	}
	return nil
}

func NewArtifactCache(repo interfaces.ProcessingArtifactRepository) *ArtifactCache {
	return &ArtifactCache{
		repo:      repo,
		instance:  uuid.NewString(),
		leaseTime: artifactLeaseDuration,
	}
}

// GetOrComputeJSON returns hit=true only when a previously ready artifact was
// reused. result must be a non-nil pointer. A successful empty result is still
// persisted, which is important for negative OCR results.
func (c *ArtifactCache) GetOrComputeJSON(
	ctx context.Context,
	spec ArtifactCacheSpec,
	result any,
	compute func() (any, error),
) (bool, error) {
	if result == nil {
		return false, errors.New("artifact cache result must not be nil")
	}
	if c == nil || c.repo == nil || spec.TenantID == 0 {
		value, err := compute()
		if err != nil {
			return false, err
		}
		return false, copyArtifactResult(value, result)
	}
	if spec.SchemaVersion == "" {
		spec.SchemaVersion = "v1"
	}

	cacheKey := spec.CacheKey()
	for {
		artifact, acquired, err := c.acquire(ctx, spec)
		if err != nil {
			return false, err
		}
		if artifact != nil && artifact.Status == types.ProcessingArtifactReady {
			if err := c.decodeJSON(ctx, artifact, result); err != nil {
				return false, fmt.Errorf("decode cached %s artifact: %w", spec.Kind, err)
			}
			logger.Infof(ctx, "processing artifact cache hit: kind=%s key=%s", spec.Kind, cacheKeyPrefix(cacheKey))
			return true, nil
		}
		if acquired {
			value, computeErr := compute()
			if computeErr != nil {
				c.markFailed(ctx, artifact, computeErr)
				return false, computeErr
			}
			if err := c.markReadyJSON(ctx, artifact, value); err != nil {
				c.markFailed(ctx, artifact, err)
				return false, err
			}
			logger.Infof(ctx, "processing artifact cache miss: kind=%s key=%s", spec.Kind, cacheKeyPrefix(cacheKey))
			return false, copyArtifactResult(value, result)
		}

		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func copyArtifactResult(value any, result any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, result)
}

func cacheKeyPrefix(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:12]
}
