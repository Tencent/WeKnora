package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type contentTestKnowledgeService struct {
	interfaces.KnowledgeService
	repository interfaces.KnowledgeRepository
	deleted    []string
}

func (s *contentTestKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repository
}

func (s *contentTestKnowledgeService) DeleteKnowledge(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func newContentTestManager(
	t *testing.T, rows ...*types.Knowledge,
) (*DataSourceContentManager, *contentTestKnowledgeService) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	for _, row := range rows {
		require.NoError(t, db.Create(row).Error)
	}
	service := &contentTestKnowledgeService{repository: repository.NewKnowledgeRepository(db)}
	return NewDataSourceContentManager(service, nil), service
}

func TestDataSourceContentFindScopesSameExternalIDToDataSource(t *testing.T) {
	manager, _ := newContentTestManager(t,
		&types.Knowledge{
			ID: "knowledge-a", TenantID: 7, KnowledgeBaseID: "kb",
			Metadata: types.JSON(`{"datasource_id":"ds-a","external_id":"same"}`),
		},
		&types.Knowledge{
			ID: "knowledge-b", TenantID: 7, KnowledgeBaseID: "kb",
			Metadata: types.JSON(`{"datasource_id":"ds-b","external_id":"same"}`),
		},
	)

	got, err := manager.Find(context.Background(), &types.DataSource{
		ID: "ds-b", TenantID: 7, KnowledgeBaseID: "kb",
	}, "same")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "knowledge-b", got.ID)
}

func TestDataSourceContentDeleteByDataSourceDoesNotCrossSourceBoundary(t *testing.T) {
	manager, service := newContentTestManager(t,
		&types.Knowledge{
			ID: "delete-me", TenantID: 7, KnowledgeBaseID: "kb",
			Metadata: types.JSON(`{"datasource_id":"ds-a","external_id":"one"}`),
		},
		&types.Knowledge{
			ID: "other-source", TenantID: 7, KnowledgeBaseID: "kb",
			Metadata: types.JSON(`{"datasource_id":"ds-b","external_id":"one"}`),
		},
		&types.Knowledge{
			ID: "other-kb", TenantID: 7, KnowledgeBaseID: "other",
			Metadata: types.JSON(`{"datasource_id":"ds-a","external_id":"one"}`),
		},
		&types.Knowledge{
			ID: "other-workspace", TenantID: 8, KnowledgeBaseID: "kb",
			Metadata: types.JSON(`{"datasource_id":"ds-a","external_id":"one"}`),
		},
	)

	deleted, err := manager.DeleteByDataSource(context.Background(), &types.DataSource{
		ID: "ds-a", TenantID: 7, KnowledgeBaseID: "kb",
	})
	require.NoError(t, err)
	require.Equal(t, 1, deleted)
	require.Equal(t, []string{"delete-me"}, service.deleted)
}
