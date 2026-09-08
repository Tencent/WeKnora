package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
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
	queryErr     error
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

func (stub *graphStoreStub) Query(_ context.Context, query knowledgegraph.Query) (*knowledgegraph.Graph, error) {
	if stub.queryErr != nil {
		return nil, stub.queryErr
	}
	if stub.graph == nil {
		return nil, nil
	}
	requested := make(map[knowledge.KnowledgeType]struct{}, len(query.Types))
	for _, knowledgeType := range query.Types {
		requested[knowledgeType] = struct{}{}
	}
	result := &knowledgegraph.Graph{Stats: knowledgegraph.GraphStats{TypeCounts: make(map[string]int)}}
	for _, node := range stub.graph.Nodes {
		if query.VideoID != "" && node.SourceVideoID != query.VideoID {
			continue
		}
		result.Stats.ScopeTotal++
		if knowledge.IsKnowledgeType(node.KnowledgeType) {
			result.Stats.TypeCounts[string(node.KnowledgeType)]++
		} else {
			result.Stats.UnknownTypeCount++
		}
		if len(requested) > 0 {
			if _, ok := requested[node.KnowledgeType]; !ok {
				continue
			}
		}
		if query.WikiPageID != "" && node.WikiPageID != query.WikiPageID {
			continue
		}
		result.Stats.FilteredTotal++
		result.Nodes = append(result.Nodes, node)
	}
	sort.SliceStable(result.Nodes, func(i, j int) bool {
		if result.Nodes[i].Title == result.Nodes[j].Title {
			return result.Nodes[i].WikiPageID < result.Nodes[j].WikiPageID
		}
		return result.Nodes[i].Title < result.Nodes[j].Title
	})
	if query.Limit > 0 && len(result.Nodes) > query.Limit {
		result.Nodes = result.Nodes[:query.Limit]
	}
	visible := make(map[string]struct{}, len(result.Nodes))
	for _, node := range result.Nodes {
		visible[node.WikiPageID] = struct{}{}
	}
	for _, edge := range stub.graph.Edges {
		if query.WikiPageID != "" && (edge.SourceWikiPageID == query.WikiPageID || edge.TargetWikiPageID == query.WikiPageID) {
			result.Edges = append(result.Edges, edge)
			continue
		}
		_, sourceOK := visible[edge.SourceWikiPageID]
		_, targetOK := visible[edge.TargetWikiPageID]
		if sourceOK && targetOK {
			result.Edges = append(result.Edges, edge)
		}
	}
	return result, nil
}

func (stub *graphStoreStub) Close(context.Context) error {
	return nil
}

func TestEntityGraphUsesProjectedWikiPageAsStableIdentity(t *testing.T) {
	videoID := uuid.NewString()
	pageID := uuid.NewString()
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
	if err := db.Create(&model.Video{ID: videoID, Title: "方法论培训", VideoType: "training", TranscriptGeneration: "generation-1"}).Error; err != nil {
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
	if strings.Join(response.Data.Attributes, ",") != "实体,概念,方法论,案例,洞察" {
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
		db: openTestVideoDB(t),
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
		db:    openTestVideoDB(t),
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

func TestEntityGraphDoesNotProjectKnowledgeBaseWhenNeo4jIsEmpty(t *testing.T) {
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

	store := &graphStoreStub{graph: &knowledgegraph.Graph{}}
	handler := &EntityGraphHandler{
		db:    openTestVideoDB(t),
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
	if store.projectCount != 0 {
		t.Fatalf("read-only graph endpoint called ProjectKnowledgeBase %d times", store.projectCount)
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
		t.Fatalf("response nodes = %#v", response.Data.Nodes)
	}
}

func TestEntityGraphSeparatesSemanticEdgesReadingAssociationsAndOrphans(t *testing.T) {
	pageAID := "70000000-0000-4000-8000-000000000001"
	pageBID := "70000000-0000-4000-8000-000000000002"
	orphanPageID := "70000000-0000-4000-8000-000000000003"
	pageContent := func(id, typ, title string) string {
		return "---\nknowledge_object_id: " + id + "\ntype: " + typ + "\nsource_video_id: video-1\ntranscript_generation: generation-1\naudit_status: passed\nclassification_confidence: 0.9\nevidence_ids: [evs:" + id + "]\n---\n# " + title + "\n\n核心内容：正文内容。"
	}
	wikiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/knowledgebase/kb-1/wiki/pages" {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{Pages: []weknora.WikiPage{
			{ID: pageAID, Slug: "concept/a", Title: "概念 A", PageType: "index", Content: pageContent("object-a", "concept", "概念 A") + "\n关联 [[concept/b|概念 B]] 和 [[concept/missing|缺失页面]]。"},
			{ID: pageBID, Slug: "concept/b", Title: "概念 B", PageType: "index", Content: pageContent("object-b", "concept", "概念 B")},
			{ID: orphanPageID, Slug: "insight/orphan", Title: "孤岛洞察", PageType: "index", Content: pageContent("object-orphan", "insight", "孤岛洞察")},
		}, TotalPages: 1})
	}))
	defer wikiServer.Close()

	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate chunks: %v", err)
	}
	if err := db.Create(&model.Video{ID: "video-1", Title: "图谱视频", TranscriptGeneration: "generation-1"}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	for index, objectID := range []string{"object-a", "object-b", "object-orphan"} {
		if err := db.Create(&model.VideoTranscriptChunk{
			VideoID: "video-1", Generation: "generation-1", ChunkIndex: index,
			KnowledgeID: "knowledge-" + objectID, EvidenceSentenceID: "evs:" + objectID,
			StartMs: index * 1000, EndMs: (index + 1) * 1000, ContentHash: "hash-" + objectID, Status: "completed",
		}).Error; err != nil {
			t.Fatalf("create chunk: %v", err)
		}
	}
	store := &graphStoreStub{graph: &knowledgegraph.Graph{
		Nodes: []knowledgegraph.Node{
			{ID: "wiki:" + pageAID, WikiPageID: pageAID, KnowledgeObjectID: "object-a", KnowledgeType: knowledge.TypeConcept, SourceVideoID: "video-1", TranscriptGeneration: "generation-1", AuditStatus: "passed", EvidenceIDs: []string{"evs:object-a"}},
			{ID: "wiki:" + pageBID, WikiPageID: pageBID, KnowledgeObjectID: "object-b", KnowledgeType: knowledge.TypeConcept, SourceVideoID: "video-1", TranscriptGeneration: "generation-1", AuditStatus: "passed", EvidenceIDs: []string{"evs:object-b"}},
			{ID: "wiki:" + orphanPageID, WikiPageID: orphanPageID, KnowledgeObjectID: "object-orphan", KnowledgeType: knowledge.TypeInsight, SourceVideoID: "video-1", TranscriptGeneration: "generation-1", AuditStatus: "passed", EvidenceIDs: []string{"evs:object-orphan"}},
		},
		Edges: []knowledgegraph.Edge{{
			ID: "relation-1", SourceWikiPageID: pageAID, TargetWikiPageID: pageBID,
			RelationType: "explains", Confidence: 0.9, EvidenceIDs: []string{"evs:object-a"},
		}},
	}}
	handler := &EntityGraphHandler{db: db, graph: store, wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}), kbID: "kb-1"}
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
	var foundExisting, foundMissing bool
	for _, association := range response.Data.ReadingAssociations {
		if association.Target == "wiki:"+pageBID {
			foundExisting = true
			if !association.TargetExists || association.TargetTitle != "概念 B" || association.TargetSlug != "concept/b" {
				t.Fatalf("existing target association = %#v", association)
			}
		}
		if association.Target == "wiki:concept/missing" {
			foundMissing = true
			if association.TargetExists || association.TargetTitle != "" || association.TargetSlug != "" || association.Counted || association.RelationKind != "reading" || association.RelationSource != "wiki_link" {
				t.Fatalf("missing target association = %#v", association)
			}
		}
	}
	if !foundExisting || !foundMissing {
		t.Fatalf("missing target reading association not returned: %#v", response.Data.ReadingAssociations)
	}
	for _, node := range response.Data.Nodes {
		if node.WikiPageID == orphanPageID && (!node.IsOrphan || node.LinkCount != 0) {
			t.Fatalf("orphan node = %#v", node)
		}
		if node.WikiPageID == pageAID && (node.IsOrphan || node.LinkCount != 1) {
			t.Fatalf("connected node = %#v", node)
		}
	}
}

func TestEntityGraphSemanticallyNormalizesFivePagesIntoTwoKnowledgeObjects(t *testing.T) {
	nodes := []EntityGraphNode{
		semanticGraphNode("page-deepseek", "deepseek-canonical", knowledge.TypeEntity, "product", "DeepSeek", "DeepSeek 是可读写本地文件并调用工具的 AI 助手。", "ev-deepseek-1"),
		semanticGraphNode("page-deepseek-entity", "deepseek-duplicate", knowledge.TypeEntity, "product", "DeepSeek（实体）", "视频中的 DeepSeek 是能够读写本地文件、调用工具的 AI Agent 配套工具。", "ev-deepseek-2"),
		semanticGraphNode("page-brain", "brain-canonical", knowledge.TypeConcept, "", "第二大脑", "第二大脑是由本地知识库和 AI Agent 组成、能够调用知识并执行工作的系统。", "ev-brain-1"),
		semanticGraphNode("page-brain-concept", "brain-duplicate-1", knowledge.TypeConcept, "", "第二大脑（概念）", "第二大脑是让 AI Agent 调用本地知识并执行工作的知识系统。", "ev-brain-2"),
		semanticGraphNode("page-agent-brain", "brain-duplicate-2", knowledge.TypeConcept, "", "AI Agent 第二大脑（概念）", "接入 AI Agent 后，本地知识库升级为能够调用知识并执行工作的第二大脑。", "ev-brain-3"),
	}

	aggregated, canonicalByPageID := aggregateSemanticGraphNodes(nodes)
	if len(aggregated) != 2 {
		t.Fatalf("semantic graph nodes = %#v, want 2 objects", aggregated)
	}
	byTitle := make(map[string]EntityGraphNode, len(aggregated))
	for _, node := range aggregated {
		byTitle[node.Name] = node
	}
	if len(byTitle["DeepSeek"].Evidence) != 2 || len(byTitle["第二大脑"].Evidence) != 3 {
		t.Fatalf("merged evidence = DeepSeek:%#v 第二大脑:%#v", byTitle["DeepSeek"].Evidence, byTitle["第二大脑"].Evidence)
	}
	if canonicalByPageID["page-deepseek-entity"] != "page-deepseek" || canonicalByPageID["page-agent-brain"] != "page-brain" {
		t.Fatalf("canonical page mapping = %#v", canonicalByPageID)
	}

	edges := canonicalizeSemanticGraphEdges([]EntityGraphEdge{
		{ID: "self-after-merge", Source: "wiki:page-deepseek", Target: "wiki:page-deepseek-entity", Type: "complements", EvidenceIDs: []string{"ev-deepseek-1"}},
		{ID: "relation-a", Source: "wiki:page-brain", Target: "wiki:page-deepseek", Type: "involves", EvidenceIDs: []string{"ev-brain-1"}, Confidence: 0.8},
		{ID: "relation-b", Source: "wiki:page-brain-concept", Target: "wiki:page-deepseek-entity", Type: "involves", EvidenceIDs: []string{"ev-brain-2"}, Confidence: 0.9},
	}, canonicalByPageID)
	if len(edges) != 1 {
		t.Fatalf("canonical graph edges = %#v, want one deduplicated non-self edge", edges)
	}
	if edges[0].Source != "wiki:page-brain" || edges[0].Target != "wiki:page-deepseek" || edges[0].Confidence != 0.9 {
		t.Fatalf("canonical edge = %#v", edges[0])
	}
	if strings.Join(edges[0].EvidenceIDs, ",") != "ev-brain-1,ev-brain-2" {
		t.Fatalf("canonical edge evidence = %#v", edges[0].EvidenceIDs)
	}
}

func semanticGraphNode(pageID, objectID string, knowledgeType knowledge.KnowledgeType, entitySubType, title, core, evidenceID string) EntityGraphNode {
	fields := []knowledge.DetailField{{Key: "definition", Value: core}, {Key: "mechanism", Value: "调用知识并执行工作"}}
	if knowledgeType == knowledge.TypeEntity {
		fields = []knowledge.DetailField{{Key: "product_type", Value: "AI 编程与工具调用助手"}, {Key: "core_function", Value: "读写本地文件并调用工具"}}
	}
	evidence := EntityGraphEvidence{VideoID: "video-1", TranscriptGeneration: "generation-1", EvidenceSentenceID: evidenceID, KnowledgeID: evidenceID}
	detail := &EntityGraphKnowledgeDetail{
		ID: pageID, KnowledgeObjectID: objectID, Title: title, KnowledgeType: knowledgeType,
		PrimaryType: knowledgeType, EntitySubType: entitySubType, CoreContent: core,
		StructureFields: fields, EvidenceIDs: []string{evidenceID},
	}
	return EntityGraphNode{
		ID: "wiki:" + pageID, Name: title, Label: title, KnowledgeType: knowledgeType,
		WikiPageID: pageID, KnowledgeObjectID: objectID, Evidence: []EntityGraphEvidence{evidence},
		KnowledgeDetail: detail,
	}
}
