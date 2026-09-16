package service

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type evaluationCleanupContextObservation struct {
	event       string
	ctx         context.Context
	ctxErr      error
	tenantID    any
	observedAt  time.Time
	deadline    time.Time
	hasDeadline bool
}

type evaluationCleanupContextRecorder struct {
	mu           sync.Mutex
	observations []evaluationCleanupContextObservation
}

func (r *evaluationCleanupContextRecorder) record(ctx context.Context, event string) {
	observedAt := time.Now()
	deadline, hasDeadline := ctx.Deadline()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observations = append(r.observations, evaluationCleanupContextObservation{
		event:       event,
		ctx:         ctx,
		ctxErr:      ctx.Err(),
		tenantID:    ctx.Value(types.TenantIDContextKey),
		observedAt:  observedAt,
		deadline:    deadline,
		hasDeadline: hasDeadline,
	})
}

func (r *evaluationCleanupContextRecorder) snapshot() []evaluationCleanupContextObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]evaluationCleanupContextObservation(nil), r.observations...)
}

func assertEvaluationCleanupContext(t *testing.T, observation evaluationCleanupContextObservation) {
	t.Helper()
	if observation.ctxErr != nil {
		t.Errorf("cleanup context %q error at call = %v, want nil", observation.event, observation.ctxErr)
	}
	if observation.tenantID != uint64(7) {
		t.Errorf("cleanup context %q tenant = %v, want 7", observation.event, observation.tenantID)
	}
	if !observation.hasDeadline {
		t.Errorf("cleanup context %q has no deadline", observation.event)
	} else if remaining := observation.deadline.Sub(observation.observedAt); remaining < 29*time.Second ||
		remaining > evaluationCleanupTimeout {
		t.Errorf(
			"cleanup context %q remaining budget = %v, want 29s to %v",
			observation.event,
			remaining,
			evaluationCleanupTimeout,
		)
	}
	select {
	case <-observation.ctx.Done():
	default:
		t.Errorf("cleanup context %q was not released after use", observation.event)
	}
	if !errors.Is(observation.ctx.Err(), context.Canceled) {
		t.Errorf(
			"cleanup context %q final error = %v, want context.Canceled",
			observation.event,
			observation.ctx.Err(),
		)
	}
}

type evaluationCleanupContextKnowledgeStub struct {
	interfaces.KnowledgeService
	recorder  *evaluationCleanupContextRecorder
	deleteErr error
}

func (s *evaluationCleanupContextKnowledgeStub) CreateKnowledgeFromPassageSync(
	context.Context,
	string,
	[]string,
	string,
) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "evaluation-knowledge"}, nil
}

func (s *evaluationCleanupContextKnowledgeStub) DeleteKnowledge(ctx context.Context, id string) error {
	scope, ok := ctx.Value(knowledgeCleanupKey{}).(knowledgeCleanupScope)
	if !ok || scope.tenant != 7 || scope.bindings[id] != "evaluation-kb" {
		return errors.New("evaluation cleanup lost exact tenant/KB/knowledge scope")
	}
	s.recorder.record(ctx, "knowledge:"+id)
	return s.deleteErr
}

type evaluationCleanupContextKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
	recorder *evaluationCleanupContextRecorder
}

func (s *evaluationCleanupContextKnowledgeBaseStub) DeleteKnowledgeBase(ctx context.Context, id string) error {
	s.recorder.record(ctx, "knowledge-base:"+id)
	return nil
}

type evaluationCleanupContextDatasetStub struct {
	interfaces.DatasetService
}

func (s *evaluationCleanupContextDatasetStub) GetDatasetByID(
	context.Context,
	string,
) ([]*types.QAPair, error) {
	return []*types.QAPair{
		{
			QID:      0,
			Question: "question",
			PIDs:     []int{0},
			Passages: []string{"passage"},
			Answer:   "answer",
		},
	}, nil
}

type evaluationCleanupContextNoopKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
}

func (s *evaluationCleanupContextNoopKnowledgeBaseStub) DeleteKnowledgeBase(context.Context, string) error {
	return nil
}

type evaluationCleanupContextSessionStub struct {
	interfaces.SessionService
}

func (s *evaluationCleanupContextSessionStub) KnowledgeQAByEvent(
	context.Context,
	*types.ChatManage,
	[]types.EventType,
) error {
	return nil
}

func TestEvaluationCleanupUsesBoundedDetachedContexts(t *testing.T) {
	cleanupErr := errors.New("knowledge cleanup failed")
	recorder := &evaluationCleanupContextRecorder{}
	service := &EvaluationService{
		knowledgeService: &evaluationCleanupContextKnowledgeStub{
			recorder:  recorder,
			deleteErr: cleanupErr,
		},
		knowledgeBaseService: &evaluationCleanupContextKnowledgeBaseStub{recorder: recorder},
	}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := service.deleteEvaluationKnowledge(canceledCtx, "evaluation-kb", "evaluation-knowledge"); !errors.Is(
		err,
		cleanupErr,
	) {
		t.Fatalf("deleteEvaluationKnowledge() error = %v, want cleanupErr", err)
	}
	if err := service.deleteEvaluationKnowledgeBase(canceledCtx, "evaluation-kb"); err != nil {
		t.Fatalf("deleteEvaluationKnowledgeBase() error = %v, want nil", err)
	}

	observations := recorder.snapshot()
	gotEvents := make([]string, len(observations))
	for i, observation := range observations {
		gotEvents[i] = observation.event
		assertEvaluationCleanupContext(t, observation)
	}

	wantEvents := []string{
		"knowledge:evaluation-knowledge",
		"knowledge-base:evaluation-kb",
	}
	if !reflect.DeepEqual(gotEvents, wantEvents) {
		t.Fatalf("cleanup events = %v, want %v", gotEvents, wantEvents)
	}
	if len(observations) == 2 && observations[0].ctx == observations[1].ctx {
		t.Fatal("knowledge and knowledge base cleanup shared one context")
	}
}

func TestEvalDatasetUsesBoundedDetachedCleanupContexts(t *testing.T) {
	cleanupErr := errors.New("knowledge cleanup failed")
	recorder := &evaluationCleanupContextRecorder{}
	storage := newEvaluationMemoryStorage()
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        "evaluation-task",
			DatasetID: "dataset",
		},
		Params: &types.ChatManage{PipelineRequest: types.PipelineRequest{
			ChatModelID: "test-chat-model",
		}},
	}
	storage.register(detail)
	service := &EvaluationService{
		dataset: &evaluationCleanupContextDatasetStub{},
		knowledgeService: &evaluationCleanupContextKnowledgeStub{
			recorder:  recorder,
			deleteErr: cleanupErr,
		},
		knowledgeBaseService:     &evaluationCleanupContextNoopKnowledgeBaseStub{},
		sessionService:           &evaluationCleanupContextSessionStub{},
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := service.EvalDataset(canceledCtx, detail, "evaluation-kb"); !errors.Is(err, context.Canceled) {
		t.Fatalf("EvalDataset() error = %v, want context.Canceled", err)
	}

	observations := recorder.snapshot()
	if len(observations) != 1 {
		t.Fatalf("knowledge cleanup observations = %d, want 1", len(observations))
	}
	if observations[0].event != "knowledge:evaluation-knowledge" {
		t.Fatalf("knowledge cleanup event = %q, want knowledge:evaluation-knowledge", observations[0].event)
	}
	assertEvaluationCleanupContext(t, observations[0])
}
