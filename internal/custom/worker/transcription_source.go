package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

const transcriptionSourceObjectName = "transcription-source-h264-aac.mp4"

type mediaSourcePreparer struct {
	MinIO                   *objstore.Client
	InternalFrontendBaseURL string
}

type mediaFormat struct {
	FormatName string        `json:"format_name"`
	Duration   mediaDuration `json:"duration"`
}

type mediaDuration float64

func (d *mediaDuration) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if raw == "" || raw == "null" || raw == "N/A" {
		*d = 0
		return nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fmt.Errorf("parse media duration: %w", err)
	}
	*d = mediaDuration(value)
	return nil
}

type probedMedia struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
	} `json:"streams"`
	Format mediaFormat `json:"format"`
}

func (p *mediaSourcePreparer) Prepare(ctx context.Context, video *model.Video) (string, error) {
	return p.PrepareWithProgress(ctx, video, nil)
}

func (p *mediaSourcePreparer) PrepareWithProgress(ctx context.Context, video *model.Video, report func(SourcePreparationProgress)) (string, error) {
	if video == nil || strings.TrimSpace(video.ID) == "" {
		return "", fmt.Errorf("video is missing")
	}
	if report != nil {
		report(SourcePreparationProgress{Phase: "source_checking", Percent: 0})
	}

	sourceURL, err := p.sourceURL(ctx, video)
	if err != nil {
		return "", err
	}
	probeURL := p.internalSourceURL(sourceURL)
	media, err := probeMedia(ctx, probeURL)
	if err != nil {
		return "", fmt.Errorf("探测媒体格式: %w", err)
	}
	if media.isCompatible() {
		if report != nil {
			report(SourcePreparationProgress{Phase: "source_preparing", Percent: 100})
		}
		if strings.TrimSpace(video.TranscriptionSourceURL) != "" {
			return strings.TrimSpace(video.TranscriptionSourceURL), nil
		}
		return strings.TrimSpace(video.FileURL), nil
	}
	if p.MinIO == nil {
		return "", fmt.Errorf("媒体编码为 %s，无法生成 H.264/AAC 转写源：对象存储未配置", media.description())
	}

	objectKey := fmt.Sprintf("videos/%s/%s", video.ID, transcriptionSourceObjectName)
	if exists, existsErr := p.MinIO.ObjectExists(ctx, objectKey); existsErr != nil {
		return "", fmt.Errorf("检查已有兼容转写源: %w", existsErr)
	} else if exists {
		cachedURL, urlErr := p.MinIO.PresignGet(ctx, objectKey, transcriptionSourceTTL)
		if urlErr != nil {
			return "", fmt.Errorf("读取已有兼容转写源: %w", urlErr)
		}
		cachedMedia, probeErr := probeMedia(ctx, cachedURL)
		if probeErr == nil && cachedMedia.isCompatible() {
			if report != nil {
				report(SourcePreparationProgress{Phase: "source_transcoding", Percent: 100})
			}
			return p.MinIO.PublicURL(objectKey), nil
		}
	}
	if report != nil {
		report(SourcePreparationProgress{Phase: "source_transcoding", Percent: 0})
	}

	outputPath, err := os.CreateTemp("", "vidsage-transcription-*.mp4")
	if err != nil {
		return "", fmt.Errorf("创建转码临时文件: %w", err)
	}
	outputPathName := outputPath.Name()
	if err := outputPath.Close(); err != nil {
		os.Remove(outputPathName)
		return "", fmt.Errorf("关闭转码临时文件: %w", err)
	}
	defer os.Remove(outputPathName)

	if err := transcodeToH264AAC(ctx, probeURL, outputPathName, float64(media.Format.Duration), func(percent int) {
		if report != nil {
			report(SourcePreparationProgress{Phase: "source_transcoding", Percent: percent})
		}
	}); err != nil {
		return "", err
	}
	converted, err := probeMedia(ctx, outputPathName)
	if err != nil {
		return "", fmt.Errorf("验证转码结果: %w", err)
	}
	if !converted.isCompatible() {
		return "", fmt.Errorf("转码结果仍不兼容：%s", converted.description())
	}
	if report != nil {
		report(SourcePreparationProgress{Phase: "source_transcoding", Percent: 100})
	}

	file, err := os.Open(outputPathName)
	if err != nil {
		return "", fmt.Errorf("打开转码结果: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("读取转码结果大小: %w", err)
	}
	if _, err := p.MinIO.PutObject(ctx, objectKey, file, info.Size(), minio.PutObjectOptions{ContentType: "video/mp4"}); err != nil {
		return "", fmt.Errorf("保存兼容转写源: %w", err)
	}
	return p.MinIO.PublicURL(objectKey), nil
}

func (p *mediaSourcePreparer) internalSourceURL(sourceURL string) string {
	return rewriteFrontendFileURL(sourceURL, p.InternalFrontendBaseURL)
}

func rewriteFrontendFileURL(sourceURL, internalFrontendBaseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(internalFrontendBaseURL), "/")
	if base == "" {
		return sourceURL
	}
	u, err := url.Parse(sourceURL)
	if err != nil || u.Host == "" || u.Path == "" {
		return sourceURL
	}
	if !strings.HasPrefix(u.Path, "/api/custom/files/") {
		return sourceURL
	}
	internal := base + u.Path
	if u.RawQuery != "" {
		internal += "?" + u.RawQuery
	}
	return internal
}

func (p *mediaSourcePreparer) sourceURL(ctx context.Context, video *model.Video) (string, error) {
	if sourceURL := strings.TrimSpace(video.TranscriptionSourceURL); sourceURL != "" {
		return sourceURL, nil
	}
	if strings.TrimSpace(video.FileURL) == "" {
		return "", fmt.Errorf("video file url is empty")
	}
	objectKey := videoObjectKey(video.ID, video.UploadObjectKey, video.FileURL)
	if p.MinIO != nil {
		url, err := p.MinIO.PresignGet(ctx, objectKey, transcriptionSourceTTL)
		if err != nil {
			return "", fmt.Errorf("读取原始视频: %w", err)
		}
		return url, nil
	}
	return strings.TrimSpace(video.FileURL), nil
}

func probeMedia(ctx context.Context, source string) (probedMedia, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name", "-show_entries", "format=format_name,duration", "-of", "json", source)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return probedMedia{}, fmt.Errorf("ffprobe: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return probedMedia{}, fmt.Errorf("ffprobe: %w", err)
	}
	var media probedMedia
	if err := json.Unmarshal(output, &media); err != nil {
		return probedMedia{}, fmt.Errorf("解析 ffprobe 输出: %w", err)
	}
	return media, nil
}

func transcodeToH264AAC(ctx context.Context, source, destination string, durationSeconds float64, report func(int)) error {
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostats", "-progress", "pipe:1", "-y",
		"-i", source,
		"-map", "0:v:0", "-map", "0:a:0",
		"-c:v", "libx264", "-preset", "veryfast", "-threads", "1", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart",
		destination,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg 输出初始化失败: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg 启动失败: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if durationSeconds <= 0 || report == nil || !strings.HasPrefix(line, "out_time_") {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok || (key != "out_time_ms" && key != "out_time_us") {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			continue
		}
		percent := int((value / 1_000_000) / durationSeconds * 100)
		if percent < 0 {
			percent = 0
		}
		if percent > 99 {
			percent = 99
		}
		report(percent)
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("读取 ffmpeg 进度失败: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("ffmpeg 转码终止: %w", ctx.Err())
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return fmt.Errorf("ffmpeg 转码失败: %w", err)
		}
		return fmt.Errorf("ffmpeg 转码失败: %w: %s", err, message)
	}
	if report != nil {
		report(100)
	}
	return nil
}

func (m probedMedia) isCompatible() bool {
	videoCodec, audioCodec := "", ""
	for _, stream := range m.Streams {
		switch stream.CodecType {
		case "video":
			if videoCodec == "" {
				videoCodec = strings.ToLower(stream.CodecName)
			}
		case "audio":
			if audioCodec == "" {
				audioCodec = strings.ToLower(stream.CodecName)
			}
		}
	}
	if videoCodec != "h264" || audioCodec != "aac" {
		return false
	}
	for _, format := range strings.Split(strings.ToLower(m.Format.FormatName), ",") {
		if format == "mp4" {
			return true
		}
	}
	return false
}

func (m probedMedia) description() string {
	videoCodec, audioCodec := "missing", "missing"
	for _, stream := range m.Streams {
		if stream.CodecType == "video" && videoCodec == "missing" {
			videoCodec = stream.CodecName
		}
		if stream.CodecType == "audio" && audioCodec == "missing" {
			audioCodec = stream.CodecName
		}
	}
	return fmt.Sprintf("video=%s audio=%s format=%s", videoCodec, audioCodec, m.Format.FormatName)
}

const transcriptionSourceTTL = 6 * time.Hour
