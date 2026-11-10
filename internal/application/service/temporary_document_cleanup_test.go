package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type cleanupFailureRepo struct {
	interfaces.TemporaryDocumentRepository
	documents []*types.TemporaryDocument
	deleteErr error
	listCalls int
}

func (r *cleanupFailureRepo) ListExpired(context.Context, time.Time, int) ([]*types.TemporaryDocument, error) {
	r.listCalls++
	if r.listCalls > 1 {
		return nil, errors.New("unexpected second expired batch query")
	}
	return r.documents, nil
}

func (r *cleanupFailureRepo) GetScoped(
	_ context.Context, _ uint64, _, documentID string,
) (*types.TemporaryDocument, error) {
	for _, document := range r.documents {
		if document.ID == documentID {
			return document, nil
		}
	}
	return nil, nil
}

func (r *cleanupFailureRepo) DeleteScoped(context.Context, uint64, string, string) error {
	return r.deleteErr
}

func TestCleanupExpiredStopsBatchAfterDeleteFailure(t *testing.T) {
	deleteErr := errors.New("database is read-only")
	documents := make([]*types.TemporaryDocument, 100)
	for i := range documents {
		documents[i] = &types.TemporaryDocument{
			ID: fmt.Sprintf("document-%d", i), TenantID: 7, SessionID: "session-1",
		}
	}
	repo := &cleanupFailureRepo{documents: documents, deleteErr: deleteErr}
	svc := &temporaryDocumentService{repo: repo}

	err := svc.CleanupExpired(t.Context())

	require.ErrorIs(t, err, deleteErr)
	require.Equal(t, 1, repo.listCalls)
}
