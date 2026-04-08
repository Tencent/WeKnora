package video

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

// FFMpegExtractor implements the Extractor interface using FFmpeg.
type FFMpegExtractor struct {
	ffmpegPath string
	tempDir    string
}

// NewFFMpegExtractor creates a new FFmpeg-based video extractor.
func NewFFMpegExtractor(config *Config) (Extractor, error) {
	ffmpegPath := "ffmpeg"
	tempDir := os.TempDir()

	if config != nil {
		if config.FFMpegPath != "" {
			ffmpegPath = config.FFMpegPath
		}
		if config.TempDir != "" {
			tempDir = config.TempDir
		}
	}

	extractor := &FFMpegExtractor{ffmpegPath: ffmpegPath, tempDir: tempDir}
	if err := extractor.checkFFmpeg(); err != nil {
		return nil, err
	}
	return extractor, nil
}

// checkFFmpeg verifies that FFmpeg is available.
func (e *FFMpegExtractor) checkFFmpeg() error {
	cmd := exec.Command(e.ffmpegPath, "-version")
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg not found or not executable: %w", err)
	}
	return nil
}

// ExtractFrames extracts frames from a video using FFmpeg.
func (e *FFMpegExtractor) ExtractFrames(ctx context.Context, videoBytes []byte, options *ExtractOptions) ([]*Frame, error) {
	if options == nil {
		options = DefaultExtractOptions()
	}

	tempVideoPath, err := e.writeTempVideo(videoBytes)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tempVideoPath)

	tempFrameDir, err := os.MkdirTemp(e.tempDir, "video-frames-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp frame directory: %w", err)
	}
	defer os.RemoveAll(tempFrameDir)

	if _, err := e.getVideoDuration(ctx, tempVideoPath); err != nil {
		logger.Warnf(ctx, "[VideoFFMpeg] Failed to get video duration: %v", err)
	}

	if err := e.extractFramesWithFFmpeg(ctx, tempVideoPath, tempFrameDir, options); err != nil {
		return nil, err
	}

	return e.readExtractedFrames(ctx, tempFrameDir, options)
}

// ExtractFramesToReaders extracts frames and returns them as io.Readers.
func (e *FFMpegExtractor) ExtractFramesToReaders(ctx context.Context, videoBytes []byte, options *ExtractOptions) ([]io.Reader, error) {
	frames, err := e.ExtractFrames(ctx, videoBytes, options)
	if err != nil {
		return nil, err
	}

	readers := make([]io.Reader, 0, len(frames))
	for _, frame := range frames {
		readers = append(readers, bytes.NewReader(frame.ImageData))
	}

	return readers, nil
}

// writeTempVideo writes the video bytes to a temporary file.
func (e *FFMpegExtractor) writeTempVideo(videoBytes []byte) (string, error) {
	tempFile, err := os.CreateTemp(e.tempDir, "video-input-*.mp4")
	if err != nil {
		return "", fmt.Errorf("failed to create temp video file: %w", err)
	}
	defer tempFile.Close()

	if _, err := tempFile.Write(videoBytes); err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to write video bytes: %w", err)
	}

	return tempFile.Name(), nil
}

// getVideoDuration gets the duration of a video using FFmpeg.
func (e *FFMpegExtractor) getVideoDuration(ctx context.Context, videoPath string) (float64, error) {
	cmd := exec.CommandContext(ctx, e.ffmpegPath,
		"-i", videoPath,
		"-f", "null",
		"-")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, err
	}

	outputStr := string(output)
	durationStr := ""
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Duration:") {
			parts := strings.Split(line, "Duration:")
			if len(parts) > 1 {
				durationPart := strings.TrimSpace(parts[1])
				durationStr = strings.Split(durationPart, ",")[0]
				break
			}
		}
	}

	if durationStr == "" {
		return 0, fmt.Errorf("could not parse duration")
	}

	timeParts := strings.Split(durationStr, ":")
	if len(timeParts) != 3 {
		return 0, fmt.Errorf("invalid duration format: %s", durationStr)
	}

	hours, _ := strconv.Atoi(timeParts[0])
	minutes, _ := strconv.Atoi(timeParts[1])
	seconds, _ := strconv.ParseFloat(timeParts[2], 64)

	totalSeconds := float64(hours*3600) + float64(minutes*60) + seconds
	return totalSeconds, nil
}

// extractFramesWithFFmpeg uses FFmpeg to extract frames from a video.
func (e *FFMpegExtractor) extractFramesWithFFmpeg(ctx context.Context, videoPath, outputDir string, options *ExtractOptions) error {
	filters := []string{fmt.Sprintf("fps=1/%f", options.FrameInterval)}

	if options.Width > 0 || options.Height > 0 {
		scaleFilter := "scale="
		if options.Width > 0 {
			scaleFilter += strconv.Itoa(options.Width)
		} else {
			scaleFilter += "-1"
		}
		scaleFilter += ":"
		if options.Height > 0 {
			scaleFilter += strconv.Itoa(options.Height)
		} else {
			scaleFilter += "-1"
		}
		filters = append(filters, scaleFilter)
	}

	args := []string{
		"-i", videoPath,
		"-vf", strings.Join(filters, ","),
	}

	quality := options.Quality
	if quality < 1 {
		quality = 1
	} else if quality > 100 {
		quality = 100
	}
	if options.Format == "jpeg" {
		args = append(args, "-q:v", strconv.Itoa(quality))
	}

	outputPattern := filepath.Join(outputDir, "frame-%05d."+options.Format)
	args = append(args, outputPattern)

	cmd := exec.CommandContext(ctx, e.ffmpegPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg extraction failed: %w, stderr: %s", err, stderr.String())
	}

	logger.Infof(ctx, "[VideoFFMpeg] Extracted frames to: %s", outputDir)
	return nil
}

// readExtractedFrames reads the extracted frames from the temporary directory.
func (e *FFMpegExtractor) readExtractedFrames(ctx context.Context, frameDir string, options *ExtractOptions) ([]*Frame, error) {
	files, err := filepath.Glob(filepath.Join(frameDir, "frame-*."+options.Format))
	if err != nil {
		return nil, fmt.Errorf("failed to list frame files: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no frames were extracted")
	}

	frames := make([]*Frame, 0, len(files))
	for i, filePath := range files {
		imgData, err := os.ReadFile(filePath)
		if err != nil {
			logger.Warnf(ctx, "[VideoFFMpeg] Failed to read frame %d: %v", i, err)
			continue
		}

		frame := &Frame{
			Index:     i,
			Timestamp: float64(i) * options.FrameInterval,
			ImageData: imgData,
			Format:    options.Format,
			Width:     options.Width,
			Height:    options.Height,
		}

		frames = append(frames, frame)

		if options.MaxFrames > 0 && len(frames) >= options.MaxFrames {
			break
		}
	}

	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames could be read")
	}

	logger.Infof(ctx, "[VideoFFMpeg] Successfully read %d frames", len(frames))
	return frames, nil
}

// GetVideoInfo returns basic information about a video.
func (e *FFMpegExtractor) GetVideoInfo(ctx context.Context, videoBytes []byte) (map[string]interface{}, error) {
	tempVideoPath, err := e.writeTempVideo(videoBytes)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tempVideoPath)

	duration, err := e.getVideoDuration(ctx, tempVideoPath)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, e.ffmpegPath, "-i", tempVideoPath, "-f", "null", "-")
	output, _ := cmd.CombinedOutput()
	outputStr := string(output)

	width, height := 0, 0
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Video:") {
			parts := strings.Split(line, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if strings.Contains(part, "x") && !strings.Contains(part, "SAR") {
					resParts := strings.Split(part, "x")
					if len(resParts) == 2 {
						width, _ = strconv.Atoi(strings.TrimSpace(resParts[0]))
						heightStr := strings.TrimSpace(resParts[1])
						heightParts := strings.Split(heightStr, " ")
						if len(heightParts) > 0 {
							height, _ = strconv.Atoi(heightParts[0])
						}
					}
					break
				}
			}
			break
		}
	}

	videoInfo := map[string]interface{}{
		"duration": duration,
		"width":    width,
		"height":   height,
	}

	return videoInfo, nil
}
