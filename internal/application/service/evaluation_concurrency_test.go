package service

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type evaluationConcurrentDataset struct {
	interfaces.DatasetService
	pairs []*types.QAPair
}

func (d evaluationConcurrentDataset) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	return d.pairs, nil
}

// evaluationConcurrentSession holds the first two QA calls at a barrier so
// they run at the same time, then fails the question named failQuestion.
type evaluationConcurrentSession struct {
	interfaces.SessionService
	failQuestion string
	failErr      error
	arrived      atomic.Int32
	overlap      chan struct{}
}

func (s *evaluationConcurrentSession) KnowledgeQAByEvent(
	_ context.Context, cm *types.ChatManage, _ []types.EventType,
) error {
	if s.arrived.Add(1) == 2 {
		close(s.overlap)
	}
	select {
	case <-s.overlap:
	case <-time.After(2 * time.Second):
	}
	if cm.Query == s.failQuestion {
		return s.failErr
	}
	return nil
}

func runConcurrentEvaluation(t *testing.T, failQuestion string, failErr error) (*EvaluationService, error) {
	t.Helper()
	previous := runtime.GOMAXPROCS(4)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })

	pairs := make([]*types.QAPair, 4)
	for i := range pairs {
		pairs[i] = &types.QAPair{
			QID: i, Question: fmt.Sprintf("q%d", i), PIDs: []int{i}, Passages: []string{"passage"},
		}
	}
	knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 7, KnowledgeBaseID: "kb-1"}
	svc := &EvaluationService{
		dataset: evaluationConcurrentDataset{pairs: pairs},
		knowledgeService: &evaluationCleanupKnowledge{
			repo: &evaluationCleanupKnowledgeRepo{knowledge: knowledge}, knowledge: knowledge,
		},
		knowledgeBaseService: &evaluationCleanupKB{},
		sessionService: &evaluationConcurrentSession{
			failQuestion: failQuestion, failErr: failErr, overlap: make(chan struct{}),
		},
		evaluationMemoryStorage: newEvaluationMemoryStorage(),
	}
	detail := &types.EvaluationDetail{
		Task:   &types.EvaluationTask{ID: "task-1", DatasetID: "dataset-1"},
		Params: &types.ChatManage{},
	}
	svc.evaluationMemoryStorage.register(detail)
	return svc, svc.EvalDataset(types.WithExecutionTenant(t.Context(), 7), detail, "kb-1")
}

func TestEvalDatasetReturnsFailureFromConcurrentWorker(t *testing.T) {
	failure := errors.New("qa pipeline failed")
	_, err := runConcurrentEvaluation(t, "q1", failure)
	require.ErrorIs(t, err, failure)
}

func TestEvalDatasetCountsEveryConcurrentWorker(t *testing.T) {
	svc, err := runConcurrentEvaluation(t, "", nil)
	require.NoError(t, err)
	detail, getErr := svc.evaluationMemoryStorage.get("task-1")
	require.NoError(t, getErr)
	require.Equal(t, 4, detail.Task.Finished)
}
