package types

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// EvaluationTaskLabelMaxFilterCount bounds the AND label filter.
	EvaluationTaskLabelMaxFilterCount = 5
	// EvaluationTaskLabelMaxPerTask bounds the labels stored on one task.
	EvaluationTaskLabelMaxPerTask = 20
	// EvaluationTaskLabelMaxBytes bounds one normalized UTF-8 label.
	EvaluationTaskLabelMaxBytes     = 64
	evaluationTaskListCursorVersion = 2
)

var (
	// ErrEvaluationTaskLabelInvalid reports an invalid atomic label set.
	ErrEvaluationTaskLabelInvalid = errors.New("evaluation task label invalid")
	// ErrEvaluationTaskQueryInvalid reports invalid list filter parameters.
	ErrEvaluationTaskQueryInvalid = errors.New("evaluation task query invalid")
	// ErrEvaluationTaskListInvalidCursor reports malformed, stale, or filter-mismatched cursors.
	ErrEvaluationTaskListInvalidCursor = errors.New("evaluation task list cursor is invalid")
)

// EvaluationTaskLabelEntity stores one normalized tenant-scoped task label.
type EvaluationTaskLabelEntity struct {
	TenantID  uint64    `gorm:"primaryKey"`
	TaskID    string    `gorm:"primaryKey;type:varchar(128)"`
	Label     string    `gorm:"primaryKey;type:varchar(64)"`
	CreatedAt time.Time `gorm:"not null"`
}

// TableName binds EvaluationTaskLabelEntity to its migration-owned table.
func (EvaluationTaskLabelEntity) TableName() string { return "evaluation_task_labels" }

// EvaluationTaskListFilters is the canonical filter set bound into a cursor.
type EvaluationTaskListFilters struct {
	Status           *EvaluationStatue
	DatasetID        string
	DatasetVersionID string
	ModelID          string
	StartedFrom      *time.Time
	StartedTo        *time.Time
	Labels           []string
}

// EvaluationTaskKeyset is one exclusive (start_time DESC, id DESC) boundary.
type EvaluationTaskKeyset struct {
	StartTime time.Time
	ID        string
}

// NormalizeEvaluationTaskLabels applies trim and Unicode lowercase, then
// validates the complete set before any repository write occurs.
func NormalizeEvaluationTaskLabels(raw []string, maxCount int) ([]string, error) {
	if len(raw) > maxCount {
		return nil, fmt.Errorf("%w: expected at most %d labels", ErrEvaluationTaskLabelInvalid, maxCount)
	}
	labels := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		label := strings.ToLower(strings.TrimSpace(item))
		if label == "" {
			return nil, fmt.Errorf("%w: label must not be empty", ErrEvaluationTaskLabelInvalid)
		}
		if len(label) > EvaluationTaskLabelMaxBytes {
			return nil, fmt.Errorf(
				"%w: label exceeds %d UTF-8 bytes",
				ErrEvaluationTaskLabelInvalid,
				EvaluationTaskLabelMaxBytes,
			)
		}
		if _, ok := seen[label]; ok {
			return nil, fmt.Errorf("%w: duplicate label %q", ErrEvaluationTaskLabelInvalid, label)
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	return labels, nil
}

type evaluationTaskListCursorPayload struct {
	Version    int       `json:"v"`
	StartTime  time.Time `json:"start_time"`
	ID         string    `json:"id"`
	FilterHash string    `json:"filter"`
}

// EncodeEvaluationTaskListCursor binds the keyset boundary to every active filter.
func EncodeEvaluationTaskListCursor(
	keyset EvaluationTaskKeyset,
	filters EvaluationTaskListFilters,
) (string, error) {
	if keyset.StartTime.IsZero() || keyset.ID == "" {
		return "", fmt.Errorf("%w: missing keyset boundary", ErrEvaluationTaskListInvalidCursor)
	}
	payload, err := json.Marshal(evaluationTaskListCursorPayload{
		Version:    evaluationTaskListCursorVersion,
		StartTime:  keyset.StartTime.UTC(),
		ID:         keyset.ID,
		FilterHash: evaluationTaskListFilterHash(filters),
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode payload: %v", ErrEvaluationTaskListInvalidCursor, err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// DecodeEvaluationTaskListCursor validates the version, boundary, and filter digest.
func DecodeEvaluationTaskListCursor(
	encoded string,
	filters EvaluationTaskListFilters,
) (*EvaluationTaskKeyset, error) {
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed encoding", ErrEvaluationTaskListInvalidCursor)
	}
	var cursor evaluationTaskListCursorPayload
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return nil, fmt.Errorf("%w: malformed payload", ErrEvaluationTaskListInvalidCursor)
	}
	if cursor.Version != evaluationTaskListCursorVersion {
		return nil, fmt.Errorf("%w: unsupported version", ErrEvaluationTaskListInvalidCursor)
	}
	if cursor.StartTime.IsZero() || cursor.ID == "" {
		return nil, fmt.Errorf("%w: missing keyset boundary", ErrEvaluationTaskListInvalidCursor)
	}
	if cursor.FilterHash != evaluationTaskListFilterHash(filters) {
		return nil, fmt.Errorf("%w: filter changed", ErrEvaluationTaskListInvalidCursor)
	}
	return &EvaluationTaskKeyset{StartTime: cursor.StartTime.UTC(), ID: cursor.ID}, nil
}

func evaluationTaskListFilterHash(filters EvaluationTaskListFilters) string {
	type canonicalFilters struct {
		Status           *EvaluationStatue `json:"status"`
		DatasetID        string            `json:"dataset_id"`
		DatasetVersionID string            `json:"dataset_version_id"`
		ModelID          string            `json:"model_id"`
		StartedFrom      string            `json:"started_from"`
		StartedTo        string            `json:"started_to"`
		Labels           []string          `json:"labels"`
	}
	labels := append([]string(nil), filters.Labels...)
	sort.Strings(labels)
	canonical := canonicalFilters{
		Status:           filters.Status,
		DatasetID:        filters.DatasetID,
		DatasetVersionID: filters.DatasetVersionID,
		ModelID:          filters.ModelID,
		Labels:           labels,
	}
	if filters.StartedFrom != nil {
		canonical.StartedFrom = filters.StartedFrom.UTC().Format(time.RFC3339Nano)
	}
	if filters.StartedTo != nil {
		canonical.StartedTo = filters.StartedTo.UTC().Format(time.RFC3339Nano)
	}
	payload, _ := json.Marshal(canonical)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
