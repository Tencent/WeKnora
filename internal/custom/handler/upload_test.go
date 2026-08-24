package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestMultipartPartUsesSameOriginBackendAndReturnsETag(t *testing.T) {
	db := openTestVideoDB(t)
	videoID := uuid.NewString()
	if err := db.Create(&model.Video{
		ID:     videoID,
		Title:  "clip",
		Status: model.VideoStatusUploading,
	}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}

	router := NewRouter(db, &config.Config{
		MinIO: config.MinIOConfig{
			Backend:   "local",
			LocalDir:  t.TempDir(),
			PublicURL: "http://127.0.0.1:8090/api/custom/files",
		},
	})
	body := []byte("part-content")
	req := httptest.NewRequest(http.MethodPut, "/api/custom/uploads/multipart/part", bytes.NewReader(body))
	req.Header.Set("X-Video-ID", videoID)
	req.Header.Set("X-Object-Key", "videos/"+videoID+"/source.mp4")
	req.Header.Set("X-Upload-ID", "test-upload")
	req.Header.Set("X-Part-Number", "1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Header().Get("ETag") == "" {
		t.Fatal("ETag header is empty")
	}
}
