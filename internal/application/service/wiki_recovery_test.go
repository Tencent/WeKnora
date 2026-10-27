package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wikiRecoveryRepoStub struct {
	repository.WikiRecoveryRepository
	count int
	err   error
}

func (r wikiRecoveryRepoStub) RetryDocuments(context.Context, uint64, string, []string) (int, error) {
	return r.count, r.err
}

type wikiRecoveryQueueStub struct {
	tasks []*asynq.Task
	err   error
}

func (q *wikiRecoveryQueueStub) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{}, q.err
}

func TestWikiRecoverySchedulesDurableAndRepeatedRequests(t *testing.T) {
	for _, n := range []int{0, 1} {
		queue := &wikiRecoveryQueueStub{}
		s := NewWikiRecoveryService(wikiRecoveryRepoStub{count: n}, queue)
		got, err := s.RetryDocuments(context.Background(), 7, "kb", []string{"doc"})
		require.NoError(t, err)
		assert.Equal(t, n, got)
		require.Len(t, queue.tasks, 1)
		var payload WikiIngestPayload
		require.NoError(t, json.Unmarshal(queue.tasks[0].Payload(), &payload))
		assert.Equal(t, uint64(7), payload.TenantID)
		assert.Equal(t, "kb", payload.KnowledgeBaseID)
	}
}

func TestWikiRecoveryReportsPersistenceAndSchedulingFailures(t *testing.T) {
	queue := &wikiRecoveryQueueStub{}
	s := NewWikiRecoveryService(wikiRecoveryRepoStub{err: errors.New("database unavailable")}, queue)
	_, err := s.RetryDocuments(context.Background(), 1, "kb", []string{"doc"})
	require.ErrorContains(t, err, "database unavailable")
	assert.Empty(t, queue.tasks)
	queue.err = errors.New("Redis unavailable")
	s = NewWikiRecoveryService(wikiRecoveryRepoStub{count: 1}, queue)
	n, err := s.RetryDocuments(context.Background(), 1, "kb", []string{"doc"})
	require.ErrorContains(t, err, "operations are saved")
	assert.Equal(t, 1, n)
}
