package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type manualAttemptTaskEnqueuer struct {
	task *asynq.Task
}

func (e *manualAttemptTaskEnqueuer) Enqueue(
	task *asynq.Task,
	_ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	e.task = task
	return &asynq.TaskInfo{ID: "manual-task"}, nil
}

func TestEnqueueManualProcessingCarriesReparseAttempt(t *testing.T) {
	enqueuer := &manualAttemptTaskEnqueuer{}
	svc := &knowledgeService{task: enqueuer}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        42,
		KnowledgeBaseID: "kb-1",
	}

	taskID, err := svc.enqueueManualProcessing(
		context.Background(),
		knowledge,
		"# unchanged content",
		true,
		7,
	)
	require.NoError(t, err)
	require.Equal(t, "manual-task", taskID)
	require.NotNil(t, enqueuer.task)
	require.Equal(t, types.TypeManualProcess, enqueuer.task.Type())

	var payload types.ManualProcessPayload
	require.NoError(t, json.Unmarshal(enqueuer.task.Payload(), &payload))
	require.Equal(t, 7, payload.Attempt)
	require.True(t, payload.NeedCleanup)
	require.Equal(t, knowledge.ID, payload.KnowledgeID)
}

type manualAttemptKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	updated *types.Knowledge
}

func (r *manualAttemptKnowledgeRepo) UpdateKnowledge(
	_ context.Context,
	knowledge *types.Knowledge,
) error {
	r.updated = knowledge
	return nil
}

type manualAttemptSpanTracker struct {
	noopSpanTracker
	knowledgeID  string
	attempt      int
	status       string
	errorCode    string
	errorMessage string
}

func (t *manualAttemptSpanTracker) FinalizeAttempt(
	_ context.Context,
	knowledgeID string,
	attempt int,
	status string,
	_ types.JSONMap,
	errorCode string,
	errorMessage string,
) {
	t.knowledgeID = knowledgeID
	t.attempt = attempt
	t.status = status
	t.errorCode = errorCode
	t.errorMessage = errorMessage
}

func TestFailManualProcessingAttemptClosesTrace(t *testing.T) {
	repo := &manualAttemptKnowledgeRepo{}
	tracker := &manualAttemptSpanTracker{}
	svc := &knowledgeService{
		repo:        repo,
		spanTracker: tracker,
	}
	knowledge := &types.Knowledge{ID: "knowledge-1"}

	svc.failManualProcessingAttempt(
		context.Background(),
		knowledge,
		6,
		"manual_cleanup_failed",
		"failed to cleanup old resources",
		errors.New("neo4j unavailable"),
	)

	require.Same(t, knowledge, repo.updated)
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
	require.Equal(
		t,
		"failed to cleanup old resources: neo4j unavailable",
		knowledge.ErrorMessage,
	)
	require.Equal(t, knowledge.ID, tracker.knowledgeID)
	require.Equal(t, 6, tracker.attempt)
	require.Equal(t, types.SpanStatusFailed, tracker.status)
	require.Equal(t, "manual_cleanup_failed", tracker.errorCode)
	require.Equal(t, knowledge.ErrorMessage, tracker.errorMessage)
}
