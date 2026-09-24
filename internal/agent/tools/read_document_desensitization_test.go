package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/types"
)

func desensitizedMobileKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID: "kb-1",
		DesensitizationConfig: &types.DesensitizationConfig{
			Enabled:     true,
			Engine:      types.DesensitizationEngineBuiltin,
			EntityTypes: []string{types.DesensitizationEntityCNMobile},
		},
	}
}

func TestReadDocumentModelOutputOmitsManualMetadataContent(t *testing.T) {
	t.Parallel()
	const phone = "13800138000"
	doc := &types.Knowledge{
		ID: "doc-manual", TenantID: 7, KnowledgeBaseID: "kb-1",
		Title: "劳动合同", Type: "passage", ParseStatus: "completed",
	}
	if err := doc.SetManualMetadata(types.NewManualKnowledgeMetadata(
		"联系人手机 "+phone, types.ManualKnowledgeStatusPublish, 1,
	)); err != nil {
		t.Fatal(err)
	}
	repo := &readDocChunkRepo{ordered: []*types.Chunk{{
		ID: "chunk-0", TenantID: 7, KnowledgeID: doc.ID, KnowledgeBaseID: "kb-1",
		ChunkIndex: 0, ChunkType: types.ChunkTypeText, IsEnabled: true,
		Content: "联系人手机 <手机号>",
	}}}
	tool := NewReadDocumentTool(
		&readDocKnowledgeService{docs: map[string]*types.Knowledge{doc.ID: doc}},
		&readDocChunkService{repo: repo},
		types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TenantID: 7}},
		stubKBLookup{kb: desensitizedMobileKB()},
		nil,
	)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-manual"}`))
	if err != nil || res == nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	modelOut := modelcontext.NewRegistry(true).ModelToolResultForTool(ToolReadDocument, res)
	if strings.Contains(modelOut, phone) {
		t.Fatalf("model output leaked stored manual content: %s", modelOut)
	}
	if strings.Contains(res.Output, phone) {
		t.Fatalf("tool output leaked stored manual content: %s", res.Output)
	}
	info, _ := res.Data["document"].(map[string]interface{})
	if metadata, ok := info["metadata"].(map[string]interface{}); ok {
		content, _ := metadata["content"].(string)
		if strings.Contains(content, phone) {
			t.Fatalf("document header still exports Metadata.content: %+v", metadata)
		}
	}

	stored, err := doc.ManualMetadata()
	if err != nil || stored == nil || !strings.Contains(stored.Content, phone) {
		t.Fatalf("stored Metadata.content was rewritten: %+v err=%v", stored, err)
	}
}

func TestReadDocumentModelOutputOmitsURLSourcePII(t *testing.T) {
	t.Parallel()
	const phone = "13800138000"
	rawURL := "https://example.com/docs?mobile=" + phone
	doc := &types.Knowledge{
		ID: "doc-url", TenantID: 7, KnowledgeBaseID: "kb-1",
		Title: "外链文档", Type: "url", Source: rawURL, ParseStatus: "completed",
	}
	repo := &readDocChunkRepo{ordered: []*types.Chunk{{
		ID: "chunk-0", TenantID: 7, KnowledgeID: doc.ID, KnowledgeBaseID: "kb-1",
		ChunkIndex: 0, ChunkType: types.ChunkTypeText, IsEnabled: true,
		Content: "正文 <手机号>",
	}}}
	tool := NewReadDocumentTool(
		&readDocKnowledgeService{docs: map[string]*types.Knowledge{doc.ID: doc}},
		&readDocChunkService{repo: repo},
		types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TenantID: 7}},
		stubKBLookup{kb: desensitizedMobileKB()},
		nil,
	)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-url"}`))
	if err != nil || res == nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	modelOut := modelcontext.NewRegistry(true).ModelToolResultForTool(ToolReadDocument, res)
	if strings.Contains(modelOut, phone) || strings.Contains(modelOut, rawURL) {
		t.Fatalf("model output leaked source URL: %s", modelOut)
	}
	if strings.Contains(res.Output, phone) || strings.Contains(res.Output, rawURL) {
		t.Fatalf("tool output leaked source URL: %s", res.Output)
	}
	if doc.Source != rawURL {
		t.Fatalf("stored Source was rewritten: %q", doc.Source)
	}
}
