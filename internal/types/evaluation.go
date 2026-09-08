package types

import (
	"database/sql/driver"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/yanyiwu/gojieba"
	"gorm.io/gorm"
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

// BenchmarkContractVersionV11 identifies the frozen Benchmark v1.1 contract.
const BenchmarkContractVersionV11 = "v1.1"

// EvaluationTask contains information about an evaluation task
type EvaluationTask struct {
	ID        string `json:"id"`         // Unique task ID
	TenantID  uint64 `json:"tenant_id"`  // Tenant/Organization ID
	DatasetID string `json:"dataset_id"` // Dataset ID for evaluation

	StartTime time.Time        `json:"start_time"`        // Task start time
	Status    EvaluationStatue `json:"status"`            // Current task status
	ErrMsg    string           `json:"err_msg,omitempty"` // Error message if failed

	Total    int `json:"total,omitempty"`    // Total items to evaluate
	Finished int `json:"finished,omitempty"` // Completed items count
}

// EvaluationDetail contains detailed evaluation information
type EvaluationDetail struct {
	Task   *EvaluationTask `json:"task"`             // Evaluation task info
	Params *ChatManage     `json:"params"`           // Evaluation parameters
	Metric *MetricResult   `json:"metric,omitempty"` // Evaluation metrics
}

// EvaluationConfigSnapshotV1 is the versioned, secret-free allowlist needed
// to explain and reconstruct the effective configuration of an evaluation.
// It intentionally does not embed Model, ModelParameters, or global Config.
type EvaluationConfigSnapshotV1 struct {
	SnapshotSchemaVersion    int                          `json:"snapshot_schema_version"`
	BenchmarkContractVersion string                       `json:"benchmark_contract_version,omitempty"`
	Dataset                  EvaluationDatasetSnapshot    `json:"dataset"`
	Pipeline                 EvaluationPipelineSnapshot   `json:"pipeline"`
	Retrieval                EvaluationRetrievalSnapshot  `json:"retrieval"`
	Models                   EvaluationModelsSnapshot     `json:"models"`
	SourceKnowledgeBase      *EvaluationSourceKBSnapshot  `json:"source_knowledge_base,omitempty"`
	Generation               EvaluationGenerationSnapshot `json:"generation"`
	Execution                EvaluationExecutionSnapshot  `json:"execution"`
}

// EvaluationDatasetSnapshot records the dataset identity used by an evaluation.
type EvaluationDatasetSnapshot struct {
	DatasetID             string `json:"dataset_id"`
	DatasetSemanticSHA256 string `json:"dataset_semantic_sha256,omitempty"`
	CorpusCount           int    `json:"corpus_count,omitempty"`
	QuestionCount         int    `json:"question_count,omitempty"`
	QrelsCount            int    `json:"qrels_count,omitempty"`
	AnswerCount           int    `json:"answer_count,omitempty"`
	CorpusMode            string `json:"corpus_mode,omitempty"`
	ChunkingApplied       bool   `json:"chunking_applied"`
}

// EvaluationPipelineSnapshot records the evaluation pipeline configuration.
type EvaluationPipelineSnapshot struct {
	Name        string                      `json:"name"`
	Metrics     []string                    `json:"metrics"`
	NDCGCutoffs []int                       `json:"ndcg_cutoffs"`
	Tokenizer   EvaluationTokenizerSnapshot `json:"tokenizer"`
}

// EvaluationTokenizerSnapshot records the tokenizer configuration.
type EvaluationTokenizerSnapshot struct {
	Name                  string `json:"name"`
	DictionaryMode        string `json:"dictionary_mode"`
	DictionaryFingerprint string `json:"dictionary_fingerprint,omitempty"`
}

// EvaluationRetrievalSnapshot records the effective retrieval configuration.
type EvaluationRetrievalSnapshot struct {
	VectorThreshold  float64 `json:"vector_threshold"`
	KeywordThreshold float64 `json:"keyword_threshold"`
	EmbeddingTopK    int     `json:"embedding_top_k"`
	RerankTopK       int     `json:"rerank_top_k"`
	RerankThreshold  float64 `json:"rerank_threshold"`
	RetrieveDriver   string  `json:"retrieve_driver,omitempty"`
}

// EvaluationModelsSnapshot records the model identities used by an evaluation.
type EvaluationModelsSnapshot struct {
	EmbeddingModelID string  `json:"embedding_model_id"`
	ChatModelID      string  `json:"chat_model_id"`
	RerankModelID    *string `json:"rerank_model_id,omitempty"`
	SummaryModelID   string  `json:"summary_model_id,omitempty"`

	Embedding *EvaluationConfiguredModelSnapshot `json:"embedding,omitempty"`
	Chat      *EvaluationConfiguredModelSnapshot `json:"chat,omitempty"`
	Rerank    *EvaluationConfiguredModelSnapshot `json:"rerank,omitempty"`
	Summary   *EvaluationConfiguredModelSnapshot `json:"summary,omitempty"`
}

// EvaluationConfiguredModelSnapshot is an explicit secret-free allowlist.
// Never replace it by serializing Model or ModelParameters wholesale.
type EvaluationConfiguredModelSnapshot struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Source        string `json:"source"`
	Provider      string `json:"provider"`
	InterfaceType string `json:"interface_type"`
	// EndpointFingerprint identifies the effective network destination without
	// persisting its URL, credentials, query parameters, or custom headers.
	EndpointFingerprint string                       `json:"endpoint_fingerprint,omitempty"`
	Embedding           *EvaluationEmbeddingSnapshot `json:"embedding,omitempty"`
}

// EvaluationEmbeddingSnapshot records embedding-specific model properties.
type EvaluationEmbeddingSnapshot struct {
	Dimension                 int  `json:"dimension"`
	TruncatePromptTokens      int  `json:"truncate_prompt_tokens"`
	SupportsDimensionOverride bool `json:"supports_dimension_override"`
}

// EvaluationSourceKBSnapshot records the source knowledge base identity.
type EvaluationSourceKBSnapshot struct {
	ID               string `json:"id"`
	EmbeddingModelID string `json:"embedding_model_id"`
	SummaryModelID   string `json:"summary_model_id"`
}

// EvaluationGenerationSnapshot records the effective generation configuration.
type EvaluationGenerationSnapshot struct {
	MaxRounds           int           `json:"max_rounds"`
	SummaryConfig       SummaryConfig `json:"summary_config"`
	FallbackResponse    string        `json:"fallback_response"`
	RewritePromptSystem string        `json:"rewrite_prompt_system"`
	RewritePromptUser   string        `json:"rewrite_prompt_user"`
}

// EvaluationExecutionSnapshot records execution settings for an evaluation.
type EvaluationExecutionSnapshot struct {
	WorkerLimit int `json:"worker_limit"`
}

// Value implements driver.Valuer for an evaluation configuration snapshot.
func (s EvaluationConfigSnapshotV1) Value() (driver.Value, error) { return json.Marshal(s) }

// Scan implements sql.Scanner for an evaluation configuration snapshot.
func (s *EvaluationConfigSnapshotV1) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var data []byte
	switch value := value.(type) {
	case []byte:
		data = value
	case string:
		data = []byte(value)
	default:
		return nil
	}
	return json.Unmarshal(data, s)
}

// EvaluationRun is the database source of truth for a run-level evaluation.
// Metric pointers preserve the distinction between not-yet-produced and zero.
type EvaluationRun struct {
	ID                    string           `gorm:"type:varchar(36);primaryKey"`
	TaskID                string           `gorm:"type:varchar(255);not null;uniqueIndex"`
	TenantID              uint64           `gorm:"not null;index:idx_evaluation_runs_tenant_created,priority:1"`
	DatasetID             string           `gorm:"type:varchar(255);not null"`
	SourceKnowledgeBaseID *string          `gorm:"type:varchar(36)"`
	EmbeddingModelID      string           `gorm:"type:varchar(64);not null"`
	RerankModelID         *string          `gorm:"type:varchar(64)"`
	ChatModelID           string           `gorm:"type:varchar(64);not null"`
	Status                EvaluationStatue `gorm:"not null"`
	Total                 int              `gorm:"not null;default:0"`
	Finished              int              `gorm:"not null;default:0"`
	Precision             *float64
	Recall                *float64
	NDCG3                 *float64 `gorm:"column:ndcg_3"`
	NDCG10                *float64 `gorm:"column:ndcg_10"`
	MRR                   *float64
	MAP                   *float64
	BLEU1                 *float64                   `gorm:"column:bleu_1"`
	BLEU2                 *float64                   `gorm:"column:bleu_2"`
	BLEU4                 *float64                   `gorm:"column:bleu_4"`
	ROUGE1                *float64                   `gorm:"column:rouge_1"`
	ROUGE2                *float64                   `gorm:"column:rouge_2"`
	ROUGEL                *float64                   `gorm:"column:rouge_l"`
	ConfigSnapshot        EvaluationConfigSnapshotV1 `gorm:"type:jsonb;not null"`
	StartedAt             *time.Time
	FinishedAt            *time.Time
	DurationMS            *int64    `gorm:"column:duration_ms"`
	ErrorMessage          string    `gorm:"type:text"`
	CreatedAt             time.Time `gorm:"index:idx_evaluation_runs_tenant_created,priority:2"`
	UpdatedAt             time.Time
}

// TableName returns the database table for evaluation runs.
func (EvaluationRun) TableName() string { return "evaluation_runs" }

// BeforeCreate assigns an identifier to a new evaluation run.
func (r *EvaluationRun) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	return nil
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
