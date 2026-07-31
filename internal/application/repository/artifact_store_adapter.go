package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type artifactStoreAdapter struct {
	repo interfaces.ProcessingArtifactRepository
}

// NewArtifactStore adapts the application repository to artifact.Runtime.
func NewArtifactStore(repo interfaces.ProcessingArtifactRepository) artifact.Store {
	return &artifactStoreAdapter{repo: repo}
}

func (s *artifactStoreAdapter) PutIfAbsent(ctx context.Context, record *artifact.Record) (bool, error) {
	return s.repo.PutIfAbsent(ctx, &types.ProcessingArtifact{
		ID:              record.ID,
		TenantID:        record.TenantID,
		Stage:           record.Stage,
		KeyVersion:      record.KeyVersion,
		ArtifactKey:     record.ArtifactKey,
		ProcessorDigest: record.ProcessorDigest,
		OutputDigest:    record.OutputDigest,
		OutputSchema:    record.OutputSchema,
		Codec:           record.Codec,
		Payload:         record.Payload,
		PayloadChecksum: record.PayloadChecksum,
		PayloadSize:     record.PayloadSize,
		CreatedAt:       record.CreatedAt,
		ExpiresAt:       record.ExpiresAt,
	})
}

func (s *artifactStoreAdapter) Get(
	ctx context.Context, tenantID uint64, stage string, keyVersion int, artifactKey string,
) (*artifact.Record, error) {
	item, err := s.repo.Get(ctx, tenantID, stage, keyVersion, artifactKey)
	if errors.Is(err, ErrProcessingArtifactNotFound) {
		return nil, artifact.ErrCacheMiss
	}
	if err != nil {
		return nil, err
	}
	return &artifact.Record{
		ID:              item.ID,
		TenantID:        item.TenantID,
		Stage:           item.Stage,
		KeyVersion:      item.KeyVersion,
		ArtifactKey:     item.ArtifactKey,
		ProcessorDigest: item.ProcessorDigest,
		OutputDigest:    item.OutputDigest,
		OutputSchema:    item.OutputSchema,
		Codec:           item.Codec,
		Payload:         item.Payload,
		PayloadChecksum: item.PayloadChecksum,
		PayloadSize:     item.PayloadSize,
		CreatedAt:       item.CreatedAt,
		ExpiresAt:       item.ExpiresAt,
	}, nil
}

func (s *artifactStoreAdapter) DeleteObservedChecksum(
	ctx context.Context, tenantID uint64, id string, payloadChecksum string,
) (bool, error) {
	return s.repo.DeleteObservedChecksum(ctx, tenantID, id, payloadChecksum)
}
