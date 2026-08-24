// Package worker transcription job 处理（VP-T005）。
//
// 设计要点：
//   - 调听悟 CreateTask 拿 external_task_id 持久化
//   - 轮询 GetTask；callback 启用时也可走回调（本版本先实现轮询）
//   - 完成后把转写 JSON 暂存 result_payload，并触发 subtitle_generate job
//   - 失败按 attempt_count / max_attempts 重试
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/custom/client/tongyi"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

// TranscriptionHandler 转写 job
type TranscriptionHandler struct {
	DB     *gorm.DB
	Tongyi *tongyi.Client
}

// NewTranscriptionHandler 构造
func NewTranscriptionHandler(db *gorm.DB, t *tongyi.Client) *TranscriptionHandler {
	return &TranscriptionHandler{DB: db, Tongyi: t}
}

// JobType job 类型
func (h *TranscriptionHandler) JobType() string { return "transcription" }

// Run 编排：发起 → 轮询 → 下载 → 写 result → 触发下游
func (h *TranscriptionHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	if h.Tongyi == nil {
		return fmt.Errorf("听悟 client 未配置")
	}
	if err := h.DB.Model(video).
		Where("status IN ?", []string{model.VideoStatusReady, model.VideoStatusProcessing}).
		Update("status", model.VideoStatusProcessing).Error; err != nil {
		return fmt.Errorf("mark video processing: %w", err)
	}

	// 第一次跑：创建 external task
	if job.ExternalTaskID == "" {
		slog.Info("tingwu create task", "video_id", video.ID, "file_url", video.FileURL)
		task, err := h.Tongyi.CreateTask(ctx, tongyi.CreateTaskRequest{
			FileURL:      video.FileURL,
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
	}

	// 循环轮询，直到听悟完成 / 失败 / 上下文取消。
	// 听悟中间态有 ONGOING / SUBMITTED / RUNNING 等多种取值，这里只认终态
	// （COMPLETED / FAILED），其余一律视为进行中、等待后再查。
	var task *tongyi.GetTaskResponse
	for {
		getCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
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
		if task.Status == "COMPLETED" || task.Status == "FAILED" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}

	if task.Status == "FAILED" {
		return fmt.Errorf("听悟失败 Code=%s Msg=%s", task.ErrorCode, task.ErrorMessage)
	}
	if task.Result == "" {
		return fmt.Errorf("听悟结果为空")
	}

	// 写 result_payload + 触发 subtitle_generate job
	subtitleJob := model.VideoProcessingJob{
		ID:             uuid.NewString(),
		VideoID:        video.ID,
		JobType:        "subtitle_generate",
		Provider:       "aliyun_tingwu",
		Status:         "pending",
		MaxAttempts:    3,
		IdempotencyKey: fmt.Sprintf("subtitle_generate:%s", video.ID),
	}
	if err := h.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "idempotency_key"}},
		DoNothing: true,
	}).Create(&subtitleJob).Error; err != nil {
		return fmt.Errorf("enqueue subtitle_generate: %w", err)
	}

	// 把 result 写回 job（供 subtitle_generate 读取）
	payload, _ := json.Marshal(map[string]any{
		"task_id":      job.ExternalTaskID,
		"raw_result":   task.Result,
		"completed_at": time.Now().UTC(),
	})
	if err := h.DB.Model(job).Update("result_payload", string(payload)).Error; err != nil {
		return fmt.Errorf("save transcription result: %w", err)
	}

	return nil
}
