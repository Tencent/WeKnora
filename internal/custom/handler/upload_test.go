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
		ID:                  videoID,
		Title:               "clip",
		Status:              model.VideoStatusUploading,
		UploadID:            "test-upload",
		UploadSizeBytes:     defaultMultipartPartSize,
		UploadPartSizeBytes: defaultMultipartPartSize,
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
	body := bytes.Repeat([]byte("p"), int(defaultMultipartPartSize))
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
	videoID := uuid.NewString()
	if err := db.Create(&model.Video{
		ID:                  videoID,
		Title:               "clip",
		Status:              model.VideoStatusUploading,
		UploadID:            "upload-1",
		UploadSizeBytes:     defaultMultipartPartSize,
		UploadPartSizeBytes: defaultMultipartPartSize,
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
	body := []byte(`{
		"video_id":"` + videoID + `",
		"object_key":"videos/` + videoID + `/source.mp4",
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

func TestMultipartPartRejectsWrongContentLength(t *testing.T) {
	db := openTestVideoDB(t)
	videoID := uuid.NewString()
	if err := db.Create(&model.Video{
		ID:                  videoID,
		Title:               "clip",
		Status:              model.VideoStatusUploading,
		UploadID:            "test-upload",
		UploadSizeBytes:     defaultMultipartPartSize,
		UploadPartSizeBytes: defaultMultipartPartSize,
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
	req := httptest.NewRequest(http.MethodPut, "/api/custom/uploads/multipart/part", bytes.NewReader([]byte("short")))
	req.Header.Set("X-Video-ID", videoID)
	req.Header.Set("X-Object-Key", "videos/"+videoID+"/source.mp4")
	req.Header.Set("X-Upload-ID", "test-upload")
	req.Header.Set("X-Part-Number", "1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Content-Length")) {
		t.Fatalf("response does not explain Content-Length mismatch: %s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"part_number":1`)) {
		t.Fatalf("response does not identify part number: %s", rec.Body.String())
	}
}

func TestMultipartAbortMarksUploadingVideoFailed(t *testing.T) {
	db := openTestVideoDB(t)
	videoID := uuid.NewString()
	if err := db.Create(&model.Video{
		ID:     videoID,
		Title:  "orphan",
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
	body := []byte(`{
		"video_id":"` + videoID + `",
		"object_key":"videos/` + videoID + `/source.mp4",
		"upload_id":"upload-1",
		"reason":"browser_cancelled"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/custom/uploads/multipart/abort", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Upload-Trace-ID", "trace-test")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got model.Video
	if err := db.First(&got, "id = ?", videoID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if got.Status != model.VideoStatusFailed {
		t.Fatalf("video status = %q, want %q", got.Status, model.VideoStatusFailed)
	}
	if got.ProcessingErrorSummary != "browser_cancelled" {
		t.Fatalf("processing_error_summary = %q, want browser_cancelled", got.ProcessingErrorSummary)
	}
}
