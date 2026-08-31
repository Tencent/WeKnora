package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type graphSourceStub struct {
	data *weknora.EntityGraphData
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
