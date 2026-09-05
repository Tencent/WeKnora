package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/outline"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
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
	"outline",
	"summary",
	"assemble",
	"graph",
	"summary_enhance",
}

var retryableProcessingStages = map[string]bool{
	"transcription":     true,
	"subtitle_generate": true,
	"index":             true,
	"graph":             true,
	"summary_enhance":   true,
	"outline":           true,
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
	Phase                string     `json:"phase,omitempty"`
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
	FoundationStatus     string                  `json:"foundation_status"`
	EnhancementStatus    string                  `json:"enhancement_status"`
	CurrentStage         string                  `json:"current_stage,omitempty"`
	TranscriptGeneration string                  `json:"transcript_generation,omitempty"`
	CompletedStages      []string                `json:"completed_stages"`
	Failure              *ProcessingFailure      `json:"failure,omitempty"`
	EnhancementFailure   *ProcessingFailure      `json:"enhancement_failure,omitempty"`
	RetryableJob         *RetryableProcessingJob `json:"retryable_job,omitempty"`
	Jobs                 []ProcessingJobStatus   `json:"jobs"`
	UpdatedAt            time.Time               `json:"updated_at"`
}

type ProcessingHandler struct {
	DB   *gorm.DB
	Wiki *weknora.WikiClient
	KBID string
}

type ProcessingDependencies struct {
	Wiki *weknora.WikiClient
	KBID string
}

func NewProcessingHandler(db *gorm.DB, dependencies ...ProcessingDependencies) *ProcessingHandler {
	handler := &ProcessingHandler{DB: db}
	if len(dependencies) > 0 {
		handler.Wiki = dependencies[0].Wiki
		handler.KBID = dependencies[0].KBID
	}
	return handler
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
	recreated := false
	err := h.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var video model.Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&video, "id = ?", videoID).Error; err != nil {
			return err
		}
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("video_id = ? AND job_type = ?", videoID, jobType)
		// A successful transcription job carries the provider task generation;
		// the video generation is assigned later when the normalized transcript
		// is indexed. Filtering transcription retries by the latter would hide
		// the only retryable job after a completed run.
		if video.TranscriptGeneration != "" && jobType != "transcription" {
			query = query.Where("transcript_generation IN ?", []string{"", video.TranscriptGeneration})
		}
		if err := query.Order("CASE WHEN transcript_generation = '' THEN 1 ELSE 0 END, updated_at DESC").First(&retried).Error; err != nil {
			return err
		}
		if retried.Status == "succeeded" && jobType == "transcription" {
			recreatedJob := model.VideoProcessingJob{
				ID: uuid.NewString(), VideoID: videoID, JobType: "transcription", Provider: retried.Provider,
				Status: "pending", MaxAttempts: 3,
				IdempotencyKey: fmt.Sprintf("transcription:%s:rerun:%s", videoID, uuid.NewString()),
			}
			if err := tx.Create(&recreatedJob).Error; err != nil {
				return err
			}
			retried = recreatedJob
			recreated = true
		} else if retried.Status == "succeeded" && !allowsExplicitSummaryRegeneration(jobType) && h.stageArtifactAvailable(c.Request.Context(), video, retried) {
			return errStageAlreadySucceeded
		} else if retried.Status == "pending" || retried.Status == "running" {
			return errStageInProgress
		} else if retried.Status == "failed" || retried.Status == "cancelled" || retried.Status == "succeeded" {
			updates := map[string]any{
				"status": "pending", "progress": 0, "attempt_count": 0,
				"error_category": "", "error_code": "", "error_message": "",
				"started_at": nil, "completed_at": nil,
			}
			// A failed external task is terminal at the provider. Retrying must
			// create a fresh provider task so a newly prepared source URL is used;
			// otherwise the worker would keep polling the old failed task.
			if jobType == "transcription" {
				updates["external_task_id"] = ""
			}
			if jobType == "summary" || jobType == "summary_enhance" {
				inputPayload, payloadErr := skill.MarkExplicitSummaryRegeneration(retried.InputPayload)
				if payloadErr != nil {
					return fmt.Errorf("mark explicit summary regeneration: %w", payloadErr)
				}
				updates["input_payload"] = inputPayload
			}
			if err := tx.Model(&retried).Updates(updates).Error; err != nil {
				return err
			}
		}
		if jobType == "graph" {
			return nil
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
	c.JSON(http.StatusOK, gin.H{"job_id": retried.ID, "job_type": retried.JobType, "status": "pending", "reused": !recreated})
}

func (h *ProcessingHandler) stageArtifactAvailable(ctx context.Context, video model.Video, job model.VideoProcessingJob) bool {
	if job.ResultStage == "draft" {
		return stageArtifactAvailable(video, job)
	}
	if (job.JobType != "outline" && job.JobType != "summary") || h.Wiki == nil || strings.TrimSpace(h.KBID) == "" {
		return stageArtifactAvailable(video, job)
	}
	pageID := video.OutlineWikiPageID
	if job.JobType == "summary" {
		pageID = video.SummaryWikiPageID
	}
	if job.ResultStage == "draft" {
		if job.JobType == "outline" {
			pageID = video.OutlineDraftWikiPageID
		} else if job.JobType == "summary" {
			pageID = video.SummaryDraftWikiPageID
		}
	}
	if strings.TrimSpace(pageID) == "" {
		return false
	}
	page, err := h.Wiki.GetPageByID(ctx, h.KBID, pageID)
	if err != nil || page == nil || strings.TrimSpace(page.Content) == "" {
		return false
	}
	frontmatter := page.ParsedFrontmatter()
	actualType, _ := frontmatter["type"].(string)
	sourceVideoID, _ := frontmatter["source_video_id"].(string)
	pageGeneration, _ := frontmatter["transcript_generation"].(string)
	expectedType := "outline"
	if job.JobType == "summary" {
		expectedType = "typed_summary"
	}
	if actualType != expectedType || sourceVideoID != video.ID ||
		(strings.TrimSpace(video.TranscriptGeneration) != "" && pageGeneration != video.TranscriptGeneration) {
		return false
	}
	if job.JobType == "summary" {
		document, parseErr := summary.ParseStored(page.Content)
		return parseErr == nil && summary.ValidateStored(document, video.VideoType) == nil
	}
	document, parseErr := outline.Parse(page.Content)
	if parseErr != nil {
		return outline.IsLegacyMarkdown(page.Content)
	}
	pageSchemaVersion, ok := frontmatterInt(frontmatter, "schema_version")
	return ok && pageSchemaVersion == outline.SchemaVersion && outline.Validate(document, video.DurationSeconds, nil) == nil
}

var errStageAlreadySucceeded = errors.New("processing stage already succeeded")
var errStageInProgress = errors.New("processing stage is already in progress")

func allowsExplicitSummaryRegeneration(jobType string) bool {
	return jobType == "summary" || jobType == "summary_enhance"
}

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
		isUpstreamStage := job.JobType == "transcription" || job.JobType == "subtitle_generate"
		if !isUpstreamStage && video.TranscriptGeneration != "" && job.TranscriptGeneration != video.TranscriptGeneration {
			continue
		}
		previous, exists := latest[job.JobType]
		if !exists || prefersProcessingJob(video, job, previous) {
			latest[job.JobType] = job
		}
	}

	response := ProcessingStatusResponse{
		VideoID: video.ID, Status: ProcessingStateReady,
		FoundationStatus: ProcessingStateReady, EnhancementStatus: ProcessingStateReady,
		TranscriptGeneration: video.TranscriptGeneration,
		CompletedStages:      make([]string, 0, len(latest)),
		Jobs:                 make([]ProcessingJobStatus, 0, len(latest)),
		UpdatedAt:            video.UpdatedAt,
	}
	var foundationFailed *model.VideoProcessingJob
	var enhancementFailed *model.VideoProcessingJob
	var foundationActive *model.VideoProcessingJob
	var enhancementActive *model.VideoProcessingJob
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
			} else if isEnhancementJob(job.JobType) && enhancementFailed == nil {
				copy := job
				copy.Status = "failed"
				copy.ErrorCategory, copy.ErrorCode, copy.ErrorMessage = missingStageArtifactError(stage)
				enhancementFailed = &copy
			} else if foundationFailed == nil {
				copy := job
				copy.Status = "failed"
				copy.ErrorCategory, copy.ErrorCode, copy.ErrorMessage = missingStageArtifactError(stage)
				foundationFailed = &copy
			}
		case "failed":
			if isEnhancementJob(job.JobType) {
				if enhancementFailed == nil {
					copy := job
					enhancementFailed = &copy
				}
			} else if foundationFailed == nil {
				copy := job
				foundationFailed = &copy
			}
		case "pending", "running":
			if isEnhancementJob(job.JobType) {
				if enhancementActive == nil {
					copy := job
					enhancementActive = &copy
				}
			} else if foundationActive == nil {
				copy := job
				foundationActive = &copy
			}
		}
	}

	if foundationFailed != nil {
		response.Status = ProcessingStateFailed
		response.FoundationStatus = ProcessingStateFailed
		response.CurrentStage = foundationFailed.JobType
		response.Failure = &ProcessingFailure{
			JobID: foundationFailed.ID, JobType: foundationFailed.JobType, Category: fallbackCategory(foundationFailed.ErrorCategory),
			Code: foundationFailed.ErrorCode, Message: foundationFailed.ErrorMessage, UpdatedAt: foundationFailed.UpdatedAt,
		}
		response.RetryableJob = &RetryableProcessingJob{JobID: foundationFailed.ID, JobType: foundationFailed.JobType}
		return response
	}
	if enhancementFailed != nil {
		response.EnhancementStatus = ProcessingStateFailed
		response.EnhancementFailure = &ProcessingFailure{
			JobID: enhancementFailed.ID, JobType: enhancementFailed.JobType, Category: fallbackCategory(enhancementFailed.ErrorCategory),
			Code: enhancementFailed.ErrorCode, Message: enhancementFailed.ErrorMessage, UpdatedAt: enhancementFailed.UpdatedAt,
		}
		if response.RetryableJob == nil {
			response.RetryableJob = &RetryableProcessingJob{JobID: enhancementFailed.ID, JobType: enhancementFailed.JobType}
		}
	}
	if enhancementFailed == nil && video.KnowledgeAuditStatus == "failed" {
		response.EnhancementStatus = ProcessingStateFailed
	} else if enhancementFailed == nil && video.KnowledgeAuditStatus == "conditional" {
		response.EnhancementStatus = ProcessingStatePartial
	}
	if foundationActive != nil || enhancementActive != nil {
		response.Status = ProcessingStateRunning
		if len(response.CompletedStages) > 0 {
			response.Status = ProcessingStatePartial
		}
		if foundationActive != nil {
			response.CurrentStage = foundationActive.JobType
		} else {
			response.CurrentStage = enhancementActive.JobType
		}
		if foundationActive != nil {
			response.FoundationStatus = ProcessingStateRunning
		} else if response.FoundationStatus == ProcessingStateReady {
			response.FoundationStatus = ProcessingStateCompleted
		}
		if enhancementActive != nil {
			response.EnhancementStatus = ProcessingStateRunning
		}
		return response
	}
	if assemble, ok := latest["assemble"]; ok && assemble.Status == "succeeded" && hasReadableContentReferences(video) {
		response.Status = ProcessingStateCompleted
		response.FoundationStatus = ProcessingStateCompleted
		response.CurrentStage = "assemble"
		if enhancementFailed == nil && enhancementActive == nil && video.KnowledgeAuditStatus != "failed" && video.KnowledgeAuditStatus != "conditional" {
			response.EnhancementStatus = ProcessingStateCompleted
		}
		return response
	}
	if foundationArtifactsReady(video) {
		response.FoundationStatus = ProcessingStateCompleted
	} else if len(response.CompletedStages) > 0 {
		response.FoundationStatus = ProcessingStatePartial
	}
	if enhancementFailed == nil && enhancementActive == nil {
		if video.KnowledgeAuditStatus == "failed" {
			response.EnhancementStatus = ProcessingStateFailed
		} else if video.KnowledgeAuditStatus == "conditional" {
			response.EnhancementStatus = ProcessingStatePartial
		} else if strings.TrimSpace(video.KnowledgeBaseWikiPageID) != "" {
			response.EnhancementStatus = ProcessingStateCompleted
		} else {
			response.EnhancementStatus = ProcessingStatePartial
		}
	}
	if len(response.CompletedStages) > 0 {
		response.Status = ProcessingStatePartial
		response.CurrentStage = nextIncompleteStage(latest)
	}
	return response
}

// prefersProcessingJob selects the effective attempt for a stage. A stage can
// legitimately have a draft and a final attempt at the same time. Activity is
// considered before timestamps so a newer pending final attempt cannot make a
// currently running draft appear to have regressed to pending.
func prefersProcessingJob(video model.Video, candidate, previous model.VideoProcessingJob) bool {
	// A new transcription has no generation until its provider task completes.
	// Keep an active unbound transcription ahead of an older bound attempt.
	candidateNewTranscription := candidate.JobType == "transcription" && candidate.TranscriptGeneration == "" && isActiveProcessingStatus(candidate.Status)
	previousNewTranscription := previous.JobType == "transcription" && previous.TranscriptGeneration == "" && isActiveProcessingStatus(previous.Status)
	if candidateNewTranscription != previousNewTranscription {
		return candidateNewTranscription
	}
	// A successful formal result is authoritative over an in-flight draft. A
	// pending/running formal attempt does not get this shortcut: the draft is
	// still the effective status until that attempt produces a result.
	candidateFinalSuccess := processingResultStageRank(candidate.ResultStage) == 2 && candidate.Status == "succeeded" && stageArtifactAvailable(video, candidate)
	previousFinalSuccess := processingResultStageRank(previous.ResultStage) == 2 && previous.Status == "succeeded" && stageArtifactAvailable(video, previous)
	if candidateFinalSuccess != previousFinalSuccess {
		return candidateFinalSuccess
	}

	candidateCurrent := video.TranscriptGeneration != "" && candidate.TranscriptGeneration == video.TranscriptGeneration
	previousCurrent := video.TranscriptGeneration != "" && previous.TranscriptGeneration == video.TranscriptGeneration
	if candidateCurrent != previousCurrent {
		return candidateCurrent
	}

	candidateActivity := processingActivityRank(candidate.Status)
	previousActivity := processingActivityRank(previous.Status)
	if candidateActivity != previousActivity {
		return candidateActivity > previousActivity
	}
	// When both attempts are terminal, prefer the one whose claimed result is
	// actually readable. A stale successful final row must not hide a usable
	// completed draft merely because its result_stage sorts later.
	candidateReadableSuccess := candidate.Status == "succeeded" && stageArtifactAvailable(video, candidate)
	previousReadableSuccess := previous.Status == "succeeded" && stageArtifactAvailable(video, previous)
	if candidateReadableSuccess != previousReadableSuccess && !isActiveProcessingStatus(candidate.Status) && !isActiveProcessingStatus(previous.Status) {
		return candidateReadableSuccess
	}

	// For equal activity, prefer the formal result over a draft. This keeps a
	// final attempt visible when both attempts are waiting or terminal.
	candidateStage := processingResultStageRank(candidate.ResultStage)
	previousStage := processingResultStageRank(previous.ResultStage)
	if candidateStage != previousStage {
		return candidateStage > previousStage
	}
	if candidate.UpdatedAt.Equal(previous.UpdatedAt) {
		return candidate.ID > previous.ID
	}
	return candidate.UpdatedAt.After(previous.UpdatedAt)
}

func isActiveProcessingStatus(status string) bool {
	return status == "pending" || status == "running"
}

func processingActivityRank(status string) int {
	switch status {
	case "running":
		return 3
	case "pending":
		return 2
	case "succeeded", "failed":
		return 1
	default:
		return 0
	}
}

func processingResultStageRank(stage string) int {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "draft", "draft_ready":
		return 1
	case "", "final", "final_ready":
		// Empty is the legacy representation of a final result. The artifact
		// selector treats every non-draft attempt as final as well.
		return 2
	default:
		return 0
	}
}

func processingJobStatus(job model.VideoProcessingJob) ProcessingJobStatus {
	return ProcessingJobStatus{
		JobID: job.ID, JobType: job.JobType, TranscriptGeneration: job.TranscriptGeneration,
		Provider: job.Provider, ExternalTaskID: job.ExternalTaskID,
		Status: job.Status, Phase: processingJobPhase(job), Progress: job.Progress, AttemptCount: job.AttemptCount, MaxAttempts: job.MaxAttempts,
		InputAvailable: strings.TrimSpace(job.InputPayload) != "", ResultAvailable: strings.TrimSpace(job.ResultPayload) != "",
		ErrorCategory: job.ErrorCategory, ErrorCode: job.ErrorCode, ErrorMessage: job.ErrorMessage,
		UpdatedAt: job.UpdatedAt, StartedAt: job.StartedAt, CompletedAt: job.CompletedAt,
	}
}

func processingJobPhase(job model.VideoProcessingJob) string {
	if job.JobType != "transcription" || (job.Status != "pending" && job.Status != "running") {
		return ""
	}
	if strings.TrimSpace(job.ExternalTaskID) == "" {
		return "source_preparing"
	}
	if job.Provider == "tencent_mps" {
		return "mps_running"
	}
	return "tingwu_running"
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
	case "summary_enhance":
		return strings.TrimSpace(video.SummaryWikiPageID) != ""
	case "outline":
		if job.ResultStage == "draft" {
			return strings.TrimSpace(video.OutlineDraftWikiPageID) != ""
		}
		return strings.TrimSpace(video.OutlineWikiPageID) != ""
	case "summary":
		if job.ResultStage == "draft" {
			return strings.TrimSpace(video.SummaryDraftWikiPageID) != ""
		}
		return strings.TrimSpace(video.SummaryWikiPageID) != ""
	case "assemble":
		return hasReadableContentReferences(video)
	default:
		return true
	}
}

func isEnhancementJob(jobType string) bool {
	return jobType == "graph" || jobType == "summary_enhance"
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
	return strings.TrimSpace(video.OutlineWikiPageID) != "" &&
		strings.TrimSpace(video.SummaryWikiPageID) != "" &&
		strings.TrimSpace(video.TranscriptPageWikiPageID) != ""
}

func foundationArtifactsReady(video model.Video) bool {
	return strings.TrimSpace(video.OutlineWikiPageID) != "" &&
		strings.TrimSpace(video.SummaryWikiPageID) != ""
}

func fallbackCategory(category string) string {
	if category == "" {
		return "unknown"
	}
	return category
}
