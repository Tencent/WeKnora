// Package worker 后台任务引擎（VP-T003 / VP-T005 / VP-T009）。
//
// 设计要点：
//   - 扫描 `video_processing_jobs` 表，按 job_type 派发到对应 handler
//   - 状态机：pending → running → succeeded / failed / cancelled
//   - 失败按 max_attempts 重试，超限置 failed；幂等键由 idempotency_key 唯一约束保证
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

// Handler 各类 job 的具体处理函数
type Handler interface {
	JobType() string
	Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error
}

// Engine 任务引擎
type Engine struct {
	db       *gorm.DB
	cfg      *config.WorkerConfig
	handlers map[string]Handler
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

const stuckUploadTimeout = 30 * time.Minute

// NewEngine 构造引擎
func NewEngine(db *gorm.DB, cfg *config.WorkerConfig, handlers ...Handler) *Engine {
	e := &Engine{
		db:       db,
		cfg:      cfg,
		handlers: make(map[string]Handler, len(handlers)),
	}
	for _, h := range handlers {
		e.handlers[h.JobType()] = h
	}
	return e
}

// Start 启动 worker 协程池
func (e *Engine) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	e.cancel = cancel
	for i := 0; i < e.cfg.Concurrency; i++ {
		e.wg.Add(1)
		go e.loop(ctx, i)
	}
	slog.Info("worker engine started", "concurrency", e.cfg.Concurrency, "poll_interval_sec", e.cfg.PollIntervalSeconds)
}

// Stop 优雅关闭
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
}

// loop 单个 worker 循环
func (e *Engine) loop(ctx context.Context, id int) {
	defer e.wg.Done()
	ticker := time.NewTicker(time.Duration(e.cfg.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.tick(ctx); err != nil {
				slog.Warn("worker tick", "id", id, "error", err)
			}
		}
	}
}

// tick 处理一轮：扫描 pending / 重试 failed-pending 的 job
func (e *Engine) tick(ctx context.Context) error {
	if _, err := CleanupStuckUploads(e.db, time.Now().UTC(), stuckUploadTimeout); err != nil {
		return err
	}
	for {
		var job model.VideoProcessingJob
		err := e.db.Transaction(func(tx *gorm.DB) error {
			err := tx.Raw(`
				SELECT * FROM video_processing_jobs
				WHERE status = 'pending'
				ORDER BY created_at ASC
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			`).Scan(&job).Error
			if err != nil {
				return err
			}
			if job.ID == "" {
				return nil // 无可处理任务
			}
			now := time.Now().UTC()
			return tx.Model(&job).Updates(map[string]any{
				"status":        "running",
				"started_at":    now,
				"attempt_count": job.AttemptCount + 1,
			}).Error
		})
		if err != nil {
			return err
		}
		if job.ID == "" {
			return nil
		}
		e.dispatch(ctx, &job)
	}
}

// CleanupStuckUploads closes upload records that can no longer make progress.
// A processing job is deliberately excluded: its retry state owns the next
// transition, while an uploading row without a job is an orphan.
func CleanupStuckUploads(db *gorm.DB, now time.Time, timeout time.Duration) (int64, error) {
	if timeout <= 0 {
		timeout = stuckUploadTimeout
	}
	cutoff := now.Add(-timeout)
	result := db.Model(&model.Video{}).
		Where("status = ? AND (updated_at < ? OR (updated_at IS NULL AND created_at < ?))", model.VideoStatusUploading, cutoff, cutoff).
		Where("NOT EXISTS (?)", db.Model(&model.VideoProcessingJob{}).Select("1").Where("video_processing_jobs.video_id = videos.id")).
		Updates(map[string]any{
			"status":                   model.VideoStatusFailed,
			"processing_error_summary": "upload timed out without a processing job",
		})
	return result.RowsAffected, result.Error
}

// dispatch 执行单 job（状态回写 + 重试判断）
func (e *Engine) dispatch(ctx context.Context, job *model.VideoProcessingJob) {
	handler, ok := e.handlers[job.JobType]
	if !ok {
		slog.Warn("no handler for job_type", "job_type", job.JobType, "job_id", job.ID)
		e.markFailed(job, "no_handler", "no handler registered")
		return
	}

	var video model.Video
	if err := e.db.First(&video, "id = ?", job.VideoID).Error; err != nil {
		e.markFailed(job, "video_not_found", err.Error())
		return
	}

	if err := handler.Run(ctx, job, &video); err != nil {
		slog.Warn("job run failed", "job_id", job.ID, "job_type", job.JobType, "attempt", job.AttemptCount, "error", err)
		if job.AttemptCount >= job.MaxAttempts {
			e.markFailed(job, "max_attempts", err.Error())
		} else {
			// 退避：重置 pending 等下一轮 tick 重试
			e.db.Model(job).Updates(map[string]any{
				"status":        "pending",
				"error_message": err.Error(),
			})
		}
		return
	}

	e.markSucceeded(job)
}

func (e *Engine) markSucceeded(job *model.VideoProcessingJob) {
	now := time.Now().UTC()
	e.db.Model(job).Updates(map[string]any{
		"status":       "succeeded",
		"progress":     100,
		"completed_at": now,
	})
}

func (e *Engine) markFailed(job *model.VideoProcessingJob, code, msg string) {
	now := time.Now().UTC()
	e.db.Model(job).Updates(map[string]any{
		"status":        "failed",
		"error_code":    code,
		"error_message": msg,
		"completed_at":  now,
	})
	updates := map[string]any{"processing_error_summary": msg}
	if job.JobType == "thumbnail" {
		updates["status"] = model.VideoStatusFailed
	}
	e.db.Model(&model.Video{}).
		Where("id = ?", job.VideoID).
		Updates(updates)
}

// ErrRetryable 标识 job 可重试（暂留接口位）
var ErrRetryable = errors.New("retryable error")
