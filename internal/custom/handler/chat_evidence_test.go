package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestChatEvidenceMapsKnowledgeIDsToVideoSource(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate chunks: %v", err)
	}
	video := model.Video{ID: uuid.NewString(), Title: "来源视频", ThumbnailURL: "https://cdn.example.com/cover.jpg", Status: model.VideoStatusCompleted}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	chunk := model.VideoTranscriptChunk{
		VideoID: video.ID, Generation: "generation-1", Revision: 1, ChunkIndex: 3,
		StartMs: 125000, EndMs: 130000, KnowledgeID: "knowledge-1", ContentHash: "hash", Status: "completed",
	}
	if err := db.Create(&chunk).Error; err != nil {
		t.Fatalf("create chunk: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/chat/evidence?knowledge_ids=knowledge-1", nil)

	NewChatEvidenceHandler(db).Lookup(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data []ChatEvidenceItem `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("data length = %d", len(payload.Data))
	}
	item := payload.Data[0]
	if item.KnowledgeID != "knowledge-1" || item.VideoID != video.ID || item.VideoTitle != video.Title || item.VideoCover == "" {
		t.Fatalf("unexpected evidence item: %#v", item)
	}
	if item.Seconds != 125 || item.Timestamp != "02:05" {
		t.Fatalf("time = %d/%q, want 125/02:05", item.Seconds, item.Timestamp)
	}
}
