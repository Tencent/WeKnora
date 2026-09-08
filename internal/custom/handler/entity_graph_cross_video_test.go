package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestEntityGraphCrossVideoRequiresVideoID(t *testing.T) {
	h := &EntityGraphHandler{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph/cross-video", nil)
	h.CrossVideo(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() == "" {
		t.Fatal("expected structured error response")
	}
}
