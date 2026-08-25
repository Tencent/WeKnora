// Package worker thumbnail job 处理（VP-T003）。
//
// 设计要点：
//   - 走 FFmpeg 子进程抽帧（不引入 CGO）
//   - 抽帧失败不阻塞后续流程（封面可选）
//   - 顺手用 ffprobe 拿视频时长回写 duration_seconds
//   - 输出 jpg，回写 thumbnail_url 到 videos 表
package worker

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

// ThumbnailHandler thumbnail job
type ThumbnailHandler struct {
	DB                    *gorm.DB
	MinIO                 *objstore.Client
	ContentWorkersEnabled bool
}

type CoreFileUnavailableError struct {
	Reason string
}

func (e *CoreFileUnavailableError) Error() string {
	return "core video file unavailable: " + e.Reason
}

// NewThumbnailHandler 构造
func NewThumbnailHandler(db *gorm.DB, m *objstore.Client, contentWorkersEnabled bool) *ThumbnailHandler {
	return &ThumbnailHandler{DB: db, MinIO: m, ContentWorkersEnabled: contentWorkersEnabled}
}

// JobType 返回 job 类型标识
func (h *ThumbnailHandler) JobType() string { return "thumbnail" }

// Run 抽帧 → 上传 → 回写 URL + 时长
func (h *ThumbnailHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	objectKey := videoObjectKey(video.ID, video.UploadObjectKey, video.FileURL)
	if strings.TrimSpace(video.FileURL) == "" {
		return &CoreFileUnavailableError{Reason: "video file_url empty"}
	}
	if h.MinIO == nil {
		return fmt.Errorf("minio client 未配置")
	}
	if h.MinIO.PublicURL(objectKey) == "" {
		return fmt.Errorf("minio public url 未配置，无法生成浏览器可访问地址")
	}
	exists, err := h.MinIO.ObjectExists(ctx, objectKey)
	if err != nil {
		return fmt.Errorf("check source object: %w", err)
	}
	if !exists {
		return &CoreFileUnavailableError{Reason: "source object does not exist"}
	}

	if err := h.DB.Model(video).
		Where("status IN ?", []string{model.VideoStatusUploaded, model.VideoStatusInitializing, model.VideoStatusFailed}).
		Update("status", model.VideoStatusInitializing).Error; err != nil {
		return fmt.Errorf("mark video initializing: %w", err)
	}

	// 抽帧（抽第 5 秒处，避免黑屏帧；失败时回退到 0 秒）
	videoURL, err := h.MinIO.PresignGet(ctx, objectKey, 15*time.Minute)
	if err != nil {
		return fmt.Errorf("presign source video: %w", err)
	}

	// 时长是增强信息，读取失败时保留 0，让前端显示占位状态。
	durationSeconds := probeDuration(ctx, videoURL)
	if durationSeconds > 0 {
		if err := h.DB.Model(video).Update("duration_seconds", durationSeconds).Error; err != nil {
			return fmt.Errorf("update duration_seconds: %w", err)
		}
	}

	// 抽帧失败不应让已经上传成功的视频从列表消失。
	frame, err := extractFrame(ctx, videoURL, 5)
	if err != nil {
		frame, err = extractFrame(ctx, videoURL, 0)
		if err != nil {
			return fmt.Errorf("extract thumbnail frame: %w", err)
		}
	}

	objectKey = fmt.Sprintf("thumbnails/%s/cover.jpg", video.ID)
	if err := uploadBytes(ctx, h.MinIO, objectKey, frame, "image/jpeg"); err != nil {
		return fmt.Errorf("upload thumbnail: %w", err)
	}
	publicURL := h.MinIO.PublicURL(objectKey)
	if publicURL == "" {
		return fmt.Errorf("thumbnail public url empty")
	}

	now := time.Now().UTC()
	updates := map[string]any{
		"thumbnail_url":            publicURL,
		"status":                   model.VideoStatusReady,
		"ready_at":                 now,
		"processing_error_summary": "",
	}
	if durationSeconds > 0 {
		updates["duration_seconds"] = durationSeconds
	}
	if durationSeconds <= 0 {
		updates["processing_error_summary"] = "无法读取视频时长，已保留播放入口"
	}
	if err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Video{}).Where("id = ?", video.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("mark video ready: %w", err)
		}

		if h.ContentWorkersEnabled {
			transcriptionJob := model.VideoProcessingJob{
				ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Provider: "aliyun_tingwu",
				Status: "pending", MaxAttempts: 3, IdempotencyKey: fmt.Sprintf("transcription:%s", video.ID),
			}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&transcriptionJob).Error; err != nil {
				return fmt.Errorf("enqueue transcription: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func videoObjectKey(videoID, uploadObjectKey, fileURL string) string {
	if strings.TrimSpace(uploadObjectKey) != "" {
		return strings.TrimLeft(strings.TrimSpace(uploadObjectKey), "/")
	}
	// The URL fallback keeps older rows readable after the migration.
	const prefix = "videos/"
	if index := bytes.Index([]byte(fileURL), []byte(prefix)); index >= 0 {
		return fileURL[index:]
	}
	return fmt.Sprintf("videos/%s/source.mp4", videoID)
}

// extractFrame 调 ffmpeg 抽帧，返回 jpg 字节
func extractFrame(ctx context.Context, videoURL string, seconds int) ([]byte, error) {
	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%d", seconds),
		"-i", videoURL,
		"-frames:v", "1",
		"-q:v", "2",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-",
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// probeDuration 调 ffprobe 拿视频时长（秒），失败返回 0
func probeDuration(ctx context.Context, videoURL string) int {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoURL,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		slog.Warn("ffprobe duration", "url", videoURL, "error", err)
		return 0
	}
	raw := strings.TrimSpace(out.String())
	if raw == "" || raw == "N/A" {
		return 0
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int(f)
}

// uploadBytes 把字节上传到 MinIO
func uploadBytes(ctx context.Context, m *objstore.Client, objectKey string, data []byte, contentType string) error {
	reader := bytes.NewReader(data)
	_, err := m.PutObject(ctx, objectKey, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}
