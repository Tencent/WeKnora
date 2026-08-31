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

func TestChatScopeGlobalRequiresConfiguredKnowledgeBase(t *testing.T) {
	db := openTestVideoDB(t)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/chat/scope/global", nil)

	NewChatScopeHandler(db, "", "", "").Global(context)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestChatScopeGlobalReturnsConfiguredTenant(t *testing.T) {
	db := openTestVideoDB(t)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/chat/scope/global", nil)

	NewChatScopeHandler(db, "kb-1", "agent-1", "10000").Global(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data ChatScopeResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Data.TenantID != "10000" {
		t.Fatalf("tenant_id = %q, want 10000", payload.Data.TenantID)
	}
	if payload.Data.SessionMeta["tenant_id"] != "10000" {
		t.Fatalf("session_meta.tenant_id = %q, want 10000", payload.Data.SessionMeta["tenant_id"])
	}
}

func TestChatScopeVideoRejectsVideoWithoutRealKnowledge(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate chunks: %v", err)
	}
	video := model.Video{ID: uuid.NewString(), Title: "未解析视频", Status: model.VideoStatusUploaded}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/chat-scope", nil)

	NewChatScopeHandler(db, "kb-1", "", "").Video(context)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestChatScopeVideoReturnsCompletedTranscriptKnowledgeAndSourceMeta(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate chunks: %v", err)
	}
	video := model.Video{
		ID: uuid.NewString(), Title: "真实课程", Status: model.VideoStatusCompleted,
		ThumbnailURL: "https://cdn.example.com/cover.jpg", TranscriptGeneration: "generation-1",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	chunks := []model.VideoTranscriptChunk{
		{VideoID: video.ID, Generation: "generation-1", Revision: 1, ChunkIndex: 0, StartMs: 0, EndMs: 1000, KnowledgeID: "knowledge-1", ContentHash: "a", Status: "completed"},
		{VideoID: video.ID, Generation: "generation-1", Revision: 1, ChunkIndex: 1, StartMs: 1000, EndMs: 2000, KnowledgeID: "knowledge-2", ContentHash: "b", Status: "completed"},
		{VideoID: video.ID, Generation: "old", Revision: 0, ChunkIndex: 2, StartMs: 2000, EndMs: 3000, KnowledgeID: "old-knowledge", ContentHash: "c", Status: "completed"},
	}
	if err := db.Create(&chunks).Error; err != nil {
		t.Fatalf("create chunks: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/chat-scope", nil)

	NewChatScopeHandler(db, "kb-1", "agent-1", "10000").Video(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data ChatScopeResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Data.Scope != "video" || payload.Data.VideoID != video.ID || payload.Data.VideoTitle != video.Title {
		t.Fatalf("unexpected scope data: %#v", payload.Data)
	}
	if payload.Data.AgentID != "agent-1" {
		t.Fatalf("agent_id = %q, want agent-1", payload.Data.AgentID)
	}
	if payload.Data.TenantID != "10000" {
		t.Fatalf("tenant_id = %q, want 10000", payload.Data.TenantID)
	}
	if got, want := payload.Data.KnowledgeBaseIDs, []string{"kb-1"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("knowledge_base_ids = %#v, want %#v", got, want)
	}
	if got := payload.Data.KnowledgeIDs; len(got) != 2 || got[0] != "knowledge-1" || got[1] != "knowledge-2" {
		t.Fatalf("knowledge_ids = %#v", got)
	}
	if payload.Data.SessionMeta["scope"] != "video" || payload.Data.SessionMeta["video_cover_url"] == "" {
		t.Fatalf("session_meta = %#v", payload.Data.SessionMeta)
	}
}
