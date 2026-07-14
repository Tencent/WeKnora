package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

type orphanKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
	err       error
}

func (r *orphanKnowledgeRepo) GetKnowledgeByIDOnly(_ context.Context, _ string) (*types.Knowledge, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.knowledge, nil
}

type orphanKBService struct {
	interfaces.KnowledgeBaseService
	kb  *types.KnowledgeBase
	err error
}

func (s *orphanKBService) GetKnowledgeBaseByIDOnly(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.kb, nil
}

func TestShouldDropOrphanedMultimodal(t *testing.T) {
	t.Parallel()
	svc := &ImageMultimodalService{}

	drop, err := svc.shouldDropOrphanedMultimodal(context.Background(), &types.ImageMultimodalPayload{
		KnowledgeID: "missing",
	})
	if err != nil || drop {
		t.Fatalf("nil repo should not drop: drop=%v err=%v", drop, err)
	}

	svc.knowledgeRepo = &orphanKnowledgeRepo{err: repository.ErrKnowledgeNotFound}
	drop, err = svc.shouldDropOrphanedMultimodal(context.Background(), &types.ImageMultimodalPayload{
		KnowledgeID: "missing",
	})
	if err != nil || !drop {
		t.Fatalf("missing knowledge should drop: drop=%v err=%v", drop, err)
	}

	svc.knowledgeRepo = &orphanKnowledgeRepo{knowledge: &types.Knowledge{ParseStatus: types.ParseStatusCancelled}}
	drop, err = svc.shouldDropOrphanedMultimodal(context.Background(), &types.ImageMultimodalPayload{
		KnowledgeID: "cancelled",
	})
	if err != nil || !drop {
		t.Fatalf("cancelled knowledge should drop: drop=%v err=%v", drop, err)
	}

	svc.knowledgeRepo = &orphanKnowledgeRepo{knowledge: &types.Knowledge{ParseStatus: types.ParseStatusProcessing}}
	svc.kbService = &orphanKBService{err: repository.ErrKnowledgeBaseNotFound}
	drop, err = svc.shouldDropOrphanedMultimodal(context.Background(), &types.ImageMultimodalPayload{
		KnowledgeID:     "live",
		KnowledgeBaseID: "missing-kb",
	})
	if err != nil || !drop {
		t.Fatalf("missing kb should drop: drop=%v err=%v", drop, err)
	}
}

func TestImageMultimodalHandleDropsMissingKnowledge(t *testing.T) {
	t.Parallel()
	svc := &ImageMultimodalService{
		knowledgeRepo: &orphanKnowledgeRepo{err: repository.ErrKnowledgeNotFound},
		kbService:     &orphanKBService{kb: &types.KnowledgeBase{ID: "kb-1"}},
	}
	payload, err := json.Marshal(types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "missing",
		KnowledgeBaseID: "kb-1",
		ImageURL:        "minio://bucket/img.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Handle(context.Background(), asynq.NewTask(types.TypeImageMultimodal, payload)); err != nil {
		t.Fatalf("orphan task should succeed without retry: %v", err)
	}
}

func TestImageMultimodalHandlePropagatesTransientKnowledgeError(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db unavailable")
	svc := &ImageMultimodalService{
		knowledgeRepo: &orphanKnowledgeRepo{err: dbErr},
		kbService:     &orphanKBService{kb: &types.KnowledgeBase{ID: "kb-1"}},
	}
	payload, err := json.Marshal(types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Handle(context.Background(), asynq.NewTask(types.TypeImageMultimodal, payload)); !errors.Is(err, dbErr) {
		t.Fatalf("transient error should propagate: %v", err)
	}
}
