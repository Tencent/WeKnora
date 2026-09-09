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

// EvaluationRunSummary is the secret-minimized representation used by history
// lists. Full params and per-call records remain available only from the
// existing single-task detail endpoint.
type EvaluationRunSummary struct {
	Task      *EvaluationTask      `json:"task"`
	RunConfig *EvaluationRunConfig `json:"run_config,omitempty"`
	Metric    *MetricResult        `json:"metric,omitempty"`
	Usage     *EvaluationUsage     `json:"usage,omitempty"`
}

// EvaluationRunPage is a tenant-scoped, stable page of evaluation history.
type EvaluationRunPage struct {
	Items  []EvaluationRunSummary `json:"items"`
	Total  int64                  `json:"total"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
}

// EvaluationRunConfig is an immutable, secret-free snapshot of everything
// needed to explain and reproduce an evaluation run. IDs alone are not enough:
// datasets and model rows can change after a run, so their content/config
// fingerprints are retained alongside the effective chunking and RAG options.
type EvaluationRunConfig struct {
	SchemaVersion             int                        `json:"schema_version"`
	DatasetID                 string                     `json:"dataset_id"`
	DatasetFingerprint        string                     `json:"dataset_fingerprint"`
	DatasetSamples            int                        `json:"dataset_samples"`
	SourceKnowledgeBaseID     string                     `json:"source_knowledge_base_id,omitempty"`
	EvaluationKnowledgeBaseID string                     `json:"evaluation_knowledge_base_id"`
	Chunking                  ChunkingConfig             `json:"chunking"`
	Pipeline                  EvaluationPipelineSnapshot `json:"pipeline"`
	Models                    []EvaluationModelSnapshot  `json:"models"`
	CodeVersion               string                     `json:"code_version"`
	ConfigFingerprint         string                     `json:"config_fingerprint"`
	ControlledFingerprint     string                     `json:"controlled_fingerprint"`
}

// EvaluationPipelineSnapshot contains the effective RAG controls without any
// prompt or response bodies. Textual templates are represented only by hashes.
type EvaluationPipelineSnapshot struct {
	MaxRounds                 int                       `json:"max_rounds"`
	VectorThreshold           float64                   `json:"vector_threshold"`
	KeywordThreshold          float64                   `json:"keyword_threshold"`
	EmbeddingTopK             int                       `json:"embedding_top_k"`
	RerankModelID             string                    `json:"rerank_model_id,omitempty"`
	RerankTopK                int                       `json:"rerank_top_k"`
	RerankThreshold           float64                   `json:"rerank_threshold"`
	ChatModelID               string                    `json:"chat_model_id"`
	FallbackStrategy          FallbackStrategy          `json:"fallback_strategy,omitempty"`
	CitationEnabled           *bool                     `json:"citation_enabled,omitempty"`
	EnableRewrite             bool                      `json:"enable_rewrite"`
	EnableQueryExpansion      bool                      `json:"enable_query_expansion"`
	QueryUnderstandModelID    string                    `json:"query_understand_model_id,omitempty"`
	Summary                   EvaluationSummarySnapshot `json:"summary"`
	FallbackResponseSHA256    string                    `json:"fallback_response_sha256"`
	FallbackPromptSHA256      string                    `json:"fallback_prompt_sha256"`
	RewritePromptSystemSHA256 string                    `json:"rewrite_prompt_system_sha256"`
	RewritePromptUserSHA256   string                    `json:"rewrite_prompt_user_sha256"`
}

type EvaluationSummarySnapshot struct {
	MaxTokens             int     `json:"max_tokens"`
	RepeatPenalty         float64 `json:"repeat_penalty"`
	TopK                  int     `json:"top_k"`
	TopP                  float64 `json:"top_p"`
	FrequencyPenalty      float64 `json:"frequency_penalty"`
	PresencePenalty       float64 `json:"presence_penalty"`
	Temperature           float64 `json:"temperature"`
	Seed                  int     `json:"seed"`
	MaxCompletionTokens   int     `json:"max_completion_tokens"`
	Thinking              *bool   `json:"thinking,omitempty"`
	PromptSHA256          string  `json:"prompt_sha256"`
	ContextTemplateSHA256 string  `json:"context_template_sha256"`
	NoMatchPrefixSHA256   string  `json:"no_match_prefix_sha256"`
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
	RequestFingerprint      string          `json:"request_fingerprint,omitempty"`
	Usage                   TokenUsage      `json:"usage"`
	Pricing                 LLMTokenPricing `json:"pricing"`
	EstimatedCost           float64         `json:"estimated_cost"`
	DurationMS              int64           `json:"duration_ms"`
	Success                 bool            `json:"success"`
	Error                   string          `json:"error,omitempty"`
	CreatedAt               time.Time       `json:"created_at"`
}

// WikiCacheBenchmarkEvidence is a portable, prompt-free record of a controlled
// cold/warm replay through the production Wiki chat path.
type WikiCacheBenchmarkEvidence struct {
	SchemaVersion    int                       `json:"schema_version"`
	BenchmarkID      string                    `json:"benchmark_id"`
	GeneratedAt      time.Time                 `json:"generated_at"`
	CodeVersion      string                    `json:"code_version"`
	ModelID          string                    `json:"model_id"`
	ModelName        string                    `json:"model_name"`
	Repetitions      int                       `json:"repetitions"`
	WorkloadSHA256   string                    `json:"workload_sha256"`
	ConfigurationSHA string                    `json:"configuration_sha256"`
	Cold             WikiCacheBenchmarkCohort  `json:"cold"`
	Warm             WikiCacheBenchmarkCohort  `json:"warm"`
	StrictValidation WikiCacheStrictValidation `json:"strict_validation"`
	Warnings         []string                  `json:"warnings,omitempty"`
	ReportSHA256     string                    `json:"report_sha256"`
}

// WikiCacheBenchmarkCohort contains only metadata and provider usage; prompt
// and response bodies are intentionally excluded.
type WikiCacheBenchmarkCohort struct {
	Calls           []EvaluationEvidenceCall `json:"model_calls"`
	Usage           EvaluationUsage          `json:"usage"`
	MedianLatencyMS float64                  `json:"median_latency_ms"`
	P95LatencyMS    float64                  `json:"p95_latency_ms"`
}

// WikiCacheStrictValidation records whether the two cohorts meet the evidence
// protocol rather than merely reporting a favorable cache percentage.
type WikiCacheStrictValidation struct {
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
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
	RetrievalMetrics  RetrievalMetrics           `json:"retrieval_metrics"`  // Retrieval performance metrics
	GenerationMetrics GenerationMetrics          `json:"generation_metrics"` // Text generation quality metrics
	Samples           []EvaluationSampleEvidence `json:"samples,omitempty"`
}

// EvaluationSampleEvidence is a secret-free audit record for one dataset row.
// It retains stable IDs, ranks, scores and content hashes, but never stores the
// question, reference answer, generated answer or retrieved passage bodies.
type EvaluationSampleEvidence struct {
	Index                 int                           `json:"index"`
	QuestionID            int                           `json:"question_id"`
	AnswerID              int                           `json:"answer_id"`
	QuestionSHA256        string                        `json:"question_sha256"`
	ReferenceAnswerSHA256 string                        `json:"reference_answer_sha256"`
	ResponseSHA256        string                        `json:"response_sha256"`
	ResponseBytes         int                           `json:"response_bytes"`
	Retrieved             []EvaluationRetrievedEvidence `json:"retrieved"`
	RetrievalMetrics      RetrievalMetrics              `json:"retrieval_metrics"`
	GenerationMetrics     GenerationMetrics             `json:"generation_metrics"`
}

// EvaluationRetrievedEvidence records the ordered retrieval provenance without
// retaining chunk content. DatasetPassageID is nil when a retrieved chunk could
// not be mapped back to the frozen dataset corpus.
type EvaluationRetrievedEvidence struct {
	Rank             int       `json:"rank"`
	DatasetPassageID *int      `json:"dataset_passage_id,omitempty"`
	ChunkID          string    `json:"chunk_id,omitempty"`
	KnowledgeID      string    `json:"knowledge_id,omitempty"`
	ChunkIndex       int       `json:"chunk_index"`
	Score            float64   `json:"score"`
	MatchType        MatchType `json:"match_type"`
	ContentSHA256    string    `json:"content_sha256"`
}

// EvaluationEvidenceReport is a deterministic, portable proof bundle. The
// report hash is computed over this structure with ReportSHA256 left empty.
type EvaluationEvidenceReport struct {
	SchemaVersion int                      `json:"schema_version"`
	ReportSHA256  string                   `json:"report_sha256"`
	Task          *EvaluationTask          `json:"task"`
	RunConfig     *EvaluationRunConfig     `json:"run_config,omitempty"`
	Metric        *MetricResult            `json:"metric,omitempty"`
	Usage         *EvaluationUsage         `json:"usage,omitempty"`
	ModelCalls    []EvaluationEvidenceCall `json:"model_calls,omitempty"`
	Warnings      []string                 `json:"warnings,omitempty"`
}

// EvaluationEvidenceCall deliberately omits provider errors and all text.
type EvaluationEvidenceCall struct {
	ID                      string          `json:"id"`
	ModelID                 string          `json:"model_id"`
	ModelName               string          `json:"model_name"`
	ModelType               ModelType       `json:"model_type"`
	Purpose                 string          `json:"purpose,omitempty"`
	PromptPrefixFingerprint string          `json:"prompt_prefix_fingerprint,omitempty"`
	RequestFingerprint      string          `json:"request_fingerprint,omitempty"`
	Usage                   TokenUsage      `json:"usage"`
	Pricing                 LLMTokenPricing `json:"pricing"`
	EstimatedCost           float64         `json:"estimated_cost"`
	DurationMS              int64           `json:"duration_ms"`
	Success                 bool            `json:"success"`
	CreatedAt               time.Time       `json:"created_at"`
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
