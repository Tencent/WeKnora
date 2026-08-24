// Package handler 提供上传相关 HTTP handler（presigned + 分片 + 确认）。
//
// 设计要点：
//   - 大文件走分片，断点续传（D2）
//   - 前端 presigned 直传 MinIO，后端不中转视频流（FR-001）
//   - 上传确认后入 videos 表 + 触发 thumbnail job（VP-T003）
package handler

import (
	"fmt"
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
		ID:      videoID,
		Title:   strings.TrimSuffix(req.Filename, filepath.Ext(req.Filename)),
		FileURL: h.MinIO.PublicURL(objectKey),
		Status:  model.VideoStatusUploading,
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
		ID:         videoID,
		Title:      strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)),
		FileURL:    h.MinIO.PublicURL(objectKey),
		Status:     model.VideoStatusUploaded,
		VideoType:  videoType,
		UploadedAt: &now,
	}
	if err := h.DB.Create(&video).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create video record: " + err.Error()})
		return
	}

	jobID, err := enqueueInitialProcessingJob(h.DB, videoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "enqueue thumbnail job: " + err.Error()})
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
	now := time.Now().UTC()
	res := h.DB.Model(&model.Video{}).
		Where("id = ?", req.VideoID).
		Updates(map[string]any{
			"status":                   model.VideoStatusUploaded,
			"file_url":                 h.MinIO.PublicURL(req.ObjectKey),
			"duration_seconds":         req.DurationSeconds,
			"video_type":               req.VideoType,
			"uploaded_at":              now,
			"processing_error_summary": "",
		})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
		return
	}

	jobID, err := enqueueInitialProcessingJob(h.DB, req.VideoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "enqueue thumbnail job: " + err.Error()})
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
	Filename    string `json:"filename" binding:"required"`
	ContentType string `json:"content_type"`
	VideoType   string `json:"video_type"`
}

// MultipartInitResp 初始化响应
type MultipartInitResp struct {
	VideoID   string `json:"video_id"`
	ObjectKey string `json:"object_key"`
	UploadID  string `json:"upload_id"`
}

// MultipartInit 初始化分片上传（VP-T002）
func (h *UploadHandler) MultipartInit(c *gin.Context) {
	var req MultipartInitReq
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

	handle, err := h.MinIO.InitiateMultipartUpload(c.Request.Context(), objectKey, req.ContentType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	video := model.Video{
		ID:        videoID,
		Title:     strings.TrimSuffix(req.Filename, filepath.Ext(req.Filename)),
		FileURL:   h.MinIO.PublicURL(objectKey),
		Status:    model.VideoStatusUploading,
		VideoType: req.VideoType,
	}
	if err := h.DB.Create(&video).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create video: " + err.Error()})
		return
	}

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
	partNumber, err := parsePositivePartNumber(c.GetHeader("X-Part-Number"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if videoID == "" || objectKey == "" || uploadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing multipart upload headers"})
		return
	}

	var video model.Video
	if err := h.DB.Select("id").Where("id = ?", videoID).First(&video).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
		return
	}
	if !strings.HasPrefix(objectKey, "videos/"+videoID+"/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "object key does not belong to video"})
		return
	}

	etag, err := h.MinIO.UploadMultipartPart(
		c.Request.Context(),
		objectKey,
		uploadID,
		partNumber,
		c.Request.Body,
		c.Request.ContentLength,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("ETag", etag)
	c.JSON(http.StatusOK, gin.H{"part_number": partNumber, "etag": etag})
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

// MultipartCompleteReq 合并分片请求
type MultipartCompleteReq struct {
	VideoID   string               `json:"video_id" binding:"required"`
	ObjectKey string               `json:"object_key" binding:"required"`
	UploadID  string               `json:"upload_id" binding:"required"`
	Parts     []minio.CompletePart `json:"parts" binding:"required"`
}

// MultipartComplete 合并分片（VP-T002）+ 触发 thumbnail job
func (h *UploadHandler) MultipartComplete(c *gin.Context) {
	var req MultipartCompleteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.MinIO.CompleteMultipartUpload(c.Request.Context(), req.ObjectKey, req.UploadID, req.Parts); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	now := time.Now().UTC()
	res := h.DB.Model(&model.Video{}).
		Where("id = ?", req.VideoID).
		Updates(map[string]any{
			"status":      model.VideoStatusUploaded,
			"file_url":    h.MinIO.PublicURL(req.ObjectKey),
			"uploaded_at": now,
		})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": res.Error.Error()})
		return
	}

	jobID, err := enqueueInitialProcessingJob(h.DB, req.VideoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"video_id":    req.VideoID,
		"object_key":  req.ObjectKey,
		"status":      "uploaded",
		"job_id":      jobID,
		"uploaded_at": now,
	})
}

// MultipartAbortReq 取消分片
type MultipartAbortReq struct {
	VideoID   string `json:"video_id" binding:"required"`
	ObjectKey string `json:"object_key" binding:"required"`
	UploadID  string `json:"upload_id" binding:"required"`
}

// MultipartAbort 取消分片（VP-T002）
func (h *UploadHandler) MultipartAbort(c *gin.Context) {
	var req MultipartAbortReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.MinIO.AbortMultipartUpload(c.Request.Context(), req.ObjectKey, req.UploadID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"video_id": req.VideoID, "status": "aborted"})
}

func enqueueInitialProcessingJob(db *gorm.DB, videoID string) (string, error) {
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
	return existing.ID, nil
}
