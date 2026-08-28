package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

const (
	ProcessingStateReady     = "ready"
	ProcessingStateRunning   = "processing"
	ProcessingStatePartial   = "partial_completed"
	ProcessingStateCompleted = "completed"
	ProcessingStateFailed    = "failed"
)

var processingStageOrder = []string{
	"transcription",
	"subtitle_generate",
	"index",
	"graph",
	"outline",
	"overview",
	"summary",
	"assemble",
}

var retryableProcessingStages = map[string]bool{
	"transcription":     true,
	"subtitle_generate": true,
	"index":             true,
	"graph":             true,
	"outline":           true,
	"overview":          true,
	"summary":           true,
	"assemble":          true,
}

type ProcessingFailure struct {
	JobID     string    `json:"job_id"`
	JobType   string    `json:"job_type"`
	Category  string    `json:"category"`
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	UpdatedAt time.Time `json:"updated_at"`
}

type RetryableProcessingJob struct {
	JobID   string `json:"job_id"`
	JobType string `json:"job_type"`
}

type ProcessingJobStatus struct {
	JobID                string     `json:"job_id"`
	JobType              string     `json:"job_type"`
	TranscriptGeneration string     `json:"transcript_generation"`
	Provider             string     `json:"provider,omitempty"`
	ExternalTaskID       string     `json:"external_task_id,omitempty"`
	Status               string     `json:"status"`
	Progress             int        `json:"progress"`
	AttemptCount         int        `json:"attempt_count"`
	MaxAttempts          int        `json:"max_attempts"`
	InputAvailable       bool       `json:"input_available"`
	ResultAvailable      bool       `json:"result_available"`
	ErrorCategory        string     `json:"error_category,omitempty"`
	ErrorCode            string     `json:"error_code,omitempty"`
	ErrorMessage         string     `json:"error_message,omitempty"`
	UpdatedAt            time.Time  `json:"updated_at"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
}

type ProcessingStatusResponse struct {
	VideoID              string                  `json:"video_id"`
	Status               string                  `json:"status"`
	CurrentStage         string                  `json:"current_stage,omitempty"`
	TranscriptGeneration string                  `json:"transcript_generation,omitempty"`
	CompletedStages      []string                `json:"completed_stages"`
	Failure              *ProcessingFailure      `json:"failure,omitempty"`
	RetryableJob         *RetryableProcessingJob `json:"retryable_job,omitempty"`
	Jobs                 []ProcessingJobStatus   `json:"jobs"`
	UpdatedAt            time.Time               `json:"updated_at"`
}

type ProcessingHandler struct {
	DB *gorm.DB
}

func NewProcessingHandler(db *gorm.DB) *ProcessingHandler {
	return &ProcessingHandler{DB: db}
}

func (h *ProcessingHandler) Status(c *gin.Context) {
	video, jobs, ok := h.load(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, buildProcessingStatus(video, jobs))
}

func (h *ProcessingHandler) Retry(c *gin.Context) {
	videoID := c.Param("id")
	jobType := c.Param("jobType")
	if !retryableProcessingStages[jobType] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported processing stage"})
		return
	}

	var retried model.VideoProcessingJob
	err := h.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var video model.Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&video, "id = ?", videoID).Error; err != nil {
			return err
		}
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("video_id = ? AND job_type = ?", videoID, jobType)
		if video.TranscriptGeneration != "" {
			query = query.Where("transcript_generation IN ?", []string{"", video.TranscriptGeneration})
		}
		if err := query.Order("CASE WHEN transcript_generation = '' THEN 1 ELSE 0 END, updated_at DESC").First(&retried).Error; err != nil {
			return err
		}
		if retried.Status == "succeeded" && stageArtifactAvailable(video, retried) {
			return errStageAlreadySucceeded
		}
		if retried.Status == "pending" || retried.Status == "running" {
			return errStageInProgress
		}
		if retried.Status == "failed" || retried.Status == "cancelled" || retried.Status == "succeeded" {
			updates := map[string]any{
				"status": "pending", "progress": 0, "attempt_count": 0,
				"error_category": "", "error_code": "", "error_message": "",
				"started_at": nil, "completed_at": nil,
			}
			if jobType == "transcription" && retried.ErrorCategory == "external_task" {
				updates["external_task_id"] = ""
			}
			if err := tx.Model(&retried).Updates(updates).Error; err != nil {
				return err
			}
		}
		return tx.Model(&model.Video{}).Where("id = ?", videoID).Updates(map[string]any{
			"status": model.VideoStatusProcessing, "processing_error_summary": "",
		}).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "video or processing stage not found"})
		return
	}
	if errors.Is(err, errStageAlreadySucceeded) {
		c.JSON(http.StatusConflict, gin.H{"error": "successful stage cannot be retried"})
		return
	}
	if errors.Is(err, errStageInProgress) {
		c.JSON(http.StatusConflict, gin.H{"error": "processing stage is already in progress"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"job_id": retried.ID, "job_type": retried.JobType, "status": "pending", "reused": true})
}

var errStageAlreadySucceeded = errors.New("processing stage already succeeded")
var errStageInProgress = errors.New("processing stage is already in progress")

func (h *ProcessingHandler) load(c *gin.Context) (model.Video, []model.VideoProcessingJob, bool) {
	var video model.Video
	if err := h.DB.First(&video, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
		return model.Video{}, nil, false
	}
	var jobs []model.VideoProcessingJob
	if err := h.DB.Where("video_id = ?", video.ID).Order("updated_at ASC").Find(&jobs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return model.Video{}, nil, false
	}
	return video, jobs, true
}

func buildProcessingStatus(video model.Video, jobs []model.VideoProcessingJob) ProcessingStatusResponse {
	latest := make(map[string]model.VideoProcessingJob, len(processingStageOrder))
	for _, job := range jobs {
		if !retryableProcessingStages[job.JobType] {
			continue
		}
		if video.TranscriptGeneration != "" && job.TranscriptGeneration != "" && job.TranscriptGeneration != video.TranscriptGeneration {
			continue
		}
		previous, exists := latest[job.JobType]
		candidateIsCurrent := video.TranscriptGeneration != "" && job.TranscriptGeneration == video.TranscriptGeneration
		previousIsCurrent := video.TranscriptGeneration != "" && previous.TranscriptGeneration == video.TranscriptGeneration
		if !exists || (candidateIsCurrent && !previousIsCurrent) || (candidateIsCurrent == previousIsCurrent && job.UpdatedAt.After(previous.UpdatedAt)) {
			latest[job.JobType] = job
		}
	}

	response := ProcessingStatusResponse{
		VideoID: video.ID, Status: ProcessingStateReady,
		TranscriptGeneration: video.TranscriptGeneration,
		CompletedStages:      make([]string, 0, len(latest)),
		Jobs:                 make([]ProcessingJobStatus, 0, len(latest)),
		UpdatedAt:            video.UpdatedAt,
	}
	var active *model.VideoProcessingJob
	var failed *model.VideoProcessingJob
	for _, stage := range processingStageOrder {
		job, exists := latest[stage]
		if !exists {
			continue
		}
		jobStatus := processingJobStatus(job)
		if job.Status == "succeeded" && !stageArtifactAvailable(video, job) {
			jobStatus.Status = "failed"
			jobStatus.ErrorCategory, jobStatus.ErrorCode, jobStatus.ErrorMessage = missingStageArtifactError(stage)
		}
		response.Jobs = append(response.Jobs, jobStatus)
		if job.UpdatedAt.After(response.UpdatedAt) {
			response.UpdatedAt = job.UpdatedAt
		}
		switch job.Status {
		case "succeeded":
			if stageArtifactAvailable(video, job) {
				response.CompletedStages = append(response.CompletedStages, stage)
			} else if failed == nil {
				copy := job
				copy.Status = "failed"
				copy.ErrorCategory, copy.ErrorCode, copy.ErrorMessage = missingStageArtifactError(stage)
				failed = &copy
			}
		case "failed":
			if failed == nil {
				copy := job
				failed = &copy
			}
		case "pending", "running":
			if active == nil {
				copy := job
				active = &copy
			}
		}
	}

	if failed != nil {
		response.Status = ProcessingStateFailed
		response.CurrentStage = failed.JobType
		response.Failure = &ProcessingFailure{
			JobID: failed.ID, JobType: failed.JobType, Category: fallbackCategory(failed.ErrorCategory),
			Code: failed.ErrorCode, Message: failed.ErrorMessage, UpdatedAt: failed.UpdatedAt,
		}
		response.RetryableJob = &RetryableProcessingJob{JobID: failed.ID, JobType: failed.JobType}
		return response
	}
	if active != nil {
		response.Status = ProcessingStateRunning
		if len(response.CompletedStages) > 0 {
			response.Status = ProcessingStatePartial
		}
		response.CurrentStage = active.JobType
		return response
	}
	if assemble, ok := latest["assemble"]; ok && assemble.Status == "succeeded" && hasReadableContentReferences(video) {
		response.Status = ProcessingStateCompleted
		response.CurrentStage = "assemble"
		return response
	}
	if len(response.CompletedStages) > 0 {
		response.Status = ProcessingStatePartial
		response.CurrentStage = nextIncompleteStage(latest)
	}
	return response
}

func processingJobStatus(job model.VideoProcessingJob) ProcessingJobStatus {
	return ProcessingJobStatus{
		JobID: job.ID, JobType: job.JobType, TranscriptGeneration: job.TranscriptGeneration,
		Provider: job.Provider, ExternalTaskID: job.ExternalTaskID,
		Status: job.Status, Progress: job.Progress, AttemptCount: job.AttemptCount, MaxAttempts: job.MaxAttempts,
		InputAvailable: strings.TrimSpace(job.InputPayload) != "", ResultAvailable: strings.TrimSpace(job.ResultPayload) != "",
		ErrorCategory: job.ErrorCategory, ErrorCode: job.ErrorCode, ErrorMessage: job.ErrorMessage,
		UpdatedAt: job.UpdatedAt, StartedAt: job.StartedAt, CompletedAt: job.CompletedAt,
	}
}

func nextIncompleteStage(latest map[string]model.VideoProcessingJob) string {
	for _, stage := range processingStageOrder {
		job, ok := latest[stage]
		if !ok || job.Status != "succeeded" {
			return stage
		}
	}
	return ""
}

func stageArtifactAvailable(video model.Video, job model.VideoProcessingJob) bool {
	switch job.JobType {
	case "transcription":
		return strings.TrimSpace(job.ResultPayload) != ""
	case "subtitle_generate":
		return strings.TrimSpace(video.SubtitleFileURL) != ""
	case "index":
		return strings.TrimSpace(video.TranscriptGeneration) != "" && strings.TrimSpace(video.TranscriptKnowledgeID) != ""
	case "graph":
		return strings.TrimSpace(video.KnowledgeBaseWikiPageID) != ""
	case "outline":
		return strings.TrimSpace(video.OutlineWikiPageID) != ""
	case "overview":
		return strings.TrimSpace(video.OverviewWikiPageID) != ""
	case "summary":
		return strings.TrimSpace(video.SummaryWikiPageID) != ""
	case "assemble":
		return hasReadableContentReferences(video)
	default:
		return true
	}
}

func missingStageArtifactError(jobType string) (string, string, string) {
	switch jobType {
	case "transcription":
		return "response_parse", "transcription_result_missing", "transcription completed without a readable result"
	case "subtitle_generate":
		return "object_storage", "subtitle_artifact_missing", "subtitle stage completed without a readable file"
	case "index":
		return "weknora", "transcript_index_missing", "index stage completed without an active transcript reference"
	default:
		return "wiki_artifact", "content_artifact_missing", "stage completed but referenced content artifact is unavailable"
	}
}

func hasReadableContentReferences(video model.Video) bool {
	return strings.TrimSpace(video.KnowledgeBaseWikiPageID) != "" &&
		strings.TrimSpace(video.OutlineWikiPageID) != "" &&
		strings.TrimSpace(video.OverviewWikiPageID) != "" &&
		strings.TrimSpace(video.SummaryWikiPageID) != "" &&
		strings.TrimSpace(video.TranscriptPageWikiPageID) != ""
}

func fallbackCategory(category string) string {
	if category == "" {
		return "unknown"
	}
	return category
}
