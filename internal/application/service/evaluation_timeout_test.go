package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const evaluationTimeoutTestWait = 2 * time.Second

type evaluationContextObservation struct {
	err         error
	deadline    time.Time
	hasDeadline bool
	done        <-chan struct{}
}

func observeEvaluationContext(ctx context.Context) evaluationContextObservation {
	deadline, hasDeadline := ctx.Deadline()
	return evaluationContextObservation{
		err:         ctx.Err(),
		deadline:    deadline,
		hasDeadline: hasDeadline,
		done:        ctx.Done(),
	}
}

type evaluationTimeoutDatasetStub struct {
	interfaces.DatasetService
}

func (s *evaluationTimeoutDatasetStub) GetDatasetByID(
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

type evaluationTimeoutKnowledgeStub struct {
	interfaces.KnowledgeService
	deleteEntered chan<- evaluationContextObservation
	deleteRelease <-chan struct{}
	deleteErr     error
}

func (s *evaluationTimeoutKnowledgeStub) CreateKnowledgeFromPassageSync(
	context.Context,
	string,
	[]string,
	string,
) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "evaluation-knowledge"}, nil
}

func (s *evaluationTimeoutKnowledgeStub) DeleteKnowledge(ctx context.Context, _ string) error {
	if s.deleteEntered != nil {
		s.deleteEntered <- observeEvaluationContext(ctx)
	}
	if s.deleteRelease != nil {
		select {
		case <-s.deleteRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.deleteErr
}

type evaluationTimeoutKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
}

func (s *evaluationTimeoutKnowledgeBaseStub) DeleteKnowledgeBase(context.Context, string) error {
	return nil
}

type evaluationTimeoutSessionStub struct {
	interfaces.SessionService
	err             error
	contextObserved chan<- evaluationContextObservation
	deadlineError   chan<- error
	safetyRelease   <-chan struct{}
}

func (s *evaluationTimeoutSessionStub) KnowledgeQAByEvent(
	ctx context.Context,
	_ *types.ChatManage,
	_ []types.EventType,
) error {
	if s.contextObserved != nil {
		s.contextObserved <- observeEvaluationContext(ctx)
	}
	if s.err != nil {
		return s.err
	}
	select {
	case <-ctx.Done():
		if s.deadlineError != nil {
			s.deadlineError <- ctx.Err()
		}
		return ctx.Err()
	case <-s.safetyRelease:
		return nil
	}
}

func newEvaluationTimeoutDetail(storage *evaluationMemoryStorage) *types.EvaluationDetail {
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        "evaluation-task",
			DatasetID: "dataset",
			Status:    types.EvaluationStatuePending,
		},
		Params: &types.ChatManage{PipelineRequest: types.PipelineRequest{
			ChatModelID: "test-chat-model",
		}},
	}
	storage.register(detail)
	return detail
}

func waitForEvaluationObservation(
	t *testing.T,
	observed <-chan evaluationContextObservation,
	name string,
) evaluationContextObservation {
	t.Helper()
	select {
	case observation := <-observed:
		return observation
	case <-time.After(evaluationTimeoutTestWait):
		t.Fatalf("timed out waiting for %s context", name)
		return evaluationContextObservation{}
	}
}

func waitForEvaluationRun(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case runErr := <-result:
		return runErr
	case <-time.After(evaluationTimeoutTestWait):
		t.Fatal("timed out waiting for evaluation run")
		return nil
	}
}

func waitPastEvaluationTaskDeadline(t *testing.T, observation evaluationContextObservation) {
	t.Helper()
	if !observation.hasDeadline {
		t.Fatal("evaluation task context has no deadline")
	}
	delay := time.Until(observation.deadline)
	if delay > 0 {
		timer := time.NewTimer(delay + 10*time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-time.After(evaluationTimeoutTestWait):
			t.Fatal("timed out waiting for evaluation task deadline")
		}
	}
	select {
	case <-observation.done:
	case <-time.After(evaluationTimeoutTestWait):
		t.Fatal("evaluation task context did not stop at its deadline")
	}
}

func requireIndependentEvaluationCleanupContext(t *testing.T, observation evaluationContextObservation) {
	t.Helper()
	if observation.err != nil {
		t.Fatalf("cleanup context error on entry = %v, want nil", observation.err)
	}
	if !observation.hasDeadline {
		t.Fatal("cleanup context has no deadline")
	}
	if remaining := time.Until(observation.deadline); remaining < evaluationCleanupTimeout/2 {
		t.Fatalf(
			"cleanup context deadline remaining = %v, want an independent %v budget",
			remaining,
			evaluationCleanupTimeout,
		)
	}
	select {
	case <-observation.done:
		t.Fatal("cleanup context was canceled with the evaluation task context")
	default:
	}
}

func requireEvaluationRunningWithoutEndTime(t *testing.T, storage *evaluationMemoryStorage, taskID string) {
	t.Helper()
	current, err := storage.get(taskID)
	if err != nil {
		t.Fatalf("storage.get() error = %v", err)
	}
	if current.Task.Status != types.EvaluationStatueRunning {
		t.Fatalf("task status during cleanup = %v, want Running", current.Task.Status)
	}
	if current.Task.EndTime != nil {
		t.Fatalf("task end time during cleanup = %v, want nil", current.Task.EndTime)
	}
}

func requireEvaluationTerminal(
	t *testing.T,
	storage *evaluationMemoryStorage,
	taskID string,
	status types.EvaluationStatue,
	errMsg string,
) *types.EvaluationDetail {
	t.Helper()
	terminal, err := storage.get(taskID)
	if err != nil {
		t.Fatalf("storage.get() error = %v", err)
	}
	if terminal.Task.Status != status {
		t.Fatalf("terminal task status = %v, want %v", terminal.Task.Status, status)
	}
	if terminal.Task.ErrMsg != errMsg {
		t.Fatalf("terminal task error = %q, want %q", terminal.Task.ErrMsg, errMsg)
	}
	if terminal.Task.EndTime == nil {
		t.Fatal("terminal task end time is nil")
	}
	return terminal
}

func TestEvaluationStatusValuesRemainCompatible(t *testing.T) {
	tests := []struct {
		name   string
		status types.EvaluationStatue
		want   int
	}{
		{name: "pending", status: types.EvaluationStatuePending, want: 0},
		{name: "running", status: types.EvaluationStatueRunning, want: 1},
		{name: "success", status: types.EvaluationStatueSuccess, want: 2},
		{name: "failed", status: types.EvaluationStatueFailed, want: 3},
		{name: "timed out", status: types.EvaluationStatueTimedOut, want: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := int(test.status); got != test.want {
				t.Fatalf("status value = %d, want %d", got, test.want)
			}
		})
	}
}

func TestEvaluationServiceMarksTaskTimedOutAfterIndependentCleanup(t *testing.T) {
	cleanupErr := errors.New("delete failed after timeout")
	storage := newEvaluationMemoryStorage()
	detail := newEvaluationTimeoutDetail(storage)
	workerDeadlineError := make(chan error, 1)
	taskContextObserved := make(chan evaluationContextObservation, 1)
	cleanupEntered := make(chan evaluationContextObservation, 1)
	cleanupRelease := make(chan struct{})
	cleanupReleased := false
	defer func() {
		if !cleanupReleased {
			close(cleanupRelease)
		}
	}()
	service := &EvaluationService{
		config: &config.Config{
			Evaluation: &config.EvaluationConfig{TaskTimeout: 20 * time.Millisecond},
		},
		dataset: &evaluationTimeoutDatasetStub{},
		knowledgeService: &evaluationTimeoutKnowledgeStub{
			deleteEntered: cleanupEntered,
			deleteRelease: cleanupRelease,
			deleteErr:     cleanupErr,
		},
		knowledgeBaseService: &evaluationTimeoutKnowledgeBaseStub{},
		sessionService: &evaluationTimeoutSessionStub{
			contextObserved: taskContextObserved,
			deadlineError:   workerDeadlineError,
		},
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	result := make(chan error, 1)
	go func() {
		ctx := types.WithExecutionTenant(context.Background(), detail.Task.TenantID)
		result <- service.runEvaluation(ctx, detail, "evaluation-kb")
	}()
	taskObservation := waitForEvaluationObservation(t, taskContextObserved, "task")
	cleanupObservation := waitForEvaluationObservation(t, cleanupEntered, "cleanup")
	waitPastEvaluationTaskDeadline(t, taskObservation)
	requireIndependentEvaluationCleanupContext(t, cleanupObservation)
	requireEvaluationRunningWithoutEndTime(t, storage, detail.Task.ID)
	select {
	case runErr := <-result:
		t.Fatalf("runEvaluation() completed during cleanup with error %v", runErr)
	default:
	}

	close(cleanupRelease)
	cleanupReleased = true
	runErr := waitForEvaluationRun(t, result)
	if !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("runEvaluation() error = %v, want context.DeadlineExceeded", runErr)
	}
	select {
	case observedErr := <-workerDeadlineError:
		if !errors.Is(observedErr, context.DeadlineExceeded) {
			t.Fatalf("worker context error = %v, want context.DeadlineExceeded", observedErr)
		}
	default:
		t.Fatal("worker did not observe the evaluation task deadline")
	}
	terminal := requireEvaluationTerminal(
		t,
		storage,
		detail.Task.ID,
		types.EvaluationStatueTimedOut,
		context.DeadlineExceeded.Error(),
	)
	if len(terminal.Task.CleanupErrors) != 1 {
		t.Fatalf("cleanup errors = %v, want one error", terminal.Task.CleanupErrors)
	}
	wantCleanupError := "delete knowledge evaluation-knowledge: " + cleanupErr.Error()
	if terminal.Task.CleanupErrors[0] != wantCleanupError {
		t.Fatalf("cleanup error = %q, want %q", terminal.Task.CleanupErrors[0], wantCleanupError)
	}
}

func TestEvaluationServiceKeepsBusinessErrorWhenCleanupCrossesTaskDeadline(t *testing.T) {
	workerErr := errors.New("worker failed before cleanup")
	storage := newEvaluationMemoryStorage()
	detail := newEvaluationTimeoutDetail(storage)
	taskContextObserved := make(chan evaluationContextObservation, 1)
	cleanupEntered := make(chan evaluationContextObservation, 1)
	cleanupRelease := make(chan struct{})
	cleanupReleased := false
	defer func() {
		if !cleanupReleased {
			close(cleanupRelease)
		}
	}()
	service := &EvaluationService{
		config: &config.Config{
			Evaluation: &config.EvaluationConfig{TaskTimeout: 20 * time.Millisecond},
		},
		dataset: &evaluationTimeoutDatasetStub{},
		knowledgeService: &evaluationTimeoutKnowledgeStub{
			deleteEntered: cleanupEntered,
			deleteRelease: cleanupRelease,
		},
		knowledgeBaseService: &evaluationTimeoutKnowledgeBaseStub{},
		sessionService: &evaluationTimeoutSessionStub{
			err:             workerErr,
			contextObserved: taskContextObserved,
		},
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	result := make(chan error, 1)
	go func() {
		ctx := types.WithExecutionTenant(context.Background(), detail.Task.TenantID)
		result <- service.runEvaluation(ctx, detail, "evaluation-kb")
	}()
	taskObservation := waitForEvaluationObservation(t, taskContextObserved, "task")
	cleanupObservation := waitForEvaluationObservation(t, cleanupEntered, "cleanup")
	waitPastEvaluationTaskDeadline(t, taskObservation)
	requireIndependentEvaluationCleanupContext(t, cleanupObservation)
	requireEvaluationRunningWithoutEndTime(t, storage, detail.Task.ID)
	select {
	case runErr := <-result:
		t.Fatalf("runEvaluation() completed during cleanup with error %v", runErr)
	default:
	}

	close(cleanupRelease)
	cleanupReleased = true
	runErr := waitForEvaluationRun(t, result)
	if !errors.Is(runErr, workerErr) {
		t.Fatalf("runEvaluation() error = %v, want errors.Is(error, workerErr)", runErr)
	}
	requireEvaluationTerminal(t, storage, detail.Task.ID, types.EvaluationStatueFailed, workerErr.Error())
}

func TestEvaluationServiceKeepsSuccessWhenCleanupCrossesTaskDeadline(t *testing.T) {
	storage := newEvaluationMemoryStorage()
	detail := newEvaluationTimeoutDetail(storage)
	taskContextObserved := make(chan evaluationContextObservation, 1)
	cleanupEntered := make(chan evaluationContextObservation, 1)
	cleanupRelease := make(chan struct{})
	cleanupReleased := false
	defer func() {
		if !cleanupReleased {
			close(cleanupRelease)
		}
	}()
	qaCompleted := make(chan struct{})
	close(qaCompleted)
	service := &EvaluationService{
		config: &config.Config{
			Evaluation: &config.EvaluationConfig{TaskTimeout: 20 * time.Millisecond},
		},
		dataset: &evaluationTimeoutDatasetStub{},
		knowledgeService: &evaluationTimeoutKnowledgeStub{
			deleteEntered: cleanupEntered,
			deleteRelease: cleanupRelease,
		},
		knowledgeBaseService: &evaluationTimeoutKnowledgeBaseStub{},
		sessionService: &evaluationTimeoutSessionStub{
			contextObserved: taskContextObserved,
			safetyRelease:   qaCompleted,
		},
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	result := make(chan error, 1)
	go func() {
		ctx := types.WithExecutionTenant(context.Background(), detail.Task.TenantID)
		result <- service.runEvaluation(ctx, detail, "evaluation-kb")
	}()
	taskObservation := waitForEvaluationObservation(t, taskContextObserved, "task")
	cleanupObservation := waitForEvaluationObservation(t, cleanupEntered, "cleanup")
	waitPastEvaluationTaskDeadline(t, taskObservation)
	requireIndependentEvaluationCleanupContext(t, cleanupObservation)
	requireEvaluationRunningWithoutEndTime(t, storage, detail.Task.ID)
	select {
	case runErr := <-result:
		t.Fatalf("runEvaluation() completed during cleanup with error %v", runErr)
	default:
	}

	close(cleanupRelease)
	cleanupReleased = true
	if runErr := waitForEvaluationRun(t, result); runErr != nil {
		t.Fatalf("runEvaluation() error = %v, want nil", runErr)
	}
	requireEvaluationTerminal(t, storage, detail.Task.ID, types.EvaluationStatueSuccess, "")
}

func TestEvaluationServiceKeepsDownstreamDeadlineFailureFailed(t *testing.T) {
	storage := newEvaluationMemoryStorage()
	detail := newEvaluationTimeoutDetail(storage)
	service := &EvaluationService{
		config: &config.Config{
			Evaluation: &config.EvaluationConfig{TaskTimeout: time.Hour},
		},
		dataset:                  &evaluationTimeoutDatasetStub{},
		knowledgeService:         &evaluationTimeoutKnowledgeStub{},
		knowledgeBaseService:     &evaluationTimeoutKnowledgeBaseStub{},
		sessionService:           &evaluationTimeoutSessionStub{err: context.DeadlineExceeded},
		evaluationTaskRepository: storage,
		ownerID:                  storage.ownerID,
	}

	runErr := service.runEvaluation(
		types.WithExecutionTenant(context.Background(), detail.Task.TenantID),
		detail,
		"evaluation-kb",
	)
	if !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("runEvaluation() error = %v, want context.DeadlineExceeded", runErr)
	}
	requireEvaluationTerminal(
		t,
		storage,
		detail.Task.ID,
		types.EvaluationStatueFailed,
		context.DeadlineExceeded.Error(),
	)
}
