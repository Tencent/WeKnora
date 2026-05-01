package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubKnowledgeBaseService struct {
	kb      *types.KnowledgeBase
	results []*types.SearchResult
}

func (s *stubKnowledgeBaseService) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *stubKnowledgeBaseService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s *stubKnowledgeBaseService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s *stubKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *stubKnowledgeBaseService) FillKnowledgeBaseCounts(context.Context, *types.KnowledgeBase) error {
	return nil
}

func (s *stubKnowledgeBaseService) ListKnowledgeBases(context.Context) ([]*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *stubKnowledgeBaseService) ListKnowledgeBasesByTenantID(context.Context, uint64) ([]*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *stubKnowledgeBaseService) UpdateKnowledgeBase(
	context.Context,
	string,
	string,
	string,
	*types.KnowledgeBaseConfig,
) (*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *stubKnowledgeBaseService) DeleteKnowledgeBase(context.Context, string) error {
	return nil
}

func (s *stubKnowledgeBaseService) TogglePinKnowledgeBase(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *stubKnowledgeBaseService) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return s.results, nil
}

func (s *stubKnowledgeBaseService) GetQueryEmbedding(context.Context, string, string) ([]float32, error) {
	return nil, nil
}

func (s *stubKnowledgeBaseService) ResolveEmbeddingModelKeys(context.Context, []*types.KnowledgeBase) map[string]string {
	return nil
}

func (s *stubKnowledgeBaseService) CopyKnowledgeBase(
	context.Context,
	string,
	string,
) (*types.KnowledgeBase, *types.KnowledgeBase, error) {
	return nil, nil, nil
}

func (s *stubKnowledgeBaseService) GetRepository() interfaces.KnowledgeBaseRepository {
	return nil
}

func (s *stubKnowledgeBaseService) ProcessKBDelete(context.Context, *asynq.Task) error {
	return nil
}

func TestQueryKnowledgeGraph_ReportsConfiguredEntityAndRelationTypes(t *testing.T) {
	tool := NewQueryKnowledgeGraphTool(&stubKnowledgeBaseService{
		kb: &types.KnowledgeBase{
			ID: "kb-1",
			ExtractConfig: &types.ExtractConfig{
				Enabled: true,
				Nodes: []*types.GraphNode{
					{Name: "合同"},
					{Name: "法务部门"},
					{Name: "审批流程"},
					{Name: "合同"},
					nil,
					{Name: ""},
				},
				Relations: []*types.GraphRelation{
					{Type: "属于"},
					{Type: "管理"},
					{Type: "审批"},
					{Type: "管理"},
					{Type: ""},
					nil,
				},
			},
		},
		results: []*types.SearchResult{
			{
				ID:             "chunk-approval-1",
				Content:        "合同审批流程由法务部门与采购部门共同维护，法务部门负责合规审查。",
				KnowledgeID:    "doc-approval",
				KnowledgeTitle: "合同审批管理制度",
				Score:          0.97,
				MatchType:      types.MatchTypeEmbedding,
			},
			{
				ID:             "chunk-approval-2",
				Content:        "采购申请提交后进入合同审批流程，审批完成后归档到合同台账。",
				KnowledgeID:    "doc-procurement",
				KnowledgeTitle: "采购与合同协作规范",
				Score:          0.89,
				MatchType:      types.MatchTypeKeywords,
			},
			{
				ID:             "chunk-approval-3",
				Content:        "法务部门管理标准合同模板，并维护合同风险审查清单。",
				KnowledgeID:    "doc-legal",
				KnowledgeTitle: "法务部职责说明",
				Score:          0.84,
				MatchType:      types.MatchTypeEmbedding,
			},
		},
	})

	args, err := json.Marshal(QueryKnowledgeGraphInput{
		KnowledgeBaseIDs: []string{"kb-1"},
		Query:            "合同审批与法务协作",
	})
	require.NoError(t, err)

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Success)
	t.Logf("tool output:\n%s", result.Output)

	assert.Contains(t, result.Output, "Entity Types (3)")
	assert.Contains(t, result.Output, "Relationship Types (3)")
	assert.NotContains(t, result.Output, "No entity types configured")
	assert.NotContains(t, result.Output, "No relationship types configured")
	assert.Contains(t, result.Output, "合同")
	assert.Contains(t, result.Output, "法务部门")
	assert.Contains(t, result.Output, "审批流程")
	assert.Contains(t, result.Output, "管理")
	assert.Contains(t, result.Output, "审批")
	assert.Contains(t, result.Output, "✓ Found 3 relevant results (deduplicated)")
	assert.Contains(t, result.Output, "Result #1:")
	assert.Contains(t, result.Output, "Result #2:")
	assert.Contains(t, result.Output, "Result #3:")
	assert.Contains(t, result.Output, "合同审批管理制度")

	graphConfig, ok := result.Data["graph_config"].(map[string]interface{})
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"合同", "审批流程", "法务部门"}, graphConfig["nodes"])
	assert.ElementsMatch(t, []string{"属于", "审批", "管理"}, graphConfig["relations"])
}
