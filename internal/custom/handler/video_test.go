package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestVideoListReturnsOnlyInitiallyAvailableVideos(t *testing.T) {
	db := openTestVideoDB(t)
	now := time.Now().UTC()
	videos := []model.Video{
		{ID: uuid.NewString(), Title: "uploaded", Status: model.VideoStatusUploaded, CreatedAt: now.Add(-4 * time.Minute)},
		{ID: uuid.NewString(), Title: "completed", Status: model.VideoStatusCompleted, FileURL: "source", ThumbnailURL: "poster", DurationSeconds: 30, CreatedAt: now.Add(-3 * time.Minute)},
		{ID: uuid.NewString(), Title: "processing", Status: model.VideoStatusProcessing, FileURL: "source", ThumbnailURL: "poster", DurationSeconds: 20, CreatedAt: now.Add(-2 * time.Minute)},
		{ID: uuid.NewString(), Title: "ready", Status: model.VideoStatusReady, FileURL: "source", ThumbnailURL: "poster", DurationSeconds: 10, CreatedAt: now.Add(-1 * time.Minute)},
	}
	for i := range videos {
		if err := db.Create(&videos[i]).Error; err != nil {
			t.Fatalf("create video %d: %v", i, err)
		}
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos", nil)

	NewVideoHandler(db).List(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			FileURL string `json:"file_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(payload.Data) != 3 {
		t.Fatalf("list length = %d, want 3", len(payload.Data))
	}
	if payload.Data[0].Status != model.VideoStatusReady {
		t.Fatalf("first video status = %q, want %q", payload.Data[0].Status, model.VideoStatusReady)
	}
	if payload.Data[1].Status != model.VideoStatusProcessing || payload.Data[2].Status != model.VideoStatusCompleted {
		t.Fatalf("unexpected order: %#v", payload.Data)
	}
	if payload.Data[0].FileURL == "" || payload.Data[1].FileURL == "" || payload.Data[2].FileURL == "" {
		t.Fatal("initially available videos must include file_url")
	}
}

func TestPosterUploadMarksVideoReady(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID:      uuid.NewString(),
		Title:   "clip",
		Status:  model.VideoStatusUploaded,
		FileURL: "http://127.0.0.1:8090/api/custom/files/videos/clip/source.mp4",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}

	router := NewRouter(db, &config.Config{
		MinIO: config.MinIOConfig{
			Backend:   "local",
			LocalDir:  t.TempDir(),
			PublicURL: "http://127.0.0.1:8090/api/custom/files",
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/custom/videos/"+video.ID+"/poster?duration_seconds=42", bytes.NewReader([]byte("jpeg-bytes")))
	req.Header.Set("Content-Type", "image/jpeg")

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got model.Video
	if err := db.First(&got, "id = ?", video.ID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if got.Status != model.VideoStatusReady {
		t.Fatalf("video status = %q, want %q", got.Status, model.VideoStatusReady)
	}
	if got.ThumbnailURL == "" {
		t.Fatal("thumbnail_url is empty")
	}
	if got.DurationSeconds != 42 {
		t.Fatalf("duration_seconds = %d, want 42", got.DurationSeconds)
	}
	if got.ReadyAt == nil {
		t.Fatal("ready_at is nil")
	}
}

func openTestVideoDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.AutoMigrate(&model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate jobs: %v", err)
	}
	return db
}
