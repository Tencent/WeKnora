package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestSummaryNotGeneratedReturnsStructuredStageStatus(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{ID: uuid.NewString(), Title: "video", Status: model.VideoStatusProcessing}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/summary", nil)

	NewContentHandler(db, nil, "kb-1").Summary(context)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Status       string `json:"status"`
		Stage        string `json:"stage"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Status != "failed" || payload.Stage != "summary" || payload.ErrorCode != "not_generated" || payload.ErrorMessage == "" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRelatedKnowledgeNotGeneratedReturnsStructuredStageStatus(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{ID: uuid.NewString(), Title: "video", Status: model.VideoStatusProcessing}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/related-knowledge", nil)

	wiki := weknora.NewWikiClient(config.WeKnoraConfig{})
	NewContentHandler(db, wiki, "kb-1").RelatedKnowledge(context)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Status       string `json:"status"`
		Stage        string `json:"stage"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Status != "failed" || payload.Stage != "graph" || payload.ErrorCode != "not_generated" || payload.ErrorMessage == "" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRelatedKnowledgeReturnsAnchorTimelineFromWikiContent(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "video", Status: model.VideoStatusProcessing,
		KnowledgeBaseWikiPageID: "knowledge-base-1",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/knowledgebase/kb-1/wiki/pages" {
			http.NotFound(writer, request)
			return
		}
		var pages []weknora.WikiPage
		switch request.URL.Query().Get("page_type") {
		case "entity":
			pages = []weknora.WikiPage{{
				ID: "entity-1", Title: "张三", PageType: "entity",
				Content: "---\ntype: person\nsource_video_id: " + video.ID + "\n---\n# 张三\n\n时间范围：00:01:02–00:01:10",
			}}
		case "index":
			pages = []weknora.WikiPage{{
				ID: "case-1", Title: "复盘案例", PageType: "index",
				Content: "---\ntype: case\nsource_video_id: " + video.ID + "\n---\n# 复盘案例\n\n时间范围：00:02:03–00:02:30",
			}}
		}
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{Pages: pages, TotalPages: 1})
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/related-knowledge", nil)
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})

	NewContentHandler(db, wiki, "kb-1").RelatedKnowledge(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Anchors map[string][]struct {
			Timestamp string `json:"timestamp"`
			Seconds   int    `json:"seconds"`
		} `json:"anchors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Anchors["entity"]) != 1 || payload.Anchors["entity"][0].Timestamp != "00:01:02" || payload.Anchors["entity"][0].Seconds != 62 {
		t.Fatalf("entity anchors = %#v", payload.Anchors["entity"])
	}
	if len(payload.Anchors["case"]) != 1 || payload.Anchors["case"][0].Timestamp != "00:02:03" || payload.Anchors["case"][0].Seconds != 123 {
		t.Fatalf("case anchors = %#v", payload.Anchors["case"])
	}
}
