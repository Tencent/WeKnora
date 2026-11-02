package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// ReparseBatchService is an optional extension consumed by the HTTP handler.
// It does not widen KnowledgeService, preserving narrow existing test doubles.
type ReparseBatchService interface {
	CreateReparseBatch(context.Context, uint64, string, string, string, []string) (*types.ReparseBatch, error)
	GetReparseBatch(context.Context, uint64, string) (*types.ReparseBatch, error)
	FindTrackingReparseBatch(context.Context, uint64, string, string) (*types.ReparseBatch, error)
	ListReparseBatches(context.Context, uint64, string, int, int) ([]*types.ReparseBatch, int64, error)
	ListReparseBatchMembers(context.Context, uint64, string, int, int) ([]*types.ReparseBatchMember, int64, error)
	RecordReparseMember(context.Context, string, string, string, string) error
	GetReparseBatchView(context.Context, uint64, string) (*types.ReparseBatchView, error)
	ListReparseBatchMemberViews(
		context.Context, uint64, string, string, int, int,
	) ([]*types.ReparseBatchMemberView, int64, error)
	ListReparseBatchViews(context.Context, uint64, string, int, int) ([]*types.ReparseBatchView, int64, error)
	ListReparseBatchViewsByIDs(context.Context, uint64, []string) ([]*types.ReparseBatchView, error)
}

func (s *knowledgeService) reparseBatches() (repository.ReparseBatchStore, error) {
	p, ok := s.repo.(interface {
		ReparseBatchStore() repository.ReparseBatchStore
	})
	if !ok {
		return nil, fmt.Errorf("reparse batch persistence is unavailable")
	}
	return p.ReparseBatchStore(), nil
}

func canonicalReparseManifest(ids []string) string {
	seen, out := make(map[string]struct{}, len(ids)), make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	sum := sha256.Sum256([]byte(strings.Join(out, "\n") + "\n"))
	return hex.EncodeToString(sum[:])
}

func (s *knowledgeService) CreateReparseBatch(
	ctx context.Context, tenant uint64, kb, mode, name string, ids []string,
) (*types.ReparseBatch, error) {
	store, err := s.reparseBatches()
	if err != nil {
		return nil, err
	}
	b := &types.ReparseBatch{
		ID: uuid.NewString(), TenantID: tenant, KnowledgeBaseID: kb, Mode: mode,
		Name: name, ManifestSHA256: canonicalReparseManifest(ids),
	}
	if err := store.CreateReparseBatch(ctx, b, ids); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *knowledgeService) GetReparseBatch(ctx context.Context, tenant uint64, id string) (*types.ReparseBatch, error) {
	store, err := s.reparseBatches()
	if err != nil {
		return nil, err
	}
	return store.GetReparseBatch(ctx, tenant, id)
}

func (s *knowledgeService) FindTrackingReparseBatch(
	ctx context.Context, tenant uint64, kb, manifest string,
) (*types.ReparseBatch, error) {
	store, err := s.reparseBatches()
	if err != nil {
		return nil, err
	}
	return store.FindTrackingReparseBatch(ctx, tenant, kb, manifest)
}

func (s *knowledgeService) ListReparseBatches(
	ctx context.Context, tenant uint64, kb string, offset, limit int,
) ([]*types.ReparseBatch, int64, error) {
	store, err := s.reparseBatches()
	if err != nil {
		return nil, 0, err
	}
	return store.ListReparseBatches(ctx, tenant, kb, offset, limit)
}

func (s *knowledgeService) ListReparseBatchMembers(
	ctx context.Context, tenant uint64, id string, offset, limit int,
) ([]*types.ReparseBatchMember, int64, error) {
	store, err := s.reparseBatches()
	if err != nil {
		return nil, 0, err
	}
	if _, err = store.GetReparseBatch(ctx, tenant, id); err != nil {
		return nil, 0, err
	}
	return store.ListReparseBatchMembers(ctx, id, offset, limit)
}

func (s *knowledgeService) RecordReparseMember(ctx context.Context, batchID, knowledgeID, state, text string) error {
	store, err := s.reparseBatches()
	if err != nil {
		return err
	}
	return store.SetReparseMemberState(ctx, batchID, knowledgeID, state, text)
}

func (s *knowledgeService) GetReparseBatchView(
	ctx context.Context, tenant uint64, id string,
) (*types.ReparseBatchView, error) {
	store, err := s.reparseBatches()
	if err != nil {
		return nil, err
	}
	return store.GetReparseBatchView(ctx, tenant, id)
}

func (s *knowledgeService) ListReparseBatchMemberViews(
	ctx context.Context, tenant uint64, id, state string, offset, limit int,
) ([]*types.ReparseBatchMemberView, int64, error) {
	store, err := s.reparseBatches()
	if err != nil {
		return nil, 0, err
	}
	return store.ListReparseBatchMemberViews(ctx, tenant, id, state, offset, limit)
}

func (s *knowledgeService) ListReparseBatchViews(
	ctx context.Context, tenant uint64, kb string, offset, limit int,
) ([]*types.ReparseBatchView, int64, error) {
	store, err := s.reparseBatches()
	if err != nil {
		return nil, 0, err
	}
	return store.ListReparseBatchViews(ctx, tenant, kb, offset, limit)
}

func (s *knowledgeService) ListReparseBatchViewsByIDs(
	ctx context.Context, tenant uint64, ids []string,
) ([]*types.ReparseBatchView, error) {
	store, err := s.reparseBatches()
	if err != nil {
		return nil, err
	}
	return store.ListReparseBatchViewsByIDs(ctx, tenant, ids)
}
