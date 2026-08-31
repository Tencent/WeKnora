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
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type graphSourceStub struct {
	data *weknora.EntityGraphData
}

func TestEntityGraphReturnsSkillKnowledgeDetail(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate chunks: %v", err)
	}
	videoID := uuid.NewString()
	video := model.Video{
		ID: videoID, Title: "方法论培训", VideoType: "training", KnowledgeBaseWikiPageID: "knowledge-base-1",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{
		VideoID: videoID, Generation: "generation-1", ChunkIndex: 2, KnowledgeID: "transcript-knowledge-1",
		StartMs: 180000, EndMs: 240000, ContentHash: "hash", Status: "completed",
	}).Error; err != nil {
		t.Fatalf("create chunk: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/knowledgebase/kb-1/wiki/pages/video/"+videoID {
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "knowledge-base-1", Slug: "video/" + videoID, PageType: "index",
				Content: "---\ntype: knowledge_base\nsource_video_id: " + videoID + "\n---\n# 视频知识底座\n\n- [[异常归因方法]]",
			})
			return
		}
		if request.URL.Path != "/api/v1/knowledgebase/kb-1/wiki/pages" {
			http.NotFound(writer, request)
			return
		}
		pages := []weknora.WikiPage{{ID: "knowledge-base-1", Slug: "video/" + videoID, PageType: "index"}}
		if strings.TrimSpace(request.URL.Query().Get("page_type")) != "" {
			pages = append(pages, weknora.WikiPage{
				ID: "method-page-1", Slug: "methodology/abnormal-attribution", PageType: "methodology", Title: "异常归因方法",
				Content: "---\nid: V001-K001\ntype: methodology\nsource_video_id: " + videoID + "\ntitle: 异常归因方法\n---\n# 异常归因方法\n\n核心内容：通过异常数据定位业务原因。\n\n## 方法论结构\n\n- 输入：留存曲线和渠道维度\n- 步骤：按渠道拆分；对比异常渠道；排查产品变更\n- 判断标准：变更时间与留存拐点接近\n- 输出：导致留存下降的变更项\n- 适用条件：单指标异常归因\n\n时间范围：00:03:00-00:04:00\n证据 ID：E001、E002\n信息性质：归纳\n关联知识：[[留存诊断|留存诊断方法]]\n关联实体：[[增长团队]]",
			})
		}
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{Pages: pages, TotalPages: 1})
	}))
	defer server.Close()

	source := &weknora.EntityGraphData{Nodes: []*weknora.EntityGraphNode{{
		Name: "异常归因方法", KnowledgeID: "transcript-knowledge-1", Attributes: []string{"methodology"}, Chunks: []string{"chunk-1"},
	}}}
	handler := &EntityGraphHandler{
		db: db, graph: graphSourceStub{data: source}, wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL}), kbID: "kb-1",
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
			Nodes []EntityGraphNode `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || len(response.Data.Nodes) != 1 {
		t.Fatalf("response = %#v", response)
	}
	detail := response.Data.Nodes[0].KnowledgeDetail
	if detail == nil {
		t.Fatalf("knowledge_detail is nil: %#v", response.Data.Nodes[0])
	}
	if detail.ID != "method-page-1" || detail.KnowledgeType != "method" || detail.CoreContent != "通过异常数据定位业务原因。" {
		t.Fatalf("detail = %#v", detail)
	}
	if strings.Join(detail.EvidenceIDs, ",") != "E001,E002" || detail.InformationNature != "归纳" {
		t.Fatalf("evidence detail = %#v", detail)
	}
	if detail.TimeRange != "00:03:00-00:04:00" || len(detail.RelatedKnowledge) != 1 || detail.RelatedKnowledge[0].Title != "留存诊断方法" || detail.RelatedKnowledge[0].Slug != "留存诊断" || len(detail.RelatedEntities) != 1 || detail.RelatedEntities[0].Title != "增长团队" {
		t.Fatalf("wiki links = %#v", detail)
	}
	if len(detail.StructureFields) != 5 || detail.StructureFields[0].Key != "input" || detail.StructureFields[2].Key != "criteria" {
		t.Fatalf("structure fields = %#v", detail.StructureFields)
	}
}

func (stub graphSourceStub) GetEntityGraph(context.Context, string, int, []string) (*weknora.EntityGraphData, error) {
	return stub.data, nil
}

func TestEntityGraphMapsVideoEvidenceAndCanonicalTypes(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate chunks: %v", err)
	}
	videoID := uuid.NewString()
	if err := db.Create(&model.Video{ID: videoID, Title: "培训视频", VideoType: "training"}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{
		VideoID: videoID, Generation: "generation-1", ChunkIndex: 0, KnowledgeID: "knowledge-1",
		StartMs: 12500, EndMs: 18800, ContentHash: "hash", Status: "completed",
	}).Error; err != nil {
		t.Fatalf("create chunk: %v", err)
	}

	source := &weknora.EntityGraphData{
		Nodes: []*weknora.EntityGraphNode{
			{Name: "语义检索", KnowledgeID: "knowledge-1", Attributes: []string{"concept"}, Chunks: []string{"chunk-1"}},
			{Name: "知识网络", KnowledgeID: "knowledge-1", Attributes: []string{"entity"}},
		},
		Edges: []*weknora.EntityGraphEdge{{
			Node1: "语义检索", Node2: "知识网络", SourceKnowledgeID: "knowledge-1", TargetKnowledgeID: "knowledge-1", Type: "explains",
		}},
	}
	handler := &EntityGraphHandler{db: db, graph: graphSourceStub{data: source}, kbID: "kb-1"}
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
			Nodes []EntityGraphNode `json:"nodes"`
			Edges []EntityGraphEdge `json:"edges"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || len(response.Data.Nodes) != 2 || len(response.Data.Edges) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if response.Data.Nodes[0].Type != "概念" || response.Data.Nodes[0].VideoID != videoID || response.Data.Nodes[0].Seconds != 12 {
		t.Fatalf("node = %#v", response.Data.Nodes[0])
	}
	if len(response.Data.Nodes[0].Evidence) != 1 || response.Data.Nodes[0].Evidence[0].StartMs != 12500 {
		t.Fatalf("evidence = %#v", response.Data.Nodes[0].Evidence)
	}
}
