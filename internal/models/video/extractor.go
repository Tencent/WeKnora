package video

import (
	"context"
	"io"
)

// Extractor defines the interface for video frame extraction operations.
type Extractor interface {
	// ExtractFrames extracts frames from a video file.
	// It returns a slice of Frame objects representing the extracted frames.
	ExtractFrames(ctx context.Context, videoBytes []byte, options *ExtractOptions) ([]*Frame, error)

	// ExtractFramesToReader extracts frames from a video and returns an io.Reader
	// for each frame's image data. This is useful for streaming processing.
	ExtractFramesToReaders(ctx context.Context, videoBytes []byte, options *ExtractOptions) ([]io.Reader, error)

	// GetVideoInfo retrieves basic information about a video file.
	// It returns a map containing duration, width, height, etc.
	GetVideoInfo(ctx context.Context, videoBytes []byte) (map[string]interface{}, error)
}

// Frame represents a single extracted frame from a video.
type Frame struct {
	// Index is the sequential index of the frame (0-based).
	Index int

	// Timestamp is the time in seconds from the start of the video.
	Timestamp float64

	// ImageData contains the raw image bytes of the frame.
	ImageData []byte

	// Format specifies the image format (e.g., "jpeg", "png").
	Format string

	// Width is the width of the frame in pixels.
	Width int

	// Height is the height of the frame in pixels.
	Height int
}

// ExtractOptions configures how frames are extracted from a video.
type ExtractOptions struct {
	// FrameInterval specifies how often to extract frames (in seconds).
	// For example, 1.0 means extract one frame per second.
	FrameInterval float64

	// MaxFrames specifies the maximum number of frames to extract.
	// If 0, extract all frames according to FrameInterval.
	MaxFrames int

	// Format specifies the output image format for extracted frames.
	// Supported formats: "jpeg" (default), "png".
	Format string

	// Quality specifies the image quality for JPEG output (1-100, default 80).
	Quality int

	// Width specifies the target width for resizing frames (0 = no resize).
	Width int

	// Height specifies the target height for resizing frames (0 = no resize).
	// If only one dimension is specified, the aspect ratio is preserved.
	Height int
}

// DefaultExtractOptions returns the default frame extraction options.
func DefaultExtractOptions() *ExtractOptions {
	return &ExtractOptions{
		FrameInterval: 1.0,
		MaxFrames:     0,
		Format:        "jpeg",
		Quality:       80,
		Width:         0,
		Height:        0,
	}
}

// Config holds the configuration needed to create a video extractor.
type Config struct {
	// FFMpegPath is the path to the ffmpeg executable.
	// If empty, it will try to find ffmpeg in the system PATH.
	FFMpegPath string

	// TempDir is the directory to use for temporary files.
	// If empty, the system default temporary directory is used.
	TempDir string
}

// NewExtractor creates a new video frame extractor based on the provided configuration.
func NewExtractor(config *Config) (Extractor, error) {
	return NewFFMpegExtractor(config)
}
