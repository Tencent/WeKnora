package doris

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestMoveIndicesRejectsNonAtomicANNReplacement(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()
	primeCompatMode(repo, dorisCompatModeInnerProductDuplicate, nil)
	require.ErrorContains(
		t,
		repo.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", []string{"chunk"}, 2, "document"),
		"use reparse mode",
	)
	require.NoError(t, mock.ExpectationsWereMet(), "rejection must issue no vector delete or insert")
}

func TestMoveIndicesLegacyUpdatesOnlyExactSourceDocument(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()
	primeCompatMode(repo, dorisCompatModeLegacy, nil)
	mock.ExpectExec("UPDATE `weknora_embeddings_2` SET knowledge_base_id = \\?, tag_id = '' WHERE "+
		"knowledge_base_id = \\? AND knowledge_id = \\?").
		WithArgs("target", "source", "doc").
		WillReturnResult(sqlmock.NewResult(0, 3))
	require.NoError(
		t,
		repo.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", []string{"chunk"}, 2, "document"),
	)
	require.NoError(t, mock.ExpectationsWereMet())
}
