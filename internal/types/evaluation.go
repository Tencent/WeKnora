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
	ID        string `json:"id" extensions:"x-agent=task_id"` // Unique task ID
	TenantID  uint64 `json:"tenant_id"`                       // Tenant/Organization ID
	DatasetID string `json:"dataset_id"`                      // Dataset ID for evaluation

	StartTime time.Time        `json:"start_time"`                  // Task start time
	Status    EvaluationStatue `json:"status" extensions:"x-agent"` // Current task status
	ErrMsg    string           `json:"err_msg,omitempty"`           // Error message if failed

	Total    int `json:"total,omitempty" extensions:"x-agent"`    // Total items to evaluate
	Finished int `json:"finished,omitempty" extensions:"x-agent"` // Completed items count
}

// EvaluationDetail contains detailed evaluation information
type EvaluationDetail struct {
	Task   *EvaluationTask `json:"task" extensions:"x-agent"`             // Evaluation task info
	Params *ChatManage     `json:"params"`                                // Evaluation parameters
	Metric *MetricResult   `json:"metric,omitempty" extensions:"x-agent"` // Evaluation metrics
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
	RetrievalMetrics  RetrievalMetrics  `json:"retrieval_metrics" extensions:"x-agent"`  // Retrieval performance metrics
	GenerationMetrics GenerationMetrics `json:"generation_metrics" extensions:"x-agent"` // Text generation quality metrics
}

// RetrievalMetrics contains metrics for retrieval evaluation
type RetrievalMetrics struct {
	Precision float64 `json:"precision" extensions:"x-agent"` // Precision score
	Recall    float64 `json:"recall" extensions:"x-agent"`    // Recall score

	NDCG3  float64 `json:"ndcg3" extensions:"x-agent"`  // Normalized Discounted Cumulative Gain at 3
	NDCG10 float64 `json:"ndcg10" extensions:"x-agent"` // Normalized Discounted Cumulative Gain at 10
	MRR    float64 `json:"mrr" extensions:"x-agent"`    // Mean Reciprocal Rank
	MAP    float64 `json:"map" extensions:"x-agent"`    // Mean Average Precision
}

// GenerationMetrics contains metrics for text generation evaluation
type GenerationMetrics struct {
	BLEU1 float64 `json:"bleu1" extensions:"x-agent"` // BLEU-1 score
	BLEU2 float64 `json:"bleu2" extensions:"x-agent"` // BLEU-2 score
	BLEU4 float64 `json:"bleu4" extensions:"x-agent"` // BLEU-4 score

	ROUGE1 float64 `json:"rouge1" extensions:"x-agent"` // ROUGE-1 score
	ROUGE2 float64 `json:"rouge2" extensions:"x-agent"` // ROUGE-2 score
	ROUGEL float64 `json:"rougel" extensions:"x-agent"` // ROUGE-L score
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
