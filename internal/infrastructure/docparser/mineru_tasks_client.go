package docparser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// minerUTasksClient implements the single-file subset of MinerU 4.1 Tasks.
// The JSON, transfer, polling and cancellation budgets are shared with Jobs.
// See https://github.com/opendatalab/MinerU/blob/mineru-4.1.0-released/docs/en/usage/tasks.md.
type minerUTasksClient struct {
	*minerUV1Client
}

type minerUTaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *minerUTaskError) Error() string {
	return e.Code + ": " + e.Message
}

type minerUTaskUpload struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

type minerUTaskFile struct {
	FileID    string            `json:"file_id"`
	Status    string            `json:"status"`
	Upload    *minerUTaskUpload `json:"upload"`
	ResultURL string            `json:"result_url"`
	Error     *minerUTaskError  `json:"error"`
}

type minerUTask struct {
	TaskID string           `json:"task_id"`
	Status string           `json:"status"`
	Files  []minerUTaskFile `json:"files"`
	Error  *minerUTaskError `json:"error"`
}

func (c *minerUTasksClient) ParseWithLayout(
	ctx context.Context, content []byte, fileName string, opts minerUV1ParseOptions,
) (string, []types.ImageRef, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.jobTimeout)
	defer cancel()

	sum := sha256.Sum256(content)
	payload := map[string]any{
		"files": []map[string]any{{
			"file_name": fileName, "bytes": len(content), "sha256sum": hex.EncodeToString(sum[:]),
		}},
		"output_formats": []string{"default"},
	}
	if opts.Tier != "" {
		payload["tier"] = opts.Tier
	}
	if opts.OCRMode != "" {
		payload["ocr_mode"] = opts.OCRMode
	}
	var task minerUTask
	// A create request is never retried: its outcome may be unknown on failure.
	if err := c.doJSON(ctx, http.MethodPost, "/v1/tasks", payload, &task); err != nil {
		return "", nil, nil, fmt.Errorf("create parse task: %w", err)
	}
	if task.TaskID == "" {
		return "", nil, nil, fmt.Errorf("create parse task: response has no task_id")
	}
	taskID := task.TaskID
	terminal := false
	defer func() {
		if !terminal {
			c.cancelTask(taskID)
		}
	}()
	if len(task.Files) != 1 || task.Files[0].FileID == "" {
		return "", nil, nil, fmt.Errorf("parse task %s: expected one file with a file_id", taskID)
	}
	fileID := task.Files[0].FileID
	path := "/v1/tasks/" + url.PathEscape(taskID)
	uploaded := false
	queried := false
	firstPoll := true
	interval := c.pollInitial
	for {
		if ctx.Err() != nil {
			return "", nil, nil, fmt.Errorf("parse task %s: %w", taskID, ctx.Err())
		}
		if task.TaskID != taskID || len(task.Files) != 1 || task.Files[0].FileID != fileID {
			return "", nil, nil, fmt.Errorf("parse task %s: inconsistent task or file identity", taskID)
		}
		terminal = minerUV1JobTerminal(task.Status)
		if task.Error != nil {
			return "", nil, nil, fmt.Errorf("parse task %s: %w", taskID, task.Error)
		}
		file := task.Files[0]
		// A 200 snapshot can carry a file failure without a top-level error.
		if file.Error != nil {
			return "", nil, nil, fmt.Errorf("parse task %s file %s: %w", taskID, fileID, file.Error)
		}
		if terminal && file.Status != "completed" {
			return "", nil, nil, fmt.Errorf("parse task %s ended %s", taskID, task.Status)
		}
		switch file.Status {
		case "failed", "canceled":
			return "", nil, nil, fmt.Errorf("parse task %s file %s %s", taskID, fileID, file.Status)
		case "completed":
			// Creation receipts omit result URLs even for cache hits. Always
			// query a fresh snapshot before attempting the download.
			if queried {
				if file.ResultURL == "" {
					return "", nil, nil, fmt.Errorf("parse task %s completed without result_url", taskID)
				}
				zipData, err := c.downloadURL(ctx, file.ResultURL)
				if err != nil {
					return "", nil, nil, fmt.Errorf("download task result: %w", err)
				}
				md, refs, err := extractMarkdownZip(zipData, c.logLabel)
				if err != nil {
					return "", nil, nil, fmt.Errorf("extract task result: %w", err)
				}
				return md, refs, minerUContentListFromZip(zipData), nil
			}
		case "waiting-file":
			if !uploaded && file.Upload != nil {
				if file.Upload.URL == "" {
					return "", nil, nil, fmt.Errorf("parse task %s: upload action has no URL", taskID)
				}
				if err := c.putBytes(ctx, &minerUV1Upload{
					UploadURL: file.Upload.URL, UploadMethod: http.MethodPut, UploadHeaders: file.Upload.Headers,
				}, content); err != nil {
					return "", nil, nil, err
				}
				uploaded = true
			}
		case "pending", "running":
		default:
			return "", nil, nil, fmt.Errorf("parse task %s: unexpected file status %q", taskID, file.Status)
		}

		if !firstPoll {
			sleepCtx(ctx, interval)
			if ctx.Err() != nil {
				return "", nil, nil, fmt.Errorf("parse task %s: %w", taskID, ctx.Err())
			}
			interval *= 2
			if interval > c.pollMax {
				interval = c.pollMax
			}
		}
		firstPoll = false
		var snapshot minerUTask
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &snapshot); err != nil {
			if ctx.Err() != nil {
				return "", nil, nil, fmt.Errorf("parse task %s: %w", taskID, ctx.Err())
			}
			var apiErr *minerUV1APIError
			if errors.As(err, &apiErr) && apiErr.status < 500 && apiErr.status != http.StatusTooManyRequests {
				return "", nil, nil, fmt.Errorf("poll parse task %s: %w", taskID, err)
			}
		} else {
			// Snapshots replace old state; never reuse a stale upload action.
			task = snapshot
			queried = true
		}
	}
}

func (c *minerUTasksClient) cancelTask(taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), c.cancelBudget)
	defer cancel()
	// Best effort; preserve the original failure if cancellation also fails.
	if err := c.doJSON(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/cancel", nil, nil); err != nil {
		logger.Warnf(ctx, "[%s] cancel task %s: %v", c.logLabel, taskID, err)
	}
}
