// Package worker transcription job 处理（VP-T005）。
//
// 设计要点：
//   - 调听悟 CreateTask 拿 external_task_id 持久化
//   - 轮询 GetTask；callback 启用时也可走回调（本版本先实现轮询）
//   - 完成后把转写 JSON 暂存 result_payload，并触发 subtitle_generate job
//   - 优先使用独立的持久化转写源，未配置时兼容回退到播放源
//   - 失败按 attempt_count / max_attempts 重试
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/client/mps"
	"github.com/Tencent/WeKnora/internal/custom/client/tongyi"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

const (
	transcriptionSourcePrepareTimeout = 30 * time.Minute
	transcriptionPollTimeout          = 30 * time.Minute
)

type SourcePreparationProgress struct {
	Phase   string
	Percent int
}

type ProgressSourcePreparer interface {
	PrepareWithProgress(context.Context, *model.Video, func(SourcePreparationProgress)) (string, error)
}

// TranscriptionHandler 转写 job
type TranscriptionHandler struct {
	DB                       *gorm.DB
	Tongyi                   TongyiClient
	MPS                      MPSClient
	MinIO                    *objstore.Client
	InternalFrontendBaseURL  string
	SourcePreparationTimeout time.Duration
	DraftsEnabled            bool
	SourcePreparer           SourcePreparer
	MPSInputPreparer         MPSInputPreparer
}

type TongyiClient interface {
	ValidateSourceFile(context.Context, string) error
	CreateTask(context.Context, tongyi.CreateTaskRequest) (*tongyi.CreateTaskResponse, error)
	GetTask(context.Context, string) (*tongyi.GetTaskResponse, error)
}

type MPSClient interface {
	CreateTask(context.Context, string, string) (string, error)
	GetTask(context.Context, string) (*mps.Task, error)
	Timeout() time.Duration
	PollInterval() time.Duration
}

type SourcePreparer interface {
	Prepare(context.Context, *model.Video) (string, error)
}

// MPSInputPreparer ensures a source can be fetched by Tencent MPS. It is kept
// separate from the Tingwu source preparer so switching providers never
// changes historical Tingwu behavior.
type MPSInputPreparer interface {
	Prepare(context.Context, *model.Video, string) (string, error)
}

// NewTranscriptionHandler 构造
func NewTranscriptionHandler(db *gorm.DB, t TongyiClient, internalFrontendBaseURL ...string) *TranscriptionHandler {
	internalURL := ""
	if len(internalFrontendBaseURL) > 0 {
		internalURL = strings.TrimRight(strings.TrimSpace(internalFrontendBaseURL[0]), "/")
	}
	return &TranscriptionHandler{DB: db, Tongyi: t, InternalFrontendBaseURL: internalURL, SourcePreparationTimeout: transcriptionSourcePrepareTimeout}
}

// SetDraftsEnabled controls whether the MPS path creates early draft jobs.
// Formal outline and summary jobs remain the default so drafts cannot consume
// the processing slots needed by the active transcript generation.
func (h *TranscriptionHandler) SetDraftsEnabled(enabled bool) {
	h.DraftsEnabled = enabled
}

// JobType job 类型
func (h *TranscriptionHandler) JobType() string { return "transcription" }

// Run 编排：发起 → 轮询 → 下载 → 写 result → 触发下游
func (h *TranscriptionHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	if normalizeProvider(job.Provider) == mps.Provider {
		return h.runMPS(ctx, job, video)
	}
	if h.Tongyi == nil {
		return fmt.Errorf("听悟 client 未配置")
	}
	if video == nil || video.ID == "" {
		return fmt.Errorf("video is missing")
	}
	sourceURL := strings.TrimSpace(video.TranscriptionSourceURL)
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(video.FileURL)
	}
	if sourceURL == "" {
		return fmt.Errorf("video transcription source url is empty")
	}
	if err := h.DB.Model(video).
		Where("status IN ?", []string{model.VideoStatusReady, model.VideoStatusProcessing}).
		Update("status", model.VideoStatusProcessing).Error; err != nil {
		return fmt.Errorf("mark video processing: %w", err)
	}

	// 第一次跑：创建 external task
	if job.ExternalTaskID == "" {
		prepareTimeout := h.SourcePreparationTimeout
		if prepareTimeout <= 0 {
			prepareTimeout = transcriptionSourcePrepareTimeout
		}
		prepareCtx, cancelPrepare := context.WithTimeout(ctx, prepareTimeout)
		prepareStartedAt := time.Now()
		stopPreparationHeartbeat := h.startPreparationHeartbeat(job)
		lastProgressAt := time.Time{}
		lastProgress := -1
		preparedURL, err := h.prepareSource(prepareCtx, video, sourceURL, func(progress SourcePreparationProgress) {
			now := time.Now()
			if progress.Percent == lastProgress && now.Sub(lastProgressAt) < 10*time.Second {
				return
			}
			lastProgress = progress.Percent
			lastProgressAt = now
			h.reportSourcePreparationProgress(job, progress)
		})
		stopPreparationHeartbeat()
		cancelPrepare()
		if err != nil {
			return fmt.Errorf("准备听悟兼容转写源失败: %w", err)
		}
		if preparedURL == "" {
			return fmt.Errorf("准备听悟兼容转写源返回空地址")
		}
		slog.Info("transcription source prepared", "video_id", video.ID, "elapsed_ms", time.Since(prepareStartedAt).Milliseconds())
		if preparedURL != sourceURL {
			if err := h.DB.Model(video).Update("transcription_source_url", preparedURL).Error; err != nil {
				return fmt.Errorf("保存听悟转写源: %w", err)
			}
			video.TranscriptionSourceURL = preparedURL
			sourceURL = preparedURL
		}
		if err := h.Tongyi.ValidateSourceFile(ctx, sourceURL); err != nil {
			return fmt.Errorf("视频源文件不可供听悟访问: %w", err)
		}
		slog.Info("tingwu create task", "video_id", video.ID)
		task, err := h.Tongyi.CreateTask(ctx, tongyi.CreateTaskRequest{
			FileURL:      sourceURL,
			SpeakerCount: 0, // 0 = 自动识别
		})
		if err != nil {
			return fmt.Errorf("create tingwu task: %w", err)
		}
		if task.TaskID == "" {
			return fmt.Errorf("听悟返回空 TaskID")
		}
		slog.Info("tingwu task created", "video_id", video.ID, "task_id", task.TaskID, "status", task.Status)
		if err := h.DB.Model(job).Update("external_task_id", task.TaskID).Error; err != nil {
			return fmt.Errorf("save external task id: %w", err)
		}
		job.ExternalTaskID = task.TaskID
		h.updateJobProgress(job, 0)
	}

	// 循环轮询，直到听悟完成 / 失败 / 上下文取消。
	// 听悟中间态有 ONGOING / SUBMITTED / RUNNING 等多种取值，这里只认终态
	// （COMPLETED / FAILED），其余一律视为进行中、等待后再查。
	pollCtx, cancelPoll := context.WithTimeout(ctx, transcriptionPollTimeout)
	defer cancelPoll()
	var task *tongyi.GetTaskResponse
	for {
		getCtx, cancel := context.WithTimeout(pollCtx, 30*time.Second)
		var err error
		task, err = h.Tongyi.GetTask(getCtx, job.ExternalTaskID)
		cancel()
		if err != nil {
			slog.Error("tingwu get task failed",
				"video_id", video.ID, "task_id", job.ExternalTaskID, "error", err)
			return fmt.Errorf("get tingwu task: %w", err)
		}
		slog.Info("tingwu poll",
			"video_id", video.ID, "task_id", job.ExternalTaskID,
			"status", task.Status, "progress", task.Progress,
			"err_code", task.ErrorCode, "err_msg", task.ErrorMessage)
		h.updateJobProgress(job, task.Progress)
		if task.Status == "COMPLETED" || task.Status == "FAILED" {
			break
		}
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("等待听悟任务完成超时: %w", pollCtx.Err())
		case <-time.After(30 * time.Second):
		}
	}

	if task.Status == "FAILED" {
		return fmt.Errorf("听悟失败 Code=%s Msg=%s", task.ErrorCode, task.ErrorMessage)
	}
	if task.Result == "" {
		return fmt.Errorf("听悟结果为空")
	}

	payload, _ := json.Marshal(map[string]any{
		"task_id":      job.ExternalTaskID,
		"raw_result":   task.Result,
		"completed_at": time.Now().UTC(),
	})
	if task.Result == "" {
		return fmt.Errorf("听悟结果为空")
	}

	// 结果和下游任务必须同事务提交，避免字幕任务先入队却读不到转写结果。
	subtitleJob := model.VideoProcessingJob{
		ID:                   uuid.NewString(),
		VideoID:              video.ID,
		JobType:              "subtitle_generate",
		TranscriptGeneration: job.TranscriptGeneration,
		Provider:             normalizeProvider(job.Provider),
		Status:               "pending",
		MaxAttempts:          3,
		InputPayload:         fmt.Sprintf(`{"transcription_job_id":%q}`, job.ID),
		IdempotencyKey:       fmt.Sprintf("subtitle_generate:%s:%s", video.ID, job.ID),
	}
	if err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(job).Update("result_payload", string(payload)).Error; err != nil {
			return fmt.Errorf("save transcription result: %w", err)
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "idempotency_key"}},
			DoNothing: true,
		}).Create(&subtitleJob).Error; err != nil {
			return fmt.Errorf("enqueue subtitle_generate: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}

// runMPS 保留独立的 MPS 任务状态机；结果直接使用 SegmentSet，避免下载/解析听悟 JSON。
func (h *TranscriptionHandler) runMPS(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	if h.MPS == nil {
		return fmt.Errorf("腾讯云 MPS client 未配置")
	}
	if video == nil || video.ID == "" {
		return fmt.Errorf("video is missing")
	}
	sourceURL := strings.TrimSpace(video.TranscriptionSourceURL)
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(video.FileURL)
	}
	if sourceURL == "" {
		return fmt.Errorf("video transcription source url is empty")
	}
	if err := h.DB.Model(video).Where("status IN ?", []string{model.VideoStatusReady, model.VideoStatusProcessing}).Update("status", model.VideoStatusProcessing).Error; err != nil {
		return fmt.Errorf("mark video processing: %w", err)
	}
	if job.ExternalTaskID == "" {
		if h.MPSInputPreparer != nil {
			preparedURL, prepareErr := h.MPSInputPreparer.Prepare(ctx, video, sourceURL)
			if prepareErr != nil {
				return fmt.Errorf("prepare mps input: %w", prepareErr)
			}
			if strings.TrimSpace(preparedURL) == "" {
				return fmt.Errorf("prepare mps input returned empty url")
			}
			sourceURL = preparedURL
		}
		taskID, err := h.MPS.CreateTask(ctx, sourceURL, mpsSessionID(job.ID))
		if err != nil {
			return fmt.Errorf("create mps task: %w", err)
		}
		if err := h.DB.Model(job).Updates(map[string]any{"external_task_id": taskID, "provider": mps.Provider}).Error; err != nil {
			return fmt.Errorf("save mps task id: %w", err)
		}
		job.ExternalTaskID = taskID
		job.Provider = mps.Provider
	}
	pollTimeout := h.MPS.Timeout()
	if pollTimeout <= 0 {
		pollTimeout = transcriptionPollTimeout
	}
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	interval := h.MPS.PollInterval()
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		task, err := h.MPS.GetTask(pollCtx, job.ExternalTaskID)
		if err != nil {
			return err
		}
		h.updateJobProgress(job, task.Progress)
		switch task.Status {
		case "FINISH":
			if task.ErrorCode != "" {
				return fmt.Errorf("MPS 任务失败 Code=%s Msg=%s", task.ErrorCode, task.ErrorMsg)
			}
			if len(task.Result.Segments) == 0 {
				return fmt.Errorf("MPS 结果不含转写片段")
			}
			resultJSON, err := task.Result.JSON()
			if err != nil {
				return fmt.Errorf("marshal mps result: %w", err)
			}
			if strings.TrimSpace(job.TranscriptGeneration) == "" {
				job.TranscriptGeneration = "mps:" + job.ExternalTaskID
			}
			payload, _ := json.Marshal(map[string]any{"task_id": job.ExternalTaskID, "provider": mps.Provider, "mps_result": json.RawMessage(resultJSON), "completed_at": time.Now().UTC()})
			subtitleJob := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "subtitle_generate", TranscriptGeneration: job.TranscriptGeneration, Provider: mps.Provider, Status: "pending", MaxAttempts: 3, InputPayload: fmt.Sprintf(`{"transcription_job_id":%q}`, job.ID), IdempotencyKey: fmt.Sprintf("subtitle_generate:%s:%s", video.ID, job.ID)}
			if err := h.DB.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(job).Updates(map[string]any{"result_payload": string(payload), "transcript_generation": job.TranscriptGeneration}).Error; err != nil {
					return err
				}
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&subtitleJob).Error; err != nil {
					return err
				}
				if !h.DraftsEnabled {
					return nil
				}
				draftInput := fmt.Sprintf(`{"transcription_job_id":%q,"transcript_generation":%q}`, job.ID, job.TranscriptGeneration)
				draftOutline := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "outline", TranscriptGeneration: job.TranscriptGeneration, Provider: "llm", ResultStage: "draft", Status: "pending", MaxAttempts: 3, InputPayload: draftInput, IdempotencyKey: fmt.Sprintf("outline:draft:%s:%s", video.ID, job.TranscriptGeneration)}
				draftSummary := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "summary", TranscriptGeneration: job.TranscriptGeneration, Provider: "llm", ResultStage: "draft", Status: "pending", MaxAttempts: 3, InputPayload: draftInput, IdempotencyKey: fmt.Sprintf("summary:draft:%s:%s", video.ID, job.TranscriptGeneration)}
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&draftOutline).Error; err != nil {
					return err
				}
				return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&draftSummary).Error
			}); err != nil {
				return fmt.Errorf("save mps result/enqueue subtitle: %w", err)
			}
			return nil
		case "WAITING", "PROCESSING", "":
			select {
			case <-pollCtx.Done():
				return fmt.Errorf("等待 MPS 任务完成超时: %w", pollCtx.Err())
			case <-time.After(interval):
			}
		default:
			return fmt.Errorf("MPS 返回未知任务状态: %s", task.Status)
		}
	}
}

func mpsSessionID(jobID string) string {
	value := strings.NewReplacer(":", "-", "/", "-", " ", "-").Replace(strings.TrimSpace(jobID))
	value = "transcription-" + value
	if len(value) > 50 {
		value = value[:50]
	}
	return value
}

func normalizeProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case mps.Provider, "mps":
		return mps.Provider
	default:
		return "aliyun_tingwu"
	}
}

func (h *TranscriptionHandler) prepareSource(ctx context.Context, video *model.Video, sourceURL string, report func(SourcePreparationProgress)) (string, error) {
	if h.SourcePreparer != nil {
		if preparer, ok := h.SourcePreparer.(ProgressSourcePreparer); ok {
			return preparer.PrepareWithProgress(ctx, video, report)
		}
		return h.SourcePreparer.Prepare(ctx, video)
	}
	if h.MinIO == nil {
		if report != nil {
			report(SourcePreparationProgress{Phase: "source_preparing", Percent: 100})
		}
		return sourceURL, nil
	}
	return (&mediaSourcePreparer{MinIO: h.MinIO, InternalFrontendBaseURL: h.InternalFrontendBaseURL}).PrepareWithProgress(ctx, video, report)
}

func (h *TranscriptionHandler) reportSourcePreparationProgress(job *model.VideoProcessingJob, progress SourcePreparationProgress) {
	percent := progress.Percent
	if percent < 0 {
		percent = 0
	}
	if percent > 99 {
		percent = 99
	}
	h.updateJobProgress(job, percent)
	slog.Debug("transcription source preparation progress", "video_id", job.VideoID, "phase", progress.Phase, "progress", percent)
}

func (h *TranscriptionHandler) updateJobProgress(job *model.VideoProcessingJob, progress int) {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	if job.Progress == progress {
		return
	}
	if err := h.DB.Model(job).Update("progress", progress).Error; err != nil {
		slog.Warn("update transcription progress failed", "video_id", job.VideoID, "job_id", job.ID, "error", err)
		return
	}
	job.Progress = progress
}

func (h *TranscriptionHandler) startPreparationHeartbeat(job *model.VideoProcessingJob) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := h.DB.Model(job).Update("updated_at", time.Now().UTC()).Error; err != nil {
					slog.Warn("update transcription heartbeat failed", "video_id", job.VideoID, "job_id", job.ID, "error", err)
				}
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
