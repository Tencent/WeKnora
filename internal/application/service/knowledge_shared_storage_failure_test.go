package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type failingSharedKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	err error
}

func (r *failingSharedKnowledgeRepo) GetKnowledgeBatch(context.Context, uint64, []string) ([]*types.Knowledge, error) {
	return []*types.Knowledge{{ID: "own", TenantID: 1, KnowledgeBaseID: "own-kb"}}, nil
}

func (r *failingSharedKnowledgeRepo) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return nil, r.err
}

func TestSharedDocumentReadsPropagateStorageFailure(t *testing.T) {
	for _, failure := range []error{errors.New("database unavailable"), repository.ErrKnowledgeNotFound} {
		repo := &failingSharedKnowledgeRepo{err: failure}
		svc := &knowledgeService{repo: repo}
		search := &knowledgeBaseService{kgRepo: repo}
		rows, err := svc.GetKnowledgeBatchWithSharedAccess(newSharedAccessContext(), 1, []string{"own", "shared"})
		found, searchErr := search.fetchKnowledgeDataWithShared(newSharedAccessContext(), 1, []string{"own", "shared"})
		if errors.Is(failure, repository.ErrKnowledgeNotFound) {
			require.NoError(t, err)
			require.NoError(t, searchErr)
			require.Len(t, rows, 1)
			require.Len(t, found, 1)
		} else {
			require.ErrorIs(t, err, failure)
			require.ErrorIs(t, searchErr, failure)
			require.Nil(t, rows)
			require.Nil(t, found)
		}
	}
}
