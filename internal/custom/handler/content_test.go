package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

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
