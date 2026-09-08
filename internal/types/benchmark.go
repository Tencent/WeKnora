package types

import "time"

// BenchmarkQualityState describes the availability of benchmark quality metrics.
type BenchmarkQualityState string

const (
	// BenchmarkQualityStatePending indicates metrics are not ready yet.
	BenchmarkQualityStatePending BenchmarkQualityState = "pending"
	// BenchmarkQualityStateComplete indicates all required metrics are available.
	BenchmarkQualityStateComplete BenchmarkQualityState = "complete"
	// BenchmarkQualityStateUnavailable indicates metrics cannot be produced.
	BenchmarkQualityStateUnavailable BenchmarkQualityState = "unavailable"
)

// BenchmarkReproducibilityState describes whether reproducibility facts are complete.
type BenchmarkReproducibilityState string

const (
	// BenchmarkReproducibilityComplete indicates all reproducibility facts are present.
	BenchmarkReproducibilityComplete BenchmarkReproducibilityState = "complete"
	// BenchmarkReproducibilityLegacyUnknown identifies legacy results without complete facts.
	BenchmarkReproducibilityLegacyUnknown BenchmarkReproducibilityState = "legacy_unknown"
)

// BenchmarkRunSummary describes the persisted state of one benchmark run.
type BenchmarkRunSummary struct {
	EvaluationRunID string           `json:"evaluation_run_id"`
	TaskID          string           `json:"task_id"`
	Status          EvaluationStatue `json:"status"`
	Total           int              `json:"total"`
	Finished        int              `json:"finished"`
	CreatedAt       time.Time        `json:"created_at"`
	StartedAt       *time.Time       `json:"started_at"`
	FinishedAt      *time.Time       `json:"finished_at"`
	ErrorMessage    string           `json:"error_message,omitempty"`
}

// BenchmarkRetrievalQuality contains retrieval quality metrics.
type BenchmarkRetrievalQuality struct {
	Precision *float64 `json:"precision"`
	Recall    *float64 `json:"recall"`
	NDCG3     *float64 `json:"ndcg_3"`
	NDCG10    *float64 `json:"ndcg_10"`
	MRR       *float64 `json:"mrr"`
	MAP       *float64 `json:"map"`
}

// BenchmarkAnswerQuality contains generated-answer quality metrics.
type BenchmarkAnswerQuality struct {
	BLEU1  *float64 `json:"bleu_1"`
	BLEU2  *float64 `json:"bleu_2"`
	BLEU4  *float64 `json:"bleu_4"`
	ROUGE1 *float64 `json:"rouge_1"`
	ROUGE2 *float64 `json:"rouge_2"`
	ROUGEL *float64 `json:"rouge_l"`
}

// BenchmarkQuality groups the availability state and quality metric families.
type BenchmarkQuality struct {
	State     BenchmarkQualityState      `json:"state"`
	Retrieval *BenchmarkRetrievalQuality `json:"retrieval,omitempty"`
	Answer    *BenchmarkAnswerQuality    `json:"answer,omitempty"`
}

// BenchmarkResult composes persisted evaluation facts with dynamically
// aggregated model facts. Comparability is intentionally absent: it is a
// relationship between two results, not a property of one run.
type BenchmarkResult struct {
	BenchmarkVersion       string                         `json:"benchmark_version"`
	Run                    BenchmarkRunSummary            `json:"run"`
	Config                 EvaluationConfigSnapshotV1     `json:"config"`
	Quality                BenchmarkQuality               `json:"quality"`
	RunWallClockDurationMS *int64                         `json:"run_wall_clock_duration_ms"`
	ModelFacts             *EvaluationModelUsageAggregate `json:"model_facts"`
	Reproducibility        BenchmarkReproducibilityState  `json:"reproducibility"`
}
