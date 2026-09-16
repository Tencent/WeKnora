package performance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/evaluation/metricregistry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBuildCoversMatrixAndCacheProof(t *testing.T) {
	report, err := Build(context.Background(), Options{
		DatasetPath: "../../../dataset/golden/v1/dataset.json",
		Commit:      "commit-a", OutputRoot: t.TempDir(),
		Sizes: []int{3}, Concurrencies: []int{1, 2}, Repetitions: 1, Seed: 42,
	})
	require.NoError(t, err)
	require.Equal(t, ReportSchemaVersion, report.SchemaVersion)
	require.Len(t, report.Results, 4)
	for _, result := range report.Results {
		require.Zero(t, result.FailureRate)
		require.EqualValues(t, 3, result.PersistedQuestions)
		require.EqualValues(t, 3, result.ListedQuestions)
		require.Equal(t, 12, result.ComparisonMetricCount)
	}
	require.Equal(t, "verified", report.CacheValidation.Embedding.Status)
	require.Zero(t, report.CacheValidation.Embedding.SecondProviderInputItems)
	require.Equal(t, "not_measured", report.CacheValidation.Wiki.Status)
}

func TestJSONSchemaAndMarkdownDerivation(t *testing.T) {
	report := &Report{
		SchemaVersion: 1, Commit: "abc", Dataset: DatasetIdentity{ID: "golden-v1", Version: 1},
		Runtime:  Runtime{GoVersion: "go1.26", GOOS: "linux", GOARCH: "amd64"},
		Database: Database{Version: "3"}, Configuration: Configuration{Repetitions: 5},
	}
	encoded, err := JSON(report)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, float64(1), decoded["schema_version"])
	require.Contains(t, string(Markdown(report)), "commit `abc`")
}

func BenchmarkMetricCalculation(b *testing.B) {
	plan := benchmarkMetricPlan(b)
	input := &types.MetricInput{
		RetrievalGT:              [][]int{{1}},
		RetrievalGrades:          map[int]int{1: 1},
		RetrievalLabelsAvailable: true,
		RetrievalIDs:             []int{1, 2, 3},
		GeneratedTexts:           "answer", GeneratedGT: "answer",
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_, _, err := plan.Compute(context.Background(), input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMetricAggregation(b *testing.B) {
	plan := benchmarkMetricPlan(b)
	rows := make([]*types.MetricResult, 100)
	for index := range rows {
		rows[index], _, _ = plan.Compute(context.Background(), &types.MetricInput{
			RetrievalGT:              [][]int{{1}},
			RetrievalGrades:          map[int]int{1: 1},
			RetrievalLabelsAvailable: true,
			RetrievalIDs:             []int{1, 2, 3},
			GeneratedTexts:           "answer", GeneratedGT: "answer",
		})
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = plan.Aggregate(rows)
	}
}

func BenchmarkQuestionPersistence(b *testing.B) {
	db, err := openDatabase(filepath.Join(b.TempDir(), "persistence.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		row := questionRow(index, "question", "answer", &types.MetricResult{}, 1, 2, 1)
		if err := db.Create(row).Error; err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQuestionList(b *testing.B) {
	db, err := openDatabase(filepath.Join(b.TempDir(), "list.db"))
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 500; index++ {
		row := questionRow(index, "question", "answer", &types.MetricResult{}, 1, 2, 1)
		if err := db.Create(row).Error; err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		var rows []*types.EvaluationQuestionResultEntity
		if err := db.Order("sample_index ASC").Limit(500).Find(&rows).Error; err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkComparison(b *testing.B) {
	plan := benchmarkMetricPlan(b)
	row, _, _ := plan.Compute(context.Background(), &types.MetricInput{
		RetrievalGT:              [][]int{{1}},
		RetrievalGrades:          map[int]int{1: 1},
		RetrievalLabelsAvailable: true,
		RetrievalIDs:             []int{1, 2, 3},
		GeneratedTexts:           "answer", GeneratedGT: "answer",
	})
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = compareMetricRows(row, row)
	}
}

func BenchmarkExport(b *testing.B) {
	rows := make([]*types.EvaluationQuestionResultEntity, 500)
	for index := range rows {
		rows[index] = questionRow(index, "question", "answer", &types.MetricResult{}, 1, 2, 1)
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := json.Marshal(rows); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkMetricPlan(b *testing.B) *metricregistry.ResolvedPlan {
	b.Helper()
	registry, err := metricregistry.NewDefaultRegistry()
	if err != nil {
		b.Fatal(err)
	}
	plan, err := registry.Resolve(metricregistry.DefaultSpecs())
	if err != nil {
		b.Fatal(err)
	}
	return plan
}
