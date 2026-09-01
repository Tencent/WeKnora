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
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
	db                    *gorm.DB
	cfg                   *config.WorkerConfig
	handlers              map[string]Handler
	cancel                context.CancelFunc
	wg                    sync.WaitGroup
	transcriptionProvider string
}

const stuckUploadTimeout = 30 * time.Minute

// NewEngine 构造引擎
func NewEngine(db *gorm.DB, cfg *config.WorkerConfig, handlers ...Handler) *Engine {
	e := &Engine{
		db:                    db,
		cfg:                   cfg,
		handlers:              make(map[string]Handler, len(handlers)),
		transcriptionProvider: "aliyun_tingwu",
	}
	for _, h := range handlers {
		e.handlers[h.JobType()] = h
	}
	return e
}

func (e *Engine) SetTranscriptionProvider(provider string) {
	e.transcriptionProvider = normalizeProvider(provider)
}

// Start 启动 worker 协程池
func (e *Engine) Start(parent context.Context) {
	if recovered, err := RecoverInterruptedJobs(e.db); err != nil {
		slog.Error("recover interrupted jobs", "component", "content-worker", "error", err)
	} else if recovered > 0 {
		slog.Warn("recovered interrupted jobs", "component", "content-worker", "job_count", recovered)
	}
	ctx, cancel := context.WithCancel(parent)
	e.cancel = cancel
	for i := 0; i < e.cfg.Concurrency; i++ {
		e.wg.Add(1)
		go e.loop(ctx, i)
	}
	slog.Info("worker engine started", "concurrency", e.cfg.Concurrency, "poll_interval_sec", e.cfg.PollIntervalSeconds)
}

func RecoverInterruptedJobs(db *gorm.DB) (int64, error) {
	result := db.Model(&model.VideoProcessingJob{}).
		Where("status = ?", "running").
		Updates(map[string]any{
			"status":        "pending",
			"attempt_count": gorm.Expr("CASE WHEN attempt_count > 0 THEN attempt_count - 1 ELSE 0 END"),
			"started_at":    nil, "completed_at": nil,
			"error_category": "", "error_code": "", "error_message": "",
		})
	return result.RowsAffected, result.Error
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
				ORDER BY CASE job_type
					WHEN 'thumbnail' THEN 0
					WHEN 'transcription' THEN 1
					WHEN 'subtitle_generate' THEN 2
					WHEN 'index' THEN 3
					WHEN 'outline' THEN 4
					WHEN 'summary' THEN 5
					WHEN 'assemble' THEN 6
					WHEN 'graph' THEN 7
					WHEN 'summary_enhance' THEN 8
					ELSE 9
				END, created_at ASC
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
		job.Status = "running"
		job.AttemptCount++
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
		e.markFailed(job, ErrorCategoryConfigurationAuth, "no_handler", "no handler registered", nil)
		return
	}

	var video model.Video
	if err := e.db.First(&video, "id = ?", job.VideoID).Error; err != nil {
		e.markFailed(job, ErrorCategoryDatabase, "video_not_found", err.Error(), err)
		return
	}
	if job.JobType != "thumbnail" {
		_ = e.db.Model(&model.Video{}).Where("id = ?", job.VideoID).Updates(map[string]any{
			"status": model.VideoStatusProcessing, "processing_error_summary": "",
		}).Error
	}

	if err := handler.Run(ctx, job, &video); err != nil {
		category, code := ClassifyProcessingError(err)
		slog.Warn("job run failed",
			"component", "content-worker", "video_id", job.VideoID, "job_id", job.ID,
			"job_type", job.JobType, "transcript_generation", job.TranscriptGeneration,
			"attempt", job.AttemptCount, "error_category", category, "error_code", code, "error", err)
		if code == "source_file_rejected" || job.AttemptCount >= job.MaxAttempts {
			e.markFailed(job, category, code, err.Error(), err)
		} else {
			// 退避：重置 pending 等下一轮 tick 重试
			e.db.Model(job).Updates(map[string]any{
				"status": "pending", "error_category": category,
				"error_code": code, "error_message": err.Error(),
			})
		}
		return
	}

	e.markSucceeded(job)
}

func (e *Engine) markSucceeded(job *model.VideoProcessingJob) {
	now := time.Now().UTC()
	e.db.Model(job).Updates(map[string]any{
		"status": "succeeded", "progress": 100, "completed_at": now,
		"error_category": "", "error_code": "", "error_message": "",
	})
	slog.Info("job completed",
		"component", "content-worker", "video_id", job.VideoID, "job_id", job.ID,
		"job_type", job.JobType, "transcript_generation", job.TranscriptGeneration,
		"status", "succeeded", "attempt", job.AttemptCount)
}

func (e *Engine) markFailed(job *model.VideoProcessingJob, category, code, msg string, cause error) {
	now := time.Now().UTC()
	e.db.Model(job).Updates(map[string]any{
		"status": "failed", "error_category": category,
		"error_code": code, "error_message": msg, "completed_at": now,
	})
	updates := map[string]any{"status": model.VideoStatusFailed, "processing_error_summary": msg}
	coverDegraded := false
	if isContentEnhancementJob(job.JobType) {
		updates["status"] = model.VideoStatusProcessing
		if job.JobType == "summary_enhance" {
			updates["processing_error_summary"] = "总结增强失败，基础内容仍可用"
		} else {
			updates["processing_error_summary"] = "知识提取失败，基础内容仍可用"
			updates["knowledge_audit_status"] = "failed"
		}
		var video model.Video
		if err := e.db.Select("outline_wiki_page_id", "summary_wiki_page_id", "transcript_page_wiki_page_id").First(&video, "id = ?", job.VideoID).Error; err == nil &&
			video.OutlineWikiPageID != "" && video.SummaryWikiPageID != "" && video.TranscriptPageWikiPageID != "" {
			updates["status"] = model.VideoStatusCompleted
		}
	}
	if job.JobType == "thumbnail" {
		var video model.Video
		if err := e.db.Select("file_url").First(&video, "id = ?", job.VideoID).Error; err != nil {
			updates["status"] = model.VideoStatusFailed
		} else {
			var coreFileUnavailable *CoreFileUnavailableError
			if errors.As(cause, &coreFileUnavailable) || video.FileURL == "" {
				updates["status"] = model.VideoStatusFailed
			} else {
				// 核心文件完好、仅封面彻底失败：降级为占位图展示，不阻塞视频露出
				coverDegraded = true
				updates["status"] = model.VideoStatusReady
				updates["ready_at"] = now
				updates["processing_error_summary"] = "封面生成失败，已使用占位图展示"
			}
		}
	}
	e.db.Model(&model.Video{}).
		Where("id = ?", job.VideoID).
		Updates(updates)
	slog.Error("job failed",
		"component", "content-worker", "video_id", job.VideoID, "job_id", job.ID,
		"job_type", job.JobType, "transcript_generation", job.TranscriptGeneration,
		"status", "failed", "error_category", category, "error_code", code, "error", cause)
	if coverDegraded {
		e.enqueueTranscriptionAfterCoverFallback(job.VideoID)
	}
}

func isContentEnhancementJob(jobType string) bool {
	return jobType == "graph" || jobType == "summary_enhance"
}

// enqueueTranscriptionAfterCoverFallback 封面降级后补投转写任务，避免内容链路死路。
// 正常链路里转写由 thumbnail 成功路径入队；仅注册了 transcription handler（内容链路开启）时才补投。
func (e *Engine) enqueueTranscriptionAfterCoverFallback(videoID string) {
	if _, ok := e.handlers["transcription"]; !ok {
		return
	}
	job := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: videoID, JobType: "transcription", Provider: normalizeProvider(e.transcriptionProvider),
		Status: "pending", MaxAttempts: 3, IdempotencyKey: fmt.Sprintf("transcription:%s", videoID),
	}
	if err := e.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&job).Error; err != nil {
		slog.Warn("enqueue transcription after cover fallback", "video_id", videoID, "error", err)
	}
}

// ErrRetryable 标识 job 可重试（暂留接口位）
var ErrRetryable = errors.New("retryable error")
