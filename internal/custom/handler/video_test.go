package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestVideoListReturnsUploadedVideosWhileEnhancementsRun(t *testing.T) {
	db := openTestVideoDB(t)
	now := time.Now().UTC()
	videos := []model.Video{
		{ID: uuid.NewString(), Title: "uploading", Status: model.VideoStatusUploading, FileURL: "source", CreatedAt: now.Add(-8 * time.Minute)},
		{ID: uuid.NewString(), Title: "uploaded without cover", Status: model.VideoStatusUploaded, FileURL: "source", UploadedAt: &now, CreatedAt: now.Add(-7 * time.Minute)},
		{ID: uuid.NewString(), Title: "initializing without cover", Status: model.VideoStatusInitializing, FileURL: "source", UploadedAt: &now, CreatedAt: now.Add(-6 * time.Minute)},
		{ID: uuid.NewString(), Title: "uploaded with cover", Status: model.VideoStatusUploaded, FileURL: "source", ThumbnailURL: "poster", UploadedAt: &now, CreatedAt: now.Add(-5 * time.Minute)},
		{ID: uuid.NewString(), Title: "completed", Status: model.VideoStatusCompleted, FileURL: "source", ThumbnailURL: "poster", DurationSeconds: 30, UploadedAt: &now, CreatedAt: now.Add(-4 * time.Minute)},
		{ID: uuid.NewString(), Title: "processing", Status: model.VideoStatusProcessing, FileURL: "source", ThumbnailURL: "poster", DurationSeconds: 20, UploadedAt: &now, CreatedAt: now.Add(-3 * time.Minute)},
		{ID: uuid.NewString(), Title: "ready", Status: model.VideoStatusReady, FileURL: "source", ThumbnailURL: "poster", DurationSeconds: 10, UploadedAt: &now, CreatedAt: now.Add(-2 * time.Minute)},
		{ID: uuid.NewString(), Title: "ready without cover", Status: model.VideoStatusReady, FileURL: "source", UploadedAt: &now, CreatedAt: now.Add(-1 * time.Minute)},
		{ID: uuid.NewString(), Title: "content failed", Status: model.VideoStatusFailed, FileURL: "source", UploadedAt: &now, ProcessingErrorSummary: "summary failed", CreatedAt: now},
		{ID: uuid.NewString(), Title: "upload failed", Status: model.VideoStatusFailed, FileURL: "source", ProcessingErrorSummary: "merge failed", CreatedAt: now.Add(time.Minute)},
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
	if payload.Data[0].Title != "content failed" || payload.Data[0].ProcessingErrorSummary != "summary failed" || !payload.Data[0].InitiallyAvailable {
		t.Fatalf("content-failed video metadata = %#v", payload.Data[0])
	}
	seen := map[string]bool{}
	for _, item := range payload.Data {
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
	if seen["upload failed"] {
		t.Fatalf("failed upload without uploaded_at must stay hidden: %#v", payload.Data)
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
		{name: "failed upload stays unavailable", status: model.VideoStatusFailed, fileURL: "source", initiallyAvailable: false, visibleInList: false},
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
			if tc.initiallyAvailable {
				uploadedAt := time.Now().UTC()
				video.UploadedAt = &uploadedAt
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

func TestVideoDeleteSoftDeletesVideoAndProcessingJobs(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID:         uuid.NewString(),
		Title:      "待删除视频",
		Status:     model.VideoStatusReady,
		FileURL:    "videos/source.mp4",
		UploadedAt: func() *time.Time { value := time.Now().UTC(); return &value }(),
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	job := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "thumbnail", Status: "succeeded"}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: video.ID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/custom/videos/"+video.ID, nil)
	NewVideoHandler(db).Delete(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var remaining model.Video
	if err := db.First(&remaining, "id = ?", video.ID).Error; err != gorm.ErrRecordNotFound {
		t.Fatalf("video lookup error = %v, want record not found", err)
	}
	var jobs int64
	if err := db.Model(&model.VideoProcessingJob{}).Where("video_id = ?", video.ID).Count(&jobs).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobs != 0 {
		t.Fatalf("jobs remaining = %d, want 0", jobs)
	}
}

func TestVideoDeleteRoutesKnowledgeByPersistedKnowledgeBase(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.VideoTranscriptChunk{}, &model.VideoTranscriptSource{}); err != nil {
		t.Fatalf("migrate transcript ownership tables: %v", err)
	}
	video := model.Video{
		ID:                    uuid.NewString(),
		Title:                 "双库删除路由",
		Status:                model.VideoStatusReady,
		FileURL:               "videos/source.mp4",
		TranscriptKnowledgeID: "evidence-anchor",
		TranscriptGeneration:  "generation-1",
		UploadedAt:            func() *time.Time { value := time.Now().UTC(); return &value }(),
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{
		VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 0,
		KnowledgeID: "evidence-chunk", ContentHash: "chunk-hash", Status: "completed",
	}).Error; err != nil {
		t.Fatalf("create evidence chunk: %v", err)
	}
	if err := db.Create(&model.VideoTranscriptSource{
		ID: uuid.NewString(), VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration,
		KnowledgeBaseID: "knowledge-kb", KnowledgeID: "knowledge-source", ContentHash: "source-hash",
		Status: "created",
	}).Error; err != nil {
		t.Fatalf("create knowledge source: %v", err)
	}

	var deleted []string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", r.Method)
		}
		deleted = append(deleted, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer remote.Close()
	evidence := weknora.New(config.WeKnoraConfig{BaseURL: remote.URL, KBID: "evidence-kb"})
	knowledge := weknora.New(config.WeKnoraConfig{BaseURL: remote.URL, KBID: "knowledge-kb"})
	h := NewVideoHandler(db)
	h.EvidenceWeKnora = evidence
	h.KnowledgeWeKnora = knowledge

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: video.ID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/custom/videos/"+video.ID, nil)
	h.Delete(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	sort.Strings(deleted)
	want := []string{
		"/api/v1/knowledge/evidence-anchor",
		"/api/v1/knowledge/evidence-chunk",
		"/api/v1/knowledge/knowledge-source",
	}
	sort.Strings(want)
	if strings.Join(deleted, "\n") != strings.Join(want, "\n") {
		t.Fatalf("deleted knowledge = %#v, want %#v", deleted, want)
	}
}

func TestRouterFailsClosedWhenKnowledgeBaseRolesAreInvalid(t *testing.T) {
	db := openTestVideoDB(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("invalid routing must not call WeKnora: %s %s", r.Method, r.URL.Path)
	}))
	defer remote.Close()
	deps := &Deps{
		DB:   db,
		Cfg:  &config.Config{WeKnora: config.WeKnoraConfig{BaseURL: remote.URL, KBID: "legacy-only"}},
		Wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: remote.URL, KBID: "knowledge-kb"}),
	}
	router := BuildRouterForDeps(deps)
	for _, path := range []string{"/api/custom/chat/wiki-search?q=term", "/api/custom/chat/scope/global"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want %d, body = %s", path, rec.Code, http.StatusServiceUnavailable, rec.Body.String())
		}
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
