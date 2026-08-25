package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	miniosdk "github.com/minio/minio-go/v7"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
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
	var got model.Video
	if err := db.First(&got, "id = ?", videoID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if got.Status != model.VideoStatusFailed {
		t.Fatalf("video status = %q, want %q", got.Status, model.VideoStatusFailed)
	}
	if got.ProcessingErrorSummary == "" {
		t.Fatal("processing_error_summary is empty")
	}
}

func TestMultipartCompleteRecoversAlreadyMergedObjectIdempotently(t *testing.T) {
	db := openTestVideoDB(t)
	videoID := uuid.NewString()
	objectKey := "videos/" + videoID + "/source.mp4"
	storageDir := t.TempDir()
	cfg := config.Config{MinIO: config.MinIOConfig{
		Backend:   "local",
		LocalDir:  storageDir,
		PublicURL: "http://127.0.0.1:8090/api/custom/files",
	}}
	client, err := objstore.New(cfg.MinIO)
	if err != nil {
		t.Fatalf("new minio client: %v", err)
	}
	if _, err := client.PutObject(t.Context(), objectKey, bytes.NewReader([]byte("merged")), int64(len("merged")), miniosdk.PutObjectOptions{}); err != nil {
		t.Fatalf("write merged object: %v", err)
	}
	if err := db.Create(&model.Video{
		ID:                  videoID,
		Title:               "clip",
		Status:              model.VideoStatusUploading,
		UploadID:            "upload-1",
		UploadObjectKey:     objectKey,
		UploadSizeBytes:     defaultMultipartPartSize,
		UploadPartSizeBytes: defaultMultipartPartSize,
	}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}

	router := NewRouter(db, &cfg)
	body := []byte(`{"video_id":"` + videoID + `","object_key":"` + objectKey + `","upload_id":"upload-1","parts":[{"part_number":1,"etag":"etag-1"}]}`)
	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/custom/uploads/multipart/complete", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want %d, body = %s", attempt+1, rec.Code, http.StatusOK, rec.Body.String())
		}
	}

	var jobs []model.VideoProcessingJob
	if err := db.Where("video_id = ? AND job_type = ?", videoID, "thumbnail").Find(&jobs).Error; err != nil {
		t.Fatalf("load jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("thumbnail job count = %d, want 1", len(jobs))
	}
	var video model.Video
	if err := db.First(&video, "id = ?", videoID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if video.Status != model.VideoStatusUploaded {
		t.Fatalf("video status = %q, want %q", video.Status, model.VideoStatusUploaded)
	}
}

func TestRetryInitialProcessingIsIdempotent(t *testing.T) {
	db := openTestVideoDB(t)
	videoID := uuid.NewString()
	objectKey := "videos/" + videoID + "/source.mp4"
	storageDir := t.TempDir()
	cfg := config.Config{MinIO: config.MinIOConfig{
		Backend:   "local",
		LocalDir:  storageDir,
		PublicURL: "http://127.0.0.1:8090/api/custom/files",
	}}
	client, err := objstore.New(cfg.MinIO)
	if err != nil {
		t.Fatalf("new minio client: %v", err)
	}
	if _, err := client.PutObject(t.Context(), objectKey, bytes.NewReader([]byte("source")), int64(len("source")), miniosdk.PutObjectOptions{}); err != nil {
		t.Fatalf("write source object: %v", err)
	}
	if err := db.Create(&model.Video{
		ID:              videoID,
		Title:           "retry",
		Status:          model.VideoStatusFailed,
		UploadObjectKey: objectKey,
	}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}

	router := NewRouter(db, &cfg)
	var first struct {
		JobID string `json:"job_id"`
	}
	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/custom/videos/"+videoID+"/retry-initial-processing", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want %d, body = %s", attempt+1, rec.Code, http.StatusOK, rec.Body.String())
		}
		var payload struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if attempt == 0 {
			first.JobID = payload.JobID
		} else if payload.JobID != first.JobID {
			t.Fatalf("retry job id = %q, want %q", payload.JobID, first.JobID)
		}
	}

	var jobs []model.VideoProcessingJob
	if err := db.Where("video_id = ? AND job_type = ?", videoID, "thumbnail").Find(&jobs).Error; err != nil {
		t.Fatalf("load jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("thumbnail job count = %d, want 1", len(jobs))
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
