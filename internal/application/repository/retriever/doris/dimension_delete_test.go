package doris

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDeleteByKnowledgeIDList_ContinuesAfterShardFailure(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema\.tables WHERE TABLE_SCHEMA = \?`).
		WithArgs("weknora").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).
			AddRow("weknora_embeddings_1024").
			AddRow("weknora_embeddings_1536"))
	mock.ExpectExec("DELETE FROM `weknora_embeddings_1024` WHERE knowledge_id IN \\(\\?\\)").
		WithArgs("knowledge-1").
		WillReturnError(errors.New("shard unavailable"))
	mock.ExpectExec("DELETE FROM `weknora_embeddings_1536` WHERE knowledge_id IN \\(\\?\\)").
		WithArgs("knowledge-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.DeleteByKnowledgeIDList(context.Background(), []string{"knowledge-1"}, 0, "")
	require.NoError(t, err, "a failed shard must not prevent deletion attempts on other shards")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteByKnowledgeIDList_ReturnsErrorWhenAllShardsFail(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema\.tables WHERE TABLE_SCHEMA = \?`).
		WithArgs("weknora").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("weknora_embeddings_1024"))
	mock.ExpectExec("DELETE FROM `weknora_embeddings_1024` WHERE knowledge_id IN \\(\\?\\)").
		WithArgs("knowledge-1").
		WillReturnError(errors.New("shard unavailable"))

	err := repo.DeleteByKnowledgeIDList(context.Background(), []string{"knowledge-1"}, 0, "")
	require.Error(t, err)
	require.ErrorContains(t, err, "failed for all 1 embedding tables")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteByKnowledgeIDList_DeletesAcrossExistingDimensionTables(t *testing.T) {
	repo, mock, _, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT TABLE_NAME FROM information_schema\.tables WHERE TABLE_SCHEMA = \?`).
		WithArgs("weknora").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).
			AddRow("weknora_embeddings_1536").
			AddRow("weknora_embeddings_1024").
			AddRow("weknora_embeddings_archive").
			AddRow("weknora_embeddings_1536_backup"))
	mock.ExpectExec("DELETE FROM `weknora_embeddings_1024` WHERE knowledge_id IN \\(\\?\\)").
		WithArgs("knowledge-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM `weknora_embeddings_1536` WHERE knowledge_id IN \\(\\?\\)").
		WithArgs("knowledge-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.DeleteByKnowledgeIDList(context.Background(), []string{"knowledge-1"}, 0, "")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
