// Package regression implements the Benchmark v1.1 quality regression gate.
//
// It is intentionally a thin adapter over the frozen unified Benchmark result
// (types.BenchmarkResult). It never re-computes or re-defines metrics: it only
// reads the twelve quality metrics the benchmark already produces, compares them
// against a baseline under an explicit threshold policy, and reports pass/fail
// with a CI-compatible exit code. It must not be extended into a benchmark or
// evaluation engine.
package regression

import "github.com/Tencent/WeKnora/internal/types"

// MetricKey is the stable comparator-facing identifier of one of the twelve
// Benchmark v1.1 quality metrics. The string values match the JSON field names
// on types.BenchmarkRetrievalQuality and types.BenchmarkAnswerQuality so a
// policy file can reference them unambiguously.
type MetricKey string

const (
	// MetricPrecision identifies retrieval precision.
	MetricPrecision MetricKey = "precision"
	// MetricRecall identifies retrieval recall.
	MetricRecall MetricKey = "recall"
	// MetricNDCG3 identifies normalized discounted cumulative gain at three.
	MetricNDCG3 MetricKey = "ndcg_3"
	// MetricNDCG10 identifies normalized discounted cumulative gain at ten.
	MetricNDCG10 MetricKey = "ndcg_10"
	// MetricMRR identifies mean reciprocal rank.
	MetricMRR MetricKey = "mrr"
	// MetricMAP identifies mean average precision.
	MetricMAP MetricKey = "map"
	// MetricBLEU1 identifies the BLEU-1 answer metric.
	MetricBLEU1 MetricKey = "bleu_1"
	// MetricBLEU2 identifies the BLEU-2 answer metric.
	MetricBLEU2 MetricKey = "bleu_2"
	// MetricBLEU4 identifies the BLEU-4 answer metric.
	MetricBLEU4 MetricKey = "bleu_4"
	// MetricROUGE1 identifies the ROUGE-1 answer metric.
	MetricROUGE1 MetricKey = "rouge_1"
	// MetricROUGE2 identifies the ROUGE-2 answer metric.
	MetricROUGE2 MetricKey = "rouge_2"
	// MetricROUGEL identifies the ROUGE-L answer metric.
	MetricROUGEL MetricKey = "rouge_l"
)

// MetricSpec is the comparator-facing contract of one quality metric. All
// twelve Benchmark v1.1 quality metrics are higher-is-better; the direction is
// stated explicitly so a regression is never inferred from the sign of a delta
// alone.
type MetricSpec struct {
	Key            MetricKey
	DisplayName    string
	HigherIsBetter bool

	// get extracts the metric from a unified result, returning nil when the
	// metric is absent. Missing is distinct from zero and must fail closed.
	get func(*types.BenchmarkResult) *float64
}

// Metrics returns the twelve Benchmark v1.1 quality metrics in a stable,
// deterministic order (retrieval first, then answer quality).
func Metrics() []MetricSpec {
	return []MetricSpec{
		{
			Key: MetricPrecision, DisplayName: "Precision", HigherIsBetter: true,
			get: ret(func(q *types.BenchmarkRetrievalQuality) *float64 { return q.Precision }),
		},
		{
			Key: MetricRecall, DisplayName: "Recall", HigherIsBetter: true,
			get: ret(func(q *types.BenchmarkRetrievalQuality) *float64 { return q.Recall }),
		},
		{
			Key: MetricNDCG3, DisplayName: "NDCG@3", HigherIsBetter: true,
			get: ret(func(q *types.BenchmarkRetrievalQuality) *float64 { return q.NDCG3 }),
		},
		{
			Key: MetricNDCG10, DisplayName: "NDCG@10", HigherIsBetter: true,
			get: ret(func(q *types.BenchmarkRetrievalQuality) *float64 { return q.NDCG10 }),
		},
		{
			Key: MetricMRR, DisplayName: "MRR", HigherIsBetter: true,
			get: ret(func(q *types.BenchmarkRetrievalQuality) *float64 { return q.MRR }),
		},
		{
			Key: MetricMAP, DisplayName: "MAP", HigherIsBetter: true,
			get: ret(func(q *types.BenchmarkRetrievalQuality) *float64 { return q.MAP }),
		},
		{
			Key: MetricBLEU1, DisplayName: "BLEU-1", HigherIsBetter: true,
			get: ans(func(q *types.BenchmarkAnswerQuality) *float64 { return q.BLEU1 }),
		},
		{
			Key: MetricBLEU2, DisplayName: "BLEU-2", HigherIsBetter: true,
			get: ans(func(q *types.BenchmarkAnswerQuality) *float64 { return q.BLEU2 }),
		},
		{
			Key: MetricBLEU4, DisplayName: "BLEU-4", HigherIsBetter: true,
			get: ans(func(q *types.BenchmarkAnswerQuality) *float64 { return q.BLEU4 }),
		},
		{
			Key: MetricROUGE1, DisplayName: "ROUGE-1", HigherIsBetter: true,
			get: ans(func(q *types.BenchmarkAnswerQuality) *float64 { return q.ROUGE1 }),
		},
		{
			Key: MetricROUGE2, DisplayName: "ROUGE-2", HigherIsBetter: true,
			get: ans(func(q *types.BenchmarkAnswerQuality) *float64 { return q.ROUGE2 }),
		},
		{
			Key: MetricROUGEL, DisplayName: "ROUGE-L", HigherIsBetter: true,
			get: ans(func(q *types.BenchmarkAnswerQuality) *float64 { return q.ROUGEL }),
		},
	}
}

func knownMetric(key MetricKey) bool {
	for _, spec := range Metrics() {
		if spec.Key == key {
			return true
		}
	}
	return false
}

func ret(f func(*types.BenchmarkRetrievalQuality) *float64) func(*types.BenchmarkResult) *float64 {
	return func(r *types.BenchmarkResult) *float64 {
		if r == nil || r.Quality.Retrieval == nil {
			return nil
		}
		return f(r.Quality.Retrieval)
	}
}

func ans(f func(*types.BenchmarkAnswerQuality) *float64) func(*types.BenchmarkResult) *float64 {
	return func(r *types.BenchmarkResult) *float64 {
		if r == nil || r.Quality.Answer == nil {
			return nil
		}
		return f(r.Quality.Answer)
	}
}

// metricValue is one extracted metric. A nil value means the metric is missing
// from the source result; it must be reported and treated as a failure, never
// skipped.
type metricValue struct {
	spec  MetricSpec
	value *float64
}

func extract(result *types.BenchmarkResult) []metricValue {
	specs := Metrics()
	out := make([]metricValue, 0, len(specs))
	for _, spec := range specs {
		var v *float64
		if result != nil {
			v = spec.get(result)
		}
		out = append(out, metricValue{spec: spec, value: v})
	}
	return out
}
