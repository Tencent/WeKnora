package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

func base64RawURLEncode(payload []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload)
}

func base64RawURLDecode(cursor string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(cursor)
}

// Per-question result states; terminal tasks keep the rows published before
// failure and never fabricate zero-value rows for unfinished samples.
const (
	EvaluationQuestionStatusSuccess  = "success"
	EvaluationQuestionStatusFailed   = "failed"
	EvaluationQuestionStatusCanceled = "canceled"
)

// Rank provenance states mirror the M0 contract: unknown sources and
// duplicate PIDs keep their rank with pid=-1 instead of compressing the
// ranking.
const (
	EvaluationRankProvenanceKnown     = "known"
	EvaluationRankProvenanceUnknown   = "unknown"
	EvaluationRankProvenanceDuplicate = "duplicate"
)

// EvaluationRankedResult is one rank position of a retrieval or rerank list.
type EvaluationRankedResult struct {
	Rank       int     `json:"rank"`
	PID        int     `json:"pid"`
	Score      float64 `json:"score"`
	Provenance string  `json:"provenance"`
}

// EvaluationQuestionResultEntity is one immutable per-question fact row.
type EvaluationQuestionResultEntity struct {
	TenantID           uint64 `json:"tenant_id" gorm:"primaryKey"`
	TaskID             string `json:"task_id" gorm:"type:varchar(128);primaryKey"`
	SampleIndex        int    `json:"sample_index" gorm:"primaryKey"`
	QID                string `json:"qid" gorm:"column:qid;type:varchar(128);not null"`
	Question           string `json:"question" gorm:"not null"`
	ReferenceAnswer    string `json:"reference_answer" gorm:"not null;default:''"`
	GroundTruthPIDs    JSON   `json:"ground_truth_pids" gorm:"column:ground_truth_pids;not null"`
	SearchResults      JSON   `json:"search_results" gorm:"not null"`
	RerankResults      JSON   `json:"rerank_results" gorm:"not null"`
	GenerationPIDs     JSON   `json:"generation_pids" gorm:"column:generation_pids;not null"`
	GeneratedText      string `json:"generated_text" gorm:"not null;default:''"`
	ErrorCode          string `json:"error_code,omitempty" gorm:"type:varchar(64);not null;default:''"`
	PerSampleMetrics   JSON   `json:"per_sample_metrics" gorm:"not null"`
	MetricObservations JSON   `json:"metric_observations" gorm:"not null"`

	// Nullable runtime facts: missing values stay null (unavailable) instead
	// of a fabricated zero.
	RetrievalMs      *int64 `json:"retrieval_ms,omitempty"`
	RerankMs         *int64 `json:"rerank_ms,omitempty"`
	GenerationMs     *int64 `json:"generation_ms,omitempty"`
	TotalMs          *int64 `json:"total_ms,omitempty"`
	PromptTokens     *int   `json:"prompt_tokens,omitempty"`
	CompletionTokens *int   `json:"completion_tokens,omitempty"`
	TotalTokens      *int   `json:"total_tokens,omitempty"`
	UsageReported    bool   `json:"usage_reported" gorm:"not null;default:false"`

	Status     string         `json:"status" gorm:"type:varchar(16);not null"`
	ResultHash string         `json:"result_hash" gorm:"type:char(64);not null"`
	CreatedAt  time.Time      `json:"created_at" gorm:"not null"`
	UpdatedAt  time.Time      `json:"updated_at" gorm:"not null"`
	DeletedAt  gorm.DeletedAt `json:"-" gorm:"index"`
}

// TableName binds EvaluationQuestionResultEntity to the per-question table.
func (EvaluationQuestionResultEntity) TableName() string { return "evaluation_question_results" }

// EvaluationQuestionResultInput carries one completed sample from the worker
// to the transactional publisher.
type EvaluationQuestionResultInput struct {
	SampleIndex      int
	QID              string
	Question         string
	ReferenceAnswer  string
	GroundTruthPIDs  []int
	SearchResults    []EvaluationRankedResult
	RerankResults    []EvaluationRankedResult
	GenerationPIDs   []int
	GeneratedText    string
	ErrorCode        string
	PerSampleMetrics *MetricResult
	Observations     []EvaluationMetricObservationSnapshot
	RetrievalMs      *int64
	RerankMs         *int64
	GenerationMs     *int64
	TotalMs          *int64
	PromptTokens     *int
	CompletionTokens *int
	TotalTokens      *int
	UsageReported    bool
	Status           string
}

// EvaluationQuestionResultHash computes the canonical content hash used for
// idempotent retries and conflict detection.
func EvaluationQuestionResultHash(input *EvaluationQuestionResultInput) string {
	canonical := canonicalEvaluationJSONBytes(map[string]any{
		"sample_index":        input.SampleIndex,
		"qid":                 input.QID,
		"question":            input.Question,
		"reference_answer":    input.ReferenceAnswer,
		"ground_truth_pids":   evaluationIntSliceToAny(input.GroundTruthPIDs),
		"search_results":      evaluationRankedSliceToAny(input.SearchResults),
		"rerank_results":      evaluationRankedSliceToAny(input.RerankResults),
		"generation_pids":     evaluationIntSliceToAny(input.GenerationPIDs),
		"generated_text":      input.GeneratedText,
		"error_code":          input.ErrorCode,
		"per_sample_metrics":  evaluationJSONValue(input.PerSampleMetrics),
		"metric_observations": evaluationJSONValue(input.Observations),
		"status":              input.Status,
		"retrieval_ms":        input.RetrievalMs,
		"rerank_ms":           input.RerankMs,
		"generation_ms":       input.GenerationMs,
		"total_ms":            input.TotalMs,
		"prompt_tokens":       input.PromptTokens,
		"completion_tokens":   input.CompletionTokens,
		"total_tokens":        input.TotalTokens,
		"usage_reported":      input.UsageReported,
	})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func evaluationJSONValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	return decoded
}

func evaluationIntSliceToAny(values []int) []any {
	output := make([]any, len(values))
	for i, value := range values {
		output[i] = value
	}
	return output
}

func evaluationRankedSliceToAny(values []EvaluationRankedResult) []any {
	output := make([]any, len(values))
	for i, value := range values {
		output[i] = map[string]any{
			"rank": value.Rank, "pid": value.PID, "score": value.Score, "provenance": value.Provenance,
		}
	}
	return output
}

// EvaluationQuestionResultPage is one keyset page ordered by sample_index.
type EvaluationQuestionResultPage struct {
	Items      []*EvaluationQuestionResultEntity `json:"items"`
	NextCursor string                            `json:"next_cursor"`
}

const (
	// EvaluationQuestionPageDefaultSize is the default per-question page size.
	EvaluationQuestionPageDefaultSize = 100
	// EvaluationQuestionPageMaxSize bounds one page.
	EvaluationQuestionPageMaxSize = 500
)

// EvaluationQuestionCursor is the versioned keyset cursor payload.
type EvaluationQuestionCursor struct {
	Version         int    `json:"v"`
	SampleIndexFrom int    `json:"sample_index_from"`
	TaskDigest      string `json:"task"`
}

// EncodeEvaluationQuestionCursor serializes one cursor as base64url JSON.
func EncodeEvaluationQuestionCursor(taskID string, sampleIndexFrom int) (string, error) {
	digest := sha256.Sum256([]byte(taskID))
	payload, err := json.Marshal(EvaluationQuestionCursor{
		Version:         1,
		SampleIndexFrom: sampleIndexFrom,
		TaskDigest:      hex.EncodeToString(digest[:4]),
	})
	if err != nil {
		return "", err
	}
	return base64RawURLEncode(payload), nil
}

// DecodeEvaluationQuestionCursor parses and validates one cursor for a task.
func DecodeEvaluationQuestionCursor(taskID string, cursor string) (int, error) {
	payload, err := base64RawURLDecode(cursor)
	if err != nil {
		return 0, err
	}
	var decoded EvaluationQuestionCursor
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return 0, err
	}
	digest := sha256.Sum256([]byte(taskID))
	if decoded.Version != 1 || decoded.TaskDigest != hex.EncodeToString(digest[:4]) ||
		decoded.SampleIndexFrom < 0 {
		return 0, errInvalidEvaluationQuestionCursor
	}
	return decoded.SampleIndexFrom, nil
}

var errInvalidEvaluationQuestionCursor = &evaluationQuestionCursorError{}

type evaluationQuestionCursorError struct{}

func (e *evaluationQuestionCursorError) Error() string { return "invalid evaluation question cursor" }

// IsEvaluationQuestionCursorError reports a malformed or mismatched cursor.
func IsEvaluationQuestionCursorError(err error) bool {
	_, ok := err.(*evaluationQuestionCursorError)
	return ok || bytes.Contains([]byte(err.Error()), []byte("invalid evaluation question cursor"))
}
