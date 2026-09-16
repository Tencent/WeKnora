package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvaluationMetricSpecSnapshotDerivesStableInstanceID(t *testing.T) {
	spec, err := NewEvaluationMetricSpecSnapshot("retrieval.ndcg", "1.0.0", json.RawMessage(`{"k":3}`), true)
	if err != nil {
		t.Fatalf("NewEvaluationMetricSpecSnapshot() error = %v", err)
	}
	if !strings.HasPrefix(spec.InstanceID, "retrieval.ndcg@1.0.0#sha256:") {
		t.Fatalf("InstanceID = %q, want key@version#config_sha256 format", spec.InstanceID)
	}

	// Key order inside config does not change the derived identity.
	reordered, err := NewEvaluationMetricSpecSnapshot("retrieval.ndcg", "1.0.0",
		json.RawMessage(`{"k":3}`), true)
	if err != nil {
		t.Fatalf("NewEvaluationMetricSpecSnapshot() error = %v", err)
	}
	if spec.InstanceID != reordered.InstanceID {
		t.Fatalf("InstanceID changed for identical config: %q != %q", spec.InstanceID, reordered.InstanceID)
	}

	// Different config (k=10) is a distinct instance of the same algorithm.
	other, err := NewEvaluationMetricSpecSnapshot("retrieval.ndcg", "1.0.0",
		json.RawMessage(`{"k":10}`), true)
	if err != nil {
		t.Fatalf("NewEvaluationMetricSpecSnapshot() error = %v", err)
	}
	if other.InstanceID == spec.InstanceID {
		t.Fatal("different config must produce a different instance ID")
	}
	if other.Key != spec.Key || other.Version != spec.Version {
		t.Fatal("instances of one algorithm share key and version")
	}
}

func TestEvaluationMetricConfigHashIsCanonical(t *testing.T) {
	first := CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"b":2,"a":1}`))
	second := CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"a":1,"b":2}`))
	if first != second {
		t.Fatalf("config hash changed with key order: %s != %s", first, second)
	}
	if first == CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"a":1,"b":3}`)) {
		t.Fatal("config hash must change when a value changes")
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("config hash = %q, want sha256: prefix", first)
	}
}

func TestEvaluationMetricPlanSnapshotCanonicalAndSorted(t *testing.T) {
	ndcg10, err := NewEvaluationMetricSpecSnapshot("retrieval.ndcg", "1.0.0", json.RawMessage(`{"k":10}`), false)
	if err != nil {
		t.Fatal(err)
	}
	ndcg3, err := NewEvaluationMetricSpecSnapshot("retrieval.ndcg", "1.0.0", json.RawMessage(`{"k":3}`), true)
	if err != nil {
		t.Fatal(err)
	}
	precision, err := NewEvaluationMetricSpecSnapshot("retrieval.precision", "1.0.0", json.RawMessage(`{}`), true)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := NewEvaluationMetricPlanSnapshot(
		[]EvaluationMetricSpecSnapshot{ndcg10, precision, ndcg3})
	if err != nil {
		t.Fatalf("NewEvaluationMetricPlanSnapshot() error = %v", err)
	}
	if plan.SchemaVersion != EvaluationMetricSnapshotSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", plan.SchemaVersion, EvaluationMetricSnapshotSchemaVersion)
	}
	if !strings.HasPrefix(plan.PlanHash, "sha256:") {
		t.Fatalf("PlanHash = %q, want sha256: prefix", plan.PlanHash)
	}
	for i := 1; i < len(plan.Metrics); i++ {
		if plan.Metrics[i-1].InstanceID > plan.Metrics[i].InstanceID {
			t.Fatal("plan metrics must be sorted by instance ID")
		}
	}

	// Assembly order does not change the frozen plan.
	reversed, err := NewEvaluationMetricPlanSnapshot(
		[]EvaluationMetricSpecSnapshot{ndcg3, precision, ndcg10})
	if err != nil {
		t.Fatal(err)
	}
	if reversed.PlanHash != plan.PlanHash {
		t.Fatal("plan hash must not depend on assembly order")
	}

	// Canonical bytes exclude plan_hash and never HTML-escape.
	canonical, err := plan.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonical), "plan_hash") {
		t.Fatal("canonical JSON must exclude the plan hash itself")
	}
	if strings.Contains(string(canonical), `<`) {
		t.Fatal("canonical JSON must not HTML-escape content")
	}
}

func TestEvaluationMetricPlanSnapshotCloneIsDeep(t *testing.T) {
	spec, err := NewEvaluationMetricSpecSnapshot("generation.bleu", "1.0.0", json.RawMessage(`{"n":4}`), true)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewEvaluationMetricPlanSnapshot([]EvaluationMetricSpecSnapshot{spec})
	if err != nil {
		t.Fatal(err)
	}
	clone := plan.Clone()
	clone.Metrics[0].Config[1] = 'X'
	if string(plan.Metrics[0].Config) == string(clone.Metrics[0].Config) {
		t.Fatal("clone must deep-copy config bytes")
	}
	var nilPlan *EvaluationMetricPlanSnapshot
	if nilPlan.Clone() != nil {
		t.Fatal("cloning a nil plan must stay nil")
	}
}

func TestEvaluationMetricObservationSnapshotStates(t *testing.T) {
	value := 0.0
	observation := EvaluationMetricObservationSnapshot{
		InstanceID: "retrieval.precision@1.0.0#sha256:abc",
		Value:      &value,
		Status:     EvaluationMetricObservationValid,
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"value":0`) {
		t.Fatalf("real zero scores must stay visible, got %s", encoded)
	}

	unavailable := EvaluationMetricObservationSnapshot{
		InstanceID: observation.InstanceID,
		Value:      nil,
		Status:     EvaluationMetricObservationSkipped,
	}
	encoded, err = json.Marshal(unavailable)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"value":null`) {
		t.Fatalf("skipped observations must carry null value, got %s", encoded)
	}
}

func TestEvaluationMetricObservationsPreserveCurrentMissingScores(t *testing.T) {
	plan, err := DefaultEvaluationMetricPlan()
	if err != nil {
		t.Fatal(err)
	}
	result := &MetricResult{
		RetrievalMetrics: RetrievalMetrics{MAP: 0.75},
		Scores:           make(map[string]EvaluationMetricScore),
	}
	var mapInstanceID string
	for _, spec := range plan.Metrics {
		if spec.Key == "retrieval.map" {
			mapInstanceID = spec.InstanceID
			result.Scores[spec.InstanceID] = EvaluationMetricScore{
				Status: EvaluationMetricObservationMissing, ErrorCode: "relevance_labels_missing",
			}
		}
	}
	if mapInstanceID == "" {
		t.Fatal("default plan has no retrieval.map instance")
	}
	observations := EvaluationMetricObservationsFromResult(plan, result)
	for _, observation := range observations {
		if observation.InstanceID != mapInstanceID {
			continue
		}
		if observation.Status != EvaluationMetricObservationMissing || observation.Value != nil ||
			observation.ErrorCode != "relevance_labels_missing" {
			t.Fatalf("missing registry score was not preserved: %+v", observation)
		}
		return
	}
	t.Fatal("retrieval.map observation not found")
}
