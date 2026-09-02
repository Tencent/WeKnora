package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgegraph"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type graphStoreStub struct {
	graph        *knowledgegraph.Graph
	projectGraph *knowledgegraph.Graph
	projectCount int
}

func (stub graphStoreStub) ProjectVideo(context.Context, *model.Video, *weknora.WikiPage) error {
	return nil
}

func (stub *graphStoreStub) ProjectKnowledgeBase(context.Context) error {
	stub.projectCount++
	if stub.projectGraph != nil {
		stub.graph = stub.projectGraph
	}
	return nil
}

func (stub *graphStoreStub) Query(context.Context, knowledgegraph.Query) (*knowledgegraph.Graph, error) {
	return stub.graph, nil
}

func (stub *graphStoreStub) Close(context.Context) error {
	return nil
}

func TestEntityGraphUsesProjectedWikiPageAsStableIdentity(t *testing.T) {
	videoID := uuid.NewString()
	pageID := "method-page-1"
	wikiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages:      []weknora.WikiPage{{ID: pageID, Slug: "methodology/abnormal-attribution"}},
				TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/methodology/abnormal-attribution",
			"/api/v1/knowledgebase/kb-1/wiki/pages/methodology%2Fabnormal-attribution":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: pageID, Slug: "methodology/abnormal-attribution", PageType: "index", Title: "异常归因方法",
				Content: "---\nknowledge_object_id: V001-K001\ntype: methodology\nprimary_type: methodology\nsource_video_id: " + videoID + "\ntranscript_generation: generation-1\naudit_status: passed\nclassification_confidence: 0.92\n---\n# 异常归因方法\n\n核心内容：通过异常数据定位业务原因。\n\n### 方法论结构\n\n- 输入：留存曲线和渠道维度\n- 步骤：按渠道拆分；对比异常渠道；排查产品变更\n- 判断标准：变更时间与留存拐点接近\n- 输出：导致留存下降的变更项\n- 适用条件：单指标异常归因\n\n时间范围：00:03:00-00:04:00\n证据 ID：chunk-1\n信息性质：方法论",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer wikiServer.Close()

	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&model.Video{ID: videoID, Title: "方法论培训", VideoType: "training"}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{
		VideoID: videoID, Generation: "generation-1", ChunkIndex: 0, KnowledgeID: "chunk-1",
		StartMs: 180000, EndMs: 240000, ContentHash: "hash", Status: "completed",
	}).Error; err != nil {
		t.Fatalf("create chunk: %v", err)
	}

	handler := &EntityGraphHandler{
		db: db,
		graph: &graphStoreStub{graph: &knowledgegraph.Graph{
			Nodes: []knowledgegraph.Node{{
				ID: "wiki:" + pageID, WikiPageID: pageID, KnowledgeObjectID: "V001-K001",
				KnowledgeType: knowledge.TypeMethodology, Title: "异常归因方法",
				SourceVideoID: videoID, TranscriptGeneration: "generation-1", AuditStatus: "passed",
				ClassificationConfidence: 0.92, EvidenceIDs: []string{"chunk-1"},
			}},
		}},
		wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}),
		kbID: "kb-1",
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph?limit=10", nil)
	handler.Get(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			KnowledgeBaseID string            `json:"knowledge_base_id"`
			Nodes           []EntityGraphNode `json:"nodes"`
			Attributes      []string          `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || len(response.Data.Nodes) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if response.Data.KnowledgeBaseID != "kb-1" {
		t.Fatalf("knowledge_base_id = %q, want kb-1", response.Data.KnowledgeBaseID)
	}
	node := response.Data.Nodes[0]
	if node.ID != "wiki:"+pageID || node.WikiPageID != pageID || node.KnowledgeObjectID != "V001-K001" {
		t.Fatalf("node identity = %#v", node)
	}
	if node.KnowledgeType != knowledge.TypeMethodology || node.Type != "方法论" {
		t.Fatalf("node type = %#v", node)
	}
	if node.KnowledgeDetail == nil || node.KnowledgeDetail.ID != pageID || node.KnowledgeDetail.KnowledgeType != knowledge.TypeMethodology {
		t.Fatalf("node detail = %#v", node.KnowledgeDetail)
	}
	if node.KnowledgeDetail.PrimaryType != knowledge.TypeMethodology || node.KnowledgeDetail.SourceVideoTitle != "方法论培训" || node.KnowledgeDetail.Timestamp != "00:03:00" || node.KnowledgeDetail.Seconds != 180 {
		t.Fatalf("node detail source projection = %#v", node.KnowledgeDetail)
	}
	if node.Seconds != 180 || len(node.Evidence) != 1 || node.Evidence[0].ChunkIDs[0] != "chunk-1" {
		t.Fatalf("node evidence = %#v", node.Evidence)
	}
	if strings.Join(response.Data.Attributes, ",") != "实体,概念,案例,方法论,洞察" {
		t.Fatalf("attributes = %#v", response.Data.Attributes)
	}
}

func TestGraphKnowledgeDetailReadsLegacyStructureContainers(t *testing.T) {
	page := weknora.WikiPage{
		ID: "page-1", Slug: "concept/one", PageType: "index", Title: "关键概念",
		Content: `---
knowledge_object_id: object-1
type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
classification_confidence: 0.91
evidence_ids: [chunk-1]
concept_structure:
  definition: 来自旧字段容器的定义
  mechanism: 来自旧字段容器的机制
---
# 关键概念

核心内容：关键概念的原文概括。`,
	}

	detail := graphKnowledgeDetail(page)
	if detail == nil {
		t.Fatal("detail is nil")
	}
	if len(detail.StructureFields) != 2 {
		t.Fatalf("structure fields = %#v, want 2 fields", detail.StructureFields)
	}
	if detail.StructureFields[0].Key != "definition" || detail.StructureFields[0].Value != "来自旧字段容器的定义" {
		t.Fatalf("first field = %#v", detail.StructureFields[0])
	}
}

func TestStructuredRelationTargetsPreferWikiSlugWhenPresent(t *testing.T) {
	detail := &EntityGraphKnowledgeDetail{
		Relations: []knowledge.StructuredRelation{{
			RelationType:     "explains",
			TargetObjectID:   "object-2",
			TargetWikiPageID: "page-2",
		}},
	}
	enrichStructuredRelationTargets(detail.Relations, map[string]weknora.WikiPage{
		"page-2": {
			ID: "page-2", Slug: "methodology/agent-eval", Title: "Agent Eval", PageType: "index",
		},
	})
	if got := detail.Relations[0].TargetSlug; got != "methodology/agent-eval" {
		t.Fatalf("target slug = %q, want methodology/agent-eval", got)
	}
	if got := detail.Relations[0].TargetTitle; got != "Agent Eval" {
		t.Fatalf("target title = %q, want Agent Eval", got)
	}
}

func TestDisplayableGraphDetailRejectsOneCharacterGlossaryConcept(t *testing.T) {
	if displayableGraphDetail(&EntityGraphKnowledgeDetail{
		Title:         "实",
		KnowledgeType: knowledge.TypeConcept,
		CoreContent:   "本页定义“实”这一概念，指不仅知道某事物的存在。",
	}) {
		t.Fatal("one-character glossary concept must not be displayed in product graph")
	}
	if !displayableGraphDetail(&EntityGraphKnowledgeDetail{
		Title:         "熵",
		KnowledgeType: knowledge.TypeConcept,
		CoreContent:   "熵用于描述系统不确定性。",
		StructureFields: []knowledge.DetailField{{
			Key: "definition", Label: "定义", Value: "系统不确定性的度量",
		}},
	}) {
		t.Fatal("short concept with structure fields should remain displayable")
	}
}

func TestParseTranscriptEvidenceRef(t *testing.T) {
	ref := parseTranscriptEvidenceRef("chunk-831|transcript/video-1/generation-1/000831")
	if ref.KnowledgeID != "chunk-831" || ref.VideoID != "video-1" || ref.Generation != "generation-1" || ref.ChunkIndex != 831 || !ref.HasIndex {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestEntityGraphDoesNotFallbackToTitleForMissingWikiPage(t *testing.T) {
	wikiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/knowledgebase/kb-1/wiki/pages" {
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{Pages: []weknora.WikiPage{}, TotalPages: 1})
			return
		}
		http.NotFound(writer, request)
	}))
	defer wikiServer.Close()

	handler := &EntityGraphHandler{
		graph: &graphStoreStub{graph: &knowledgegraph.Graph{Nodes: []knowledgegraph.Node{{
			ID: "wiki:missing", WikiPageID: "missing", KnowledgeObjectID: "K1",
			KnowledgeType: knowledge.TypeConcept, Title: "同名知识", AuditStatus: "passed",
		}}}},
		wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}),
		kbID: "kb-1",
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph", nil)
	handler.Get(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Nodes []EntityGraphNode `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data.Nodes) != 0 {
		t.Fatalf("missing Wiki page must not be replaced by title match: %#v", response.Data.Nodes)
	}
}

func TestEntityGraphReturnsEmptySlicesForEmptyGraph(t *testing.T) {
	wikiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.NotFound(writer, request)
	}))
	defer wikiServer.Close()

	handler := &EntityGraphHandler{
		graph: &graphStoreStub{graph: &knowledgegraph.Graph{}},
		wiki:  weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}),
		kbID:  "kb-1",
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph", nil)
	handler.Get(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Nodes      []EntityGraphNode `json:"nodes"`
			Edges      []EntityGraphEdge `json:"edges"`
			Attributes []string          `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Data.Nodes == nil || response.Data.Edges == nil {
		t.Fatalf("empty graph slices must not be nil: %#v", response.Data)
	}
	if len(response.Data.Nodes) != 0 || len(response.Data.Edges) != 0 {
		t.Fatalf("empty graph response = %#v", response.Data)
	}
}

func TestEntityGraphProjectsKnowledgeBaseWhenNeo4jIsEmpty(t *testing.T) {
	wikiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages: []weknora.WikiPage{{ID: "legacy-page", Slug: "concept/legacy"}},
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/concept%2Flegacy", "/api/v1/knowledgebase/kb-1/wiki/pages/concept/legacy":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "legacy-page", Slug: "concept/legacy", PageType: "concept", Title: "历史概念",
				Content: "# 历史概念\n\n真实 Wiki 页面。",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer wikiServer.Close()

	store := &graphStoreStub{
		graph: &knowledgegraph.Graph{},
		projectGraph: &knowledgegraph.Graph{Nodes: []knowledgegraph.Node{{
			ID: "wiki:legacy-page", WikiPageID: "legacy-page", KnowledgeObjectID: "legacy-page",
			KnowledgeType: knowledge.TypeConcept, Title: "历史概念", AuditStatus: "passed",
			ClassificationConfidence: 1,
		}}},
	}
	handler := &EntityGraphHandler{
		graph: store,
		wiki:  weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}),
		kbID:  "kb-1",
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph", nil)
	handler.Get(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if store.projectCount != 1 {
		t.Fatalf("ProjectKnowledgeBase called %d times, want 1", store.projectCount)
	}
	var response struct {
		Data struct {
			Nodes []EntityGraphNode `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data.Nodes) != 1 || response.Data.Nodes[0].Type != "概念" {
		t.Fatalf("response nodes = %#v", response.Data.Nodes)
	}
}

func TestEntityGraphSeparatesSemanticEdgesReadingAssociationsAndOrphans(t *testing.T) {
	pageContent := func(id, typ, title string) string {
		return "---\nknowledge_object_id: " + id + "\ntype: " + typ + "\naudit_status: passed\nclassification_confidence: 0.9\n---\n# " + title + "\n\n正文内容。"
	}
	wikiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/knowledgebase/kb-1/wiki/pages" {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{Pages: []weknora.WikiPage{
			{ID: "page-a", Slug: "concept/a", Title: "概念 A", PageType: "index", Content: pageContent("object-a", "concept", "概念 A") + "\n关联 [[concept/b|概念 B]] 和 [[concept/missing|缺失页面]]。"},
			{ID: "page-b", Slug: "concept/b", Title: "概念 B", PageType: "index", Content: pageContent("object-b", "concept", "概念 B")},
			{ID: "page-orphan", Slug: "insight/orphan", Title: "孤岛洞察", PageType: "index", Content: pageContent("object-orphan", "insight", "孤岛洞察")},
		}, TotalPages: 1})
	}))
	defer wikiServer.Close()

	store := &graphStoreStub{graph: &knowledgegraph.Graph{
		Nodes: []knowledgegraph.Node{
			{ID: "wiki:page-a", WikiPageID: "page-a", KnowledgeObjectID: "object-a", KnowledgeType: knowledge.TypeConcept, AuditStatus: "passed"},
			{ID: "wiki:page-b", WikiPageID: "page-b", KnowledgeObjectID: "object-b", KnowledgeType: knowledge.TypeConcept, AuditStatus: "passed"},
			{ID: "wiki:page-orphan", WikiPageID: "page-orphan", KnowledgeObjectID: "object-orphan", KnowledgeType: knowledge.TypeInsight, AuditStatus: "passed"},
		},
		Edges: []knowledgegraph.Edge{{ID: "relation-1", SourceWikiPageID: "page-a", TargetWikiPageID: "page-b", RelationType: "explains", Confidence: 0.9}},
	}}
	handler := &EntityGraphHandler{graph: store, wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}), kbID: "kb-1"}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph", nil)
	handler.Get(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Nodes               []EntityGraphNode               `json:"nodes"`
			Edges               []EntityGraphEdge               `json:"edges"`
			ReadingAssociations []EntityGraphReadingAssociation `json:"reading_associations"`
			Meta                struct {
				SemanticEdgeCount       int `json:"semantic_edge_count"`
				ReadingAssociationCount int `json:"reading_association_count"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data.Edges) != 1 || response.Data.Meta.SemanticEdgeCount != 1 {
		t.Fatalf("semantic edges = %#v meta=%#v", response.Data.Edges, response.Data.Meta)
	}
	if response.Data.Edges[0].RelationKind != "semantic" || response.Data.Edges[0].RelationSource != "skill" || !response.Data.Edges[0].Counted {
		t.Fatalf("semantic edge contract = %#v", response.Data.Edges[0])
	}
	if len(response.Data.ReadingAssociations) != 2 || response.Data.Meta.ReadingAssociationCount != 2 {
		t.Fatalf("reading associations = %#v meta=%#v", response.Data.ReadingAssociations, response.Data.Meta)
	}
	var foundMissing bool
	for _, association := range response.Data.ReadingAssociations {
		if association.Target == "wiki:concept/missing" {
			foundMissing = true
			if association.TargetExists || association.Counted || association.RelationKind != "reading" || association.RelationSource != "wiki_link" {
				t.Fatalf("missing target association = %#v", association)
			}
		}
	}
	if !foundMissing {
		t.Fatalf("missing target reading association not returned: %#v", response.Data.ReadingAssociations)
	}
	for _, node := range response.Data.Nodes {
		if node.WikiPageID == "page-orphan" && (!node.IsOrphan || node.LinkCount != 0) {
			t.Fatalf("orphan node = %#v", node)
		}
		if node.WikiPageID == "page-a" && (node.IsOrphan || node.LinkCount != 1) {
			t.Fatalf("connected node = %#v", node)
		}
	}
}
