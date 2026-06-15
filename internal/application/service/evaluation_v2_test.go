package service

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestComputeEvaluationScoresMissingContexts(t *testing.T) {
	scores := computeEvaluationScores(
		[]types.EvaluationMetricSelection{{Name: "precision", Version: "v1"}, {Name: "bleu1", Version: "v1"}},
		"参考答案", nil, "生成答案", nil,
	)
	if scores["precision"].Status != types.EvaluationMetricNotApplicable {
		t.Fatalf("precision status = %s", scores["precision"].Status)
	}
	if scores["precision"].Score != nil || scores["precision"].Reason == "" {
		t.Fatal("not_applicable metric must have nil score and a reason")
	}
	if scores["bleu1"].Status != types.EvaluationMetricScored || scores["bleu1"].Score == nil {
		t.Fatal("generation metric should still be scored")
	}
}

func TestRetrievalMetricInputChunkIDThenTextFallback(t *testing.T) {
	input := retrievalMetricInput(
		[]types.EvaluationReferenceContext{{Text: "A", ChunkID: "chunk-1"}, {Text: " 文本 B "}},
		[]types.EvaluationRetrievedContext{{Text: "different", ChunkID: "chunk-1"}, {Text: "文本\r\n\tB", ChunkID: "retrieved-id"}},
	)
	if len(input.RetrievalIDs) != 2 || input.RetrievalIDs[0] != 1 || input.RetrievalIDs[1] != 2 {
		t.Fatalf("unexpected retrieval IDs: %#v", input.RetrievalIDs)
	}
}

func TestNormalizeEvaluationTextPreservesCaseAndWordBoundaries(t *testing.T) {
	if normalizeEvaluationText(" New\r\n\tYork ") != "New York" {
		t.Fatal("normalization must trim and collapse whitespace")
	}
	if normalizeEvaluationText("New York") == normalizeEvaluationText("newyork") {
		t.Fatal("normalization must preserve case and word boundaries")
	}
}

func TestAggregateEvaluationMetricScoresOnlyScored(t *testing.T) {
	score := 0.8
	failedScores, _ := json.Marshal(types.EvaluationMetricScores{"precision": {Name: "precision", Version: "v1", Status: types.EvaluationMetricFailed}})
	scoredScores, _ := json.Marshal(types.EvaluationMetricScores{"precision": {Name: "precision", Version: "v1", Category: "retrieval", Status: types.EvaluationMetricScored, HigherIsBetter: true, Score: &score}})
	raw, err := aggregateEvaluationMetricScores([]*types.EvaluationRunResult{{MetricScores: scoredScores}, {MetricScores: failedScores}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	var aggregate types.EvaluationMetricScores
	if err := json.Unmarshal(raw, &aggregate); err != nil {
		t.Fatal(err)
	}
	result := aggregate["precision"]
	if result.Score == nil || *result.Score != score {
		t.Fatalf("score = %#v", result.Score)
	}
	if result.ScoredSampleCount != 1 || result.TotalSampleCount != 2 {
		t.Fatalf("counts = %d/%d", result.ScoredSampleCount, result.TotalSampleCount)
	}
}

func TestAggregateEvaluationMetricScoresKeepsUnscoredMetric(t *testing.T) {
	scores, _ := json.Marshal(types.EvaluationMetricScores{"precision": {Name: "precision", Version: "v1", Category: "retrieval", Status: types.EvaluationMetricNotApplicable, HigherIsBetter: true, Reason: "reference_contexts missing"}})
	raw, err := aggregateEvaluationMetricScores([]*types.EvaluationRunResult{{MetricScores: scores}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	var aggregate types.EvaluationMetricScores
	if err := json.Unmarshal(raw, &aggregate); err != nil {
		t.Fatal(err)
	}
	result, ok := aggregate["precision"]
	if !ok || result.Status != types.EvaluationMetricNotApplicable || result.Score != nil {
		t.Fatalf("unexpected aggregate result: %#v", aggregate)
	}
	if result.ScoredSampleCount != 0 || result.TotalSampleCount != 1 {
		t.Fatalf("counts = %d/%d", result.ScoredSampleCount, result.TotalSampleCount)
	}
}

func TestMetricScoreJSONContract(t *testing.T) {
	score := 0.8
	scores := types.EvaluationMetricScores{"precision": {Name: "precision", Version: "v1", Category: "retrieval", Score: &score, Status: types.EvaluationMetricScored, HigherIsBetter: true, Reason: "", Error: ""}}
	raw, err := json.Marshal(scores)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]types.EvaluationMetricScore
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["precision"].Name != "precision" || decoded["precision"].Status != types.EvaluationMetricScored {
		t.Fatalf("unexpected JSON: %s", raw)
	}
}

func TestCompareEvaluationResultSetsRejectsDifferentMetricVersions(t *testing.T) {
	baselineScore, candidateScore := 0.5, 0.7
	baselineMetrics, _ := json.Marshal(types.EvaluationMetricScores{"precision": {Name: "precision", Version: "v1", Score: &baselineScore, Status: types.EvaluationMetricScored}})
	candidateMetrics, _ := json.Marshal(types.EvaluationMetricScores{"precision": {Name: "precision", Version: "v2", Score: &candidateScore, Status: types.EvaluationMetricScored}})
	comparison := compareEvaluationResultSets("dataset", "baseline", "candidate", []*types.EvaluationRunResult{{SampleID: "sample", MetricScores: baselineMetrics}}, []*types.EvaluationRunResult{{SampleID: "sample", MetricScores: candidateMetrics}})
	if len(comparison.Metrics) != 0 {
		t.Fatalf("different metric versions must not be compared: %#v", comparison.Metrics)
	}
}
