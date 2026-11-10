package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDeleteFailureCallbackPreservesTaskScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	for _, row := range []*types.Knowledge{
		{ID: "doc", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusDeleting},
		{ID: "other-kb", TenantID: 7, KnowledgeBaseID: "other", ParseStatus: types.ParseStatusDeleting},
		{ID: "other-tenant", TenantID: 8, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusDeleting},
	} {
		require.NoError(t, db.Create(row).Error)
	}
	repo := repository.NewKnowledgeRepository(db)
	payload := types.KnowledgeListDeletePayload{
		TenantID:        7,
		KnowledgeBaseID: "kb",
		KnowledgeIDs:    []string{"doc", "other-kb", "other-tenant"},
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	task := asynq.NewTask(types.TypeKnowledgeListDelete, data)
	markKnowledgeListDeleteFailed(context.Background(), repo, task, fmt.Errorf("rejected scope: %w", asynq.SkipRetry))
	var count int64
	require.NoError(
		t,
		db.Model(&types.Knowledge{}).Where("parse_status = ?", types.ParseStatusFailed).Count(&count).Error,
	)
	require.Zero(t, count)
	markKnowledgeListDeleteFailed(context.Background(), repo, task, errors.New("graph unavailable"))
	row, err := repo.GetKnowledgeByID(context.Background(), 7, "doc")
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusFailed, row.ParseStatus)
	row, err = repo.GetKnowledgeByID(context.Background(), 7, "other-kb")
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusDeleting, row.ParseStatus)
	row, err = repo.GetKnowledgeByID(context.Background(), 8, "other-tenant")
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusDeleting, row.ParseStatus)
}
