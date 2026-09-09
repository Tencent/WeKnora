package types

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/yanyiwu/gojieba"
)

// Jieba is a global instance of Chinese text segmentation tool
var Jieba *gojieba.Jieba = newJieba()

func newJieba() *gojieba.Jieba {
	dictDir := os.Getenv("JIEBA_DICT_DIR")
	if dictDir == "" {
		return gojieba.NewJieba()
	}

	return gojieba.NewJieba(
		filepath.Join(dictDir, "jieba.dict.utf8"),
		filepath.Join(dictDir, "hmm_model.utf8"),
		filepath.Join(dictDir, "user.dict.utf8"),
		filepath.Join(dictDir, "idf.utf8"),
		filepath.Join(dictDir, "stop_words.utf8"),
	)
}

// EvaluationStatue represents the status of an evaluation task
type EvaluationStatue int

const (
	EvaluationStatuePending EvaluationStatue = iota // Task is waiting to start
	EvaluationStatueRunning                         // Task is in progress
	EvaluationStatueSuccess                         // Task completed successfully
	EvaluationStatueFailed                          // Task failed
)

// EvaluationTask contains information about an evaluation task
type EvaluationTask struct {
	ID        string `json:"id"`         // Unique task ID
	TenantID  uint64 `json:"tenant_id"`  // Tenant/Organization ID
	DatasetID string `json:"dataset_id"` // Dataset ID for evaluation

	StartTime  time.Time        `json:"start_time"`         // Task start time
	EndTime    *time.Time       `json:"end_time,omitempty"` // Task completion time
	DurationMS int64            `json:"duration_ms"`        // End-to-end task latency in milliseconds
	Status     EvaluationStatue `json:"status"`             // Current task status
	ErrMsg     string           `json:"err_msg,omitempty"`  // Error message if failed

	Total    int `json:"total,omitempty"`    // Total items to evaluate
	Finished int `json:"finished,omitempty"` // Completed items count
}

// EvaluationDetail contains detailed evaluation information
type EvaluationDetail struct {
	Task       *EvaluationTask       `json:"task"`                  // Evaluation task info
	Params     *ChatManage           `json:"params"`                // Evaluation parameters
	RunConfig  *EvaluationRunConfig  `json:"run_config,omitempty"`  // Reproducible experiment snapshot
	Metric     *MetricResult         `json:"metric,omitempty"`      // Evaluation metrics
	Usage      *EvaluationUsage      `json:"usage,omitempty"`       // Aggregated model usage
	ModelCalls []EvaluationModelCall `json:"model_calls,omitempty"` // Structured model calls
}

// EvaluationRunConfig is an immutable, secret-free snapshot of everything
// needed to explain and reproduce an evaluation run. IDs alone are not enough:
// datasets and model rows can change after a run, so their content/config
// fingerprints are retained alongside the effective chunking and RAG options.
type EvaluationRunConfig struct {
	SchemaVersion             int                       `json:"schema_version"`
	DatasetID                 string                    `json:"dataset_id"`
	DatasetFingerprint        string                    `json:"dataset_fingerprint"`
	DatasetSamples            int                       `json:"dataset_samples"`
	SourceKnowledgeBaseID     string                    `json:"source_knowledge_base_id,omitempty"`
	EvaluationKnowledgeBaseID string                    `json:"evaluation_knowledge_base_id"`
	Chunking                  ChunkingConfig            `json:"chunking"`
	Pipeline                  PipelineRequest           `json:"pipeline"`
	Models                    []EvaluationModelSnapshot `json:"models"`
	CodeVersion               string                    `json:"code_version"`
}

// EvaluationModelSnapshot identifies the exact non-secret model configuration
// used by a run. ConfigFingerprint excludes credentials and custom headers.
type EvaluationModelSnapshot struct {
	Role              string      `json:"role"`
	ID                string      `json:"id"`
	Name              string      `json:"name"`
	DisplayName       string      `json:"display_name,omitempty"`
	Type              ModelType   `json:"type"`
	Source            ModelSource `json:"source"`
	Provider          string      `json:"provider,omitempty"`
	Dimensions        int         `json:"dimensions,omitempty"`
	ConfigFingerprint string      `json:"config_fingerprint"`
	UpdatedAt         time.Time   `json:"updated_at"`
}

// EvaluationUsage aggregates model-call telemetry for one evaluation run.
type EvaluationUsage struct {
	CallCount             int                `json:"call_count"`
	SuccessfulCalls       int                `json:"successful_calls"`
	FailedCalls           int                `json:"failed_calls"`
	PromptTokens          int                `json:"prompt_tokens"`
	CompletionTokens      int                `json:"completion_tokens"`
	TotalTokens           int                `json:"total_tokens"`
	CacheReadTokens       int                `json:"cache_read_tokens"`
	CacheWriteTokens      int                `json:"cache_write_tokens"`
	CacheMissTokens       int                `json:"cache_miss_tokens"`
	CacheReportedCalls    int                `json:"cache_reported_calls"`
	CacheHitCalls         int                `json:"cache_hit_calls"`
	CacheHitRate          float64            `json:"cache_hit_rate"`
	CacheCoverageRate     float64            `json:"cache_coverage_rate"`
	ModelDurationMS       int64              `json:"model_duration_ms"`
	AverageModelLatencyMS float64            `json:"average_model_latency_ms"`
	PricedCalls           int                `json:"priced_calls"`
	UnpricedCalls         int                `json:"unpriced_calls"`
	CostByCurrency        map[string]float64 `json:"cost_by_currency"`
}

// ModelUsageStat aggregates persisted evaluation, chat, Wiki, and background
// traffic for one model in a tenant. It contains no prompt or response bodies.
type ModelUsageStat struct {
	ModelID        string                   `json:"model_id"`
	ModelName      string                   `json:"model_name"`
	ModelType      ModelType                `json:"model_type"`
	Usage          EvaluationUsage          `json:"usage"`
	Purposes       []ModelPurposeUsageStat  `json:"purposes,omitempty"`
	EmbeddingCache *EmbeddingCacheUsageStat `json:"embedding_cache,omitempty"`
}

// EmbeddingCacheUsageStat reports cache effectiveness separately from provider
// token caching. A deduplicated input is repeated within one batch and therefore
// avoids a provider computation without being a stored-cache hit.
type EmbeddingCacheUsageStat struct {
	LookupCount         int     `json:"lookup_count"`
	HitCount            int     `json:"hit_count"`
	MissCount           int     `json:"miss_count"`
	DeduplicatedCount   int     `json:"deduplicated_count"`
	AvoidedComputations int     `json:"avoided_computations"`
	HitRate             float64 `json:"hit_rate"`
	AvoidedRate         float64 `json:"avoided_rate"`
}

// ModelPurposeUsageStat breaks a model's aggregate down by a secret-free call
// purpose such as knowledge_qa or wiki_page_modify.
type ModelPurposeUsageStat struct {
	Purpose string          `json:"purpose"`
	Usage   EvaluationUsage `json:"usage"`
}

// EvaluationModelCall describes one model call without storing prompt content.
type EvaluationModelCall struct {
	ID                      string          `json:"id"`
	ModelID                 string          `json:"model_id"`
	ModelName               string          `json:"model_name"`
	ModelType               ModelType       `json:"model_type"`
	Purpose                 string          `json:"purpose,omitempty"`
	PromptPrefixFingerprint string          `json:"prompt_prefix_fingerprint,omitempty"`
	Usage                   TokenUsage      `json:"usage"`
	Pricing                 LLMTokenPricing `json:"pricing"`
	EstimatedCost           float64         `json:"estimated_cost"`
	DurationMS              int64           `json:"duration_ms"`
	Success                 bool            `json:"success"`
	Error                   string          `json:"error,omitempty"`
	CreatedAt               time.Time       `json:"created_at"`
}

// String returns JSON representation of EvaluationTask
func (e *EvaluationTask) String() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// MetricInput contains input data for metric calculation
type MetricInput struct {
	RetrievalGT  [][]int // Ground truth for retrieval
	RetrievalIDs []int   // Retrieved IDs

	GeneratedTexts string // Generated text for evaluation
	GeneratedGT    string // Ground truth text for comparison
}

// MetricResult contains evaluation metrics
type MetricResult struct {
	RetrievalMetrics  RetrievalMetrics  `json:"retrieval_metrics"`  // Retrieval performance metrics
	GenerationMetrics GenerationMetrics `json:"generation_metrics"` // Text generation quality metrics
}

// RetrievalMetrics contains metrics for retrieval evaluation
type RetrievalMetrics struct {
	Precision float64 `json:"precision"` // Precision score
	Recall    float64 `json:"recall"`    // Recall score

	NDCG3  float64 `json:"ndcg3"`  // Normalized Discounted Cumulative Gain at 3
	NDCG10 float64 `json:"ndcg10"` // Normalized Discounted Cumulative Gain at 10
	MRR    float64 `json:"mrr"`    // Mean Reciprocal Rank
	MAP    float64 `json:"map"`    // Mean Average Precision
}

// GenerationMetrics contains metrics for text generation evaluation
type GenerationMetrics struct {
	BLEU1 float64 `json:"bleu1"` // BLEU-1 score
	BLEU2 float64 `json:"bleu2"` // BLEU-2 score
	BLEU4 float64 `json:"bleu4"` // BLEU-4 score

	ROUGE1 float64 `json:"rouge1"` // ROUGE-1 score
	ROUGE2 float64 `json:"rouge2"` // ROUGE-2 score
	ROUGEL float64 `json:"rougel"` // ROUGE-L score
}

// EvalState represents different stages of evaluation process
type EvalState int

const (
	StateBegin             EvalState = iota // Evaluation started
	StateAfterQaPairs                       // After loading QA pairs
	StateAfterDataset                       // After processing dataset
	StateAfterEmbedding                     // After generating embeddings
	StateAfterVectorSearch                  // After vector search
	StateAfterRerank                        // After reranking
	StateAfterComplete                      // After completion
	StateEnd                                // Evaluation ended
)
