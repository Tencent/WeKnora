// Package model 定义自研业务库的 GORM 模型。
// 遵循「单一数据源 + 引用映射」：视频内容存 WeKnora，这里只存 WeKnora 对象 ID 引用。
package model

import (
	"time"

	"gorm.io/gorm"
)

// Video 视频元数据 + WeKnora 内容引用
type Video struct {
	ID                       string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	Title                    string         `gorm:"type:varchar(255);not null" json:"title"`
	VideoType                string         `gorm:"type:varchar(50);index" json:"video_type"` // interview/tutorial/lecture/case_analysis
	DurationSeconds          int            `json:"duration_seconds"`
	FileURL                  string         `gorm:"type:text" json:"file_url"`
	ThumbnailURL             string         `gorm:"type:text" json:"thumbnail_url"`
	SubtitleFileURL          string         `gorm:"type:text" json:"subtitle_file_url"`
	TranscriptKnowledgeID    string         `gorm:"type:varchar(64)" json:"transcript_knowledge_id"` // 兼容入口锚点；完整集合见 video_transcript_chunks
	TranscriptGeneration     string         `gorm:"type:varchar(64);index" json:"transcript_generation"`
	TranscriptRevision       int64          `json:"transcript_revision"`
	TranscriptActiveRevision int64          `json:"transcript_active_revision"`
	KnowledgeBaseWikiPageID  string         `gorm:"type:varchar(64)" json:"knowledge_base_wiki_page_id"` // extract-video-knowledge 产物「知识底座」索引页 ID
	OutlineWikiPageID        string         `gorm:"type:varchar(64)" json:"outline_wiki_page_id"`
	OverviewWikiPageID       string         `gorm:"type:varchar(64)" json:"overview_wiki_page_id"`
	SummaryWikiPageID        string         `gorm:"type:varchar(64)" json:"summary_wiki_page_id"`
	TranscriptPageWikiPageID string         `gorm:"type:varchar(64)" json:"transcript_page_wiki_page_id"`
	Status                   string         `gorm:"type:varchar(50);index" json:"status"`
	ProcessingErrorSummary   string         `gorm:"type:text" json:"processing_error_summary"`
	UploadID                 string         `gorm:"type:varchar(128);index" json:"-"`
	UploadIdempotencyKey     string         `gorm:"type:varchar(128);index" json:"-"`
	UploadObjectKey          string         `gorm:"type:text" json:"-"`
	UploadSizeBytes          int64          `json:"-"`
	UploadPartSizeBytes      int64          `json:"-"`
	UploadedAt               *time.Time     `json:"uploaded_at"`
	ReadyAt                  *time.Time     `json:"ready_at"`
	CreatedAt                time.Time      `json:"created_at"`
	UpdatedAt                time.Time      `json:"updated_at"`
	DeletedAt                gorm.DeletedAt `gorm:"index" json:"-"`
}

// VideoTranscriptChunk 保存方案 A 中每个字幕块对应的 WeKnora Knowledge。
// (video_id, generation, chunk_index) 唯一，作为跨 HTTP 重试的持久化检查点。
type VideoTranscriptChunk struct {
	VideoID     string    `gorm:"type:varchar(36);primaryKey" json:"video_id"`
	Generation  string    `gorm:"type:varchar(64);primaryKey" json:"generation"`
	Revision    int64     `gorm:"not null;index" json:"revision"`
	ChunkIndex  int       `gorm:"primaryKey" json:"chunk_index"`
	KnowledgeID string    `gorm:"type:varchar(64);uniqueIndex" json:"knowledge_id"`
	ContentHash string    `gorm:"type:varchar(64);not null" json:"content_hash"`
	Status      string    `gorm:"type:varchar(32);not null" json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// VideoProcessingJob 视频处理任务状态机
type VideoProcessingJob struct {
	ID                 string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	VideoID            string     `gorm:"type:varchar(36);index:idx_video_job_status,priority:1" json:"video_id"`
	JobType            string     `gorm:"type:varchar(50);index:idx_video_job_status,priority:2" json:"job_type"` // thumbnail/transcription/subtitle_generate/index/graph/outline/overview/summary/assemble
	Provider           string     `gorm:"type:varchar(50)" json:"provider"`                                       // local/aliyun_tingwu/weknora
	ExternalTaskID     string     `gorm:"type:varchar(128);index" json:"external_task_id"`
	IdempotencyKey     string     `gorm:"type:varchar(128);uniqueIndex" json:"idempotency_key"`
	Status             string     `gorm:"type:varchar(50);index:idx_video_job_status,priority:3" json:"status"` // pending/running/succeeded/failed/cancelled
	Progress           int        `json:"progress"`
	AttemptCount       int        `json:"attempt_count"`
	MaxAttempts        int        `json:"max_attempts"`
	InputPayload       string     `gorm:"type:text" json:"input_payload"`
	ResultPayload      string     `gorm:"type:text" json:"result_payload"`
	ErrorCode          string     `gorm:"type:varchar(100)" json:"error_code"`
	ErrorMessage       string     `gorm:"type:text" json:"error_message"`
	CallbackReceivedAt *time.Time `json:"callback_received_at"`
	StartedAt          *time.Time `json:"started_at"`
	CompletedAt        *time.Time `json:"completed_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// VideoSummaryFramework 视频类型 → 总结框架路由
type VideoSummaryFramework struct {
	ID        string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	VideoType string    `gorm:"type:varchar(50);uniqueIndex" json:"video_type"` // interview/tutorial/lecture/case_analysis
	Framework string    `gorm:"type:text" json:"framework"`                     // 总结框架定义（JSON）
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
