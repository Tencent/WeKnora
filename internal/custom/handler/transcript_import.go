package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	miniosdk "github.com/minio/minio-go/v7"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/subtitle"
)

const maxImportedSRTBytes = 16 * 1024 * 1024

type TranscriptImportHandler struct {
	DB    *gorm.DB
	MinIO *objstore.Client
}

func NewTranscriptImportHandler(db *gorm.DB, minioClient *objstore.Client) *TranscriptImportHandler {
	return &TranscriptImportHandler{DB: db, MinIO: minioClient}
}

func (h *TranscriptImportHandler) Import(c *gin.Context) {
	videoID := strings.TrimSpace(c.Param("id"))
	if videoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "video id is required"})
		return
	}
	if h.MinIO == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "object storage client is not configured"})
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multipart field file is required"})
		return
	}
	defer file.Close()
	if header.Size > maxImportedSRTBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "srt file is too large"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxImportedSRTBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read srt file: " + err.Error()})
		return
	}
	if len(data) > maxImportedSRTBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "srt file is too large"})
		return
	}
	paragraphs, err := subtitle.ParseSRT(bytes.NewReader(data))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "parse srt: " + err.Error()})
		return
	}

	var video model.Video
	if err := h.DB.WithContext(c.Request.Context()).First(&video, "id = ?", videoID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load video: " + err.Error()})
		return
	}
	if err := subtitle.ValidateTranscriptQuality(paragraphs, video.DurationSeconds); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "transcript quality gate: " + err.Error()})
		return
	}
	normalizedSRT := subtitle.ParagraphsToSRT(paragraphs)
	contentHash := sha256.Sum256([]byte(normalizedSRT))
	hashText := hex.EncodeToString(contentHash[:])
	// Keep idempotency keys within the existing varchar(128) contract while
	// retaining the full digest in ResultPayload for audit and diagnostics.
	keyHash := hashText[:32]
	importKey := fmt.Sprintf("transcription:%s:srt-import:%s", videoID, keyHash)

	var existing model.VideoProcessingJob
	if err := h.DB.WithContext(c.Request.Context()).Where("idempotency_key = ?", importKey).First(&existing).Error; err == nil {
		c.JSON(http.StatusOK, gin.H{"video_id": videoID, "status": "accepted", "reused": true, "transcription_job_id": existing.ID})
		return
	} else if err != gorm.ErrRecordNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "check imported transcript: " + err.Error()})
		return
	}

	objectKey := fmt.Sprintf("subtitles/%s/transcript.srt", videoID)
	if _, err := h.MinIO.PutObject(c.Request.Context(), objectKey, strings.NewReader(normalizedSRT), int64(len(normalizedSRT)), miniosdk.PutObjectOptions{ContentType: "application/x-subrip"}); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "store normalized srt: " + err.Error()})
		return
	}

	payload, err := json.Marshal(map[string]any{"paragraphs": paragraphs, "language": "zh", "source": "srt_import", "sha256": hashText})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encode imported transcript: " + err.Error()})
		return
	}
	now := time.Now().UTC()
	transcriptionJob := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: videoID, JobType: "transcription", Provider: "local_srt_import", Status: "succeeded", Progress: 100,
		AttemptCount: 1, MaxAttempts: 3, IdempotencyKey: importKey, ResultPayload: string(payload), CompletedAt: &now,
	}
	subtitleJob := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: videoID, JobType: "subtitle_generate", Provider: "local_srt_import", Status: "succeeded", Progress: 100,
		AttemptCount: 1, MaxAttempts: 3, IdempotencyKey: fmt.Sprintf("subtitle_generate:%s:srt-import:%s", videoID, keyHash), ResultPayload: string(payload), CompletedAt: &now,
		InputPayload: fmt.Sprintf(`{"transcription_job_id":%q,"source":"srt_import"}`, transcriptionJob.ID),
	}
	var indexJob model.VideoProcessingJob
	err = h.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var locked model.Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", videoID).Error; err != nil {
			return err
		}
		revision := locked.TranscriptRevision + 1
		indexJob = model.VideoProcessingJob{
			ID: uuid.NewString(), VideoID: videoID, JobType: "index", Provider: "weknora", Status: "pending", MaxAttempts: 3,
			IdempotencyKey: fmt.Sprintf("index:%s:%d:%s", videoID, revision, hashText), ResultPayload: string(payload),
			InputPayload: fmt.Sprintf(`{"revision":%d,"source":"srt_import"}`, revision),
		}
		if err := tx.Create(&transcriptionJob).Error; err != nil {
			return fmt.Errorf("save transcription import job: %w", err)
		}
		if err := tx.Create(&subtitleJob).Error; err != nil {
			return fmt.Errorf("save subtitle import job: %w", err)
		}
		if err := tx.Create(&indexJob).Error; err != nil {
			return fmt.Errorf("save transcript index job: %w", err)
		}
		return tx.Model(&locked).Updates(map[string]any{
			"transcript_revision":      revision,
			"subtitle_file_url":        h.MinIO.PublicURL(objectKey),
			"status":                   model.VideoStatusProcessing,
			"processing_error_summary": "",
		}).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create imported transcript jobs: " + err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"video_id":             videoID,
		"status":               "accepted",
		"reused":               false,
		"source":               "srt_import",
		"transcription_job_id": transcriptionJob.ID,
		"subtitle_job_id":      subtitleJob.ID,
		"index_job_id":         indexJob.ID,
		"subtitle_file_url":    h.MinIO.PublicURL(objectKey),
		"subtitle_count":       len(paragraphs),
		"transcript_revision":  video.TranscriptRevision + 1,
	})
}
