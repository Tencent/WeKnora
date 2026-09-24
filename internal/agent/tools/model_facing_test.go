package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubKBLookup struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s stubKBLookup) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func TestMaskKnowledgeForModelCopiesTitle(t *testing.T) {
	t.Parallel()
	kb := &types.KnowledgeBase{
		ID: "kb-1",
		DesensitizationConfig: &types.DesensitizationConfig{
			Enabled:     true,
			Engine:      types.DesensitizationEngineBuiltin,
			EntityTypes: []string{types.DesensitizationEntityCNMobile},
		},
	}
	knowledge := &types.Knowledge{
		KnowledgeBaseID: "kb-1",
		Title:           "劳动合同-张三-13800138000",
		FileName:        "劳动合同-张三-13800138000.pdf",
		Description:     "电话 13800138000",
	}
	title, filename, desc, err := maskKnowledgeForModel(context.Background(), stubKBLookup{kb: kb}, nil, knowledge)
	if err != nil {
		t.Fatal(err)
	}
	if knowledge.Title != "劳动合同-张三-13800138000" ||
		knowledge.FileName != "劳动合同-张三-13800138000.pdf" {
		t.Fatalf("stored knowledge rewritten: %+v", knowledge)
	}
	for _, got := range []string{title, filename, desc} {
		if strings.Contains(got, "13800138000") {
			t.Fatalf("model-facing text leaked phone: %q", got)
		}
		if !strings.Contains(got, "<手机号>") {
			t.Fatalf("expected masked copy, got %q", got)
		}
	}
}

func TestApplyModelFacingTitlesLeavesOriginalSearchResult(t *testing.T) {
	t.Parallel()
	kb := &types.KnowledgeBase{
		ID: "kb-1",
		DesensitizationConfig: &types.DesensitizationConfig{
			Enabled:     true,
			Engine:      types.DesensitizationEngineBuiltin,
			EntityTypes: []string{types.DesensitizationEntityCNMobile},
		},
	}
	original := &types.SearchResult{
		KnowledgeBaseID: "kb-1",
		KnowledgeTitle:  "劳动合同-张三-13800138000",
	}
	tool := &SearchKnowledgeTool{knowledgeBaseService: stubKBLookup{kb: kb}}
	results := []*searchResultWithMeta{{SearchResult: original}}
	if err := tool.applyModelFacingTitles(context.Background(), results); err != nil {
		t.Fatal(err)
	}
	if original.KnowledgeTitle != "劳动合同-张三-13800138000" {
		t.Fatalf("original SearchResult title rewritten: %q", original.KnowledgeTitle)
	}
}
