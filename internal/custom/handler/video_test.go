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

func TestVideoListReturnsUploadedVideosWhileEnhancementsRun(t *testing.T) {
	db := openTestVideoDB(t)
	now := time.Now().UTC()
	videos := []model.Video{
		{ID: uuid.NewString(), Title: "uploading", Status: model.VideoStatusUploading, FileURL: "source", CreatedAt: now.Add(-8 * time.Minute)},
		{ID: uuid.NewString(), Title: "uploaded without cover", Status: model.VideoStatusUploaded, FileURL: "source", CreatedAt: now.Add(-7 * time.Minute)},
		{ID: uuid.NewString(), Title: "initializing without cover", Status: model.VideoStatusInitializing, FileURL: "source", CreatedAt: now.Add(-6 * time.Minute)},
		{ID: uuid.NewString(), Title: "uploaded with cover", Status: model.VideoStatusUploaded, FileURL: "source", ThumbnailURL: "poster", CreatedAt: now.Add(-5 * time.Minute)},
		{ID: uuid.NewString(), Title: "completed", Status: model.VideoStatusCompleted, FileURL: "source", ThumbnailURL: "poster", DurationSeconds: 30, CreatedAt: now.Add(-4 * time.Minute)},
		{ID: uuid.NewString(), Title: "processing", Status: model.VideoStatusProcessing, FileURL: "source", ThumbnailURL: "poster", DurationSeconds: 20, CreatedAt: now.Add(-3 * time.Minute)},
		{ID: uuid.NewString(), Title: "ready", Status: model.VideoStatusReady, FileURL: "source", ThumbnailURL: "poster", DurationSeconds: 10, CreatedAt: now.Add(-2 * time.Minute)},
		{ID: uuid.NewString(), Title: "ready without cover", Status: model.VideoStatusReady, FileURL: "source", CreatedAt: now.Add(-1 * time.Minute)},
		{ID: uuid.NewString(), Title: "failed", Status: model.VideoStatusFailed, ProcessingErrorSummary: "merge failed", CreatedAt: now},
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
			ID                     string `json:"id"`
			Title                  string `json:"title"`
			Status                 string `json:"status"`
			FileURL                string `json:"file_url"`
			PlayURL                string `json:"play_url"`
			CoverURL               string `json:"cover_url"`
			ThumbnailURL           string `json:"thumbnail_url"`
			DurationSeconds        int    `json:"duration_seconds"`
			ProcessingErrorSummary string `json:"processing_error_summary"`
			InitiallyAvailable     bool   `json:"initially_available"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(payload.Data) != 8 {
		t.Fatalf("list length = %d, want 8", len(payload.Data))
	}
	if payload.Data[0].Status != model.VideoStatusFailed {
		t.Fatalf("first video status = %q, want %q", payload.Data[0].Status, model.VideoStatusFailed)
	}
	if payload.Data[0].ProcessingErrorSummary != "merge failed" || payload.Data[0].InitiallyAvailable {
		t.Fatalf("failed video metadata = %#v", payload.Data[0])
	}
	seen := map[string]bool{}
	for _, item := range payload.Data[1:] {
		seen[item.Title] = true
		if item.FileURL == "" || item.PlayURL == "" || !item.InitiallyAvailable {
			t.Fatalf("initially available video metadata = %#v", item)
		}
		if item.Title == "completed" && (item.CoverURL != "poster" || item.ThumbnailURL != "poster" || item.DurationSeconds != 30) {
			t.Fatalf("completed video media metadata = %#v", item)
		}
	}
	// 文件已合并的视频立即出现；封面和时长由后台异步补齐。
	for _, title := range []string{"uploaded without cover", "initializing without cover", "uploaded with cover", "completed", "processing", "ready", "ready without cover"} {
		if !seen[title] {
			t.Fatalf("video %q missing from list: %#v", title, payload.Data)
		}
	}
	if seen["uploading"] {
		t.Fatalf("active upload must stay hidden: %#v", payload.Data)
	}
}

func TestVideoDetailUsesInitialAvailability(t *testing.T) {
	db := openTestVideoDB(t)
	cases := []struct {
		name               string
		status             string
		fileURL            string
		thumbnailURL       string
		initiallyAvailable bool
		visibleInList      bool
	}{
		{name: "cover generating remains playable", status: model.VideoStatusInitializing, fileURL: "source", initiallyAvailable: true, visibleInList: true},
		{name: "cover degraded to placeholder is available", status: model.VideoStatusReady, fileURL: "source", initiallyAvailable: true, visibleInList: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			video := model.Video{
				ID:           uuid.NewString(),
				Title:        tc.name,
				Status:       tc.status,
				FileURL:      tc.fileURL,
				ThumbnailURL: tc.thumbnailURL,
			}
			if err := db.Create(&video).Error; err != nil {
				t.Fatalf("create video: %v", err)
			}

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Params = gin.Params{{Key: "id", Value: video.ID}}
			c.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID, nil)

			NewVideoHandler(db).Detail(c)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			var payload struct {
				InitiallyAvailable bool   `json:"initially_available"`
				VisibleInList      bool   `json:"visible_in_list"`
				PlayURL            string `json:"play_url"`
				CoverURL           string `json:"cover_url"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			if payload.InitiallyAvailable != tc.initiallyAvailable || payload.VisibleInList != tc.visibleInList {
				t.Fatalf("detail availability = %#v, want initially_available=%v visible_in_list=%v", payload, tc.initiallyAvailable, tc.visibleInList)
			}
			if payload.PlayURL != tc.fileURL || payload.CoverURL != tc.thumbnailURL {
				t.Fatalf("detail media URLs = %#v, want play_url=%q cover_url=%q", payload, tc.fileURL, tc.thumbnailURL)
			}
		})
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
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
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
