package router

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// errorSplittingRuneAt8192 returns an error whose message, once prefixed,
// places byte 8192 inside a three-byte CJK rune.
func errorSplittingRuneAt8192(prefix string) error {
	return errors.New(strings.Repeat("a", 8192-len(prefix)-1) + strings.Repeat("中", 8))
}

func TestDeadLetterFailureMessageIsTruncatedAtRuneBoundary(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "callback.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	repo := repository.NewKnowledgeRepository(db)

	t.Run("document process", func(t *testing.T) {
		require.NoError(t, db.Create(&types.Knowledge{
			ID: "processing", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusProcessing,
		}).Error)
		payload, err := json.Marshal(
			types.DocumentProcessPayload{TenantID: 7, KnowledgeBaseID: "kb", KnowledgeID: "processing"},
		)
		require.NoError(t, err)
		taskErr := errorSplittingRuneAt8192("task " + types.TypeDocumentProcess + " exhausted retries: ")

		callback := newDeadLetterKnowledgeFailer(transferFailureKnowledge{repo: repo}, nil)
		callback(context.Background(), asynq.NewTask(types.TypeDocumentProcess, payload), taskErr)

		row, err := repo.GetKnowledgeByID(context.Background(), 7, "processing")
		require.NoError(t, err)
		require.Equal(t, types.ParseStatusFailed, row.ParseStatus)
		require.True(t, utf8.ValidString(row.ErrorMessage), "error_message must be valid UTF-8")
		require.LessOrEqual(t, len(row.ErrorMessage), 8192)
		require.Len(t, row.ErrorMessage, 8191)
	})

	t.Run("knowledge list delete", func(t *testing.T) {
		require.NoError(t, db.Create(&types.Knowledge{
			ID: "deleting", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusDeleting,
		}).Error)
		payload, err := json.Marshal(types.KnowledgeListDeletePayload{
			TenantID: 7, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"deleting"},
		})
		require.NoError(t, err)
		taskErr := errorSplittingRuneAt8192("delete task exhausted retries: ")

		markKnowledgeListDeleteFailed(
			context.Background(), repo, asynq.NewTask(types.TypeKnowledgeListDelete, payload), taskErr,
		)

		row, err := repo.GetKnowledgeByID(context.Background(), 7, "deleting")
		require.NoError(t, err)
		require.Equal(t, types.ParseStatusFailed, row.ParseStatus)
		require.True(t, utf8.ValidString(row.ErrorMessage), "error_message must be valid UTF-8")
		require.LessOrEqual(t, len(row.ErrorMessage), 8192)
		require.Len(t, row.ErrorMessage, 8191)
	})
}
