package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// EvaluationMetricDefinition is one versioned metric registry entry.
type EvaluationMetricDefinition struct {
	Key           string          `json:"key"`
	Version       string          `json:"version"`
	Kind          string          `json:"kind"`
	Description   string          `json:"description"`
	DefaultConfig json.RawMessage `json:"default_config"`
	ConfigSchema  json.RawMessage `json:"config_schema"`
}

// ListEvaluationMetrics returns the server's stable metric registry catalog.
func (c *Client) ListEvaluationMetrics(ctx context.Context) ([]EvaluationMetricDefinition, error) {
	response, err := c.doRequest(ctx, http.MethodGet, "/api/v1/evaluation/metrics", nil, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Items []EvaluationMetricDefinition `json:"items"`
		} `json:"data"`
	}
	if err := parseResponse(response, &payload); err != nil {
		return nil, err
	}
	return payload.Data.Items, nil
}
