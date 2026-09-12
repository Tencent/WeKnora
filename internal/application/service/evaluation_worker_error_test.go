package service

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type evaluationDatasetStub struct {
	interfaces.DatasetService
	dataset []*types.QAPair
}

func (s *evaluationDatasetStub) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	return s.dataset, nil
}

type evaluationKnowledgeStub struct {
	interfaces.KnowledgeService
}

func (s *evaluationKnowledgeStub) CreateKnowledgeFromPassageSync(
	context.Context,
	string,
	[]string,
	string,
) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "evaluation-knowledge"}, nil
}

func (s *evaluationKnowledgeStub) DeleteKnowledge(context.Context, string) error {
	return nil
}

type evaluationKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
}

func (s *evaluationKnowledgeBaseStub) DeleteKnowledgeBase(context.Context, string) error {
	return nil
}

type evaluationBarrierSessionStub struct {
	interfaces.SessionService
	ready   *sync.WaitGroup
	release <-chan struct{}
	err     error
}

func (s *evaluationBarrierSessionStub) KnowledgeQAByEvent(
	context.Context,
	*types.ChatManage,
	[]types.EventType,
) error {
	s.ready.Done()
	<-s.release
	runtime.Gosched()
	return s.err
}

type evaluationSessionStub struct {
	interfaces.SessionService
	run func(context.Context, *types.ChatManage, []types.EventType) error
}

func (s *evaluationSessionStub) KnowledgeQAByEvent(
	ctx context.Context,
	chatManage *types.ChatManage,
	events []types.EventType,
) error {
	return s.run(ctx, chatManage, events)
}

type evaluationBlockingError struct {
	message string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *evaluationBlockingError) Error() string {
	e.once.Do(func() {
		close(e.entered)
		<-e.release
	})
	return e.message
}

func TestEvaluationServiceEvalDatasetPreservesWorkerError(t *testing.T) {
	previousMaxProcs := runtime.GOMAXPROCS(3)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(previousMaxProcs)
	})

	dataset := []*types.QAPair{
		{
			QID:      0,
			Question: "failing-question",
			PIDs:     []int{0},
			Passages: []string{"failing-passage"},
		},
		{
			QID:      1,
			Question: "successful-question",
			PIDs:     []int{1},
			Passages: []string{"successful-passage"},
		},
	}

	workerErr := &evaluationBlockingError{
		message: "worker failed",
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	session := &evaluationSessionStub{
		run: func(_ context.Context, chatManage *types.ChatManage, _ []types.EventType) error {
			if chatManage.Query == "successful-question" {
				<-workerErr.entered
				return nil
			}
			return workerErr
		},
	}

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
		dataset:                  &evaluationDatasetStub{dataset: dataset},
		knowledgeService:         &evaluationKnowledgeStub{},
		knowledgeBaseService:     &evaluationKnowledgeBaseStub{},
		sessionService:           session,
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	// Hold the failing worker in log formatting until the successful worker has
	// published progress. A shared error variable lets the nil result overwrite
	// the failure before the failing worker returns it.
	watchResult := make(chan error, 1)
	go func() {
		select {
		case <-workerErr.entered:
		case <-time.After(5 * time.Second):
			close(workerErr.release)
			watchResult <- errors.New("timed out waiting for worker error logging")
			return
		}

		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		timeout := time.NewTimer(5 * time.Second)
		defer timeout.Stop()
		for {
			current, getErr := storage.get(detail.Task.ID)
			if getErr != nil {
				close(workerErr.release)
				watchResult <- fmt.Errorf("load evaluation progress: %w", getErr)
				return
			}
			finished := current.Task.Finished
			if finished == 1 {
				close(workerErr.release)
				watchResult <- nil
				return
			}

			select {
			case <-ticker.C:
			case <-timeout.C:
				close(workerErr.release)
				watchResult <- errors.New("timed out waiting for successful worker progress")
				return
			}
		}
	}()

	err := service.EvalDataset(context.Background(), detail, "knowledge-base")
	if watchErr := <-watchResult; watchErr != nil {
		t.Fatal(watchErr)
	}
	if !errors.Is(err, workerErr) {
		t.Fatalf("EvalDataset() error = %v, want errors.Is(error, workerErr)", err)
	}
}

func TestEvaluationServiceEvalDatasetConcurrentWorkerError(t *testing.T) {
	const workerCount = 16
	previousMaxProcs := runtime.GOMAXPROCS(workerCount + 1)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(previousMaxProcs)
	})

	dataset := make([]*types.QAPair, workerCount)
	for i := range dataset {
		dataset[i] = &types.QAPair{
			QID:      i,
			Question: fmt.Sprintf("question-%d", i),
			PIDs:     []int{i},
			Passages: []string{fmt.Sprintf("passage-%d", i)},
		}
	}

	workerErr := errors.New("worker failed")
	ready := &sync.WaitGroup{}
	ready.Add(workerCount)
	release := make(chan struct{})
	barrierResult := make(chan error, 1)
	go func() {
		readyDone := make(chan struct{})
		go func() {
			ready.Wait()
			close(readyDone)
		}()

		select {
		case <-readyDone:
			close(release)
			barrierResult <- nil
		case <-time.After(5 * time.Second):
			close(release)
			barrierResult <- errors.New("timed out waiting for concurrent evaluation workers")
		}
	}()

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
		dataset:              &evaluationDatasetStub{dataset: dataset},
		knowledgeService:     &evaluationKnowledgeStub{},
		knowledgeBaseService: &evaluationKnowledgeBaseStub{},
		sessionService: &evaluationBarrierSessionStub{
			ready:   ready,
			release: release,
			err:     workerErr,
		},
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	err := service.EvalDataset(context.Background(), detail, "knowledge-base")
	if barrierErr := <-barrierResult; barrierErr != nil {
		t.Fatal(barrierErr)
	}
	if !errors.Is(err, workerErr) {
		t.Fatalf("EvalDataset() error = %v, want errors.Is(error, workerErr)", err)
	}
}
