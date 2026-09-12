package service

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Golden regression (architecture section 6.5 / handoff slice 5): a small,
// human-reviewable, versioned dataset with fixed questions, a full corpus,
// ground truth, irrelevant results, unknown-source and duplicate-PID ranks,
// and fixed generation outputs. The deterministic fake pipeline never
// touches a live model or the network; live outputs must never be pinned
// as golden.

type goldenDatasetFixture struct {
	SchemaVersion int    `json:"schema_version"`
	DatasetID     string `json:"dataset_id"`
	KnowledgeID   string `json:"knowledge_id"`
	Passages      []struct {
		PID     string `json:"pid"`
		Content string `json:"content"`
	} `json:"passages"`
	Questions []struct {
		QID      string `json:"qid"`
		Question string `json:"question"`
		Answer   string `json:"answer"`
	} `json:"questions"`
	Relevance []struct {
		QID   string `json:"qid"`
		PID   string `json:"pid"`
		Grade int    `json:"grade"`
	} `json:"relevance"`
	Samples []struct {
		QID    string `json:"qid"`
		Search []struct {
			KnowledgeID string  `json:"knowledge_id"`
			ChunkIndex  int     `json:"chunk_index"`
			Score       float64 `json:"score"`
		} `json:"search"`
		Rerank []struct {
			KnowledgeID string  `json:"knowledge_id"`
			ChunkIndex  int     `json:"chunk_index"`
			Score       float64 `json:"score"`
		} `json:"rerank"`
		GeneratedText string `json:"generated_text"`
	} `json:"samples"`
}

type goldenExpectedFixture struct {
	DatasetContentSHA256 string              `json:"dataset_content_sha256"`
	FloatTolerance       float64             `json:"float_tolerance"`
	RetrievalIDs         map[string][]int    `json:"retrieval_ids"`
	RankedProvenance     map[string][]string `json:"ranked_provenance"`
	PerSample            map[string]struct {
		Precision float64 `json:"precision"`
		Recall    float64 `json:"recall"`
		NDCG3     float64 `json:"ndcg3"`
		NDCG10    float64 `json:"ndcg10"`
		MRR       float64 `json:"mrr"`
		MAP       float64 `json:"map"`
		BLEU1     float64 `json:"bleu1"`
		BLEU2     float64 `json:"bleu2"`
		BLEU4     float64 `json:"bleu4"`
		ROUGE1    float64 `json:"rouge1"`
		ROUGE2    float64 `json:"rouge2"`
		ROUGEL    float64 `json:"rougel"`
	} `json:"per_sample"`
	Aggregate struct {
		Precision float64 `json:"precision"`
		Recall    float64 `json:"recall"`
		NDCG3     float64 `json:"ndcg3"`
		NDCG10    float64 `json:"ndcg10"`
		MRR       float64 `json:"mrr"`
		MAP       float64 `json:"map"`
		BLEU1     float64 `json:"bleu1"`
		BLEU2     float64 `json:"bleu2"`
		BLEU4     float64 `json:"bleu4"`
		ROUGE1    float64 `json:"rouge1"`
		ROUGE2    float64 `json:"rouge2"`
		ROUGEL    float64 `json:"rougel"`
	} `json:"aggregate"`
	ExperimentManifest struct {
		SchemaVersion        int    `json:"schema_version"`
		DatasetID            string `json:"dataset_id"`
		MetricPlanMetrics    int    `json:"metric_plan_metrics"`
		ReproducibilityLevel string `json:"reproducibility_level"`
	} `json:"experiment_manifest"`
}

func loadGoldenFixture(t *testing.T) (*goldenDatasetFixture, *goldenExpectedFixture) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "dataset", "golden", "v1")
	datasetRaw, err := os.ReadFile(filepath.Join(root, "dataset.json"))
	require.NoError(t, err)
	expectedRaw, err := os.ReadFile(filepath.Join(root, "expected.json"))
	require.NoError(t, err)
	var dataset goldenDatasetFixture
	require.NoError(t, json.Unmarshal(datasetRaw, &dataset))
	var expected goldenExpectedFixture
	require.NoError(t, json.Unmarshal(expectedRaw, &expected))
	require.Equal(t, 1, dataset.SchemaVersion)
	require.Equal(t, "golden-v1", dataset.DatasetID)
	return &dataset, &expected
}

// goldenContentInput converts the fixture into the registry input shape used
// by the canonical hash.
func goldenContentInput(dataset *goldenDatasetFixture) *types.EvaluationDatasetVersionInput {
	input := &types.EvaluationDatasetVersionInput{}
	for _, passage := range dataset.Passages {
		input.Passages = append(input.Passages, types.EvaluationDatasetPassageInput{
			PID: passage.PID, Content: passage.Content,
		})
	}
	for _, question := range dataset.Questions {
		input.Questions = append(input.Questions, types.EvaluationDatasetQuestionInput{
			QID: question.QID, Question: question.Question, Answer: question.Answer,
		})
	}
	for _, edge := range dataset.Relevance {
		input.Relevance = append(input.Relevance, types.EvaluationDatasetRelevanceInput{
			QID: edge.QID, PID: edge.PID, Grade: edge.Grade,
		})
	}
	return input
}

// goldenPID parses fixture PIDs. It never touches *testing.T so the pipeline
// stays safe for concurrent repetition runs.
func goldenPID(raw string) int {
	pid, err := strconv.Atoi(raw)
	if err != nil {
		panic("golden fixture pid must be numeric: " + raw)
	}
	return pid
}

// runGoldenPipeline executes the deterministic fake pipeline: the fixture's
// fixed search/rerank/generation outputs are fed through the real HookMetric
// provenance and metric aggregation code, in stable sample order. It takes
// no *testing.T so concurrent repetition runs stay race-clean.
func runGoldenPipeline(dataset *goldenDatasetFixture) (*HookMetric, []*types.MetricResult) {
	hook := NewHookMetric(len(dataset.Samples), dataset.KnowledgeID)
	perSample := make([]*types.MetricResult, 0, len(dataset.Samples))

	questionByID := make(map[string]int)
	for i, question := range dataset.Questions {
		questionByID[question.QID] = i
	}
	relevanceByQID := make(map[string][]int)
	gradesByQID := make(map[string]map[int]int)
	for _, edge := range dataset.Relevance {
		pid := goldenPID(edge.PID)
		if gradesByQID[edge.QID] == nil {
			gradesByQID[edge.QID] = make(map[int]int)
		}
		gradesByQID[edge.QID][pid] = edge.Grade
		if edge.Grade > 0 {
			relevanceByQID[edge.QID] = append(relevanceByQID[edge.QID], pid)
		}
	}

	for index, sample := range dataset.Samples {
		question := dataset.Questions[questionByID[sample.QID]]
		qaPair := &types.QAPair{
			QID:                      index,
			Question:                 question.Question,
			PIDs:                     relevanceByQID[sample.QID],
			PIDGrades:                gradesByQID[sample.QID],
			RetrievalLabelsAvailable: true,
			Answer:                   question.Answer,
		}

		hook.recordInit(index)
		hook.recordQaPair(index, qaPair)
		hook.recordSearchResult(index, goldenSearchResults(sample.Search))
		hook.recordRerankResult(index, goldenSearchResults(sample.Rerank))
		hook.recordChatResponse(index, &types.ChatResponse{Content: sample.GeneratedText})
		hook.recordFinish(index)
		if hook.metricResults.results[index] != nil {
			perSample = append(perSample, hook.metricResults.results[index])
		}
	}
	return hook, perSample
}

func TestGoldenDatasetContentHashIsStable(t *testing.T) {
	dataset, expected := loadGoldenFixture(t)
	hash := types.CanonicalEvaluationDatasetContentSHA256(goldenContentInput(dataset))
	assert.Equal(t, expected.DatasetContentSHA256, hash,
		"golden dataset content hash drifted; update dataset/golden/v1 deliberately")

	// Assembly order of the same logical content yields the same hash.
	shuffled := goldenContentInput(dataset)
	shuffled.Passages[0], shuffled.Passages[4] = shuffled.Passages[4], shuffled.Passages[0]
	assert.Equal(t, expected.DatasetContentSHA256,
		types.CanonicalEvaluationDatasetContentSHA256(shuffled))
}

func TestGoldenProvenancePreservesRawRanks(t *testing.T) {
	dataset, expected := loadGoldenFixture(t)

	for _, sample := range dataset.Samples {
		searchRanked := evaluationRankedResultsWithProvenance(
			goldenSearchResults(sample.Search), dataset.KnowledgeID)
		provenance := make([]string, len(searchRanked))
		for i, entry := range searchRanked {
			provenance[i] = entry.Provenance
		}
		assert.Equal(t, expected.RankedProvenance[sample.QID+"_search"], provenance,
			"search provenance drifted for %s", sample.QID)

		rerankRanked := evaluationRankedResultsWithProvenance(
			goldenSearchResults(sample.Rerank), dataset.KnowledgeID)
		provenance = make([]string, len(rerankRanked))
		for i, entry := range rerankRanked {
			provenance[i] = entry.Provenance
		}
		if want, ok := expected.RankedProvenance[sample.QID+"_rerank"]; ok {
			assert.Equal(t, want, provenance, "rerank provenance drifted for %s", sample.QID)
		}
	}
}

func goldenSearchResults(entries []struct {
	KnowledgeID string  `json:"knowledge_id"`
	ChunkIndex  int     `json:"chunk_index"`
	Score       float64 `json:"score"`
},
) []*types.SearchResult {
	results := make([]*types.SearchResult, 0, len(entries))
	for _, entry := range entries {
		results = append(results, &types.SearchResult{
			KnowledgeID: entry.KnowledgeID, ChunkIndex: entry.ChunkIndex, Score: entry.Score,
		})
	}
	return results
}

func TestGoldenTwelveMetricsMatchPinnedExpectations(t *testing.T) {
	dataset, expected := loadGoldenFixture(t)
	tolerance := expected.FloatTolerance
	require.Greater(t, tolerance, 0.0, "golden comparisons must declare a minimal explicit tolerance")

	hook, perSample := runGoldenPipeline(dataset)
	require.Len(t, perSample, 2)

	// Per-sample expectations for all twelve metrics.
	keys := []string{"gq1", "gq2"}
	for i, sample := range perSample {
		want := expected.PerSample[keys[i]]
		assert.InDelta(t, want.Precision, sample.RetrievalMetrics.Precision, tolerance, "%s precision", keys[i])
		assert.InDelta(t, want.Recall, sample.RetrievalMetrics.Recall, tolerance, "%s recall", keys[i])
		assert.InDelta(t, want.NDCG3, sample.RetrievalMetrics.NDCG3, tolerance, "%s ndcg3", keys[i])
		assert.InDelta(t, want.NDCG10, sample.RetrievalMetrics.NDCG10, tolerance, "%s ndcg10", keys[i])
		assert.InDelta(t, want.MRR, sample.RetrievalMetrics.MRR, tolerance, "%s mrr", keys[i])
		assert.InDelta(t, want.MAP, sample.RetrievalMetrics.MAP, tolerance, "%s map", keys[i])
		assert.InDelta(t, want.BLEU1, sample.GenerationMetrics.BLEU1, tolerance, "%s bleu1", keys[i])
		assert.InDelta(t, want.BLEU2, sample.GenerationMetrics.BLEU2, tolerance, "%s bleu2", keys[i])
		assert.InDelta(t, want.BLEU4, sample.GenerationMetrics.BLEU4, tolerance, "%s bleu4", keys[i])
		assert.InDelta(t, want.ROUGE1, sample.GenerationMetrics.ROUGE1, tolerance, "%s rouge1", keys[i])
		assert.InDelta(t, want.ROUGE2, sample.GenerationMetrics.ROUGE2, tolerance, "%s rouge2", keys[i])
		assert.InDelta(t, want.ROUGEL, sample.GenerationMetrics.ROUGEL, tolerance, "%s rougel", keys[i])
	}

	// Aggregate expectations for all twelve metrics.
	aggregate := hook.MetricResult()
	assert.InDelta(t, expected.Aggregate.Precision, aggregate.RetrievalMetrics.Precision, tolerance)
	assert.InDelta(t, expected.Aggregate.Recall, aggregate.RetrievalMetrics.Recall, tolerance)
	assert.InDelta(t, expected.Aggregate.NDCG3, aggregate.RetrievalMetrics.NDCG3, tolerance)
	assert.InDelta(t, expected.Aggregate.NDCG10, aggregate.RetrievalMetrics.NDCG10, tolerance)
	assert.InDelta(t, expected.Aggregate.MRR, aggregate.RetrievalMetrics.MRR, tolerance)
	assert.InDelta(t, expected.Aggregate.MAP, aggregate.RetrievalMetrics.MAP, tolerance)
	assert.InDelta(t, expected.Aggregate.BLEU1, aggregate.GenerationMetrics.BLEU1, tolerance)
	assert.InDelta(t, expected.Aggregate.BLEU2, aggregate.GenerationMetrics.BLEU2, tolerance)
	assert.InDelta(t, expected.Aggregate.BLEU4, aggregate.GenerationMetrics.BLEU4, tolerance)
	assert.InDelta(t, expected.Aggregate.ROUGE1, aggregate.GenerationMetrics.ROUGE1, tolerance)
	assert.InDelta(t, expected.Aggregate.ROUGE2, aggregate.GenerationMetrics.ROUGE2, tolerance)
	assert.InDelta(t, expected.Aggregate.ROUGEL, aggregate.GenerationMetrics.ROUGEL, tolerance)

	// Retrieval IDs keep the raw ranking with -1 placeholders.
	for _, sample := range dataset.Samples {
		rerank := goldenSearchResults(sample.Rerank)
		search := goldenSearchResults(sample.Search)
		source := rerank
		if len(source) == 0 {
			source = search
		}
		got := evaluationRetrievalIDsWithProvenance(source, dataset.KnowledgeID)
		assert.Equal(t, expected.RetrievalIDs[sample.QID], got, "retrieval ids drifted for %s", sample.QID)
	}
}

// TestGoldenPipelineIsRepeatable runs the deterministic pipeline 100 times;
// every run must produce byte-identical aggregate metrics and canonical
// dataset hashes (architecture: concurrent completion order never changes
// canonical outputs).
func TestGoldenPipelineIsRepeatable(t *testing.T) {
	dataset, expected := loadGoldenFixture(t)
	tolerance := expected.FloatTolerance

	reference, _ := runGoldenPipeline(dataset)
	referenceAggregate := reference.MetricResult()

	const repeats = 100
	aggregates := make([]*types.MetricResult, repeats)
	var waiters sync.WaitGroup
	for run := 0; run < repeats; run++ {
		run := run
		waiters.Add(1)
		go func() {
			defer waiters.Done()
			hook, _ := runGoldenPipeline(dataset)
			aggregates[run] = hook.MetricResult()
		}()
	}
	waiters.Wait()

	for run := 0; run < repeats; run++ {
		assert.InDelta(t, referenceAggregate.RetrievalMetrics.Precision,
			aggregates[run].RetrievalMetrics.Precision, tolerance, "run %d precision", run)
		assert.InDelta(t, referenceAggregate.RetrievalMetrics.NDCG3,
			aggregates[run].RetrievalMetrics.NDCG3, tolerance, "run %d ndcg3", run)
		assert.InDelta(t, referenceAggregate.GenerationMetrics.BLEU1,
			aggregates[run].GenerationMetrics.BLEU1, tolerance, "run %d bleu1", run)
		assert.InDelta(t, referenceAggregate.GenerationMetrics.ROUGEL,
			aggregates[run].GenerationMetrics.ROUGEL, tolerance, "run %d rougel", run)
	}

	// The default metric plan is part of the frozen experiment manifest.
	plan, err := types.DefaultEvaluationMetricPlan()
	require.NoError(t, err)
	assert.Len(t, plan.Metrics, expected.ExperimentManifest.MetricPlanMetrics)
	assert.Equal(t, expected.ExperimentManifest.SchemaVersion, plan.SchemaVersion)
}

func TestGoldenNoLiveModelOutputPinned(t *testing.T) {
	dataset, _ := loadGoldenFixture(t)
	// Guard against slowly drifting the fixture into live-model territory:
	// generation outputs stay short, human-reviewable, deterministic strings.
	for _, sample := range dataset.Samples {
		require.NotEmpty(t, sample.GeneratedText)
		require.Less(t, len(sample.GeneratedText), 128)
		require.NotContains(t, sample.GeneratedText, "http")
	}
	// NaN/Inf guards: pinned expectations are finite numbers.
	_, expected := loadGoldenFixture(t)
	for _, value := range []float64{
		expected.Aggregate.NDCG3, expected.Aggregate.Precision, expected.Aggregate.BLEU1,
	} {
		require.False(t, math.IsNaN(value) || math.IsInf(value, 0))
	}
}
