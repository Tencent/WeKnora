package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// baseReadKBService returns a scripted result for the one lookup the parse
// workers make before touching a document.
type baseReadKBService struct {
	interfaces.KnowledgeBaseService
	kb  *types.KnowledgeBase
	err error
}

func (s baseReadKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, s.err
}

// baseReadKnowledgeRepo records the terminal-state write the helper performs
// only when the knowledge base is actually gone.
type baseReadKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	updated     *types.Knowledge
	updateErr   error
	updateCalls int
}

func (r *baseReadKnowledgeRepo) UpdateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	r.updateCalls++
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = knowledge
	return nil
}

func TestSettleKnowledgeBaseReadFailure(t *testing.T) {
	newService := func(kbErr error, updateErr error) (*knowledgeService, *baseReadKnowledgeRepo) {
		repo := &baseReadKnowledgeRepo{updateErr: updateErr}
		return &knowledgeService{
			kbService: baseReadKBService{err: kbErr},
			repo:      repo,
		}, repo
	}
	newKnowledge := func() *types.Knowledge {
		return &types.Knowledge{
			ID: "k-1", TenantID: 7, KnowledgeBaseID: "kb-1",
			ParseStatus: types.ParseStatusProcessing,
		}
	}

	t.Run("a transient read error is returned for a queue retry", func(t *testing.T) {
		readErr := errors.New("dial tcp 10.0.0.1:5432: connection refused")
		svc, repo := newService(readErr, nil)
		knowledge := newKnowledge()

		err := svc.settleKnowledgeBaseReadFailure(context.Background(), knowledge, readErr, "ProcessDocument")

		require.ErrorIs(t, err, readErr)
		require.Contains(t, err.Error(), "read knowledge base kb-1")
		require.Zero(t, repo.updateCalls, "a transient read must not settle the row")
		require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus)
		require.Empty(t, knowledge.ErrorMessage)
	})

	t.Run("a deleted knowledge base settles the row as failed", func(t *testing.T) {
		readErr := fmt.Errorf("lookup: %w", repository.ErrKnowledgeBaseNotFound)
		svc, repo := newService(readErr, nil)
		knowledge := newKnowledge()

		require.NoError(t, svc.settleKnowledgeBaseReadFailure(
			context.Background(), knowledge, readErr, "ProcessManualUpdate"))

		require.Equal(t, 1, repo.updateCalls)
		require.Same(t, knowledge, repo.updated)
		require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
		require.Contains(t, knowledge.ErrorMessage, "failed to get knowledge base")
	})

	t.Run("a failed settle write is retried instead of acknowledged", func(t *testing.T) {
		readErr := repository.ErrKnowledgeBaseNotFound
		updateErr := errors.New("database is read-only")
		svc, repo := newService(readErr, updateErr)
		knowledge := newKnowledge()

		err := svc.settleKnowledgeBaseReadFailure(
			context.Background(), knowledge, readErr, "ProcessDocument")

		require.ErrorIs(t, err, updateErr)
		require.Contains(t, err.Error(), "mark knowledge k-1 failed")
		require.Equal(t, 1, repo.updateCalls)
		require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
	})
}
