package service

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PR 5 (#1303) — service-layer chain helper for the per-KB ownership
// lookups. The handler-level adapters in internal/handler/rbac_lookups.go
// translate this method's repository sentinels into
// middleware.ErrResourceNotFound; this file pins the contract on the
// service side so a future refactor can't quietly drop a sentinel.

// stubKnowledgeRepoForChain implements just GetKnowledgeByID; everything
// else panics so a test that reaches outside the contract fails loudly.
type stubKnowledgeRepoForChain struct {
	interfaces.KnowledgeRepository
	getByID func(ctx context.Context, tenantID uint64, id string) (*types.Knowledge, error)
}

func (s *stubKnowledgeRepoForChain) GetKnowledgeByID(
	ctx context.Context, tenantID uint64, id string,
) (*types.Knowledge, error) {
	return s.getByID(ctx, tenantID, id)
}

// stubKBServiceForChain mirrors the handler-side stub but lives in the
// service test package — embedding the interface keeps untouched
// methods nil-panicky on purpose.
type stubKBServiceForChain struct {
	interfaces.KnowledgeBaseService
	getByID func(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

func (s *stubKBServiceForChain) GetKnowledgeBaseByID(
	ctx context.Context, id string,
) (*types.KnowledgeBase, error) {
	return s.getByID(ctx, id)
}

func newKnowledgeServiceForChain(
	repo interfaces.KnowledgeRepository, kb interfaces.KnowledgeBaseService,
) *knowledgeService {
	return &knowledgeService{repo: repo, kbService: kb}
}

func chainCtx(tenantID uint64) context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
}

func TestGetOwningKBCreatorID_KnowledgeNotFound(t *testing.T) {
	// Repository sentinel passes through unchanged so the handler-level
	// translator can map it to middleware.ErrResourceNotFound.
	s := newKnowledgeServiceForChain(
		&stubKnowledgeRepoForChain{
			getByID: func(_ context.Context, _ uint64, _ string) (*types.Knowledge, error) {
				return nil, repository.ErrKnowledgeNotFound
			},
		},
		&stubKBServiceForChain{
			getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
				t.Fatalf("KB service must not be called when knowledge lookup fails")
				return nil, nil
			},
		},
	)
	_, err := s.GetOwningKBCreatorID(chainCtx(1), "kn-missing")
	if !stderrors.Is(err, repository.ErrKnowledgeNotFound) {
		t.Fatalf("expected ErrKnowledgeNotFound, got %v", err)
	}
}

func TestGetOwningKBCreatorID_KBNotFound(t *testing.T) {
	// Knowledge resolves but its KB has been deleted (or was never
	// created — defence-in-depth). The KB sentinel must surface so the
	// handler can render 404 rather than 503/500.
	s := newKnowledgeServiceForChain(
		&stubKnowledgeRepoForChain{
			getByID: func(_ context.Context, _ uint64, _ string) (*types.Knowledge, error) {
				return &types.Knowledge{ID: "kn-1", KnowledgeBaseID: "kb-missing"}, nil
			},
		},
		&stubKBServiceForChain{
			getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
				return nil, repository.ErrKnowledgeBaseNotFound
			},
		},
	)
	_, err := s.GetOwningKBCreatorID(chainCtx(1), "kn-1")
	if !stderrors.Is(err, repository.ErrKnowledgeBaseNotFound) {
		t.Fatalf("expected ErrKnowledgeBaseNotFound, got %v", err)
	}
}

func TestGetOwningKBCreatorID_KBNilWithoutErrSurfacesAsNotFound(t *testing.T) {
	// Some KB-service implementations return (nil, nil) for "not found"
	// instead of a sentinel error. The chain must treat that as
	// ErrKnowledgeBaseNotFound rather than dereferencing nil.
	s := newKnowledgeServiceForChain(
		&stubKnowledgeRepoForChain{
			getByID: func(_ context.Context, _ uint64, _ string) (*types.Knowledge, error) {
				return &types.Knowledge{ID: "kn-1", KnowledgeBaseID: "kb-1"}, nil
			},
		},
		&stubKBServiceForChain{
			getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
				return nil, nil
			},
		},
	)
	_, err := s.GetOwningKBCreatorID(chainCtx(1), "kn-1")
	if !stderrors.Is(err, repository.ErrKnowledgeBaseNotFound) {
		t.Fatalf("nil KB without error must surface as ErrKnowledgeBaseNotFound, got %v", err)
	}
}

func TestGetOwningKBCreatorID_HappyPathReturnsKBCreator(t *testing.T) {
	s := newKnowledgeServiceForChain(
		&stubKnowledgeRepoForChain{
			getByID: func(_ context.Context, tenantID uint64, _ string) (*types.Knowledge, error) {
				if tenantID != 1 {
					t.Fatalf("expected tenant context to flow into repo (got %d)", tenantID)
				}
				return &types.Knowledge{ID: "kn-1", KnowledgeBaseID: "kb-1"}, nil
			},
		},
		&stubKBServiceForChain{
			getByID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
				if id != "kb-1" {
					t.Fatalf("KB lookup must use the knowledge's KnowledgeBaseID, got %q", id)
				}
				return &types.KnowledgeBase{ID: "kb-1", TenantID: 1, CreatorID: "u-creator"}, nil
			},
		},
	)
	creator, err := s.GetOwningKBCreatorID(chainCtx(1), "kn-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creator != "u-creator" {
		t.Fatalf("expected creator=u-creator, got %q", creator)
	}
}

func TestGetOwningKBCreatorID_LegacyKBHasEmptyCreatorID(t *testing.T) {
	// Legacy KBs created before PR 1 have CreatorID == ""; the chain
	// helper must surface that empty string unchanged so the
	// middleware's "" -> tenant-owned branch fires (Admin+ only).
	s := newKnowledgeServiceForChain(
		&stubKnowledgeRepoForChain{
			getByID: func(_ context.Context, _ uint64, _ string) (*types.Knowledge, error) {
				return &types.Knowledge{ID: "kn-1", KnowledgeBaseID: "kb-legacy"}, nil
			},
		},
		&stubKBServiceForChain{
			getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
				return &types.KnowledgeBase{ID: "kb-legacy", TenantID: 1, CreatorID: ""}, nil
			},
		},
	)
	creator, err := s.GetOwningKBCreatorID(chainCtx(1), "kn-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creator != "" {
		t.Fatalf("legacy KB must surface empty creator id, got %q", creator)
	}
}
