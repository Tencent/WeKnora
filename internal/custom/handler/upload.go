// Package handler 提供上传相关 HTTP handler（presigned + 分片 + 确认）。
//
// 设计要点：
//   - 大文件走分片，断点续传（D2）
//   - 前端 presigned 直传 MinIO，后端不中转视频流（FR-001）
//   - 上传确认后入 videos 表 + 触发 thumbnail job（VP-T003）
package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	miniosdk "github.com/minio/minio-go/v7"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

// UploadHandler 上传相关路由
type UploadHandler struct {
	DB    *gorm.DB
	MinIO *minio.Client
}

const (
	uploadTraceHeader        = "X-Upload-Trace-ID"
	uploadAttemptHeader      = "X-Upload-Attempt"
	defaultMultipartPartSize = int64(5 * 1024 * 1024)
	maxMultipartPartSize     = int64(5 * 1024 * 1024 * 1024)
)

// NewUploadHandler 构造 handler
func NewUploadHandler(db *gorm.DB, m *minio.Client) *UploadHandler {
	return &UploadHandler{DB: db, MinIO: m}
}

// PresignReq presigned 直传请求体
type PresignReq struct {
	Filename    string `json:"filename" binding:"required"`
	ContentType string `json:"content_type"`
}

// PresignResp 签名结果
type PresignResp struct {
	VideoID       string    `json:"video_id"`
	ObjectKey     string    `json:"object_key"`
	UploadURL     string    `json:"upload_url"`
	ExpiresAt     time.Time `json:"expires_at"`
	PublicFileURL string    `json:"public_file_url"`
}

// Presign 一次性 PUT presigned（VP-T001，小文件 / 演示）
func (h *UploadHandler) Presign(c *gin.Context) {
	var req PresignReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	videoID := uuid.NewString()
	ext := strings.ToLower(filepath.Ext(req.Filename))
	if ext == "" {
		ext = ".mp4"
	}
	objectKey := fmt.Sprintf("videos/%s/source%s", videoID, ext)

	res, err := h.MinIO.PresignPut(c.Request.Context(), objectKey, 15*time.Minute)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 同步落 videos 记录（status=pending_upload），前端 confirm 后切到 uploaded
	video := model.Video{
		ID:              videoID,
		Title:           strings.TrimSuffix(req.Filename, filepath.Ext(req.Filename)),
		FileURL:         h.MinIO.PublicURL(objectKey),
		Status:          model.VideoStatusUploading,
		UploadObjectKey: objectKey,
	}
	if err := h.DB.Create(&video).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create video record: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, PresignResp{
		VideoID:       videoID,
		ObjectKey:     objectKey,
		UploadURL:     res.URL,
		ExpiresAt:     res.ExpiresAt,
		PublicFileURL: video.FileURL,
	})
}

// Direct 服务端中转上传（本地/联调测试用）：浏览器同源传文件，后端中转写 MinIO。
// 绕开 presigned 直传的浏览器 CORS 限制；正式链路仍走 presigned 直传。
func (h *UploadHandler) Direct(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "parse file: " + err.Error()})
		return
	}
	defer file.Close()

	videoType := c.PostForm("video_type")
	if videoType == "" {
		videoType = "tutorial"
	}

	videoID := uuid.NewString()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".mp4"
	}
	objectKey := fmt.Sprintf("videos/%s/source%s", videoID, ext)

	// 服务端写 MinIO
	if _, err := h.MinIO.PutObject(c.Request.Context(), objectKey, file, header.Size, miniosdk.PutObjectOptions{
		ContentType: header.Header.Get("Content-Type"),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "upload to minio: " + err.Error()})
		return
	}

	now := time.Now().UTC()
	video := model.Video{
		ID:              videoID,
		Title:           strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)),
		FileURL:         h.MinIO.PublicURL(objectKey),
		Status:          model.VideoStatusUploaded,
		UploadObjectKey: objectKey,
		VideoType:       videoType,
		UploadedAt:      &now,
	}
	jobID, err := createUploadedVideoWithJob(h.DB, &video)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "persist uploaded video: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"video_id":    videoID,
		"status":      "uploaded",
		"object_key":  objectKey,
		"file_url":    video.FileURL, // 返回真实可访问 URL，前端上传后即可播放（C 修复）
		"job_id":      jobID,
		"uploaded_at": now,
	})
}

// ConfirmReq 上传确认请求体
type ConfirmReq struct {
	VideoID         string `json:"video_id" binding:"required"`
	ObjectKey       string `json:"object_key" binding:"required"`
	DurationSeconds int    `json:"duration_seconds"`
	VideoType       string `json:"video_type"`
}

// Confirm 上传确认（VP-T001）：写 uploaded_at + 入库 + 入 thumbnail job
func (h *UploadHandler) Confirm(c *gin.Context) {
	var req ConfirmReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !strings.HasPrefix(req.ObjectKey, "videos/"+req.VideoID+"/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "object key does not belong to video"})
		return
	}
	now := time.Now().UTC()
	jobID, err := finalizeUploadedVideo(h.DB, req.VideoID, req.ObjectKey, map[string]any{
		"status":                   model.VideoStatusUploaded,
		"file_url":                 h.MinIO.PublicURL(req.ObjectKey),
		"duration_seconds":         req.DurationSeconds,
		"video_type":               req.VideoType,
		"uploaded_at":              now,
		"processing_error_summary": "",
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
			return
		}
		markUploadFailed(h.DB, req.VideoID, "persist confirmed upload failed: "+err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "persist confirmed upload: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"video_id":    req.VideoID,
		"status":      "uploaded",
		"job_id":      jobID,
		"uploaded_at": now,
	})
}

// MultipartInitReq 初始化分片
type MultipartInitReq struct {
	Filename      string `json:"filename" binding:"required"`
	ContentType   string `json:"content_type"`
	VideoType     string `json:"video_type"`
	FileSizeBytes int64  `json:"file_size_bytes"`
	PartSizeBytes int64  `json:"part_size_bytes"`
}

// MultipartInitResp 初始化响应
type MultipartInitResp struct {
	VideoID   string `json:"video_id"`
	ObjectKey string `json:"object_key"`
	UploadID  string `json:"upload_id"`
}

// MultipartInit 初始化分片上传（VP-T002）
func (h *UploadHandler) MultipartInit(c *gin.Context) {
	uploadLog(c, "init_start")
	var req MultipartInitReq
	if err := c.ShouldBindJSON(&req); err != nil {
		uploadError(c, http.StatusBadRequest, "init", err)
		return
	}
	if err := validateMultipartInit(req); err != nil {
		uploadError(c, http.StatusBadRequest, "init_validate", err)
		return
	}
	uploadLog(c, "init_validated", "filename", truncateUploadLogValue(req.Filename), "content_type", req.ContentType, "video_type", req.VideoType)
	videoID := uuid.NewString()
	ext := strings.ToLower(filepath.Ext(req.Filename))
	if ext == "" {
		ext = ".mp4"
	}
	objectKey := fmt.Sprintf("videos/%s/source%s", videoID, ext)

	handle, err := h.MinIO.InitiateMultipartUpload(c.Request.Context(), objectKey, req.ContentType)
	if err != nil {
		uploadError(c, http.StatusInternalServerError, "minio_init", err)
		return
	}
	uploadLog(c, "minio_init_succeeded", "video_id", videoID, "upload_id", handle.UploadID, "object_key", objectKey)

	video := model.Video{
		ID:                  videoID,
		Title:               strings.TrimSuffix(req.Filename, filepath.Ext(req.Filename)),
		FileURL:             h.MinIO.PublicURL(objectKey),
		Status:              model.VideoStatusUploading,
		VideoType:           req.VideoType,
		UploadID:            handle.UploadID,
		UploadObjectKey:     objectKey,
		UploadSizeBytes:     req.FileSizeBytes,
		UploadPartSizeBytes: req.PartSizeBytes,
	}
	if err := h.DB.Create(&video).Error; err != nil {
		_ = h.MinIO.AbortMultipartUpload(c.Request.Context(), objectKey, handle.UploadID)
		uploadError(c, http.StatusInternalServerError, "db_init", fmt.Errorf("create video: %w", err))
		return
	}

	c.Header(uploadTraceHeader, uploadTraceID(c))
	c.Header("X-Upload-ID", handle.UploadID)
	c.JSON(http.StatusOK, MultipartInitResp{
		VideoID:   videoID,
		ObjectKey: objectKey,
		UploadID:  handle.UploadID,
	})
}

// MultipartSignReq 单分片签名请求
type MultipartSignReq struct {
	VideoID    string `json:"video_id" binding:"required"`
	ObjectKey  string `json:"object_key" binding:"required"`
	UploadID   string `json:"upload_id" binding:"required"`
	PartNumber int    `json:"part_number" binding:"required"`
}

// MultipartSignResp 单分片签名响应
type MultipartSignResp struct {
	PartURL   string    `json:"part_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// MultipartPart 同源服务端分片上传。
func (h *UploadHandler) MultipartPart(c *gin.Context) {
	videoID := strings.TrimSpace(c.GetHeader("X-Video-ID"))
	objectKey := strings.TrimSpace(c.GetHeader("X-Object-Key"))
	uploadID := strings.TrimSpace(c.GetHeader("X-Upload-ID"))
	partLogFields := []any{
		"video_id", videoID,
		"upload_id", uploadID,
		"part_number", c.GetHeader("X-Part-Number"),
		"attempt", c.GetHeader(uploadAttemptHeader),
	}
	uploadLog(c, "part_start", partLogFields...)
	partNumber, err := parsePositivePartNumber(c.GetHeader("X-Part-Number"))
	if err != nil {
		uploadError(c, http.StatusBadRequest, "part_validate", err, partLogFields...)
		return
	}
	if videoID == "" || objectKey == "" || uploadID == "" {
		uploadError(c, http.StatusBadRequest, "part_validate", errors.New("missing multipart upload headers"), partLogFields...)
		return
	}

	var video model.Video
	if err := h.DB.Select("id", "status", "upload_id", "upload_size_bytes", "upload_part_size_bytes").
		Where("id = ?", videoID).First(&video).Error; err != nil {
		uploadError(c, http.StatusNotFound, "part_db_lookup", errors.New("video not found"), partLogFields...)
		return
	}
	if video.Status != model.VideoStatusUploading {
		uploadError(c, http.StatusConflict, "part_validate", fmt.Errorf("video upload is not active: status=%s", video.Status), partLogFields...)
		return
	}
	if video.UploadID != "" && video.UploadID != uploadID {
		uploadError(c, http.StatusBadRequest, "part_validate", errors.New("upload id does not belong to video"), partLogFields...)
		return
	}
	if !strings.HasPrefix(objectKey, "videos/"+videoID+"/") {
		uploadError(c, http.StatusBadRequest, "part_validate", errors.New("object key does not belong to video"), partLogFields...)
		return
	}
	expectedSize, err := expectedMultipartPartSize(video.UploadSizeBytes, video.UploadPartSizeBytes, partNumber)
	if err != nil {
		uploadError(c, http.StatusBadRequest, "part_validate", err, partLogFields...)
		return
	}
	if c.Request.ContentLength < 0 {
		uploadError(c, http.StatusLengthRequired, "part_validate", errors.New("Content-Length is required for multipart part"), append(partLogFields, "expected_content_length", expectedSize)...)
		return
	}
	if c.Request.ContentLength != expectedSize {
		uploadError(c, http.StatusBadRequest, "part_validate",
			fmt.Errorf("invalid Content-Length: got %d, want %d", c.Request.ContentLength, expectedSize),
			append(partLogFields, "content_length", c.Request.ContentLength, "expected_content_length", expectedSize)...)
		return
	}

	uploadLog(c, "minio_part_start", append(partLogFields, "content_length", c.Request.ContentLength)...)
	partStartedAt := time.Now()
	etag, err := h.MinIO.UploadMultipartPart(
		c.Request.Context(),
		objectKey,
		uploadID,
		partNumber,
		c.Request.Body,
		c.Request.ContentLength,
	)
	if err != nil {
		code := "minio_write_failed"
		status := http.StatusInternalServerError
		if errors.Is(err, minio.ErrMultipartRequestRead) || c.Request.Context().Err() != nil {
			code = "connection_interrupted"
			status = 499
		}
		uploadErrorWithCode(c, status, "part", code, err, append(partLogFields, "elapsed_ms", time.Since(partStartedAt).Milliseconds())...)
		return
	}
	uploadLog(c, "minio_part_succeeded", append(partLogFields, "etag", etag, "elapsed_ms", time.Since(partStartedAt).Milliseconds())...)
	if err := h.DB.Model(&model.Video{}).
		Where("id = ? AND status = ?", videoID, model.VideoStatusUploading).
		Update("updated_at", time.Now().UTC()).Error; err != nil {
		uploadError(c, http.StatusInternalServerError, "part_db_touch", err, partLogFields...)
		return
	}
	c.Header("ETag", etag)
	c.JSON(http.StatusOK, gin.H{"part_number": partNumber, "etag": etag, "trace_id": uploadTraceID(c)})
}

// MultipartSign 单分片签名（VP-T002）
func (h *UploadHandler) MultipartSign(c *gin.Context) {
	var req MultipartSignReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	urlStr, err := h.MinIO.PresignPart(c.Request.Context(), req.ObjectKey, req.UploadID, req.PartNumber, 1*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, MultipartSignResp{
		PartURL:   urlStr,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
}

func parsePositivePartNumber(raw string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid part number")
	}
	return n, nil
}

func validateMultipartInit(req MultipartInitReq) error {
	if strings.TrimSpace(req.Filename) == "" {
		return errors.New("filename is required")
	}
	if req.FileSizeBytes <= 0 {
		return errors.New("file_size_bytes must be positive")
	}
	if req.PartSizeBytes == 0 {
		return errors.New("part_size_bytes is required")
	}
	if req.PartSizeBytes < defaultMultipartPartSize || req.PartSizeBytes > maxMultipartPartSize {
		return fmt.Errorf("part_size_bytes must be between %d and %d", defaultMultipartPartSize, maxMultipartPartSize)
	}
	return nil
}

func expectedMultipartPartSize(fileSize, partSize int64, partNumber int) (int64, error) {
	if fileSize <= 0 || partSize < defaultMultipartPartSize {
		return 0, errors.New("multipart upload metadata is missing or invalid")
	}
	totalParts := (fileSize + partSize - 1) / partSize
	if int64(partNumber) > totalParts {
		return 0, fmt.Errorf("part number %d exceeds total parts %d", partNumber, totalParts)
	}
	start := int64(partNumber-1) * partSize
	remaining := fileSize - start
	if remaining < partSize {
		return remaining, nil
	}
	return partSize, nil
}

// MultipartCompleteReq 合并分片请求
type MultipartCompleteReq struct {
	VideoID   string               `json:"video_id" binding:"required"`
	ObjectKey string               `json:"object_key" binding:"required"`
	UploadID  string               `json:"upload_id" binding:"required"`
	Parts     []minio.CompletePart `json:"parts" binding:"required"`
}

// MultipartComplete 合并分片（VP-T002）+ 触发 thumbnail job
func (h *UploadHandler) MultipartComplete(c *gin.Context) {
	uploadLog(c, "complete_start")
	var req MultipartCompleteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		if req.VideoID != "" {
			markUploadFailed(h.DB, req.VideoID, "invalid multipart complete request: "+err.Error())
		}
		uploadError(c, http.StatusBadRequest, "complete_validate", err)
		return
	}
	var video model.Video
	if err := h.DB.Select("id", "status", "upload_id", "upload_object_key", "file_url", "upload_size_bytes", "upload_part_size_bytes").
		Where("id = ?", req.VideoID).First(&video).Error; err != nil {
		uploadError(c, http.StatusNotFound, "complete_db_lookup", errors.New("video not found"))
		return
	}
	if video.Status != model.VideoStatusUploading {
		if video.UploadID != "" && req.UploadID != video.UploadID {
			uploadError(c, http.StatusBadRequest, "complete_validate", errors.New("upload id does not belong to video"))
			return
		}
		if !isCompletedVideoStatus(video.Status) || !sameUploadObject(h.MinIO, video, req.ObjectKey) {
			uploadError(c, http.StatusConflict, "complete_validate", fmt.Errorf("video upload is not active: status=%s", video.Status))
			return
		}
		jobID, err := ensureInitialProcessingJob(h.DB, req.VideoID, false)
		if err != nil {
			uploadError(c, http.StatusInternalServerError, "db_enqueue", err, "video_id", req.VideoID)
			return
		}
		c.JSON(http.StatusOK, gin.H{"video_id": req.VideoID, "object_key": req.ObjectKey, "status": video.Status, "job_id": jobID, "uploaded_at": video.UploadedAt, "trace_id": uploadTraceID(c)})
		return
	}
	if video.UploadID != "" && video.UploadID != req.UploadID {
		markUploadFailed(h.DB, req.VideoID, "upload id does not belong to video")
		uploadError(c, http.StatusBadRequest, "complete_validate", errors.New("upload id does not belong to video"))
		return
	}
	if !strings.HasPrefix(req.ObjectKey, "videos/"+req.VideoID+"/") {
		markUploadFailed(h.DB, req.VideoID, "object key does not belong to video")
		uploadError(c, http.StatusBadRequest, "complete_validate", errors.New("object key does not belong to video"))
		return
	}
	if video.UploadSizeBytes <= 0 || video.UploadPartSizeBytes <= 0 {
		markUploadFailed(h.DB, req.VideoID, "multipart upload metadata is missing or invalid")
		uploadError(c, http.StatusBadRequest, "complete_validate", errors.New("multipart upload metadata is missing or invalid"))
		return
	}
	totalParts := (video.UploadSizeBytes + video.UploadPartSizeBytes - 1) / video.UploadPartSizeBytes
	if totalParts <= 0 || int64(len(req.Parts)) != totalParts {
		markUploadFailed(h.DB, req.VideoID, fmt.Sprintf("invalid multipart parts count: got %d, want %d", len(req.Parts), totalParts))
		uploadError(c, http.StatusBadRequest, "complete_validate",
			fmt.Errorf("invalid multipart parts count: got %d, want %d", len(req.Parts), totalParts))
		return
	}
	completeFields := []any{
		"video_id", req.VideoID,
		"upload_id", req.UploadID,
		"object_key", req.ObjectKey,
		"parts", len(req.Parts),
	}
	uploadLog(c, "complete_validated", completeFields...)
	completeStartedAt := time.Now()
	merged, err := h.MinIO.ObjectExists(c.Request.Context(), req.ObjectKey)
	if err == nil && !merged {
		err = h.MinIO.CompleteMultipartUpload(c.Request.Context(), req.ObjectKey, req.UploadID, req.Parts)
		if err == nil {
			merged = true
		}
	}
	if err != nil {
		if errors.Is(err, minio.ErrInvalidMultipartParts) {
			markUploadFailed(h.DB, req.VideoID, "multipart complete validation failed: "+err.Error())
			uploadError(c, http.StatusBadRequest, "minio_complete_validate", err, append(completeFields, "elapsed_ms", time.Since(completeStartedAt).Milliseconds())...)
			return
		}
		markUploadFailed(h.DB, req.VideoID, "multipart complete failed: "+err.Error())
		uploadError(c, http.StatusInternalServerError, "minio_complete", err, append(completeFields, "elapsed_ms", time.Since(completeStartedAt).Milliseconds())...)
		return
	}
	uploadLog(c, "minio_complete_succeeded", append(completeFields, "already_merged", merged, "elapsed_ms", time.Since(completeStartedAt).Milliseconds())...)

	now := time.Now().UTC()
	jobID, err := finalizeUploadedVideo(h.DB, req.VideoID, req.ObjectKey, map[string]any{
		"status":                   model.VideoStatusUploaded,
		"file_url":                 h.MinIO.PublicURL(req.ObjectKey),
		"uploaded_at":              now,
		"processing_error_summary": "",
	})
	if err != nil {
		markUploadFailed(h.DB, req.VideoID, "persist completed upload failed: "+err.Error())
		uploadError(c, http.StatusInternalServerError, "db_enqueue", err, completeFields...)
		return
	}
	uploadLog(c, "complete_succeeded", append(completeFields, "job_id", jobID)...)

	c.JSON(http.StatusOK, gin.H{
		"video_id":    req.VideoID,
		"object_key":  req.ObjectKey,
		"status":      "uploaded",
		"job_id":      jobID,
		"uploaded_at": now,
		"trace_id":    uploadTraceID(c),
	})
}

// RetryInitialProcessing re-enqueues the one initial thumbnail job for a video.
func (h *UploadHandler) RetryInitialProcessing(c *gin.Context) {
	videoID := strings.TrimSpace(c.Param("id"))
	if videoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "video id is required"})
		return
	}

	var video model.Video
	if err := h.DB.Where("id = ?", videoID).First(&video).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if video.UploadObjectKey == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "video has no upload object key"})
		return
	}
	merged, err := h.MinIO.ObjectExists(c.Request.Context(), video.UploadObjectKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "check uploaded object: " + err.Error()})
		return
	}
	if !merged {
		c.JSON(http.StatusConflict, gin.H{"error": "uploaded object does not exist"})
		return
	}

	jobID := ""
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		var current model.Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", videoID).First(&current).Error; err != nil {
			return err
		}
		if current.UploadObjectKey == "" {
			return errors.New("video has no upload object key")
		}
		if err := tx.Model(&model.Video{}).Where("id = ?", videoID).Updates(map[string]any{
			"status":                   model.VideoStatusUploaded,
			"file_url":                 h.MinIO.PublicURL(current.UploadObjectKey),
			"processing_error_summary": "",
		}).Error; err != nil {
			return err
		}
		var ensureErr error
		jobID, ensureErr = ensureInitialProcessingJob(tx, videoID, true)
		return ensureErr
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "retry initial processing: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"video_id": videoID, "status": model.VideoStatusUploaded, "job_id": jobID})
}

// MultipartAbortReq 取消分片
type MultipartAbortReq struct {
	VideoID   string `json:"video_id" binding:"required"`
	ObjectKey string `json:"object_key" binding:"required"`
	UploadID  string `json:"upload_id" binding:"required"`
	Reason    string `json:"reason"`
}

// MultipartAbort 取消分片（VP-T002）
func (h *UploadHandler) MultipartAbort(c *gin.Context) {
	uploadLog(c, "abort_start")
	var req MultipartAbortReq
	if err := c.ShouldBindJSON(&req); err != nil {
		uploadError(c, http.StatusBadRequest, "abort_validate", err)
		return
	}
	abortFields := []any{
		"video_id", req.VideoID,
		"upload_id", req.UploadID,
		"object_key", req.ObjectKey,
		"reason", truncateUploadLogValue(req.Reason),
	}
	uploadLog(c, "abort_validated", abortFields...)
	abortStartedAt := time.Now()
	minioErr := h.MinIO.AbortMultipartUpload(c.Request.Context(), req.ObjectKey, req.UploadID)
	if minioErr != nil {
		uploadLog(c, "minio_abort_failed", append(abortFields, "elapsed_ms", time.Since(abortStartedAt).Milliseconds(), "error", minioErr.Error())...)
	} else {
		uploadLog(c, "minio_abort_succeeded", append(abortFields, "elapsed_ms", time.Since(abortStartedAt).Milliseconds())...)
	}

	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "multipart upload aborted"
	}
	dbResult := h.DB.Model(&model.Video{}).
		Where("id = ? AND status = ?", req.VideoID, model.VideoStatusUploading).
		Updates(map[string]any{
			"status":                   model.VideoStatusFailed,
			"processing_error_summary": reason,
		})
	if dbResult.Error != nil {
		uploadError(c, http.StatusInternalServerError, "db_abort", dbResult.Error, append(abortFields, "elapsed_ms", time.Since(abortStartedAt).Milliseconds())...)
		return
	}
	uploadLog(c, "db_abort_marked", append(abortFields, "rows_affected", dbResult.RowsAffected)...)
	if minioErr != nil {
		uploadErrorWithCode(c, http.StatusInternalServerError, "abort", "minio_abort_failed", minioErr, abortFields...)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"video_id": req.VideoID,
		"status":   "aborted",
		"trace_id": uploadTraceID(c),
	})
}

func uploadTraceID(c *gin.Context) string {
	traceID := strings.TrimSpace(c.GetHeader(uploadTraceHeader))
	if traceID == "" {
		traceID = uuid.NewString()
		c.Request.Header.Set(uploadTraceHeader, traceID)
	}
	return traceID
}

func uploadLog(c *gin.Context, event string, fields ...any) {
	base := []any{
		"component", "custom-upload",
		"event", event,
		"trace_id", uploadTraceID(c),
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
	}
	if videoID := strings.TrimSpace(c.GetHeader("X-Video-ID")); videoID != "" && !hasUploadLogField(fields, "video_id") {
		base = append(base, "video_id", videoID)
	}
	if uploadID := strings.TrimSpace(c.GetHeader("X-Upload-ID")); uploadID != "" && !hasUploadLogField(fields, "upload_id") {
		base = append(base, "upload_id", uploadID)
	}
	slog.InfoContext(c.Request.Context(), "custom upload event", append(base, fields...)...)
}

func hasUploadLogField(fields []any, name string) bool {
	for i := 0; i+1 < len(fields); i += 2 {
		if key, ok := fields[i].(string); ok && key == name {
			return true
		}
	}
	return false
}

func uploadError(c *gin.Context, status int, stage string, err error, fields ...any) {
	uploadErrorWithCode(c, status, stage, uploadErrorCode(stage), err, fields...)
}

func uploadErrorCode(stage string) string {
	switch stage {
	case "init", "part_validate", "complete_validate", "init_validate", "abort_validate":
		return "request_validation_failed"
	case "part_db_lookup", "complete_db_lookup", "db_init", "db_complete", "db_enqueue", "db_abort":
		return "database_failed"
	case "minio_init":
		return "minio_init_failed"
	case "minio_complete", "minio_complete_validate":
		return "minio_complete_failed"
	default:
		return "upload_failed"
	}
}

func uploadErrorWithCode(c *gin.Context, status int, stage, code string, err error, fields ...any) {
	base := []any{
		"stage", stage,
		"code", code,
		"http_status", status,
		"error", err.Error(),
	}
	uploadLog(c, "request_failed", append(base, fields...)...)
	c.Header(uploadTraceHeader, uploadTraceID(c))
	response := gin.H{
		"error":    err.Error(),
		"code":     code,
		"stage":    stage,
		"trace_id": uploadTraceID(c),
	}
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			continue
		}
		switch key {
		case "part_number":
			if raw, ok := fields[i+1].(string); ok {
				if partNumber, parseErr := strconv.Atoi(raw); parseErr == nil {
					response[key] = partNumber
				}
			} else {
				response[key] = fields[i+1]
			}
		case "content_length", "expected_content_length", "elapsed_ms":
			response[key] = fields[i+1]
		}
	}
	c.JSON(status, response)
}

func truncateUploadLogValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 500 {
		return value
	}
	return value[:500] + "..."
}

func enqueueInitialProcessingJob(db *gorm.DB, videoID string) (string, error) {
	return ensureInitialProcessingJob(db, videoID, false)
}

func createUploadedVideoWithJob(db *gorm.DB, video *model.Video) (string, error) {
	jobID := ""
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(video).Error; err != nil {
			return err
		}
		var err error
		jobID, err = ensureInitialProcessingJob(tx, video.ID, false)
		return err
	})
	return jobID, err
}

func ensureInitialProcessingJob(db *gorm.DB, videoID string, resetFailed bool) (string, error) {
	job := model.VideoProcessingJob{
		ID:             uuid.NewString(),
		VideoID:        videoID,
		JobType:        "thumbnail",
		Provider:       "local",
		Status:         "pending",
		MaxAttempts:    3,
		IdempotencyKey: fmt.Sprintf("thumbnail:%s", videoID),
	}
	result := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&job)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected > 0 {
		return job.ID, nil
	}
	var existing model.VideoProcessingJob
	if err := db.Where("idempotency_key = ?", job.IdempotencyKey).First(&existing).Error; err != nil {
		return "", err
	}
	if resetFailed && (existing.Status == "failed" || existing.Status == "cancelled") {
		if err := db.Model(&existing).Updates(map[string]any{
			"status":        "pending",
			"progress":      0,
			"attempt_count": 0,
			"error_code":    "",
			"error_message": "",
			"completed_at":  nil,
			"started_at":    nil,
		}).Error; err != nil {
			return "", err
		}
	}
	return existing.ID, nil
}

func finalizeUploadedVideo(db *gorm.DB, videoID, objectKey string, updates map[string]any) (string, error) {
	returnedJobID := ""
	err := db.Transaction(func(tx *gorm.DB) error {
		var video model.Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", videoID).First(&video).Error; err != nil {
			return err
		}
		if video.UploadObjectKey != "" && video.UploadObjectKey != objectKey {
			return fmt.Errorf("upload object key does not belong to video")
		}
		updates["upload_object_key"] = objectKey
		if err := tx.Model(&model.Video{}).Where("id = ?", videoID).Updates(updates).Error; err != nil {
			return err
		}
		jobID, err := ensureInitialProcessingJob(tx, videoID, false)
		if err != nil {
			return err
		}
		returnedJobID = jobID
		return nil
	})
	return returnedJobID, err
}

func markUploadFailed(db *gorm.DB, videoID, reason string) {
	if err := db.Model(&model.Video{}).Where("id = ? AND status = ?", videoID, model.VideoStatusUploading).Updates(map[string]any{
		"status":                   model.VideoStatusFailed,
		"processing_error_summary": truncateUploadLogValue(reason),
	}).Error; err != nil {
		slog.Error("mark upload failed", "video_id", videoID, "error", err)
	}
}

func isCompletedVideoStatus(status string) bool {
	switch status {
	case model.VideoStatusUploaded, model.VideoStatusInitializing, model.VideoStatusReady, model.VideoStatusProcessing, model.VideoStatusCompleted:
		return true
	default:
		return false
	}
}

func sameUploadObject(client *minio.Client, video model.Video, objectKey string) bool {
	return video.UploadObjectKey == objectKey || video.FileURL == client.PublicURL(objectKey)
}
