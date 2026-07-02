package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type clearParserRulesRepoStub struct {
	interfaces.KnowledgeRepository
	knowledge     *types.Knowledge
	updatedColumn string
	updatedValue  interface{}
}

func (r *clearParserRulesRepoStub) GetKnowledgeByID(ctx context.Context, tenantID uint64, id string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *clearParserRulesRepoStub) UpdateKnowledgeColumn(ctx context.Context, id, column string, value interface{}) error {
	r.updatedColumn = column
	r.updatedValue = value
	return nil
}

func TestClearStoredParserEngineRules_RemovesRulesAndKeepsOtherOverrides(t *testing.T) {
	t.Parallel()

	k := &types.Knowledge{
		ID:       "k-1",
		TenantID: 1,
		Metadata: types.JSON(`{"process_overrides":{"parser_engine_rules":[{"file_types":["pdf"],"engine":"builtin"}],"chunking_config":{"chunk_size":1024}}}`),
	}
	repo := &clearParserRulesRepoStub{knowledge: k}
	svc := &knowledgeService{repo: repo}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	err := svc.clearStoredParserEngineRules(ctx, k.ID)

	require.NoError(t, err)
	require.Equal(t, "metadata", repo.updatedColumn)

	updated, err := k.ProcessOverrides()
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Empty(t, updated.ParserEngineRules)
	require.NotNil(t, updated.ChunkingConfig)
	require.Equal(t, 1024, updated.ChunkingConfig.ChunkSize)
}

func TestClearStoredParserEngineRules_NoOverridesIsNoOp(t *testing.T) {
	t.Parallel()

	k := &types.Knowledge{
		ID:       "k-2",
		TenantID: 1,
		Metadata: nil,
	}
	repo := &clearParserRulesRepoStub{knowledge: k}
	svc := &knowledgeService{repo: repo}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	err := svc.clearStoredParserEngineRules(ctx, k.ID)

	require.NoError(t, err)
	require.Empty(t, repo.updatedColumn)
}

func TestClearStoredParserEngineRules_MissingKnowledgeIsNoOp(t *testing.T) {
	t.Parallel()

	repo := &clearParserRulesRepoStub{knowledge: nil}
	svc := &knowledgeService{repo: repo}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	err := svc.clearStoredParserEngineRules(ctx, "missing-id")

	require.NoError(t, err)
	require.Empty(t, repo.updatedColumn)
}
