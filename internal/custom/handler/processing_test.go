package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
)

func TestProcessingStatusReportsFailedStageAndRetryableJob(t *testing.T) {
	db := openTestVideoDB(t)
	now := time.Now().UTC()
	video := model.Video{
		ID:                   uuid.NewString(),
		Title:                "failed parsing",
		FileURL:              "https://cdn.example.com/video.mp4",
		Status:               model.VideoStatusProcessing,
		TranscriptGeneration: "generation-1",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	jobs := []model.VideoProcessingJob{
		{ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", TranscriptGeneration: "generation-1", Status: "succeeded", ResultPayload: `{"task_id":"task-1"}`, IdempotencyKey: "transcription:" + video.ID + ":generation-1", UpdatedAt: now.Add(-time.Minute)},
		{ID: uuid.NewString(), VideoID: video.ID, JobType: "subtitle_generate", TranscriptGeneration: "generation-1", Status: "failed", ErrorCategory: "object_storage", ErrorCode: "object_storage_upload", ErrorMessage: "upload srt failed", IdempotencyKey: "subtitle_generate:" + video.ID + ":generation-1", UpdatedAt: now},
	}
	if err := db.Create(&jobs).Error; err != nil {
		t.Fatalf("create jobs: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/processing-status", nil)

	NewProcessingHandler(db).Status(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var payload ProcessingStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.Status != ProcessingStateFailed || payload.CurrentStage != "subtitle_generate" {
		t.Fatalf("processing state = %#v", payload)
	}
	if payload.Failure == nil || payload.Failure.JobID != jobs[1].ID || payload.Failure.Category != "object_storage" {
		t.Fatalf("failure = %#v", payload.Failure)
	}
	if payload.RetryableJob == nil || payload.RetryableJob.JobID != jobs[1].ID {
		t.Fatalf("retryable job = %#v", payload.RetryableJob)
	}
	if len(payload.CompletedStages) != 1 || payload.CompletedStages[0] != "transcription" {
		t.Fatalf("completed stages = %#v", payload.CompletedStages)
	}
	if len(payload.Jobs) != 2 || !payload.Jobs[0].ResultAvailable {
		t.Fatalf("job input/output summary = %#v", payload.Jobs)
	}
}

func TestDraftContentStagesUseDraftArtifacts(t *testing.T) {
	video := model.Video{OutlineDraftWikiPageID: "outline-draft", SummaryDraftWikiPageID: "summary-draft"}
	require.True(t, stageArtifactAvailable(video, model.VideoProcessingJob{JobType: "outline", ResultStage: "draft"}))
	require.True(t, stageArtifactAvailable(video, model.VideoProcessingJob{JobType: "summary", ResultStage: "draft"}))
	require.False(t, stageArtifactAvailable(video, model.VideoProcessingJob{JobType: "outline"}))
}

func TestProcessingJobPhaseReportsMPSProvider(t *testing.T) {
	job := model.VideoProcessingJob{JobType: "transcription", Provider: "tencent_mps", Status: "running", ExternalTaskID: "mps-task"}
	require.Equal(t, "mps_running", processingJobPhase(job))
}

func TestProcessingStatusKeepsMPSUpstreamStagesAfterIndexGenerationChanges(t *testing.T) {
	video := model.Video{ID: "video-1", TranscriptGeneration: "content-hash", SubtitleFileURL: "https://cos.example/subtitle.vtt", TranscriptKnowledgeID: "knowledge-1"}
	jobs := []model.VideoProcessingJob{
		{ID: "transcription", JobType: "transcription", Provider: "tencent_mps", TranscriptGeneration: "mps:task-1", Status: "succeeded", ResultPayload: `{"mps_result":{}}`},
		{ID: "subtitle", JobType: "subtitle_generate", Provider: "tencent_mps", TranscriptGeneration: "mps:task-1", Status: "succeeded", ResultPayload: `{"paragraphs":[{}]}`},
		{ID: "index", JobType: "index", Provider: "weknora", TranscriptGeneration: "content-hash", Status: "succeeded"},
	}
	status := buildProcessingStatus(video, jobs)
	require.Contains(t, status.CompletedStages, "transcription")
	require.Contains(t, status.CompletedStages, "subtitle_generate")
	require.Contains(t, status.CompletedStages, "index")
}

func TestProcessingJobStatusReportsTranscriptionPhase(t *testing.T) {
	preparing := processingJobStatus(model.VideoProcessingJob{
		ID: "preparing", JobType: "transcription", Status: "running", Progress: 42,
	})
	if preparing.Phase != "source_preparing" {
		t.Fatalf("preparing phase = %q, want source_preparing", preparing.Phase)
	}

	running := processingJobStatus(model.VideoProcessingJob{
		ID: "running", JobType: "transcription", Status: "running", ExternalTaskID: "task-1",
	})
	if running.Phase != "tingwu_running" {
		t.Fatalf("running phase = %q, want tingwu_running", running.Phase)
	}
}

func TestSummaryEnhancementUsesSummaryArtifact(t *testing.T) {
	video := model.Video{SummaryWikiPageID: "summary-page", KnowledgeBaseWikiPageID: ""}
	job := model.VideoProcessingJob{JobType: "summary_enhance"}
	if !stageArtifactAvailable(video, job) {
		t.Fatal("summary enhancement should be complete when the summary page exists")
	}
}

func TestProcessingHandlerRetriesInvalidOutlineArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages:      []weknora.WikiPage{{ID: "outline-page", Slug: "outline/video-1"}},
				Total:      1,
				Page:       1,
				PageSize:   100,
				TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline%2Fvideo-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID:      "outline-page",
				Content: "---\ntype: outline\nsource_video_id: video-1\ntranscript_generation: generation-1\n---\n\n...",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	handler := NewProcessingHandler(nil, ProcessingDependencies{
		Wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL}),
		KBID: "kb-1",
	})
	video := model.Video{
		ID:                   "video-1",
		OutlineWikiPageID:    "outline-page",
		TranscriptGeneration: "generation-1",
	}
	job := model.VideoProcessingJob{JobType: "outline", Status: "succeeded"}
	if handler.stageArtifactAvailable(t.Context(), video, job) {
		t.Fatal("invalid outline artifact must be retryable")
	}
}

func TestRetryFailedStageReusesSameJob(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID:                   uuid.NewString(),
		Title:                "retry parsing",
		FileURL:              "https://cdn.example.com/video.mp4",
		Status:               model.VideoStatusFailed,
		TranscriptGeneration: "generation-2",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	failedJob := model.VideoProcessingJob{
		ID:                   uuid.NewString(),
		VideoID:              video.ID,
		JobType:              "outline",
		TranscriptGeneration: video.TranscriptGeneration,
		Status:               "failed",
		AttemptCount:         3,
		MaxAttempts:          3,
		ErrorCategory:        "wiki_artifact",
		ErrorCode:            "wiki_artifact_missing",
		ErrorMessage:         "outline page missing",
		IdempotencyKey:       "outline:" + video.ID + ":" + video.TranscriptGeneration,
	}
	if err := db.Create(&failedJob).Error; err != nil {
		t.Fatalf("create failed job: %v", err)
	}

	handler := NewProcessingHandler(db)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}, {Key: "jobType", Value: "outline"}}
	context.Request = httptest.NewRequest(http.MethodPost, "/api/custom/videos/"+video.ID+"/processing-jobs/outline/retry", nil)
	handler.Retry(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first retry status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	context, _ = gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}, {Key: "jobType", Value: "outline"}}
	context.Request = httptest.NewRequest(http.MethodPost, "/api/custom/videos/"+video.ID+"/processing-jobs/outline/retry", nil)
	handler.Retry(context)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate retry status = %d, want %d, body = %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}

	var jobs []model.VideoProcessingJob
	if err := db.Where("video_id = ? AND job_type = ? AND transcript_generation = ?", video.ID, "outline", video.TranscriptGeneration).Find(&jobs).Error; err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("job count = %d, want 1", len(jobs))
	}
	if jobs[0].ID != failedJob.ID || jobs[0].Status != "pending" || jobs[0].AttemptCount != 0 {
		t.Fatalf("retried job = %#v", jobs[0])
	}
	if jobs[0].ErrorCode != "" || jobs[0].ErrorMessage != "" || jobs[0].ErrorCategory != "" {
		t.Fatalf("retry did not clear failure: %#v", jobs[0])
	}
	var gotVideo model.Video
	if err := db.First(&gotVideo, "id = ?", video.ID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if gotVideo.Status != model.VideoStatusProcessing || gotVideo.ProcessingErrorSummary != "" {
		t.Fatalf("video retry state = %#v", gotVideo)
	}
}

func TestRetrySuccessfulTranscriptionCreatesNewJob(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "rerun parsing", Status: model.VideoStatusCompleted,
		TranscriptGeneration: "generation-1",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	previous := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", TranscriptGeneration: video.TranscriptGeneration,
		Provider: "aliyun_tingwu", Status: "succeeded", ResultPayload: `{"raw_result":"real-result"}`,
		IdempotencyKey: "transcription:" + video.ID,
	}
	if err := db.Create(&previous).Error; err != nil {
		t.Fatalf("create previous job: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}, {Key: "jobType", Value: "transcription"}}
	context.Request = httptest.NewRequest(http.MethodPost, "/api/custom/videos/"+video.ID+"/processing-jobs/transcription/retry", nil)
	NewProcessingHandler(db).Retry(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("retry status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if response.JobID == "" {
		t.Fatal("retry response has no new job id")
	}

	var rerun model.VideoProcessingJob
	if err := db.First(&rerun, "id = ?", response.JobID).Error; err != nil {
		t.Fatalf("load rerun job: %v", err)
	}
	if rerun.ID == previous.ID || rerun.Status != "pending" || rerun.IdempotencyKey == previous.IdempotencyKey {
		t.Fatalf("rerun job = %#v", rerun)
	}
}

func TestRetryFailedTranscriptionClearsExternalTask(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{ID: uuid.NewString(), Title: "failed transcription", Status: model.VideoStatusFailed}
	require.NoError(t, db.Create(&video).Error)
	job := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Provider: "tencent_mps", Status: "failed", ExternalTaskID: "old-mps-task", ErrorCategory: "processing_failed", IdempotencyKey: "transcription:" + video.ID}
	require.NoError(t, db.Create(&job).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: video.ID}, {Key: "jobType", Value: "transcription"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/custom/videos/"+video.ID+"/processing-jobs/transcription/retry", nil)
	NewProcessingHandler(db).Retry(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var retried model.VideoProcessingJob
	require.NoError(t, db.First(&retried, "id = ?", job.ID).Error)
	require.Empty(t, retried.ExternalTaskID)
	require.Equal(t, "pending", retried.Status)
}

func TestRetryRunningStageRejectsDuplicateExecution(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "running retry", FileURL: "https://cdn.example.com/video.mp4",
		Status: model.VideoStatusProcessing, TranscriptGeneration: "generation-running",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	runningJob := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Provider: "aliyun_tingwu",
		TranscriptGeneration: video.TranscriptGeneration, Status: "running", AttemptCount: 1, MaxAttempts: 3,
		ExternalTaskID: "tingwu-task-running", IdempotencyKey: "transcription:" + video.ID,
	}
	if err := db.Create(&runningJob).Error; err != nil {
		t.Fatalf("create running job: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}, {Key: "jobType", Value: "transcription"}}
	context.Request = httptest.NewRequest(http.MethodPost, "/api/custom/videos/"+video.ID+"/processing-jobs/transcription/retry", nil)
	NewProcessingHandler(db).Retry(context)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("retry status = %d, want %d, body = %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	var got model.VideoProcessingJob
	if err := db.First(&got, "id = ?", runningJob.ID).Error; err != nil {
		t.Fatalf("load running job: %v", err)
	}
	if got.Status != "running" || got.AttemptCount != 1 || got.ExternalTaskID != runningJob.ExternalTaskID {
		t.Fatalf("running job changed after rejected retry: %#v", got)
	}
}

func TestSucceededStageWithMissingArtifactBecomesRetryable(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "legacy incomplete result", FileURL: "https://cdn.example.com/video.mp4",
		Status: model.VideoStatusProcessing, TranscriptGeneration: "generation-3",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	job := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "summary", TranscriptGeneration: video.TranscriptGeneration,
		Status: "succeeded", IdempotencyKey: "summary:" + video.ID + ":" + video.TranscriptGeneration,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	status := buildProcessingStatus(video, []model.VideoProcessingJob{job})
	if status.Status != ProcessingStateFailed || status.Failure == nil || status.Failure.Code != "content_artifact_missing" {
		t.Fatalf("processing status = %#v", status)
	}
	if len(status.Jobs) != 1 || status.Jobs[0].Status != "failed" || status.Jobs[0].ErrorCode != "content_artifact_missing" {
		t.Fatalf("stage status = %#v", status.Jobs)
	}
	if status.RetryableJob == nil || status.RetryableJob.JobID != job.ID {
		t.Fatalf("retryable job = %#v", status.RetryableJob)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}, {Key: "jobType", Value: "summary"}}
	context.Request = httptest.NewRequest(http.MethodPost, "/api/custom/videos/"+video.ID+"/processing-jobs/summary/retry", nil)
	NewProcessingHandler(db).Retry(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("retry status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var retried model.VideoProcessingJob
	if err := db.First(&retried, "id = ?", job.ID).Error; err != nil {
		t.Fatalf("load retried job: %v", err)
	}
	if retried.Status != "pending" {
		t.Fatalf("retried status = %q, want pending", retried.Status)
	}
	if !skill.IsExplicitSummaryRegeneration(retried.InputPayload) {
		t.Fatalf("summary retry did not mark explicit regeneration: %q", retried.InputPayload)
	}
}

func TestRetrySucceededSummaryAllowsExplicitRegeneration(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "legacy summary", FileURL: "https://cdn.example.com/video.mp4",
		Status: model.VideoStatusCompleted, TranscriptGeneration: "generation-4", SummaryWikiPageID: "existing-summary-page",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	job := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "summary", TranscriptGeneration: video.TranscriptGeneration,
		Status: "succeeded", IdempotencyKey: "summary:" + video.ID + ":" + video.TranscriptGeneration,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}, {Key: "jobType", Value: "summary"}}
	context.Request = httptest.NewRequest(http.MethodPost, "/api/custom/videos/"+video.ID+"/processing-jobs/summary/retry", nil)
	NewProcessingHandler(db).Retry(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("retry status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var retried model.VideoProcessingJob
	if err := db.First(&retried, "id = ?", job.ID).Error; err != nil {
		t.Fatalf("load retried job: %v", err)
	}
	if retried.Status != "pending" || !skill.IsExplicitSummaryRegeneration(retried.InputPayload) {
		t.Fatalf("retried summary job = %#v", retried)
	}
}

func TestProcessingStatusIgnoresFailedOldGeneration(t *testing.T) {
	video := model.Video{
		ID: uuid.NewString(), Status: model.VideoStatusProcessing, TranscriptGeneration: "generation-new",
		SummaryWikiPageID: "summary-new",
	}
	jobs := []model.VideoProcessingJob{
		{ID: "old", VideoID: video.ID, JobType: "summary", TranscriptGeneration: "generation-old", Status: "failed", ErrorMessage: "old failure", UpdatedAt: time.Now().Add(time.Minute)},
		{ID: "new", VideoID: video.ID, JobType: "summary", TranscriptGeneration: "generation-new", Status: "succeeded", UpdatedAt: time.Now()},
	}
	status := buildProcessingStatus(video, jobs)
	if status.Status == ProcessingStateFailed || status.Failure != nil {
		t.Fatalf("old generation leaked into current status: %#v", status)
	}
	if len(status.CompletedStages) != 1 || status.CompletedStages[0] != "summary" {
		t.Fatalf("completed stages = %#v", status.CompletedStages)
	}
}

func TestProcessingStatusKeepsPartialSuccessWhenLaterStageFails(t *testing.T) {
	video := model.Video{
		ID: uuid.NewString(), Status: model.VideoStatusFailed, TranscriptGeneration: "generation-1",
		OutlineWikiPageID: "outline-1",
	}
	jobs := []model.VideoProcessingJob{
		{ID: "outline", VideoID: video.ID, JobType: "outline", TranscriptGeneration: video.TranscriptGeneration, Status: "succeeded", UpdatedAt: time.Now()},
		{ID: "summary", VideoID: video.ID, JobType: "summary", TranscriptGeneration: video.TranscriptGeneration, Status: "failed", ErrorCategory: "wiki_artifact", ErrorCode: "wiki_artifact_missing", UpdatedAt: time.Now().Add(time.Second)},
	}
	status := buildProcessingStatus(video, jobs)
	if status.Status != ProcessingStateFailed || status.CurrentStage != "summary" {
		t.Fatalf("status = %#v", status)
	}
	if len(status.CompletedStages) != 1 || status.CompletedStages[0] != "outline" {
		t.Fatalf("completed stages = %#v", status.CompletedStages)
	}
}

func TestProcessingStatusIsolatesGraphFailureFromFoundation(t *testing.T) {
	video := model.Video{
		ID: uuid.NewString(), Status: model.VideoStatusCompleted, TranscriptGeneration: "generation-1",
		OutlineWikiPageID: "outline-1", OverviewWikiPageID: "overview-1", SummaryWikiPageID: "summary-1",
		TranscriptPageWikiPageID: "transcript-page-1",
	}
	now := time.Now().UTC()
	jobs := []model.VideoProcessingJob{
		{ID: "outline", VideoID: video.ID, JobType: "outline", TranscriptGeneration: video.TranscriptGeneration, Status: "succeeded", UpdatedAt: now},
		{ID: "overview", VideoID: video.ID, JobType: "overview", TranscriptGeneration: video.TranscriptGeneration, Status: "succeeded", UpdatedAt: now},
		{ID: "summary", VideoID: video.ID, JobType: "summary", TranscriptGeneration: video.TranscriptGeneration, Status: "succeeded", UpdatedAt: now},
		{ID: "assemble", VideoID: video.ID, JobType: "assemble", TranscriptGeneration: video.TranscriptGeneration, Status: "succeeded", UpdatedAt: now},
		{ID: "graph", VideoID: video.ID, JobType: "graph", TranscriptGeneration: video.TranscriptGeneration, Status: "failed", ErrorCategory: "weknora", ErrorCode: "graph_failed", ErrorMessage: "graph unavailable", UpdatedAt: now.Add(time.Second)},
	}

	status := buildProcessingStatus(video, jobs)
	if status.Status != ProcessingStateCompleted || status.Failure != nil {
		t.Fatalf("overall status = %#v", status)
	}
	if status.FoundationStatus != ProcessingStateCompleted || status.EnhancementStatus != ProcessingStateFailed {
		t.Fatalf("track status = %#v", status)
	}
	if status.EnhancementFailure == nil || status.EnhancementFailure.JobID != "graph" {
		t.Fatalf("enhancement failure = %#v", status.EnhancementFailure)
	}
}
