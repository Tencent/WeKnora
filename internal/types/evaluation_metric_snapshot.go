package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Metric wire/storage snapshots owned by M3. M4 consumes these DTOs directly;
// the M5 registry produces them through an adapter and must not define
// parallel public DTOs or a second canonical serializer.

// EvaluationMetricSnapshotSchemaVersion is the frozen schema of metric plans.
const EvaluationMetricSnapshotSchemaVersion = 1

// EvaluationMetricSpecSnapshot pins one metric instance of a frozen plan.
// InstanceID is derived as key@version#config_sha256 and is stable across
// runs; NDCG k=3 and k=10 therefore coexist as distinct instances.
type EvaluationMetricSpecSnapshot struct {
	InstanceID   string          `json:"instance_id"`
	Key          string          `json:"key"`
	Version      string          `json:"version"`
	Config       json.RawMessage `json:"config"`
	ConfigSHA256 string          `json:"config_sha256"`
	Required     bool            `json:"required"`
}

// EvaluationMetricPlanSnapshot is the versioned metric plan frozen at task
// creation. PlanHash covers the canonical specs so concurrent completion
// order cannot change the stored bytes.
type EvaluationMetricPlanSnapshot struct {
	SchemaVersion int                            `json:"schema_version"`
	PlanHash      string                         `json:"plan_hash"`
	Metrics       []EvaluationMetricSpecSnapshot `json:"metrics"`
}

// EvaluationMetricObservation states distinguish real zero values from
// unavailable, skipped, and failed measurements.
const (
	EvaluationMetricObservationValid   = "valid"
	EvaluationMetricObservationMissing = "missing"
	EvaluationMetricObservationSkipped = "skipped"
	EvaluationMetricObservationFailed  = "failed"
)

// EvaluationMetricObservationSnapshot is one per-sample metric measurement
// keyed by the stable instance ID of its plan entry.
type EvaluationMetricObservationSnapshot struct {
	InstanceID string   `json:"instance_id"`
	Value      *float64 `json:"value"`
	Status     string   `json:"status"`
	ErrorCode  string   `json:"error_code,omitempty"`
}

// CanonicalEvaluationMetricConfigSHA256 hashes one metric configuration with
// fixed key order and HTML escaping disabled.
func CanonicalEvaluationMetricConfigSHA256(config json.RawMessage) string {
	return "sha256:" + hashEvaluationCanonicalJSON(config)
}

// EvaluationMetricInstanceID derives the stable instance identifier
// key@version#config_sha256 consumed by M4/M5.
func EvaluationMetricInstanceID(key, version, configSHA256 string) string {
	return key + "@" + version + "#" + configSHA256
}

// NewEvaluationMetricSpecSnapshot builds one spec entry with derived
// config hash and instance ID.
func NewEvaluationMetricSpecSnapshot(
	key, version string,
	config json.RawMessage,
	required bool,
) (EvaluationMetricSpecSnapshot, error) {
	normalized, err := normalizeEvaluationMetricConfig(config)
	if err != nil {
		return EvaluationMetricSpecSnapshot{}, err
	}
	configHash := CanonicalEvaluationMetricConfigSHA256(normalized)
	return EvaluationMetricSpecSnapshot{
		InstanceID:   EvaluationMetricInstanceID(key, version, configHash),
		Key:          key,
		Version:      version,
		Config:       normalized,
		ConfigSHA256: configHash,
		Required:     required,
	}, nil
}

// NewEvaluationMetricPlanSnapshot builds a frozen plan: specs are sorted by
// instance ID and the plan hash covers the canonical serialization.
func NewEvaluationMetricPlanSnapshot(
	specs []EvaluationMetricSpecSnapshot,
) (*EvaluationMetricPlanSnapshot, error) {
	ordered := append([]EvaluationMetricSpecSnapshot(nil), specs...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].InstanceID < ordered[j].InstanceID })
	plan := &EvaluationMetricPlanSnapshot{
		SchemaVersion: EvaluationMetricSnapshotSchemaVersion,
		Metrics:       ordered,
	}
	canonical, err := plan.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	plan.PlanHash = "sha256:" + hex.EncodeToString(sum[:])
	return plan, nil
}

// CanonicalJSON serializes the plan with deterministic bytes: fixed field
// order, sorted metrics, no HTML escaping. The plan_hash field is excluded
// from its own hash input.
func (p *EvaluationMetricPlanSnapshot) CanonicalJSON() ([]byte, error) {
	type planBody struct {
		SchemaVersion int                            `json:"schema_version"`
		Metrics       []EvaluationMetricSpecSnapshot `json:"metrics"`
	}
	body := planBody{SchemaVersion: p.SchemaVersion, Metrics: p.Metrics}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(body); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// Clone deep-copies the plan so callers cannot mutate shared config bytes.
func (p *EvaluationMetricPlanSnapshot) Clone() *EvaluationMetricPlanSnapshot {
	if p == nil {
		return nil
	}
	clone := &EvaluationMetricPlanSnapshot{
		SchemaVersion: p.SchemaVersion,
		PlanHash:      p.PlanHash,
		Metrics:       make([]EvaluationMetricSpecSnapshot, len(p.Metrics)),
	}
	for i, spec := range p.Metrics {
		clone.Metrics[i] = spec
		clone.Metrics[i].Config = append(json.RawMessage(nil), spec.Config...)
	}
	return clone
}

func normalizeEvaluationMetricConfig(config json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(config)
	if len(trimmed) == 0 {
		trimmed = []byte(`{}`)
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return nil, err
	}
	return json.RawMessage(canonicalEvaluationJSONBytes(decoded)), nil
}

// canonicalEvaluationJSONBytes re-encodes one decoded JSON value with sorted
// object keys and no HTML escaping.
func canonicalEvaluationJSONBytes(value any) []byte {
	var buffer bytes.Buffer
	writeEvaluationCanonicalJSON(&buffer, value)
	return buffer.Bytes()
}

func writeEvaluationCanonicalJSON(buffer *bytes.Buffer, value any) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			keyBytes, _ := json.Marshal(key)
			buffer.Write(keyBytes)
			buffer.WriteByte(':')
			writeEvaluationCanonicalJSON(buffer, typed[key])
		}
		buffer.WriteByte('}')
	case []any:
		buffer.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				buffer.WriteByte(',')
			}
			writeEvaluationCanonicalJSON(buffer, item)
		}
		buffer.WriteByte(']')
	default:
		encoded, _ := json.Marshal(typed)
		buffer.Write(encoded)
	}
}

func hashEvaluationCanonicalJSON(value json.RawMessage) string {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		// Invalid JSON hashes its raw bytes; validation happens upstream.
		sum := sha256.Sum256(value)
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(canonicalEvaluationJSONBytes(decoded))
	return hex.EncodeToString(sum[:])
}
