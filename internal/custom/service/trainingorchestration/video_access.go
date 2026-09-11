package trainingorchestration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

const defaultVideoAccessConcurrency = 8

type ObjectExistenceReader interface {
	ObjectExists(context.Context, string) (bool, error)
}

// DefaultVideoAccessReader verifies uploaded objects directly when an object
// key is available and falls back to a bounded HTTP probe for legacy URLs.
type DefaultVideoAccessReader struct {
	Objects     ObjectExistenceReader
	HTTPClient  *http.Client
	Concurrency int
}

func NewVideoAccessReader(objects ObjectExistenceReader) *DefaultVideoAccessReader {
	return &DefaultVideoAccessReader{
		Objects: objects,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		Concurrency: defaultVideoAccessConcurrency,
	}
}

func (r *DefaultVideoAccessReader) CheckAccessible(ctx context.Context, videos []model.Video) (map[string]bool, error) {
	if r == nil {
		return nil, fmt.Errorf("video access reader is not configured")
	}
	result := make(map[string]bool, len(videos))
	if len(videos) == 0 {
		return result, nil
	}
	client := r.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	limit := r.Concurrency
	if limit <= 0 {
		limit = defaultVideoAccessConcurrency
	}

	var group errgroup.Group
	var resultMu sync.Mutex
	group.SetLimit(limit)
	for _, video := range videos {
		video := video
		group.Go(func() error {
			accessible, err := r.checkOne(ctx, client, video)
			if err != nil {
				return fmt.Errorf("video %s: %w", video.ID, err)
			}
			resultMu.Lock()
			result[video.ID] = accessible
			resultMu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *DefaultVideoAccessReader) checkOne(ctx context.Context, client *http.Client, video model.Video) (bool, error) {
	if objectKey := strings.TrimSpace(video.UploadObjectKey); objectKey != "" && r.Objects != nil {
		return r.Objects.ObjectExists(ctx, objectKey)
	}
	fileURL := strings.TrimSpace(video.FileURL)
	parsed, err := url.Parse(fileURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false, nil
	}
	if objectKey := objectKeyFromPublicFileURL(parsed); objectKey != "" && r.Objects != nil {
		return r.Objects.ObjectExists(ctx, objectKey)
	}
	return probeVideoURL(ctx, client, fileURL)
}

func objectKeyFromPublicFileURL(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	const prefix = "/api/custom/files/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return ""
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.Path, prefix))
	if err != nil {
		return ""
	}
	return strings.TrimLeft(strings.TrimSpace(key), "/")
}

func probeVideoURL(ctx context.Context, client *http.Client, fileURL string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return false, fmt.Errorf("build access probe: %w", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", "bytes=0-0")
	response, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("probe video URL: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusInternalServerError {
		return false, fmt.Errorf("probe video URL returned status %d", response.StatusCode)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return false, nil
	}
	oneByte := make([]byte, 1)
	n, readErr := io.ReadFull(response.Body, oneByte)
	if n == 1 {
		return true, nil
	}
	if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
		return false, nil
	}
	return false, fmt.Errorf("read video access probe: %w", readErr)
}
