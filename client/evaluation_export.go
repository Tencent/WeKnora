package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ExportEvaluation streams a JSON or CSV evaluation artifact to dst.
func (c *Client) ExportEvaluation(
	ctx context.Context,
	taskID string,
	format string,
	dst io.Writer,
) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return errors.New("evaluation task ID is required")
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format != "json" && format != "csv" {
		return errors.New("evaluation export format must be json or csv")
	}
	if dst == nil {
		return errors.New("evaluation export destination is required")
	}
	response, err := c.doRequestStream(
		ctx,
		http.MethodGet,
		"/api/v1/evaluation/tasks/"+url.PathEscape(taskID)+"/export",
		nil,
		url.Values{"format": []string{format}},
	)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var body bytes.Buffer
		_, _ = io.Copy(&body, io.LimitReader(response.Body, 1<<20))
		return newAPIError(response.StatusCode, body.Bytes())
	}
	if _, err := io.Copy(dst, response.Body); err != nil {
		return fmt.Errorf("stream evaluation export: %w", err)
	}
	return nil
}
