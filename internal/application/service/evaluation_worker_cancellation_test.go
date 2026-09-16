package service

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type evaluationCancellationDatasetStub struct {
	interfaces.DatasetService
	dataset []*types.QAPair
}

func (s *evaluationCancellationDatasetStub) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	return s.dataset, nil
}

type evaluationCancellationCleanupRecorder struct {
	mu      sync.Mutex
	events  []string
	ctxErrs []error
}

func (r *evaluationCancellationCleanupRecorder) record(ctx context.Context, event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	r.ctxErrs = append(r.ctxErrs, ctx.Err())
}

func (r *evaluationCancellationCleanupRecorder) snapshot() ([]string, []error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...), append([]error(nil), r.ctxErrs...)
}

type evaluationCancellationKnowledgeStub struct {
	interfaces.KnowledgeService
	recorder *evaluationCancellationCleanupRecorder
}

func (s *evaluationCancellationKnowledgeStub) CreateKnowledgeFromPassageSync(
	context.Context,
	string,
	[]string,
	string,
) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "evaluation-knowledge"}, nil
}

func (s *evaluationCancellationKnowledgeStub) DeleteKnowledge(ctx context.Context, id string) error {
	s.recorder.record(ctx, "knowledge:"+id)
	return nil
}

type evaluationCancellationSessionStub struct {
	interfaces.SessionService
	blockedEntered  chan struct{}
	blockedCanceled chan struct{}
	queuedCalled    chan struct{}
	safetyRelease   <-chan struct{}
	firstErr        error
	enteredOnce     sync.Once
	canceledOnce    sync.Once
	queuedOnce      sync.Once
}

func (s *evaluationCancellationSessionStub) KnowledgeQAByEvent(
	ctx context.Context,
	chatManage *types.ChatManage,
	_ []types.EventType,
) error {
	switch chatManage.Query {
	case "failing-question":
		<-s.blockedEntered
		return s.firstErr
	case "queued-question":
		s.queuedOnce.Do(func() {
			close(s.queuedCalled)
		})
		return nil
	}

	s.enteredOnce.Do(func() {
		close(s.blockedEntered)
	})
	select {
	case <-ctx.Done():
		s.canceledOnce.Do(func() {
			close(s.blockedCanceled)
		})
		return ctx.Err()
	case <-s.safetyRelease:
		return nil
	}
}

func TestEvalDatasetCancelsBlockedWorkerAfterFirstError(t *testing.T) {
	previousMaxProcs := runtime.GOMAXPROCS(3)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(previousMaxProcs)
	})

	dataset := []*types.QAPair{
		{
			QID:      0,
			Question: "blocking-question",
			PIDs:     []int{0},
			Passages: []string{"blocking-passage"},
		},
		{
			QID:      1,
			Question: "failing-question",
			PIDs:     []int{1},
			Passages: []string{"failing-passage"},
		},
		{
			QID:      2,
			Question: "queued-question",
			PIDs:     []int{2},
			Passages: []string{"queued-passage"},
		},
	}
	firstErr := errors.New("first worker failed")
	blockedEntered := make(chan struct{})
	blockedCanceled := make(chan struct{})
	queuedCalled := make(chan struct{})
	safetyRelease := make(chan struct{})
	recorder := &evaluationCancellationCleanupRecorder{}
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
		dataset:          &evaluationCancellationDatasetStub{dataset: dataset},
		knowledgeService: &evaluationCancellationKnowledgeStub{recorder: recorder},
		sessionService: &evaluationCancellationSessionStub{
			blockedEntered:  blockedEntered,
			blockedCanceled: blockedCanceled,
			queuedCalled:    queuedCalled,
			safetyRelease:   safetyRelease,
			firstErr:        firstErr,
		},
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	result := make(chan error, 1)
	go func() {
		ctx := types.WithExecutionTenant(context.Background(), detail.Task.TenantID)
		result <- service.EvalDataset(ctx, detail, "evaluation-kb")
	}()

	cancellationObserved := false
	select {
	case <-blockedCanceled:
		cancellationObserved = true
	case <-time.After(2 * time.Second):
		close(safetyRelease)
	}

	var resultErr error
	select {
	case resultErr = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for evaluation workers to stop")
	}
	if !cancellationObserved {
		t.Fatal("first worker error did not cancel the blocked worker")
	}
	if !errors.Is(resultErr, firstErr) {
		t.Fatalf("EvalDataset() error = %v, want errors.Is(error, firstErr)", resultErr)
	}
	select {
	case <-queuedCalled:
		t.Fatal("queued worker entered the session after cancellation")
	default:
	}

	events, cleanupErrs := recorder.snapshot()
	wantEvents := []string{"knowledge:evaluation-knowledge"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("cleanup events = %v, want %v", events, wantEvents)
	}
	if !reflect.DeepEqual(cleanupErrs, []error{nil}) {
		t.Fatalf("cleanup context errors = %v, want no errors", cleanupErrs)
	}
}
