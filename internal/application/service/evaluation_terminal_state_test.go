package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestEvaluationTaskTerminalFieldsRoundTripJSON(t *testing.T) {
	input := []byte(`{
		"id":"evaluation-task",
		"end_time":"2026-08-28T15:30:00Z",
		"cleanup_errors":["delete knowledge: cleanup failed"]
	}`)
	var task types.EvaluationTask
	if err := json.Unmarshal(input, &task); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	wantEndTime := time.Date(2026, time.August, 28, 15, 30, 0, 0, time.UTC)
	if task.EndTime == nil || !task.EndTime.Equal(wantEndTime) {
		t.Fatalf("EvaluationTask EndTime = %v, want %v", task.EndTime, wantEndTime)
	}
	wantCleanupErrors := []string{"delete knowledge: cleanup failed"}
	if !reflect.DeepEqual(task.CleanupErrors, wantCleanupErrors) {
		t.Fatalf("EvaluationTask CleanupErrors = %v, want %v", task.CleanupErrors, wantCleanupErrors)
	}

	output, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output, &fields); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v", err)
	}
	if _, ok := fields["end_time"]; !ok {
		t.Fatalf("EvaluationTask JSON = %s, want end_time", output)
	}
	if _, ok := fields["cleanup_errors"]; !ok {
		t.Fatalf("EvaluationTask JSON = %s, want cleanup_errors", output)
	}
}

type evaluationTerminalDatasetStub struct {
	interfaces.DatasetService
}

func (s *evaluationTerminalDatasetStub) GetDatasetByID(
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

type evaluationTerminalCleanupRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *evaluationTerminalCleanupRecorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *evaluationTerminalCleanupRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

type evaluationTerminalKnowledgeStub struct {
	interfaces.KnowledgeService
	recorder  *evaluationTerminalCleanupRecorder
	deleteErr error
}

func (s *evaluationTerminalKnowledgeStub) CreateKnowledgeFromPassageSync(
	context.Context,
	string,
	[]string,
	string,
) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "evaluation-knowledge"}, nil
}

func (s *evaluationTerminalKnowledgeStub) DeleteKnowledge(context.Context, string) error {
	s.recorder.record("knowledge:evaluation-knowledge")
	return s.deleteErr
}

type evaluationTerminalKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
	recorder  *evaluationTerminalCleanupRecorder
	entered   chan struct{}
	release   <-chan struct{}
	deleteErr error
	once      sync.Once
}

func (s *evaluationTerminalKnowledgeBaseStub) DeleteKnowledgeBase(context.Context, string) error {
	s.recorder.record("knowledge-base:evaluation-kb")
	if s.entered != nil {
		s.once.Do(func() {
			close(s.entered)
		})
	}
	if s.release != nil {
		<-s.release
	}
	return s.deleteErr
}

type evaluationTerminalSessionStub struct {
	interfaces.SessionService
	err error
}

func (s *evaluationTerminalSessionStub) KnowledgeQAByEvent(
	context.Context,
	*types.ChatManage,
	[]types.EventType,
) error {
	return s.err
}

func newEvaluationTerminalDetail(storage *evaluationMemoryStorage) *types.EvaluationDetail {
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        "evaluation-task",
			DatasetID: "dataset",
			StartTime: time.Now().Add(-time.Minute),
			Status:    types.EvaluationStatuePending,
		},
		Params: &types.ChatManage{PipelineRequest: types.PipelineRequest{
			ChatModelID: "test-chat-model",
		}},
	}
	storage.register(detail)
	return detail
}

func TestEvaluationServicePublishesTerminalStateAfterCleanup(t *testing.T) {
	knowledgeCleanupErr := errors.New("knowledge cleanup failed")
	knowledgeBaseCleanupErr := errors.New("knowledge base cleanup failed")
	recorder := &evaluationTerminalCleanupRecorder{}
	cleanupEntered := make(chan struct{})
	cleanupRelease := make(chan struct{})
	storage := newEvaluationMemoryStorage()
	detail := newEvaluationTerminalDetail(storage)
	service := &EvaluationService{
		dataset: &evaluationTerminalDatasetStub{},
		knowledgeService: &evaluationTerminalKnowledgeStub{
			recorder:  recorder,
			deleteErr: knowledgeCleanupErr,
		},
		knowledgeBaseService: &evaluationTerminalKnowledgeBaseStub{
			recorder:  recorder,
			entered:   cleanupEntered,
			release:   cleanupRelease,
			deleteErr: knowledgeBaseCleanupErr,
		},
		sessionService:           &evaluationTerminalSessionStub{},
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	result := make(chan error, 1)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		ctx := types.WithExecutionTenant(context.Background(), detail.Task.TenantID)
		result <- service.runEvaluation(ctx, detail, "evaluation-kb")
	}()
	var releaseOnce sync.Once
	releaseCleanup := func() {
		releaseOnce.Do(func() {
			close(cleanupRelease)
		})
	}
	t.Cleanup(func() {
		releaseCleanup()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
		}
	})
	select {
	case <-cleanupEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for knowledge base cleanup")
	}

	running, err := storage.get(detail.Task.ID)
	if err != nil {
		t.Fatalf("storage.get() error = %v", err)
	}
	if running.Task.Status != types.EvaluationStatueRunning {
		t.Fatalf("task status during cleanup = %v, want Running", running.Task.Status)
	}
	if running.Task.EndTime != nil {
		t.Fatalf("task end time during cleanup = %v, want nil", running.Task.EndTime)
	}
	if len(running.Task.CleanupErrors) != 0 {
		t.Fatalf("cleanup errors during cleanup = %v, want none", running.Task.CleanupErrors)
	}
	releaseCleanup()

	select {
	case runErr := <-result:
		if runErr != nil {
			t.Fatalf("runEvaluation() error = %v, want nil", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal state publication")
	}

	terminal, err := storage.get(detail.Task.ID)
	if err != nil {
		t.Fatalf("storage.get() error = %v", err)
	}
	if terminal.Task.Status != types.EvaluationStatueSuccess {
		t.Fatalf("terminal task status = %v, want Success", terminal.Task.Status)
	}
	if terminal.Task.ErrMsg != "" {
		t.Fatalf("terminal task error = %q, want empty", terminal.Task.ErrMsg)
	}
	if terminal.Task.EndTime == nil || terminal.Task.EndTime.Before(terminal.Task.StartTime) {
		t.Fatalf("terminal task end time = %v, want at or after start", terminal.Task.EndTime)
	}
	wantCleanupErrors := []string{
		"delete knowledge evaluation-knowledge: knowledge cleanup failed",
		"delete knowledge base evaluation-kb: knowledge base cleanup failed",
	}
	if !reflect.DeepEqual(terminal.Task.CleanupErrors, wantCleanupErrors) {
		t.Fatalf("terminal cleanup errors = %v, want %v", terminal.Task.CleanupErrors, wantCleanupErrors)
	}
	wantEvents := []string{"knowledge:evaluation-knowledge", "knowledge-base:evaluation-kb"}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("cleanup events = %v, want %v", got, wantEvents)
	}
}

func TestEvaluationServicePreservesPrimaryErrorAlongsideCleanupErrors(t *testing.T) {
	workerErr := errors.New("worker failed")
	recorder := &evaluationTerminalCleanupRecorder{}
	storage := newEvaluationMemoryStorage()
	detail := newEvaluationTerminalDetail(storage)
	service := &EvaluationService{
		dataset: &evaluationTerminalDatasetStub{},
		knowledgeService: &evaluationTerminalKnowledgeStub{
			recorder:  recorder,
			deleteErr: errors.New("knowledge cleanup failed"),
		},
		knowledgeBaseService: &evaluationTerminalKnowledgeBaseStub{
			recorder:  recorder,
			deleteErr: errors.New("knowledge base cleanup failed"),
		},
		sessionService:           &evaluationTerminalSessionStub{err: workerErr},
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	runErr := service.runEvaluation(
		types.WithExecutionTenant(context.Background(), detail.Task.TenantID),
		detail,
		"evaluation-kb",
	)
	if !errors.Is(runErr, workerErr) {
		t.Fatalf("runEvaluation() error = %v, want errors.Is(error, workerErr)", runErr)
	}
	terminal, err := storage.get(detail.Task.ID)
	if err != nil {
		t.Fatalf("storage.get() error = %v", err)
	}
	if terminal.Task.Status != types.EvaluationStatueFailed {
		t.Fatalf("terminal task status = %v, want Failed", terminal.Task.Status)
	}
	if terminal.Task.ErrMsg != workerErr.Error() {
		t.Fatalf("terminal task error = %q, want %q", terminal.Task.ErrMsg, workerErr)
	}
	if terminal.Task.EndTime == nil || terminal.Task.EndTime.Before(terminal.Task.StartTime) {
		t.Fatalf("terminal task end time = %v, want at or after start", terminal.Task.EndTime)
	}
	wantCleanupErrors := []string{
		"delete knowledge evaluation-knowledge: knowledge cleanup failed",
		"delete knowledge base evaluation-kb: knowledge base cleanup failed",
	}
	if !reflect.DeepEqual(terminal.Task.CleanupErrors, wantCleanupErrors) {
		t.Fatalf("terminal cleanup errors = %v, want %v", terminal.Task.CleanupErrors, wantCleanupErrors)
	}
}
