package service

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type evaluationCleanupRecorder struct {
	mu        sync.Mutex
	events    []string
	kbDeleted chan string
}

func newEvaluationCleanupRecorder() *evaluationCleanupRecorder {
	return &evaluationCleanupRecorder{kbDeleted: make(chan string, 4)}
}

func (r *evaluationCleanupRecorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *evaluationCleanupRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

type evaluationCleanupKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
	recorder *evaluationCleanupRecorder
}

func (s *evaluationCleanupKnowledgeBaseStub) GetKnowledgeBaseByID(
	context.Context,
	string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID:               "source-kb",
		EmbeddingModelID: "embedding-model",
		SummaryModelID:   "summary-model",
	}, nil
}

func (s *evaluationCleanupKnowledgeBaseStub) CreateKnowledgeBase(
	_ context.Context,
	knowledgeBase *types.KnowledgeBase,
) (*types.KnowledgeBase, error) {
	created := *knowledgeBase
	created.ID = "evaluation-kb"
	return &created, nil
}

func (s *evaluationCleanupKnowledgeBaseStub) DeleteKnowledgeBase(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.recorder.record("knowledge-base:" + id)
	s.recorder.kbDeleted <- id
	return nil
}

type evaluationCleanupModelStub struct {
	interfaces.ModelService
}

func (s *evaluationCleanupModelStub) ListModels(context.Context) ([]*types.Model, error) {
	return nil, nil
}

type evaluationCleanupDatasetStub struct {
	interfaces.DatasetService
	dataset []*types.QAPair
	err     error
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (s *evaluationCleanupDatasetStub) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	if s.entered != nil {
		s.once.Do(func() {
			close(s.entered)
		})
		<-s.release
	}
	return s.dataset, s.err
}

type evaluationCleanupKnowledgeStub struct {
	interfaces.KnowledgeService
	recorder  *evaluationCleanupRecorder
	createErr error
}

func (s *evaluationCleanupKnowledgeStub) CreateKnowledgeFromPassageSync(
	context.Context,
	string,
	[]string,
	string,
) (*types.Knowledge, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &types.Knowledge{ID: "evaluation-knowledge"}, nil
}

func (s *evaluationCleanupKnowledgeStub) DeleteKnowledge(_ context.Context, id string) error {
	s.recorder.record("knowledge:" + id)
	return nil
}

type evaluationCleanupSessionStub struct {
	interfaces.SessionService
	err error
}

func (s *evaluationCleanupSessionStub) KnowledgeQAByEvent(
	context.Context,
	*types.ChatManage,
	[]types.EventType,
) error {
	return s.err
}

func TestEvaluationServiceCleansTemporaryKnowledgeBaseOnPreparationFailure(t *testing.T) {
	tests := []struct {
		name   string
		cancel bool
	}{
		{name: "active request context"},
		{name: "canceled request context", cancel: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := newEvaluationCleanupRecorder()
			service := &EvaluationService{
				knowledgeBaseService:     &evaluationCleanupKnowledgeBaseStub{recorder: recorder},
				modelService:             &evaluationCleanupModelStub{},
				evaluationTaskRepository: newEvaluationMemoryStorage(),
				ownerID:                  evaluationMemoryStorageOwnerID,
			}
			ctx := context.WithValue(
				context.Background(),
				types.TenantIDContextKey,
				uint64(7),
			)
			if test.cancel {
				canceledCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceledCtx
			}

			_, err := service.Evaluation(ctx, "dataset", "source-kb", "", "rerank-model")
			if err == nil || err.Error() != "no default chat model found" {
				t.Fatalf("Evaluation() error = %v, want no default chat model found", err)
			}

			want := []string{"knowledge-base:evaluation-kb"}
			if got := recorder.snapshot(); !reflect.DeepEqual(got, want) {
				t.Fatalf("cleanup events = %v, want %v", got, want)
			}
		})
	}
}

func TestEvaluationServiceCleansTemporaryResourcesAfterBackgroundRun(t *testing.T) {
	datasetErr := errors.New("dataset unavailable")
	knowledgeErr := errors.New("knowledge creation failed")
	workerErr := errors.New("worker failed")
	dataset := []*types.QAPair{
		{
			QID:      0,
			Question: "question",
			PIDs:     []int{0},
			Passages: []string{"passage"},
			Answer:   "answer",
		},
	}

	tests := []struct {
		name       string
		datasetErr error
		knowledge  error
		worker     error
		status     types.EvaluationStatue
		errMsg     string
		events     []string
	}{
		{
			name:       "dataset load failure",
			datasetErr: datasetErr,
			status:     types.EvaluationStatueFailed,
			errMsg:     datasetErr.Error(),
			events:     []string{"knowledge-base:evaluation-kb"},
		},
		{
			name:      "knowledge creation failure",
			knowledge: knowledgeErr,
			status:    types.EvaluationStatueFailed,
			errMsg:    knowledgeErr.Error(),
			events:    []string{"knowledge-base:evaluation-kb"},
		},
		{
			name:   "worker failure",
			worker: workerErr,
			status: types.EvaluationStatueFailed,
			errMsg: workerErr.Error(),
			events: []string{
				"knowledge:evaluation-knowledge",
				"knowledge-base:evaluation-kb",
			},
		},
		{
			name:   "success",
			status: types.EvaluationStatueSuccess,
			events: []string{
				"knowledge:evaluation-knowledge",
				"knowledge-base:evaluation-kb",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := newEvaluationCleanupRecorder()
			datasetEntered := make(chan struct{})
			datasetRelease := make(chan struct{})
			service := &EvaluationService{
				config: &config.Config{
					Conversation: &config.ConversationConfig{
						Summary: &config.SummaryConfig{},
					},
				},
				dataset: &evaluationCleanupDatasetStub{
					dataset: dataset,
					err:     test.datasetErr,
					entered: datasetEntered,
					release: datasetRelease,
				},
				knowledgeBaseService: &evaluationCleanupKnowledgeBaseStub{recorder: recorder},
				knowledgeService: &evaluationCleanupKnowledgeStub{
					recorder:  recorder,
					createErr: test.knowledge,
				},
				sessionService:           &evaluationCleanupSessionStub{err: test.worker},
				evaluationTaskRepository: newEvaluationMemoryStorage(),
				ownerID:                  evaluationMemoryStorageOwnerID,
			}
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

			detail, err := service.Evaluation(
				ctx,
				"dataset",
				"source-kb",
				"chat-model",
				"rerank-model",
			)
			if err != nil {
				t.Fatalf("Evaluation() error = %v", err)
			}
			select {
			case <-datasetEntered:
			case <-time.After(2 * time.Second):
				close(datasetRelease)
				t.Fatal("timed out waiting for the background evaluation to start")
			}
			if got := recorder.snapshot(); len(got) != 0 {
				close(datasetRelease)
				t.Fatalf("cleanup before ownership transfer = %v, want none", got)
			}
			close(datasetRelease)

			select {
			case deletedID := <-recorder.kbDeleted:
				if deletedID != "evaluation-kb" {
					t.Fatalf("deleted knowledge base = %q, want evaluation-kb", deletedID)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for temporary knowledge base cleanup")
			}

			var stored *types.EvaluationDetail
			terminalDeadline := time.Now().Add(2 * time.Second)
			for {
				stored, err = service.EvaluationResult(ctx, detail.Task.ID)
				if err != nil {
					t.Fatalf("EvaluationResult() error = %v", err)
				}
				if stored.Task.Status == test.status && stored.Task.EndTime != nil {
					break
				}
				if time.Now().After(terminalDeadline) {
					t.Fatalf(
						"task status = %v, end time = %v, want status %v with end time",
						stored.Task.Status,
						stored.Task.EndTime,
						test.status,
					)
				}
				time.Sleep(10 * time.Millisecond)
			}

			if got := recorder.snapshot(); !reflect.DeepEqual(got, test.events) {
				t.Fatalf("cleanup events = %v, want %v", got, test.events)
			}
			if len(recorder.kbDeleted) != 0 {
				t.Fatalf("temporary knowledge base cleanup count exceeded one")
			}

			if stored.Task.ErrMsg != test.errMsg {
				t.Fatalf("task error = %q, want %q", stored.Task.ErrMsg, test.errMsg)
			}
		})
	}
}
