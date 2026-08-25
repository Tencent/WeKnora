package worker

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestCleanupStuckUploadsMarksOnlyOrphanedRecordsFailed(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	orphanID := uuid.NewString()
	activeID := uuid.NewString()
	withJobID := uuid.NewString()
	for _, video := range []model.Video{
		{ID: orphanID, Title: "orphan", Status: model.VideoStatusUploading, CreatedAt: now.Add(-31 * time.Minute), UpdatedAt: now.Add(-31 * time.Minute)},
		{ID: activeID, Title: "active", Status: model.VideoStatusUploading, CreatedAt: now.Add(-31 * time.Minute), UpdatedAt: now.Add(-5 * time.Minute)},
		{ID: withJobID, Title: "job", Status: model.VideoStatusUploading, CreatedAt: now.Add(-31 * time.Minute), UpdatedAt: now.Add(-31 * time.Minute)},
	} {
		if err := db.Create(&video).Error; err != nil {
			t.Fatalf("create video: %v", err)
		}
	}
	if err := db.Create(&model.VideoProcessingJob{
		ID:             uuid.NewString(),
		VideoID:        withJobID,
		JobType:        "thumbnail",
		Provider:       "local",
		IdempotencyKey: "thumbnail:" + withJobID,
		Status:         "pending",
	}).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	updated, err := CleanupStuckUploads(db, now, 30*time.Minute)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated rows = %d, want 1", updated)
	}

	var orphan, active, withJob model.Video
	for id, target := range map[string]*model.Video{orphanID: &orphan, activeID: &active, withJobID: &withJob} {
		if err := db.First(target, "id = ?", id).Error; err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
	}
	if orphan.Status != model.VideoStatusFailed || orphan.ProcessingErrorSummary == "" {
		t.Fatalf("orphan video not failed: status=%q reason=%q", orphan.Status, orphan.ProcessingErrorSummary)
	}
	if active.Status != model.VideoStatusUploading {
		t.Fatalf("active video status = %q, want %q", active.Status, model.VideoStatusUploading)
	}
	if withJob.Status != model.VideoStatusUploading {
		t.Fatalf("video with job status = %q, want %q", withJob.Status, model.VideoStatusUploading)
	}
}

func TestThumbnailEnhancementFailureKeepsPlayableVideoAvailable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	video := model.Video{ID: uuid.NewString(), Title: "video", Status: model.VideoStatusInitializing, FileURL: "https://cdn/video.mp4"}
	job := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "thumbnail", Status: "running", AttemptCount: 1, MaxAttempts: 1,
		IdempotencyKey: "thumbnail:" + video.ID,
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	engine := NewEngine(db, &config.WorkerConfig{}, &failingHandler{err: context.Canceled})
	engine.dispatch(context.Background(), &job)

	var got model.Video
	if err := db.First(&got, "id = ?", video.ID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if got.Status != model.VideoStatusReady {
		t.Fatalf("video status = %q, want %q (cover fallback degrades to placeholder)", got.Status, model.VideoStatusReady)
	}
	if got.ProcessingErrorSummary == "" {
		t.Fatal("cover fallback reason is missing")
	}
	if got.ReadyAt == nil {
		t.Fatal("ready_at is nil after cover fallback")
	}
	// 未注册 transcription handler（内容链路关闭）时不得补投转写任务
	var jobCount int64
	if err := db.Model(&model.VideoProcessingJob{}).Where("video_id = ? AND job_type = ?", video.ID, "transcription").Count(&jobCount).Error; err != nil {
		t.Fatalf("count transcription jobs: %v", err)
	}
	if jobCount != 0 {
		t.Fatalf("transcription jobs = %d, want 0 when content workers disabled", jobCount)
	}
}

func TestCoverFallbackEnqueuesTranscriptionWhenContentWorkersEnabled(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	video := model.Video{ID: uuid.NewString(), Title: "video", Status: model.VideoStatusInitializing, FileURL: "https://cdn/video.mp4"}
	job := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "thumbnail", Status: "running", AttemptCount: 1, MaxAttempts: 1,
		IdempotencyKey: "thumbnail:" + video.ID,
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	engine := NewEngine(db, &config.WorkerConfig{}, &failingHandler{err: context.Canceled}, &stubHandler{jobType: "transcription"})
	engine.dispatch(context.Background(), &job)

	var got model.Video
	if err := db.First(&got, "id = ?", video.ID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if got.Status != model.VideoStatusReady {
		t.Fatalf("video status = %q, want %q", got.Status, model.VideoStatusReady)
	}
	var transcription model.VideoProcessingJob
	if err := db.Where("video_id = ? AND job_type = ?", video.ID, "transcription").First(&transcription).Error; err != nil {
		t.Fatalf("transcription job should be enqueued after cover fallback: %v", err)
	}
	if transcription.Status != "pending" {
		t.Fatalf("transcription job status = %q, want pending", transcription.Status)
	}
}

func TestCoreFileUnavailableMarksVideoFailed(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	video := model.Video{ID: uuid.NewString(), Title: "video", Status: model.VideoStatusInitializing}
	job := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "thumbnail", Status: "running", AttemptCount: 1, MaxAttempts: 1,
		IdempotencyKey: "thumbnail:" + video.ID,
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	engine := NewEngine(db, &config.WorkerConfig{}, &failingHandler{err: &CoreFileUnavailableError{Reason: "source object missing"}})
	engine.dispatch(context.Background(), &job)

	var got model.Video
	if err := db.First(&got, "id = ?", video.ID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if got.Status != model.VideoStatusFailed {
		t.Fatalf("video status = %q, want %q", got.Status, model.VideoStatusFailed)
	}
	if got.ProcessingErrorSummary == "" {
		t.Fatal("core failure reason is missing")
	}
}

type failingHandler struct {
	err error
}

func (h *failingHandler) JobType() string { return "thumbnail" }

func (h *failingHandler) Run(context.Context, *model.VideoProcessingJob, *model.Video) error {
	return h.err
}

type stubHandler struct {
	jobType string
}

func (h *stubHandler) JobType() string { return h.jobType }

func (h *stubHandler) Run(context.Context, *model.VideoProcessingJob, *model.Video) error {
	return nil
}
