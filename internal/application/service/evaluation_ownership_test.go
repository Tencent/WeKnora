package service

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type evaluationOwnedKB struct {
	interfaces.KnowledgeBaseService
	deleted chan string
}

func (f *evaluationOwnedKB) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "business", EmbeddingModelID: "embed", SummaryModelID: "chat"}, nil
}
func (f *evaluationOwnedKB) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "owned-temp"}, nil
}
func (f *evaluationOwnedKB) DeleteKnowledgeBase(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.deleted <- id
	return nil
}

type evaluationOwnedDataset struct {
	interfaces.DatasetService
	err error
}

func (f evaluationOwnedDataset) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	return nil, f.err
}

type evaluationOwnedRepo struct{ interfaces.KnowledgeRepository }

func (evaluationOwnedRepo) GetKnowledgeBatch(context.Context, uint64, []string) ([]*types.Knowledge, error) {
	return nil, nil
}

type evaluationOwnedKnowledge struct {
	interfaces.KnowledgeService
	err error
}

func (f evaluationOwnedKnowledge) CreateKnowledgeFromPassageSync(ctx context.Context, _ string, _ []string, _ string) (*types.Knowledge, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &types.Knowledge{ID: "created-knowledge"}, nil
}
func (evaluationOwnedKnowledge) GetRepository() interfaces.KnowledgeRepository {
	return evaluationOwnedRepo{}
}

type evaluationOwnedModels struct{ interfaces.ModelService }

func (evaluationOwnedModels) ListModels(context.Context) ([]*types.Model, error) { return nil, nil }
func evaluationOwnershipService(kb *evaluationOwnedKB, dsErr, indexErr error) *EvaluationService {
	return &EvaluationService{config: &config.Config{Conversation: &config.ConversationConfig{Summary: &config.SummaryConfig{}}}, dataset: evaluationOwnedDataset{err: dsErr}, knowledgeBaseService: kb, knowledgeService: evaluationOwnedKnowledge{err: indexErr}, modelService: evaluationOwnedModels{}, evaluationMemoryStorage: newEvaluationMemoryStorage()}
}
func evaluationOwnershipContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func TestEvaluationOwnershipDirectExistingKBNotDeleted(t *testing.T) {
	kb := &evaluationOwnedKB{deleted: make(chan string, 4)}
	svc := evaluationOwnershipService(kb, nil, nil)
	detail := &types.EvaluationDetail{Task: &types.EvaluationTask{ID: "direct", DatasetID: "default"}, Params: &types.ChatManage{}}
	svc.evaluationMemoryStorage.register(detail)
	require.NoError(t, svc.EvalDataset(evaluationOwnershipContext(), detail, "business"))
	select {
	case id := <-kb.deleted:
		t.Fatalf("deleted unowned KB %s", id)
	default:
	}
}
func TestEvaluationOwnershipPreflightFailureCleansOnlyCreatedKB(t *testing.T) {
	kb := &evaluationOwnedKB{deleted: make(chan string, 4)}
	svc := evaluationOwnershipService(kb, nil, nil)
	_, err := svc.Evaluation(evaluationOwnershipContext(), "default", "business", "", "explicit-rerank")
	require.ErrorContains(t, err, "no default chat model")
	select {
	case id := <-kb.deleted:
		require.Equal(t, "owned-temp", id)
	default:
		t.Fatal("created temporary KB leaked on preflight failure")
	}
}
func TestEvaluationOwnershipBackgroundFailureAndSuccess(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		datasetErr, indexErr error
		cancel               bool
	}{{"dataset-failure", errors.New("dataset failure"), nil, false}, {"index-failure", nil, errors.New("index failure"), false}, {"success", nil, nil, false}, {"cancelled", context.Canceled, nil, true}} {
		t.Run(tc.name, func(t *testing.T) {
			kb := &evaluationOwnedKB{deleted: make(chan string, 4)}
			svc := evaluationOwnershipService(kb, tc.datasetErr, tc.indexErr)
			ctx := evaluationOwnershipContext()
			if tc.cancel {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			_, err := svc.Evaluation(ctx, "default", "business", "explicit-chat", "explicit-rerank")
			require.NoError(t, err)
			select {
			case id := <-kb.deleted:
				require.Equal(t, "owned-temp", id)
			case <-time.After(2 * time.Second):
				t.Fatal("owned temporary KB leaked")
			}
			select {
			case id := <-kb.deleted:
				t.Fatalf("unexpected second deletion %s", id)
			default:
			}
		})
	}
}

func TestEvaluationOwnershipStorageSnapshots(t *testing.T) {
	storage := newEvaluationMemoryStorage()
	original := &types.EvaluationDetail{Task: &types.EvaluationTask{ID: "snapshot"}, Params: &types.ChatManage{}, Metric: &types.MetricResult{}}
	storage.register(original)
	original.Task.Finished = 99
	got, err := storage.get("snapshot")
	require.NoError(t, err)
	require.Zero(t, got.Task.Finished)
	got.Task.Status = types.EvaluationStatueFailed
	got.Params.Query = "mutated"
	got.Metric.RetrievalMetrics.Precision = 99
	again, err := storage.get("snapshot")
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatuePending, again.Task.Status)
	require.Empty(t, again.Params.Query)
	require.Zero(t, again.Metric.RetrievalMetrics.Precision)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			storage.update("snapshot", func(d *types.EvaluationDetail) { d.Task.Finished = i })
		}
	}()
	for i := 0; i < 100; i++ {
		d, e := storage.get("snapshot")
		require.NoError(t, e)
		_ = d.Task.Finished
	}
	<-done
}

type evaluationConcurrentDataset struct{ interfaces.DatasetService }

func (evaluationConcurrentDataset) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	var rows []*types.QAPair
	for i := 0; i < 12; i++ {
		rows = append(rows, &types.QAPair{Question: "question", Answer: "answer", PIDs: []int{0}, Passages: []string{"passage"}})
	}
	return rows, nil
}

type evaluationConcurrentSession struct{ interfaces.SessionService }

func (evaluationConcurrentSession) KnowledgeQAByEvent(_ context.Context, c *types.ChatManage, _ []types.EventType) error {
	time.Sleep(time.Millisecond)
	c.ChatResponse = &types.ChatResponse{Content: "answer"}
	return nil
}
func TestEvaluationOwnershipConcurrentWorkersAndPolling(t *testing.T) {
	prior := runtime.GOMAXPROCS(3)
	defer runtime.GOMAXPROCS(prior)
	kb := &evaluationOwnedKB{deleted: make(chan string, 4)}
	svc := evaluationOwnershipService(kb, nil, nil)
	svc.dataset = evaluationConcurrentDataset{}
	svc.sessionService = evaluationConcurrentSession{}
	d := &types.EvaluationDetail{Task: &types.EvaluationTask{ID: "workers", DatasetID: "default"}, Params: &types.ChatManage{}}
	svc.evaluationMemoryStorage.register(d)
	done := make(chan error, 1)
	go func() { done <- svc.EvalDataset(evaluationOwnershipContext(), d, "business") }()
	for {
		select {
		case err := <-done:
			require.NoError(t, err)
			final, e := svc.evaluationMemoryStorage.get("workers")
			require.NoError(t, e)
			require.Equal(t, 12, final.Task.Finished)
			return
		default:
			snapshot, e := svc.evaluationMemoryStorage.get("workers")
			require.NoError(t, e)
			_ = snapshot.Task.Finished
			time.Sleep(time.Millisecond)
		}
	}
}

type evaluationFailedSession struct{ interfaces.SessionService }

func (evaluationFailedSession) KnowledgeQAByEvent(context.Context, *types.ChatManage, []types.EventType) error {
	return errors.New("QA injected failure")
}
func TestEvaluationOwnershipQAFailureCleansOnlyCreatedKB(t *testing.T) {
	kb := &evaluationOwnedKB{deleted: make(chan string, 4)}
	svc := evaluationOwnershipService(kb, nil, nil)
	svc.dataset = evaluationConcurrentDataset{}
	svc.sessionService = evaluationFailedSession{}
	detail, err := svc.Evaluation(evaluationOwnershipContext(), "default", "business", "explicit-chat", "explicit-rerank")
	require.NoError(t, err)
	select {
	case id := <-kb.deleted:
		require.Equal(t, "owned-temp", id)
	case <-time.After(2 * time.Second):
		t.Fatal("owned KB leaked on QA failure")
	}
	result, err := svc.evaluationMemoryStorage.get(detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatueFailed, result.Task.Status)
	require.ErrorContains(t, errors.New(result.Task.ErrMsg), "QA injected failure")
}
