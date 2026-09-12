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

// Evaluation task status values describe the lifecycle state exposed by the API.
const (
	EvaluationStatuePending     EvaluationStatue = iota // Task is waiting to start
	EvaluationStatueRunning                             // Task is in progress
	EvaluationStatueSuccess                             // Task completed successfully
	EvaluationStatueFailed                              // Task failed
	EvaluationStatueTimedOut                            // Task exceeded its configured deadline
	EvaluationStatueInterrupted                         // Task lease expired and recovery cleanup completed
	EvaluationStatueCanceled                            // Task was canceled by a persistent user request
)

// EvaluationTask contains information about an evaluation task
type EvaluationTask struct {
	ID        string `json:"id"`         // Unique task ID
	TenantID  uint64 `json:"tenant_id"`  // Tenant/Organization ID
	DatasetID string `json:"dataset_id"` // Dataset ID for evaluation

	StartTime time.Time        `json:"start_time"`         // Task start time
	EndTime   *time.Time       `json:"end_time,omitempty"` // Task completion time
	Status    EvaluationStatue `json:"status"`             // Current task status
	ErrMsg    string           `json:"err_msg,omitempty"`  // Execution failure or timeout message

	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"` // First persistent cancel request time

	CleanupErrors []string `json:"cleanup_errors,omitempty"` // Temporary resource cleanup warnings
	Labels        []string `json:"labels"`                   // Normalized experiment labels

	DatasetVersionID   *string `json:"dataset_version_id,omitempty"`
	ProvenanceComplete bool    `json:"provenance_complete"`

	Total    int `json:"total,omitempty"`    // Total items to evaluate
	Finished int `json:"finished,omitempty"` // Completed items count
}

// EvaluationDetail contains detailed evaluation information
type EvaluationDetail struct {
	Task           *EvaluationTask           `json:"task"`             // Evaluation task info
	Params         *ChatManage               `json:"params"`           // Evaluation parameters
	Metric         *MetricResult             `json:"metric,omitempty"` // Evaluation metrics
	RuntimeMetrics *EvaluationRuntimeMetrics `json:"runtime_metrics,omitempty"`

	// Experiment is the frozen schema-version-1 experiment manifest; it is
	// null for pre-M3 tasks instead of an empty fabricated object.
	Experiment *EvaluationExperimentSnapshot `json:"experiment"`
	// ProvenanceComplete reports whether dataset, model, parameter, code,
	// and environment provenance were fully frozen for this task.
	ProvenanceComplete bool `json:"provenance_complete"`
}

// EvaluationMetricDefinition is the public registry catalog DTO.
type EvaluationMetricDefinition struct {
	Key           string          `json:"key"`
	Version       string          `json:"version"`
	Kind          string          `json:"kind"`
	Description   string          `json:"description"`
	DefaultConfig json.RawMessage `json:"default_config"`
	ConfigSchema  json.RawMessage `json:"config_schema"`
}

// String returns JSON representation of EvaluationTask
func (e *EvaluationTask) String() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// MetricInput contains input data for metric calculation
type MetricInput struct {
	RetrievalGT              [][]int     // Binary ground truth for retrieval
	RetrievalGrades          map[int]int // Graded relevance keyed by passage ID
	RetrievalLabelsAvailable bool        // Distinguishes an empty labeled set from missing labels
	RetrievalIDs             []int       // Retrieved IDs

	GeneratedTexts string // Generated text for evaluation
	GeneratedGT    string // Ground truth text for comparison
}

// MetricResult contains evaluation metrics
type MetricResult struct {
	RetrievalMetrics  RetrievalMetrics  `json:"retrieval_metrics"`  // Retrieval performance metrics
	GenerationMetrics GenerationMetrics `json:"generation_metrics"` // Text generation quality metrics
	// Scores is the additive registry result keyed by the frozen metric
	// instance ID. Existing fixed fields remain populated for compatibility.
	Scores map[string]EvaluationMetricScore `json:"scores,omitempty"`
}

// EvaluationMetricScore preserves the state of one dynamic aggregate or
// per-sample score. Value remains nullable so zero and unavailable differ.
type EvaluationMetricScore struct {
	Value     *float64 `json:"value"`
	Status    string   `json:"status"`
	ErrorCode string   `json:"error_code,omitempty"`
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
