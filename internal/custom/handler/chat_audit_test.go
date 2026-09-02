package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestChatSourceAuditIsIdempotent(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.ChatSourceAudit{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	record := func() {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/custom/chat/source-audit", strings.NewReader(`{
			"event_id":"event-1",
			"session_id":"session-1",
			"scope":"video",
			"source_mode":"wiki_and_chunk",
			"fallback_used":false,
			"references_found":2,
			"wiki_page_ids":["page-1"],
			"knowledge_object_ids":["object-1"],
			"transcript_chunk_ids":["chunk-1"]
		}`))
		ctx.Request.Header.Set("Content-Type", "application/json")
		NewChatAuditHandler(db).RecordSourceAudit(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	}

	record()
	record()

	var audits []model.ChatSourceAudit
	if err := db.Find(&audits).Error; err != nil {
		t.Fatalf("load audits: %v", err)
	}
	if len(audits) != 1 || audits[0].EventID != "event-1" || audits[0].SourceMode != "wiki_and_chunk" {
		t.Fatalf("audits = %#v", audits)
	}
}

func TestChatSourceAuditRejectsInvalidSourceMode(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.ChatSourceAudit{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/custom/chat/source-audit", strings.NewReader(`{
		"event_id":"event-1","session_id":"session-1","source_mode":"invented"
	}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	NewChatAuditHandler(db).RecordSourceAudit(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestChatSourceAuditModelJSONIsStable(t *testing.T) {
	audit := model.ChatSourceAudit{
		ID: uuid.NewString(), EventID: "event-1", WikiPageIDs: `["page-1"]`,
		KnowledgeObjectIDs: `["object-1"]`, TranscriptChunkIDs: `["chunk-1"]`,
	}
	raw, err := json.Marshal(audit)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"event_id":"event-1"`) {
		t.Fatalf("json = %s", raw)
	}
}
