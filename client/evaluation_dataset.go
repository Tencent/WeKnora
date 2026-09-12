package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// EvaluationDataset describes one evaluation dataset identity.
type EvaluationDataset struct {
	ID               string    `json:"id"`
	Scope            string    `json:"scope"`
	OwnerTenantID    *uint64   `json:"owner_tenant_id,omitempty"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	CurrentVersionID string    `json:"current_version_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// EvaluationDatasetVersion describes one immutable dataset version. ID is the
// globally unique dataset_version_id; VersionNumber is per-dataset display only.
type EvaluationDatasetVersion struct {
	ID             string    `json:"id"`
	DatasetID      string    `json:"dataset_id"`
	VersionNumber  int       `json:"version_number"`
	SchemaVersion  int       `json:"schema_version"`
	ArtifactSHA256 string    `json:"artifact_sha256"`
	ContentSHA256  string    `json:"content_sha256"`
	PassageCount   int       `json:"passage_count"`
	QuestionCount  int       `json:"question_count"`
	RelevanceCount int       `json:"relevance_count"`
	CreatedAt      time.Time `json:"created_at"`
}

// EvaluationDatasetPassageInput is one passage in a version creation request.
type EvaluationDatasetPassageInput struct {
	PID      string          `json:"pid"`
	Content  string          `json:"content"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// EvaluationDatasetQuestionInput is one question in a version creation request.
// The array position fixes the stable sample_index.
type EvaluationDatasetQuestionInput struct {
	QID      string `json:"qid"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// EvaluationDatasetRelevanceInput is one relevance edge in a version creation request.
type EvaluationDatasetRelevanceInput struct {
	QID   string `json:"qid"`
	PID   string `json:"pid"`
	Grade int    `json:"grade"`
}

// EvaluationDatasetVersionInput is the structured content of one new version.
type EvaluationDatasetVersionInput struct {
	Passages  []EvaluationDatasetPassageInput   `json:"passages"`
	Questions []EvaluationDatasetQuestionInput  `json:"questions"`
	Relevance []EvaluationDatasetRelevanceInput `json:"relevance"`
}

// CreateEvaluationDatasetRequest carries one dataset creation request.
type CreateEvaluationDatasetRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// EvaluationDatasetResponse is the API envelope for one dataset.
type EvaluationDatasetResponse struct {
	Success bool               `json:"success"`
	Data    *EvaluationDataset `json:"data"`
}

// EvaluationDatasetVersionResponse is the API envelope for one version.
type EvaluationDatasetVersionResponse struct {
	Success bool                      `json:"success"`
	Data    *EvaluationDatasetVersion `json:"data"`
}

// EvaluationDatasetListResponse is the API envelope for dataset lists.
type EvaluationDatasetListResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Items []*EvaluationDataset `json:"items"`
	} `json:"data"`
}

// EvaluationDatasetVersionListResponse is the API envelope for version lists.
type EvaluationDatasetVersionListResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Items []*EvaluationDatasetVersion `json:"items"`
	} `json:"data"`
}

// CreateEvaluationDataset registers one tenant-scoped dataset.
func (c *Client) CreateEvaluationDataset(
	ctx context.Context,
	request *CreateEvaluationDatasetRequest,
) (*EvaluationDataset, error) {
	if request == nil || request.Name == "" {
		return nil, errors.New("evaluation dataset name is required")
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/evaluation/datasets", request, nil)
	if err != nil {
		return nil, err
	}
	var response EvaluationDatasetResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if !response.Success || response.Data == nil {
		return nil, errors.New("evaluation dataset response is missing data")
	}
	return response.Data, nil
}

// CreateEvaluationDatasetVersion freezes one immutable version from structured input.
func (c *Client) CreateEvaluationDatasetVersion(
	ctx context.Context,
	datasetID string,
	content *EvaluationDatasetVersionInput,
) (*EvaluationDatasetVersion, error) {
	if datasetID == "" {
		return nil, errors.New("evaluation dataset id is required")
	}
	if content == nil {
		return nil, errors.New("evaluation dataset version content is required")
	}
	resp, err := c.doRequest(
		ctx, http.MethodPost, "/api/v1/evaluation/datasets/"+datasetID+"/versions", content, nil)
	if err != nil {
		return nil, err
	}
	var response EvaluationDatasetVersionResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if !response.Success || response.Data == nil {
		return nil, errors.New("evaluation dataset version response is missing data")
	}
	return response.Data, nil
}

// ListEvaluationDatasets returns every dataset visible to the caller.
func (c *Client) ListEvaluationDatasets(ctx context.Context) ([]*EvaluationDataset, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/evaluation/datasets", nil, nil)
	if err != nil {
		return nil, err
	}
	var response EvaluationDatasetListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, errors.New("evaluation dataset list response is missing success")
	}
	return response.Data.Items, nil
}

// ListEvaluationDatasetVersions returns all versions of one visible dataset.
func (c *Client) ListEvaluationDatasetVersions(
	ctx context.Context,
	datasetID string,
) ([]*EvaluationDatasetVersion, error) {
	if datasetID == "" {
		return nil, errors.New("evaluation dataset id is required")
	}
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/evaluation/datasets/"+datasetID+"/versions", nil, nil)
	if err != nil {
		return nil, err
	}
	var response EvaluationDatasetVersionListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, errors.New("evaluation dataset version list response is missing success")
	}
	return response.Data.Items, nil
}
