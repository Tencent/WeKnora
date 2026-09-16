package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	// EvaluationComparisonSchemaVersion identifies the comparison response schema.
	EvaluationComparisonSchemaVersion = 1
	// EvaluationComparisonMinRuns is the minimum number of distinct runs in a comparison.
	EvaluationComparisonMinRuns = 2
	// EvaluationComparisonMaxRuns is the maximum number of distinct runs in a comparison.
	EvaluationComparisonMaxRuns = 10
)

const (
	// EvaluationComparisonValueValid marks a metric value that can participate in a comparison.
	EvaluationComparisonValueValid = "valid"
	// EvaluationComparisonValueMissing marks a metric value absent from a run.
	EvaluationComparisonValueMissing = "missing"
	// EvaluationComparisonValueIncompatible marks a metric whose identity differs across runs.
	EvaluationComparisonValueIncompatible = "incompatible"

	// EvaluationComparisonReasonBaselineMissing records that no baseline value is available.
	EvaluationComparisonReasonBaselineMissing = "baseline_value_missing"
	// EvaluationComparisonReasonBaselineZero records that relative change has a zero denominator.
	EvaluationComparisonReasonBaselineZero = "baseline_value_is_zero"
	// EvaluationComparisonReasonIdentityMissing records that a metric identity is unavailable.
	EvaluationComparisonReasonIdentityMissing = "metric_identity_missing"
	// EvaluationComparisonReasonIdentityDiffers records differing metric identities across runs.
	EvaluationComparisonReasonIdentityDiffers = "metric_identity_differs"
)

var (
	// ErrEvaluationComparisonInvalid indicates an invalid comparison request.
	ErrEvaluationComparisonInvalid = errors.New("evaluation comparison request invalid")
	// ErrEvaluationComparisonTaskNotFound indicates a missing or inaccessible comparison task.
	ErrEvaluationComparisonTaskNotFound = errors.New("evaluation comparison task not found")
	// ErrEvaluationComparisonConflict indicates that selected runs cannot be compared.
	ErrEvaluationComparisonConflict = errors.New("evaluation comparison conflict")
	// ErrEvaluationComparisonDataInvalid indicates malformed frozen comparison data.
	ErrEvaluationComparisonDataInvalid = errors.New("evaluation comparison data invalid")
)

// EvaluationComparisonRequest selects runs and an optional baseline for comparison.
type EvaluationComparisonRequest struct {
	TaskIDs        []string `json:"task_ids"`
	BaselineTaskID string   `json:"baseline_task_id,omitempty"`
}

// EvaluationComparisonRun describes one frozen run included in a comparison.
type EvaluationComparisonRun struct {
	TaskID                string                        `json:"task_id"`
	Status                EvaluationStatue              `json:"status"`
	IsBaseline            bool                          `json:"is_baseline"`
	DatasetID             string                        `json:"dataset_id"`
	DatasetVersionID      string                        `json:"dataset_version_id"`
	VersionNumber         int                           `json:"version_number"`
	DatasetContentSHA256  string                        `json:"dataset_content_sha256"`
	ProvenanceComplete    bool                          `json:"provenance_complete"`
	QuestionSuccessRate   *EvaluationConfidenceInterval `json:"question_success_rate,omitempty"`
	QuestionSuccessStatus string                        `json:"question_success_status"`
	QuestionNTotal        int                           `json:"question_n_total"`
	QuestionNValid        int                           `json:"question_n_valid"`
	QuestionNMissing      int                           `json:"question_n_missing"`
	TotalLatency          *EvaluationPercentiles        `json:"total_latency_ms,omitempty"`
	TokenTotals           EvaluationTokenTotals         `json:"token_totals"`
}

const (
	// EvaluationStatisticsValid marks an estimate with enough observations.
	EvaluationStatisticsValid = "valid"
	// EvaluationStatisticsInsufficientSample marks an estimate with fewer than two valid observations.
	EvaluationStatisticsInsufficientSample = "insufficient_sample"
)

// EvaluationConfidenceInterval describes one estimate and its two-sided interval.
type EvaluationConfidenceInterval struct {
	Estimate   float64 `json:"estimate"`
	Lower      float64 `json:"lower"`
	Upper      float64 `json:"upper"`
	Confidence float64 `json:"confidence"`
	Method     string  `json:"method"`
	Samples    int     `json:"samples"`
	Iterations int     `json:"iterations,omitempty"`
	Seed       int64   `json:"seed,omitempty"`
}

// EvaluationPercentiles describes latency distribution percentiles without a confidence interval.
type EvaluationPercentiles struct {
	P50      float64 `json:"p50"`
	P95      float64 `json:"p95"`
	P99      float64 `json:"p99"`
	NTotal   int     `json:"n_total"`
	NValid   int     `json:"n_valid"`
	NMissing int     `json:"n_missing"`
}

// EvaluationTokenTotals keeps additive token facts separate from statistical intervals.
type EvaluationTokenTotals struct {
	Prompt     int64 `json:"prompt"`
	Completion int64 `json:"completion"`
	Total      int64 `json:"total"`
	NTotal     int   `json:"n_total"`
	NValid     int   `json:"n_valid"`
	NMissing   int   `json:"n_missing"`
}

// EvaluationComparisonParameterValue is one run's value for a stable parameter pointer.
type EvaluationComparisonParameterValue struct {
	TaskID  string          `json:"task_id"`
	Missing bool            `json:"missing"`
	Value   json.RawMessage `json:"value,omitempty"`
}

// EvaluationComparisonParameter contains aligned parameter values across selected runs.
type EvaluationComparisonParameter struct {
	Pointer string                               `json:"pointer"`
	Differ  bool                                 `json:"differ"`
	Values  []EvaluationComparisonParameterValue `json:"values"`
}

// EvaluationComparisonMetricValue contains one run's metric value and baseline deltas.
type EvaluationComparisonMetricValue struct {
	TaskID           string                        `json:"task_id"`
	IsBaseline       bool                          `json:"is_baseline"`
	Status           string                        `json:"status"`
	Value            *float64                      `json:"value"`
	Delta            *float64                      `json:"delta"`
	RelativeDelta    *float64                      `json:"relative_delta"`
	RelativeReason   string                        `json:"relative_reason,omitempty"`
	Reason           string                        `json:"reason,omitempty"`
	Confidence       *EvaluationConfidenceInterval `json:"confidence,omitempty"`
	ConfidenceStatus string                        `json:"confidence_status"`
	NTotal           int                           `json:"n_total"`
	NValid           int                           `json:"n_valid"`
	NMissing         int                           `json:"n_missing"`
}

// EvaluationComparisonMetric contains aligned values for one numeric metric leaf.
type EvaluationComparisonMetric struct {
	Pointer        string                            `json:"pointer"`
	Key            string                            `json:"key"`
	Version        string                            `json:"version"`
	ConfigSHA256   string                            `json:"config_sha256"`
	Compatible     bool                              `json:"compatible"`
	BaselineTaskID string                            `json:"baseline_task_id"`
	Values         []EvaluationComparisonMetricValue `json:"values"`
}

// EvaluationComparisonResponse is the stable wire representation of a run comparison.
type EvaluationComparisonResponse struct {
	SchemaVersion  int                             `json:"schema_version"`
	BaselineTaskID string                          `json:"baseline_task_id"`
	Runs           []EvaluationComparisonRun       `json:"runs"`
	Parameters     []EvaluationComparisonParameter `json:"parameters"`
	Metrics        []EvaluationComparisonMetric    `json:"metrics"`
}

// EvaluationComparisonMetricIdentity identifies a metric implementation and configuration.
type EvaluationComparisonMetricIdentity struct {
	Key          string
	Version      string
	ConfigSHA256 string
}

// NormalizeEvaluationComparisonTaskIDs validates, trims, and stably deduplicates task IDs.
func NormalizeEvaluationComparisonTaskIDs(raw []string) ([]string, error) {
	seen := make(map[string]struct{}, len(raw))
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		id := strings.TrimSpace(item)
		if id == "" {
			return nil, fmt.Errorf("%w: task_ids contains an empty value", ErrEvaluationComparisonInvalid)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) < EvaluationComparisonMinRuns || len(ids) > EvaluationComparisonMaxRuns {
		return nil, fmt.Errorf("%w: expected 2 to 10 distinct task_ids", ErrEvaluationComparisonInvalid)
	}
	return ids, nil
}

var evaluationComparisonParameterRoots = []struct {
	pointer string
	keys    []string
}{
	{pointer: "/dataset", keys: []string{"dataset"}},
	{pointer: "/models", keys: []string{"models"}},
	{pointer: "/configuration/retrieval", keys: []string{"configuration", "retrieval"}},
	{pointer: "/configuration/rerank", keys: []string{"configuration", "rerank"}},
	{pointer: "/configuration/generation", keys: []string{"configuration", "generation"}},
	{pointer: "/metric_plan", keys: []string{"metric_plan"}},
}

// FlattenEvaluationComparisonParameters extracts leaves below the approved stable parameter roots.
func FlattenEvaluationComparisonParameters(snapshot JSON) (map[string]json.RawMessage, error) {
	var root map[string]any
	if err := json.Unmarshal(snapshot, &root); err != nil || root == nil {
		return nil, fmt.Errorf("%w: experiment snapshot must be a JSON object", ErrEvaluationComparisonDataInvalid)
	}
	result := make(map[string]json.RawMessage)
	for _, spec := range evaluationComparisonParameterRoots {
		var current any = root
		found := true
		for _, key := range spec.keys {
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%w: %s must be an object", ErrEvaluationComparisonDataInvalid, spec.pointer)
			}
			current, found = object[key]
			if !found {
				break
			}
		}
		if found {
			flattenEvaluationComparisonLeaves(spec.pointer, current, result, false)
		}
	}
	return result, nil
}

// FlattenEvaluationNumericMetrics extracts every numeric leaf from an aggregate metric object.
func FlattenEvaluationNumericMetrics(metric JSON) (map[string]float64, error) {
	trimmed := bytes.TrimSpace(metric)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return map[string]float64{}, nil
	}
	var root any
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return nil, fmt.Errorf("%w: aggregate metric is invalid JSON", ErrEvaluationComparisonDataInvalid)
	}
	if _, ok := root.(map[string]any); !ok {
		return nil, fmt.Errorf("%w: aggregate metric must be a JSON object", ErrEvaluationComparisonDataInvalid)
	}
	result := make(map[string]float64)
	flattenEvaluationComparisonLeaves("", root, result, true)
	return result, nil
}

func flattenEvaluationComparisonLeaves(prefix string, value any, target any, numericOnly bool) {
	switch node := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(node))
		for key := range node {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			flattenEvaluationComparisonLeaves(
				prefix+"/"+escapeEvaluationJSONPointer(key),
				node[key],
				target,
				numericOnly,
			)
		}
	case []any:
		for index, item := range node {
			flattenEvaluationComparisonLeaves(prefix+"/"+strconv.Itoa(index), item, target, numericOnly)
		}
	case float64:
		if numericOnly {
			target.(map[string]float64)[prefix] = node
			return
		}
		encoded, _ := json.Marshal(node)
		target.(map[string]json.RawMessage)[prefix] = encoded
	default:
		if numericOnly {
			return
		}
		encoded, _ := json.Marshal(node)
		target.(map[string]json.RawMessage)[prefix] = encoded
	}
}

// EvaluationComparisonMetricIdentityForPath resolves the metric identity for an aggregate metric pointer.
func EvaluationComparisonMetricIdentityForPath(
	experiment *EvaluationExperimentSnapshot,
	pointer string,
) (EvaluationComparisonMetricIdentity, bool) {
	if experiment == nil || experiment.MetricPlan == nil {
		return EvaluationComparisonMetricIdentity{}, false
	}
	key, config := evaluationComparisonFixedMetricSpec(pointer)
	if key != "" {
		configHash := CanonicalEvaluationMetricConfigSHA256(json.RawMessage(config))
		for _, spec := range experiment.MetricPlan.Metrics {
			if spec.Key == key && spec.ConfigSHA256 == configHash {
				return EvaluationComparisonMetricIdentity{
					Key: spec.Key, Version: spec.Version, ConfigSHA256: spec.ConfigSHA256,
				}, true
			}
		}
	}
	if strings.HasPrefix(pointer, "/scores/") {
		token := strings.TrimPrefix(pointer, "/scores/")
		if index := strings.IndexByte(token, '/'); index >= 0 {
			token = token[:index]
		}
		instanceID := unescapeEvaluationJSONPointer(token)
		for _, spec := range experiment.MetricPlan.Metrics {
			if spec.InstanceID == instanceID {
				return EvaluationComparisonMetricIdentity{
					Key: spec.Key, Version: spec.Version, ConfigSHA256: spec.ConfigSHA256,
				}, true
			}
		}
	}
	return EvaluationComparisonMetricIdentity{}, false
}

func evaluationComparisonFixedMetricSpec(pointer string) (string, string) {
	switch pointer {
	case "/retrieval_metrics/precision":
		return "retrieval.precision", `{}`
	case "/retrieval_metrics/recall":
		return "retrieval.recall", `{}`
	case "/retrieval_metrics/ndcg3":
		return "retrieval.ndcg", `{"k":3}`
	case "/retrieval_metrics/ndcg10":
		return "retrieval.ndcg", `{"k":10}`
	case "/retrieval_metrics/mrr":
		return "retrieval.mrr", `{}`
	case "/retrieval_metrics/map":
		return "retrieval.map", `{}`
	case "/generation_metrics/bleu1":
		return "generation.bleu", `{"n":1}`
	case "/generation_metrics/bleu2":
		return "generation.bleu", `{"n":2}`
	case "/generation_metrics/bleu4":
		return "generation.bleu", `{"n":4}`
	case "/generation_metrics/rouge1":
		return "generation.rouge", `{"variant":"rouge-1"}`
	case "/generation_metrics/rouge2":
		return "generation.rouge", `{"variant":"rouge-2"}`
	case "/generation_metrics/rougel":
		return "generation.rouge", `{"variant":"rouge-l"}`
	default:
		return "", ""
	}
}

func escapeEvaluationJSONPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func unescapeEvaluationJSONPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~1", "/"), "~0", "~")
}
