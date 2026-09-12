package types

import "encoding/json"

// DefaultEvaluationMetricPlan freezes the current twelve quality metrics as
// one versioned plan. Keys, versions, and configs are owned by M3; the M5
// registry consumes this exact shape through its adapter and must not define
// a parallel default list.
func DefaultEvaluationMetricPlan() (*EvaluationMetricPlanSnapshot, error) {
	type spec struct {
		key     string
		version string
		config  string
	}
	defaults := []spec{
		{"retrieval.precision", "1.0.0", `{}`},
		{"retrieval.recall", "1.0.0", `{}`},
		{"retrieval.ndcg", "2.0.0", `{"k":3}`},
		{"retrieval.ndcg", "2.0.0", `{"k":10}`},
		{"retrieval.mrr", "1.0.0", `{}`},
		{"retrieval.map", "2.0.0", `{}`},
		{"generation.bleu", "1.0.0", `{"n":1}`},
		{"generation.bleu", "1.0.0", `{"n":2}`},
		{"generation.bleu", "1.0.0", `{"n":4}`},
		{"generation.rouge", "1.0.0", `{"variant":"rouge-1"}`},
		{"generation.rouge", "1.0.0", `{"variant":"rouge-2"}`},
		{"generation.rouge", "1.0.0", `{"variant":"rouge-l"}`},
	}
	specs := make([]EvaluationMetricSpecSnapshot, 0, len(defaults))
	for _, entry := range defaults {
		built, err := NewEvaluationMetricSpecSnapshot(entry.key, entry.version, json.RawMessage(entry.config), true)
		if err != nil {
			return nil, err
		}
		specs = append(specs, built)
	}
	return NewEvaluationMetricPlanSnapshot(specs)
}

// EvaluationMetricObservationsFromResult maps one per-sample MetricResult to
// the stable observation list of the frozen plan. Every plan entry gets a
// valid observation; real zero scores stay visible as value=0.
func EvaluationMetricObservationsFromResult(
	plan *EvaluationMetricPlanSnapshot,
	result *MetricResult,
) []EvaluationMetricObservationSnapshot {
	if plan == nil || result == nil {
		return nil
	}
	compatibilityValueFor := func(spec EvaluationMetricSpecSnapshot) (float64, bool) {
		if spec.Version != "1.0.0" {
			return 0, false
		}
		pointer, ok := EvaluationMetricFixedPointer(spec)
		if !ok {
			return 0, false
		}
		return EvaluationMetricFixedValue(result, pointer)
	}

	observations := make([]EvaluationMetricObservationSnapshot, 0, len(plan.Metrics))
	for _, spec := range plan.Metrics {
		observation := EvaluationMetricObservationSnapshot{InstanceID: spec.InstanceID}
		if score, ok := result.Scores[spec.InstanceID]; ok {
			observation.Value = score.Value
			observation.Status = score.Status
			observation.ErrorCode = score.ErrorCode
		} else if value, ok := compatibilityValueFor(spec); ok {
			observation.Value = &value
			observation.Status = EvaluationMetricObservationValid
		} else {
			observation.Status = EvaluationMetricObservationSkipped
			observation.ErrorCode = "unknown_metric_instance"
		}
		observations = append(observations, observation)
	}
	return observations
}

// EvaluationMetricFixedPointer returns the compatibility field mirrored by a
// built-in metric configuration. Metric version remains part of the dynamic
// score identity even when multiple versions share one compatibility field.
func EvaluationMetricFixedPointer(spec EvaluationMetricSpecSnapshot) (string, bool) {
	emptyConfig := CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{}`))
	switch {
	case spec.Key == "retrieval.precision" && spec.ConfigSHA256 == emptyConfig:
		return "/retrieval_metrics/precision", true
	case spec.Key == "retrieval.recall" && spec.ConfigSHA256 == emptyConfig:
		return "/retrieval_metrics/recall", true
	case spec.Key == "retrieval.mrr" && spec.ConfigSHA256 == emptyConfig:
		return "/retrieval_metrics/mrr", true
	case spec.Key == "retrieval.map" && spec.ConfigSHA256 == emptyConfig:
		return "/retrieval_metrics/map", true
	case spec.Key == "retrieval.ndcg" &&
		spec.ConfigSHA256 == CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"k":3}`)):
		return "/retrieval_metrics/ndcg3", true
	case spec.Key == "retrieval.ndcg" &&
		spec.ConfigSHA256 == CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"k":10}`)):
		return "/retrieval_metrics/ndcg10", true
	case spec.Key == "generation.bleu" &&
		spec.ConfigSHA256 == CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"n":1}`)):
		return "/generation_metrics/bleu1", true
	case spec.Key == "generation.bleu" &&
		spec.ConfigSHA256 == CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"n":2}`)):
		return "/generation_metrics/bleu2", true
	case spec.Key == "generation.bleu" &&
		spec.ConfigSHA256 == CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"n":4}`)):
		return "/generation_metrics/bleu4", true
	case spec.Key == "generation.rouge" &&
		spec.ConfigSHA256 == CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"variant":"rouge-1"}`)):
		return "/generation_metrics/rouge1", true
	case spec.Key == "generation.rouge" &&
		spec.ConfigSHA256 == CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"variant":"rouge-2"}`)):
		return "/generation_metrics/rouge2", true
	case spec.Key == "generation.rouge" &&
		spec.ConfigSHA256 == CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"variant":"rouge-l"}`)):
		return "/generation_metrics/rougel", true
	default:
		return "", false
	}
}

// EvaluationMetricFixedValue reads one built-in compatibility field.
func EvaluationMetricFixedValue(result *MetricResult, pointer string) (float64, bool) {
	if result == nil {
		return 0, false
	}
	switch pointer {
	case "/retrieval_metrics/precision":
		return result.RetrievalMetrics.Precision, true
	case "/retrieval_metrics/recall":
		return result.RetrievalMetrics.Recall, true
	case "/retrieval_metrics/ndcg3":
		return result.RetrievalMetrics.NDCG3, true
	case "/retrieval_metrics/ndcg10":
		return result.RetrievalMetrics.NDCG10, true
	case "/retrieval_metrics/mrr":
		return result.RetrievalMetrics.MRR, true
	case "/retrieval_metrics/map":
		return result.RetrievalMetrics.MAP, true
	case "/generation_metrics/bleu1":
		return result.GenerationMetrics.BLEU1, true
	case "/generation_metrics/bleu2":
		return result.GenerationMetrics.BLEU2, true
	case "/generation_metrics/bleu4":
		return result.GenerationMetrics.BLEU4, true
	case "/generation_metrics/rouge1":
		return result.GenerationMetrics.ROUGE1, true
	case "/generation_metrics/rouge2":
		return result.GenerationMetrics.ROUGE2, true
	case "/generation_metrics/rougel":
		return result.GenerationMetrics.ROUGEL, true
	default:
		return 0, false
	}
}

// EvaluationMetricScoreValuePointer returns the JSON Pointer for a dynamic
// score value. Escaping keeps arbitrary plugin instance IDs addressable.
func EvaluationMetricScoreValuePointer(instanceID string) string {
	return "/scores/" + escapeEvaluationJSONPointer(instanceID) + "/value"
}
