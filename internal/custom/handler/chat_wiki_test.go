package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/gin-gonic/gin"
)

func TestChatWikiSearchReturnsOnlyPassedCurrentObjects(t *testing.T) {
	videoID := "video-1"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/knowledgebase/kb-1/wiki/pages" {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
			Pages: []weknora.WikiPage{
				{ID: "page-passed", Slug: "concept/passed", PageType: "index", Content: `---
knowledge_object_id: object-passed
type: concept
primary_type: concept
information_nature: 概念
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
classification_confidence: 0.9
evidence_ids: [chunk-1]
source_refs: [chunk-1]
structure_fields:
  definition: 可检索概念
  mechanism: 通过机制说明
---
# 可检索概念

核心内容：可检索概念。`},
				{ID: "page-failed", Slug: "concept/failed", PageType: "index", Content: `---
knowledge_object_id: object-failed
type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: failed
classification_confidence: 0.9
evidence_ids: [chunk-2]
source_refs: [chunk-2]
structure_fields:
  definition: 不应返回
  mechanism: 不应返回
---
# 不应返回`},
			},
			TotalPages: 1,
		})
	}))
	defer server.Close()

	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.Video{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&model.Video{ID: videoID, Title: "视频", TranscriptGeneration: "generation-1"}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/chat/wiki-search?q=可检索概念&video_id="+videoID, nil)
	handler := NewChatWikiHandler(db, weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL}), "kb-1")
	handler.Search(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data []ChatWikiSearchResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Data) != 1 || payload.Data[0].WikiPageID != "page-passed" {
		t.Fatalf("results = %#v", payload.Data)
	}
}
