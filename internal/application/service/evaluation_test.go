package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func newEvaluationStorageFixture() *types.EvaluationDetail {
	citationEnabled := true
	thinkingEnabled := false
	endTime := time.Date(2026, time.August, 28, 9, 30, 0, 0, time.UTC)
	return &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:            "evaluation-task",
			DatasetID:     "dataset",
			EndTime:       &endTime,
			Status:        types.EvaluationStatuePending,
			CleanupErrors: []string{"cleanup warning"},
			Finished:      1,
		},
		Params: &types.ChatManage{
			PipelineRequest: types.PipelineRequest{
				ChatModelID:      "test-chat-model",
				Query:            "original query",
				KnowledgeBaseIDs: nil,
				KnowledgeIDs:     []string{"knowledge-1"},
				CitationEnabled:  &citationEnabled,
				SummaryConfig: types.SummaryConfig{
					Thinking: &thinkingEnabled,
				},
			},
		},
		Metric: &types.MetricResult{
			RetrievalMetrics: types.RetrievalMetrics{Recall: 0.25},
			GenerationMetrics: types.GenerationMetrics{
				BLEU1: 0.5,
			},
		},
	}
}

func assertEvaluationStorageFixture(t *testing.T, detail *types.EvaluationDetail) {
	t.Helper()
	if detail.Task.Status != types.EvaluationStatuePending || detail.Task.Finished != 1 {
		t.Fatalf("Task = %+v, want pending with one finished item", detail.Task)
	}
	wantEndTime := time.Date(2026, time.August, 28, 9, 30, 0, 0, time.UTC)
	if detail.Task.EndTime == nil || !detail.Task.EndTime.Equal(wantEndTime) {
		t.Fatalf("Task.EndTime = %v, want %v", detail.Task.EndTime, wantEndTime)
	}
	if len(detail.Task.CleanupErrors) != 1 || detail.Task.CleanupErrors[0] != "cleanup warning" {
		t.Fatalf("Task.CleanupErrors = %#v, want [cleanup warning]", detail.Task.CleanupErrors)
	}
	if detail.Params.Query != "original query" {
		t.Fatalf("Params.Query = %q, want original query", detail.Params.Query)
	}
	if detail.Params.KnowledgeBaseIDs != nil {
		t.Fatalf("Params.KnowledgeBaseIDs = %#v, want nil", detail.Params.KnowledgeBaseIDs)
	}
	if len(detail.Params.KnowledgeIDs) != 1 || detail.Params.KnowledgeIDs[0] != "knowledge-1" {
		t.Fatalf("Params.KnowledgeIDs = %#v, want [knowledge-1]", detail.Params.KnowledgeIDs)
	}
	if detail.Params.CitationEnabled == nil || !*detail.Params.CitationEnabled {
		t.Fatalf("Params.CitationEnabled = %v, want true", detail.Params.CitationEnabled)
	}
	if detail.Params.SummaryConfig.Thinking == nil || *detail.Params.SummaryConfig.Thinking {
		t.Fatalf("Params.SummaryConfig.Thinking = %v, want false", detail.Params.SummaryConfig.Thinking)
	}
	if detail.Metric.RetrievalMetrics.Recall != 0.25 || detail.Metric.GenerationMetrics.BLEU1 != 0.5 {
		t.Fatalf("Metric = %+v, want recall 0.25 and BLEU-1 0.5", detail.Metric)
	}
}

func TestEvaluationMemoryStorageSnapshotsAreIsolated(t *testing.T) {
	t.Run("register input", func(t *testing.T) {
		storage := newEvaluationMemoryStorage()
		source := newEvaluationStorageFixture()
		storage.register(source)

		source.Task.Status = types.EvaluationStatueFailed
		source.Task.Finished = 99
		*source.Task.EndTime = source.Task.EndTime.Add(time.Hour)
		source.Task.CleanupErrors[0] = "mutated cleanup warning"
		source.Params.Query = "mutated query"
		source.Params.KnowledgeBaseIDs = []string{"mutated-kb"}
		source.Params.KnowledgeIDs[0] = "mutated-knowledge"
		*source.Params.CitationEnabled = false
		*source.Params.SummaryConfig.Thinking = true
		source.Metric.RetrievalMetrics.Recall = 1
		source.Metric.GenerationMetrics.BLEU1 = 1

		got, err := storage.get(source.Task.ID)
		if err != nil {
			t.Fatalf("get() error = %v", err)
		}
		assertEvaluationStorageFixture(t, got)
	})

	t.Run("get result", func(t *testing.T) {
		storage := newEvaluationMemoryStorage()
		storage.register(newEvaluationStorageFixture())

		first, err := storage.get("evaluation-task")
		if err != nil {
			t.Fatalf("first get() error = %v", err)
		}
		first.Task.Status = types.EvaluationStatueSuccess
		first.Task.Finished = 99
		*first.Task.EndTime = first.Task.EndTime.Add(time.Hour)
		first.Task.CleanupErrors[0] = "mutated cleanup warning"
		first.Params.Query = "mutated query"
		first.Params.KnowledgeBaseIDs = []string{"mutated-kb"}
		first.Params.KnowledgeIDs[0] = "mutated-knowledge"
		*first.Params.CitationEnabled = false
		*first.Params.SummaryConfig.Thinking = true
		first.Metric.RetrievalMetrics.Recall = 1
		first.Metric.GenerationMetrics.BLEU1 = 1

		second, err := storage.get("evaluation-task")
		if err != nil {
			t.Fatalf("second get() error = %v", err)
		}
		assertEvaluationStorageFixture(t, second)
	})
}

func TestEvaluationMemoryStoragePreservesParamsJSON(t *testing.T) {
	storage := newEvaluationMemoryStorage()
	source := newEvaluationStorageFixture()
	want, err := json.Marshal(source.Params)
	if err != nil {
		t.Fatalf("marshal source params: %v", err)
	}
	storage.register(source)

	got, err := storage.get(source.Task.ID)
	if err != nil {
		t.Fatalf("get() error = %v", err)
	}
	gotJSON, err := json.Marshal(got.Params)
	if err != nil {
		t.Fatalf("marshal stored params: %v", err)
	}
	if string(gotJSON) != string(want) {
		t.Fatalf("stored Params JSON = %s, want %s", gotJSON, want)
	}
}

func TestEvaluationMemoryStorageConcurrentSnapshots(t *testing.T) {
	storage := newEvaluationMemoryStorage()
	storage.register(newEvaluationStorageFixture())

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)

	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 1000; i++ {
			err := storage.update("evaluation-task", func(detail *types.EvaluationDetail) {
				detail.Task.Finished = i
				*detail.Task.EndTime = detail.Task.EndTime.Add(time.Nanosecond)
				if i%2 == 0 {
					detail.Task.CleanupErrors[0] = "cleanup warning a"
				} else {
					detail.Task.CleanupErrors[0] = "cleanup warning b"
				}
				detail.Metric.RetrievalMetrics.Recall = float64(i)
			})
			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 1000; i++ {
			detail, err := storage.get("evaluation-task")
			if err != nil {
				errCh <- err
				return
			}
			if _, err := json.Marshal(detail); err != nil {
				errCh <- err
				return
			}
		}
	}()

	close(start)
	workers.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent storage operation failed: %v", err)
	}
}

type evaluationLifecycleDatasetStub struct {
	dataset []*types.QAPair
}

func (s *evaluationLifecycleDatasetStub) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	return s.dataset, nil
}

type evaluationLifecycleKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
}

func (s *evaluationLifecycleKnowledgeBaseStub) GetKnowledgeBaseByID(
	context.Context,
	string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID:               "source-kb",
		EmbeddingModelID: "embedding-model",
		SummaryModelID:   "summary-model",
	}, nil
}

func (s *evaluationLifecycleKnowledgeBaseStub) CreateKnowledgeBase(
	_ context.Context,
	knowledgeBase *types.KnowledgeBase,
) (*types.KnowledgeBase, error) {
	created := *knowledgeBase
	created.ID = "evaluation-kb"
	return &created, nil
}

func (s *evaluationLifecycleKnowledgeBaseStub) DeleteKnowledgeBase(context.Context, string) error {
	return nil
}

type evaluationLifecycleKnowledgeStub struct {
	interfaces.KnowledgeService
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (s *evaluationLifecycleKnowledgeStub) CreateKnowledgeFromPassageSync(
	context.Context,
	string,
	[]string,
	string,
) (*types.Knowledge, error) {
	s.once.Do(func() {
		close(s.entered)
	})
	<-s.release
	return &types.Knowledge{ID: "evaluation-knowledge"}, nil
}

func (s *evaluationLifecycleKnowledgeStub) DeleteKnowledge(context.Context, string) error {
	return nil
}

type evaluationLifecycleSessionStub struct {
	interfaces.SessionService
	modelIDs chan<- string
}

func (s *evaluationLifecycleSessionStub) KnowledgeQAByEvent(
	ctx context.Context,
	chatManage *types.ChatManage,
	_ []types.EventType,
) error {
	release := limiter.Gate(ctx, chatManage.ChatModelID)
	defer release()
	s.modelIDs <- chatManage.ChatModelID
	return nil
}

type evaluationLimiterAcquisition struct {
	modelID    string
	limit      int
	background bool
}

type evaluationLimiterSpy struct {
	acquisitions chan<- evaluationLimiterAcquisition
}

func (s *evaluationLimiterSpy) Acquire(ctx context.Context, key string, limit int) (func(), error) {
	s.acquisitions <- evaluationLimiterAcquisition{
		modelID: key, limit: limit, background: types.IsBackgroundTask(ctx),
	}
	return func() {}, nil
}

func TestEvaluationServiceSeparatesResponseFromBackgroundRun(t *testing.T) {
	const tenantID uint64 = 7
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	knowledgeEntered := make(chan struct{})
	knowledgeRelease := make(chan struct{})
	modelIDs := make(chan string, 1)
	limiterAcquisitions := make(chan evaluationLimiterAcquisition, 1)
	limiter.SetGovernor(&evaluationLimiterSpy{acquisitions: limiterAcquisitions}, 1)
	t.Cleanup(func() { limiter.SetGovernor(nil, 0) })

	service := &EvaluationService{
		config: &config.Config{
			Conversation: &config.ConversationConfig{
				Summary: &config.SummaryConfig{},
			},
		},
		dataset: &evaluationLifecycleDatasetStub{
			dataset: []*types.QAPair{
				{
					QID:      0,
					Question: "question",
					PIDs:     []int{0},
					Passages: []string{"passage"},
					Answer:   "answer",
				},
			},
		},
		knowledgeBaseService: &evaluationLifecycleKnowledgeBaseStub{},
		knowledgeService: &evaluationLifecycleKnowledgeStub{
			entered: knowledgeEntered,
			release: knowledgeRelease,
		},
		sessionService:           &evaluationLifecycleSessionStub{modelIDs: modelIDs},
		evaluationTaskRepository: newEvaluationMemoryStorage(),
		ownerID:                  evaluationMemoryStorageOwnerID,
	}

	created, err := service.Evaluation(ctx, "dataset", "source-kb", "chat-model", "rerank-model")
	if err != nil {
		t.Fatalf("Evaluation() error = %v", err)
	}
	taskID := created.Task.ID

	select {
	case <-knowledgeEntered:
	case <-time.After(5 * time.Second):
		close(knowledgeRelease)
		t.Fatal("timed out waiting for background evaluation")
	}

	running, err := service.EvaluationResult(ctx, taskID)
	if err != nil {
		close(knowledgeRelease)
		t.Fatalf("EvaluationResult() running error = %v", err)
	}
	if running.Task.Status != types.EvaluationStatueRunning {
		close(knowledgeRelease)
		t.Fatalf("running status = %v, want %v", running.Task.Status, types.EvaluationStatueRunning)
	}

	created.Task.ID = "caller-mutated-task"
	created.Task.DatasetID = "caller-mutated-dataset"
	created.Params.ChatModelID = "caller-mutated-model"
	close(knowledgeRelease)

	var observedModelID string
	select {
	case observedModelID = <-modelIDs:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the QA worker")
	}

	var completed *types.EvaluationDetail
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for completed == nil {
		select {
		case <-ticker.C:
			result, resultErr := service.EvaluationResult(ctx, taskID)
			if resultErr != nil {
				t.Fatalf("EvaluationResult() completion error = %v", resultErr)
			}
			if result.Task.Status == types.EvaluationStatueSuccess {
				completed = result
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for successful evaluation")
		}
	}

	if observedModelID != "chat-model" {
		t.Errorf("background ChatModelID = %q, want chat-model", observedModelID)
	}
	select {
	case acquisition := <-limiterAcquisitions:
		if acquisition.modelID != "chat-model" || acquisition.limit != 1 || !acquisition.background {
			t.Errorf("limiter acquisition = %+v, want background chat-model with limit 1", acquisition)
		}
	default:
		t.Error("background evaluation model call bypassed the concurrency limiter")
	}
	if completed.Task.ID != taskID || completed.Task.DatasetID != "dataset" {
		t.Errorf("stored task = %+v, want original ID and dataset", completed.Task)
	}
	if completed.Task.Total != 1 || completed.Task.Finished != 1 {
		t.Errorf("stored progress = %d/%d, want 1/1", completed.Task.Finished, completed.Task.Total)
	}
	if completed.Params.ChatModelID != "chat-model" {
		t.Errorf("stored ChatModelID = %q, want chat-model", completed.Params.ChatModelID)
	}
}

func TestGetPassageListPreservesPassageIDAsIndex(t *testing.T) {
	passages := getPassageList([]*types.QAPair{
		{
			PIDs:     []int{3, 7},
			Passages: []string{"passage three", "passage seven"},
		},
	})

	if len(passages) != 8 {
		t.Fatalf("len(passages) = %d, want 8", len(passages))
	}
	if got := passages[3]; got != "passage three" {
		t.Errorf("passages[3] = %q, want %q", got, "passage three")
	}
	if got := passages[7]; got != "passage seven" {
		t.Errorf("passages[7] = %q, want %q", got, "passage seven")
	}
	if got := passages[0]; got != "" {
		t.Errorf("passages[0] = %q, want an empty gap", got)
	}
}
