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

func TestMultipartCompleteRejectsInvalidPartsAsBadRequest(t *testing.T) {
	db := openTestVideoDB(t)
	router := NewRouter(db, &config.Config{
		MinIO: config.MinIOConfig{
			Backend:   "local",
			LocalDir:  t.TempDir(),
			PublicURL: "http://127.0.0.1:8090/api/custom/files",
		},
	})
	body := []byte(`{
		"video_id":"video-1",
		"object_key":"videos/video-1/source.mp4",
		"upload_id":"upload-1",
		"parts":[
			{"part_number":1,"etag":"etag-1"},
			{"part_number":1,"etag":"etag-1-retry"}
		]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/custom/uploads/multipart/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
