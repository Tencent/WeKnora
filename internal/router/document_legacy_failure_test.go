package router

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type legacyFailureStore struct {
	interfaces.KnowledgeRepository
	reads int
}

func (r *legacyFailureStore) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	r.reads++
	return nil, nil
}

type legacyFailureService struct {
	interfaces.KnowledgeService
	repo    legacyFailureStore
	handled bool
	calls   int
}

func (s *legacyFailureService) GetRepository() interfaces.KnowledgeRepository { return &s.repo }

func (s *legacyFailureService) FailLegacyDocument(context.Context, *asynq.Task, error) (bool, error) {
	s.calls++
	return s.handled, nil
}

func TestDocumentLegacyFailureDelegatesGenerationOwnership(t *testing.T) {
	for _, handled := range []bool{false, true} {
		s := &legacyFailureService{handled: handled}
		callback := newDeadLetterKnowledgeFailer(s, nil)
		require.NotNil(t, callback)
		callback(context.Background(), asynq.NewTask(types.TypeDocumentProcess,
			[]byte(`{"tenant_id":1,"knowledge_base_id":"kb","knowledge_id":"doc"}`)), errors.New("admission failed"))
		require.Equal(t, 1, s.calls)
		if handled {
			require.Zero(t, s.repo.reads, "generic failure code must not bypass the generation-aware decision")
		} else {
			require.Equal(t, 1, s.repo.reads, "keep the existing fallback when admission is not supported")
		}
	}
}
