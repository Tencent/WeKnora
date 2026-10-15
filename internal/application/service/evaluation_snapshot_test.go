package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestEvaluationResultsAreIndependentSnapshots(t *testing.T) {
	s := newEvaluationMemoryStorage()
	initial := &types.EvaluationDetail{Task: &types.EvaluationTask{ID: "t1", TenantID: 1}}
	s.register(initial)
	initial.Task.TenantID = 2
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 100 {
			if err := s.update("t1", func(d *types.EvaluationDetail) { d.Task.Finished = i }); err != nil {
				t.Error(err)
			}
		}
	})
	for range 100 {
		snapshot, err := s.get("t1")
		require.NoError(t, err)
		require.EqualValues(t, 1, snapshot.Task.TenantID)
		snapshot.Task.Finished = -1
	}
	wg.Wait()
	final, err := s.get("t1")
	require.NoError(t, err)
	require.Equal(t, 99, final.Task.Finished)
}

func TestAssistantEvaluationHasStableSampleAndIndexBudget(t *testing.T) {
	input := []*types.QAPair{
		{QID: 3, PIDs: []int{999999999}, Passages: []string{"third"}},
		{QID: 1, PIDs: []int{8}, Passages: []string{"first"}},
		{QID: 2, PIDs: []int{200000000}, Passages: []string{"second"}},
	}
	rows, passages, err := boundedEvaluationSample(input, 2)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, 1, rows[0].QID)
	require.Equal(t, []string{"first", "second"}, passages)
	require.Equal(t, 3, input[0].QID, "do not reorder the provider's shared slice")
	oversized := &types.QAPair{PIDs: []int{1}, Passages: []string{strings.Repeat("x", (2<<20)+1)}}
	_, _, err = boundedEvaluationSample([]*types.QAPair{oversized}, 20)
	require.Error(t, err)
}

// Embedded interfaces make unexpected calls fail rather than masking them.
type failingEvaluationDataset struct{ interfaces.DatasetService }

func (*failingEvaluationDataset) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	return nil, errors.New("dataset unavailable")
}

type evaluationCleanupKB struct {
	interfaces.KnowledgeBaseService
	deleted  string
	canceled error
}

func (k *evaluationCleanupKB) DeleteKnowledgeBase(ctx context.Context, id string) error {
	k.deleted, k.canceled = id, ctx.Err()
	return nil
}

func TestEvaluationFailureCleansTemporaryKB(t *testing.T) {
	kb := &evaluationCleanupKB{}
	svc := &EvaluationService{dataset: &failingEvaluationDataset{}, knowledgeBaseService: kb}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := svc.EvalDataset(ctx, &types.EvaluationDetail{Task: &types.EvaluationTask{ID: "task"}}, "temporary", 20)
	require.ErrorContains(t, err, "dataset unavailable")
	require.Equal(t, "temporary", kb.deleted)
	require.NoError(t, kb.canceled)
}

func TestEvaluationResultRejectsOtherWorkspace(t *testing.T) {
	svc := &EvaluationService{evaluationMemoryStorage: newEvaluationMemoryStorage()}
	svc.evaluationMemoryStorage.register(&types.EvaluationDetail{Task: &types.EvaluationTask{ID: "task", TenantID: 1}})
	_, err := svc.EvaluationResult(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2)), "task")
	require.Error(t, err)
}
