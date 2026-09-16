package service

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/sirupsen/logrus"
)

type evaluationProgressDatasetStub struct {
	interfaces.DatasetService
	dataset []*types.QAPair
}

func (s *evaluationProgressDatasetStub) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	return s.dataset, nil
}

type evaluationProgressKnowledgeStub struct {
	interfaces.KnowledgeService
}

func (s *evaluationProgressKnowledgeStub) CreateKnowledgeFromPassageSync(
	context.Context,
	string,
	[]string,
	string,
) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "evaluation-knowledge"}, nil
}

func (s *evaluationProgressKnowledgeStub) DeleteKnowledge(context.Context, string) error {
	return nil
}

type evaluationProgressKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
}

func (s *evaluationProgressKnowledgeBaseStub) DeleteKnowledgeBase(context.Context, string) error {
	return nil
}

type evaluationProgressSessionStub struct {
	interfaces.SessionService
	firstWorkerPastError <-chan struct{}
}

func (s *evaluationProgressSessionStub) KnowledgeQAByEvent(
	_ context.Context,
	chatManage *types.ChatManage,
	_ []types.EventType,
) error {
	if chatManage.Query == "question-1" {
		select {
		case <-s.firstWorkerPastError:
		case <-time.After(5 * time.Second):
			return errors.New("timed out waiting for the first worker to pass shared error access")
		}
	}
	return nil
}

type evaluationProgressSignalWriter struct {
	firstWorkerPastError chan struct{}
	once                 sync.Once
}

func (w *evaluationProgressSignalWriter) Write(data []byte) (int, error) {
	if strings.Contains(string(data), "Recording metrics for QA pair 0") {
		w.once.Do(func() {
			close(w.firstWorkerPastError)
		})
	}
	return len(data), nil
}

func TestEvaluationServicePublishesProgressWithoutRace(t *testing.T) {
	previousMaxProcs := runtime.GOMAXPROCS(3)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(previousMaxProcs)
	})

	firstWorkerPastError := make(chan struct{})
	testLogger := logrus.New()
	testLogger.SetOutput(&evaluationProgressSignalWriter{firstWorkerPastError: firstWorkerPastError})
	testLogger.SetLevel(logrus.InfoLevel)
	ctx := context.WithValue(context.Background(), types.LoggerContextKey, logrus.NewEntry(testLogger))

	dataset := []*types.QAPair{
		{
			QID:      0,
			Question: "question-0",
			PIDs:     []int{0},
			Passages: []string{"passage-0"},
			Answer:   "answer-0",
		},
		{
			QID:      1,
			Question: "question-1",
			PIDs:     []int{1},
			Passages: []string{"passage-1"},
			Answer:   "answer-1",
		},
	}

	storage := newEvaluationMemoryStorage()
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        "evaluation-progress-task",
			DatasetID: "dataset",
		},
		Params: &types.ChatManage{PipelineRequest: types.PipelineRequest{
			ChatModelID: "test-chat-model",
		}},
	}
	storage.register(detail)

	service := &EvaluationService{
		dataset:                  &evaluationProgressDatasetStub{dataset: dataset},
		knowledgeService:         &evaluationProgressKnowledgeStub{},
		knowledgeBaseService:     &evaluationProgressKnowledgeBaseStub{},
		sessionService:           &evaluationProgressSessionStub{firstWorkerPastError: firstWorkerPastError},
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	if err := service.EvalDataset(ctx, detail, "knowledge-base"); err != nil {
		t.Fatalf("EvalDataset() error = %v", err)
	}

	result, err := storage.get(detail.Task.ID)
	if err != nil {
		t.Fatalf("get() error = %v", err)
	}
	if result.Task.Finished != len(dataset) {
		t.Fatalf("Finished = %d, want %d", result.Task.Finished, len(dataset))
	}
	if result.Metric == nil {
		t.Fatal("Metric = nil, want aggregated metric result")
	}
}
