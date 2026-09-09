package types

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/yanyiwu/gojieba"
)

// EvaluationRun is the durable database representation of an evaluation.
// Detail keeps the public API snapshot intact while the scalar columns support
// tenant-safe history queries without decoding JSON in SQL.
type EvaluationRun struct {
	ID        string           `gorm:"primaryKey;column:id" json:"id"`
	TenantID  uint64           `gorm:"column:tenant_id;not null;index:idx_evaluation_runs_tenant_created" json:"tenant_id"`
	DatasetID string           `gorm:"column:dataset_id;not null" json:"dataset_id"`
	Status    EvaluationStatue `gorm:"column:status;not null" json:"status"`
	Detail    json.RawMessage  `gorm:"column:detail;type:jsonb;not null" json:"-"`
	CreatedAt time.Time        `gorm:"column:created_at;autoCreateTime;index:idx_evaluation_runs_tenant_created" json:"created_at"`
	UpdatedAt time.Time        `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (EvaluationRun) TableName() string { return "evaluation_runs" }

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

	StartTime  time.Time        `json:"start_time"` // Task start time
	EndTime    *time.Time       `json:"end_time,omitempty"`
	DurationMS int64            `json:"duration_ms,omitempty"`
	Status     EvaluationStatue `json:"status"`            // Current task status
	ErrMsg     string           `json:"err_msg,omitempty"` // Error message if failed

	Total    int `json:"total,omitempty"`    // Total items to evaluate
	Finished int `json:"finished,omitempty"` // Completed items count
}

// EvaluationDetail contains detailed evaluation information
type EvaluationDetail struct {
	Task     *EvaluationTask     `json:"task"`   // Evaluation task info
	Params   *ChatManage         `json:"params"` // Evaluation parameters
	Snapshot *EvaluationSnapshot `json:"snapshot,omitempty"`
	Metric   *MetricResult       `json:"metric,omitempty"` // Evaluation metrics
	Items    []*EvaluationItem   `json:"items,omitempty"`
}

// EvaluationSnapshot records the identities needed to explain and reproduce a
// run. Unknown build metadata stays explicit rather than being invented.
type EvaluationSnapshot struct {
	DatasetSHA256          string `json:"dataset_sha256"`
	SourceKnowledgeBaseID  string `json:"source_knowledge_base_id"`
	RuntimeKnowledgeBaseID string `json:"runtime_knowledge_base_id"`
	ChatModelID            string `json:"chat_model_id"`
	EmbeddingModelID       string `json:"embedding_model_id"`
	RerankModelID          string `json:"rerank_model_id"`
	CodeCommit             string `json:"code_commit"`
	PromptVersion          string `json:"prompt_version"`
	PriceVersion           string `json:"price_version"`
	Currency               string `json:"currency"`
	Concurrency            int    `json:"concurrency"`
}

// WikiCacheProbeResult is one bounded, fixed-input prompt-cache observation.
// It intentionally contains no provider credential or caller supplied prompt.
type WikiCacheProbeResult struct {
	Layout            string     `json:"layout"`
	Sample            int        `json:"sample"`
	Purpose           string     `json:"purpose"`
	PrefixFingerprint string     `json:"prefix_fingerprint"`
	ExpectedSlug      string     `json:"expected_slug"`
	Output            string     `json:"output"`
	OutputValid       bool       `json:"output_valid"`
	ExpectedFound     bool       `json:"expected_found"`
	Usage             TokenUsage `json:"usage"`
	DurationMS        int64      `json:"duration_ms"`
}

type EvaluationItem struct {
	Index           int             `json:"index"`
	QuestionID      int             `json:"question_id"`
	Question        string          `json:"question"`
	ReferenceAnswer string          `json:"reference_answer"`
	GeneratedAnswer string          `json:"generated_answer"`
	SearchResults   []*SearchResult `json:"search_results,omitempty"`
	RerankResults   []*SearchResult `json:"rerank_results,omitempty"`
	Metric          *MetricResult   `json:"metric,omitempty"`
	DurationMS      int64           `json:"duration_ms"`
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
