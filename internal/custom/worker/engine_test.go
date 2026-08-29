package worker

import (
	"context"
	"errors"
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

func TestContentFailureStoresCategoryAndKeepsVideoPlayable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	video := model.Video{ID: uuid.NewString(), Title: "video", Status: model.VideoStatusProcessing, FileURL: "https://cdn/video.mp4"}
	job := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "summary", Status: "running", AttemptCount: 1, MaxAttempts: 1,
		IdempotencyKey: "summary:" + video.ID,
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	engine := NewEngine(db, &config.WorkerConfig{}, &failingHandler{jobType: "summary", err: context.DeadlineExceeded})
	engine.dispatch(context.Background(), &job)

	var gotJob model.VideoProcessingJob
	if err := db.First(&gotJob, "id = ?", job.ID).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}
	if gotJob.Status != "failed" || gotJob.ErrorCategory != "timeout" || gotJob.ErrorCode != "timeout" {
		t.Fatalf("failed job = %#v", gotJob)
	}
	var gotVideo model.Video
	if err := db.First(&gotVideo, "id = ?", video.ID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if gotVideo.Status != model.VideoStatusFailed || gotVideo.ProcessingErrorSummary == "" {
		t.Fatalf("failed video = %#v", gotVideo)
	}
	if !model.VideoIsPlayable(gotVideo.Status, gotVideo.FileURL, gotVideo.ThumbnailURL) {
		t.Fatal("content failure must not remove the existing playback entry")
	}
}

func TestSummaryEnhancementFailureDoesNotFailVideo(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	video := model.Video{
		ID: uuid.NewString(), Title: "video", Status: model.VideoStatusProcessing,
		FileURL: "https://cdn/video.mp4", OutlineWikiPageID: "outline-page",
		OverviewWikiPageID: "overview-page", SummaryWikiPageID: "summary-page",
		TranscriptPageWikiPageID: "transcript-page",
	}
	job := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "summary_enhance", Status: "running",
		AttemptCount: 1, MaxAttempts: 1, IdempotencyKey: "summary_enhance:" + video.ID,
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	engine := NewEngine(db, &config.WorkerConfig{}, &failingHandler{jobType: "summary_enhance", err: context.DeadlineExceeded})
	engine.dispatch(context.Background(), &job)

	var got model.Video
	if err := db.First(&got, "id = ?", video.ID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if got.Status != model.VideoStatusCompleted {
		t.Fatalf("video status = %q, want %q", got.Status, model.VideoStatusCompleted)
	}
	if got.ProcessingErrorSummary != "总结增强失败，基础内容仍可用" {
		t.Fatalf("processing error = %q", got.ProcessingErrorSummary)
	}
}

func TestDeterministicExternalFileErrorDoesNotRetry(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	video := model.Video{ID: uuid.NewString(), Title: "unsupported media", Status: model.VideoStatusProcessing, FileURL: "https://cdn/video.mp4"}
	job := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Status: "running", AttemptCount: 1, MaxAttempts: 3,
		ExternalTaskID: "tingwu-task-file-error", IdempotencyKey: "transcription:" + video.ID,
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	engine := NewEngine(db, &config.WorkerConfig{}, &failingHandler{jobType: "transcription", err: errors.New("听悟失败 Code=TSC.FileError Msg=unsupported media codec")})
	engine.dispatch(context.Background(), &job)

	var got model.VideoProcessingJob
	if err := db.First(&got, "id = ?", job.ID).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}
	if got.Status != "failed" || got.AttemptCount != 1 || got.ErrorCategory != ErrorCategoryExternalTask || got.ErrorCode != "source_file_rejected" {
		t.Fatalf("deterministic file failure = %#v", got)
	}
}

func TestClassifyProcessingError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		category string
		code     string
	}{
		{name: "timeout", err: context.DeadlineExceeded, category: "timeout", code: "timeout"},
		{name: "configuration", err: errors.New("听悟 client 未配置"), category: "configuration_auth", code: "configuration_missing"},
		{name: "authentication", err: errors.New("tingwu create status 401: InvalidAccessKeyId"), category: "configuration_auth", code: "authentication_failed"},
		{name: "rate limit", err: errors.New("tingwu create status 429: rate limit exceeded"), category: "external_task", code: "external_task_failed"},
		{name: "external failure", err: errors.New("听悟失败 Code=TaskFailed Msg=no audio"), category: "external_task", code: "external_task_failed"},
		{name: "external file error", err: errors.New("听悟失败 Code=TSC.FileError Msg=unsupported media codec"), category: "external_task", code: "source_file_rejected"},
		{name: "response parse", err: errors.New("decode tingwu get: invalid character"), category: "response_parse", code: "response_parse"},
		{name: "empty transcript", err: errors.New("transcript contains no non-empty timed sentences"), category: "response_parse", code: "response_parse"},
		{name: "object storage", err: errors.New("upload srt: put object failed"), category: "object_storage", code: "object_storage_operation"},
		{name: "weknora", err: errors.New("knowledge abc parse failed"), category: "weknora", code: "weknora_operation"},
		{name: "agent skill", err: errors.New("trigger skill generate-transcript-outline: upstream unavailable"), category: "weknora", code: "weknora_operation"},
		{name: "wiki artifact", err: errors.New("未找到 job=outline 的 wiki 页"), category: "wiki_artifact", code: "wiki_artifact_missing"},
		{name: "database", err: errors.New("save transcription result: database is locked"), category: "database", code: "database_operation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			category, code := ClassifyProcessingError(tc.err)
			if category != tc.category || code != tc.code {
				t.Fatalf("classification = %q/%q, want %q/%q", category, code, tc.category, tc.code)
			}
		})
	}
}

func TestEachContentStageFailureRemainsIndependentlyRetryable(t *testing.T) {
	for _, jobType := range []string{"graph", "outline", "summary", "assemble"} {
		t.Run(jobType, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			video := model.Video{ID: uuid.NewString(), Title: "video", FileURL: "https://cdn/video.mp4", Status: model.VideoStatusProcessing, TranscriptGeneration: "generation-1"}
			job := model.VideoProcessingJob{
				ID: uuid.NewString(), VideoID: video.ID, JobType: jobType, TranscriptGeneration: video.TranscriptGeneration,
				Status: "running", AttemptCount: 1, MaxAttempts: 1, IdempotencyKey: jobType + ":" + video.ID + ":" + video.TranscriptGeneration,
			}
			if err := db.Create(&video).Error; err != nil {
				t.Fatalf("create video: %v", err)
			}
			if err := db.Create(&job).Error; err != nil {
				t.Fatalf("create job: %v", err)
			}
			engine := NewEngine(db, &config.WorkerConfig{}, &failingHandler{jobType: jobType, err: errors.New("trigger skill: upstream unavailable")})
			engine.dispatch(context.Background(), &job)

			var failed model.VideoProcessingJob
			if err := db.First(&failed, "id = ?", job.ID).Error; err != nil {
				t.Fatalf("load failed job: %v", err)
			}
			if failed.Status != "failed" || failed.ErrorCategory != ErrorCategoryWeKnora || failed.ErrorMessage == "" {
				t.Fatalf("failed job = %#v", failed)
			}
		})
	}
}

func TestRecoverInterruptedJobsReusesExistingRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	jobs := []model.VideoProcessingJob{
		{ID: "running-1", VideoID: "video-1", JobType: "summary", Status: "running", AttemptCount: 2, MaxAttempts: 3, IdempotencyKey: "summary:video-1"},
		{ID: "succeeded-1", VideoID: "video-2", JobType: "summary", Status: "succeeded", AttemptCount: 1, MaxAttempts: 3, IdempotencyKey: "summary:video-2"},
	}
	if err := db.Create(&jobs).Error; err != nil {
		t.Fatalf("create jobs: %v", err)
	}

	count, err := RecoverInterruptedJobs(db)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if count != 1 {
		t.Fatalf("recovered = %d, want 1", count)
	}
	var running, succeeded model.VideoProcessingJob
	if err := db.First(&running, "id = ?", "running-1").Error; err != nil {
		t.Fatalf("load running job: %v", err)
	}
	if err := db.First(&succeeded, "id = ?", "succeeded-1").Error; err != nil {
		t.Fatalf("load succeeded job: %v", err)
	}
	if running.Status != "pending" || running.AttemptCount != 1 {
		t.Fatalf("recovered job = %#v", running)
	}
	if succeeded.Status != "succeeded" {
		t.Fatalf("succeeded job changed: %#v", succeeded)
	}
}

type failingHandler struct {
	jobType string
	err     error
}

func (h *failingHandler) JobType() string {
	if h.jobType == "" {
		return "thumbnail"
	}
	return h.jobType
}

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
