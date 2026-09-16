package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// EvaluationHumanRatingRevision is an immutable human judgment revision.
type EvaluationHumanRatingRevision struct {
	ID             string          `json:"id"`
	TenantID       uint64          `json:"tenant_id"`
	TaskID         string          `json:"task_id"`
	SampleIndex    int             `json:"sample_index"`
	Revision       int             `json:"revision"`
	RaterID        string          `json:"rater_id"`
	RubricKey      string          `json:"rubric_key"`
	RubricVersion  string          `json:"rubric_version"`
	RubricSnapshot json.RawMessage `json:"rubric_snapshot"`
	Score          int             `json:"score"`
	Comment        string          `json:"comment,omitempty"`
	SupersedesID   *string         `json:"supersedes_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// AppendEvaluationHumanRatingRequest describes one new revision.
type AppendEvaluationHumanRatingRequest struct {
	RubricKey      string          `json:"rubric_key"`
	RubricVersion  string          `json:"rubric_version"`
	RubricSnapshot json.RawMessage `json:"rubric_snapshot"`
	Score          int             `json:"score"`
	Comment        string          `json:"comment,omitempty"`
}

type evaluationHumanRatingListEnvelope struct {
	Success bool `json:"success"`
	Data    *struct {
		Items []*EvaluationHumanRatingRevision `json:"items"`
	} `json:"data"`
}

type evaluationHumanRatingEnvelope struct {
	Success bool                           `json:"success"`
	Data    *EvaluationHumanRatingRevision `json:"data"`
}

func evaluationHumanRatingPath(taskID string, sampleIndex int) (string, error) {
	if taskID == "" || sampleIndex < 0 {
		return "", errors.New("evaluation task id and non-negative sample index are required")
	}
	return "/api/v1/evaluation/tasks/" + taskID + "/questions/" + strconv.Itoa(sampleIndex) + "/ratings", nil
}

// ListEvaluationHumanRatings returns newest-first immutable rating revisions.
func (c *Client) ListEvaluationHumanRatings(
	ctx context.Context, taskID string, sampleIndex int,
) ([]*EvaluationHumanRatingRevision, error) {
	path, err := evaluationHumanRatingPath(taskID, sampleIndex)
	if err != nil {
		return nil, err
	}
	response, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var envelope evaluationHumanRatingListEnvelope
	if err := parseResponse(response, &envelope); err != nil {
		return nil, err
	}
	if !envelope.Success || envelope.Data == nil || envelope.Data.Items == nil {
		return nil, errors.New("evaluation human ratings response is missing data")
	}
	return envelope.Data.Items, nil
}

// AppendEvaluationHumanRating appends one immutable rating revision.
func (c *Client) AppendEvaluationHumanRating(
	ctx context.Context, taskID string, sampleIndex int, request AppendEvaluationHumanRatingRequest,
) (*EvaluationHumanRatingRevision, error) {
	path, err := evaluationHumanRatingPath(taskID, sampleIndex)
	if err != nil {
		return nil, err
	}
	if request.Score < 1 || request.Score > 5 || request.RubricKey == "" || request.RubricVersion == "" ||
		len(request.RubricSnapshot) == 0 {
		return nil, errors.New("rating score, rubric identity, and rubric snapshot are required")
	}
	response, err := c.doRequest(ctx, http.MethodPost, path, request, nil)
	if err != nil {
		return nil, err
	}
	var envelope evaluationHumanRatingEnvelope
	if err := parseResponse(response, &envelope); err != nil {
		return nil, err
	}
	if !envelope.Success || envelope.Data == nil {
		return nil, fmt.Errorf("evaluation human rating response is missing data")
	}
	return envelope.Data, nil
}
