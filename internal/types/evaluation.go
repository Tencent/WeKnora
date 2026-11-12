package types

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yanyiwu/gojieba"
)

// gojieba resolves its default dictionaries relative to the build machine's
// module cache path (baked into the binary via runtime.Caller), so a binary
// moved to any other machine panics at init. Embed the dictionaries instead
// and materialize them under the user config directory on first start.
//
//go:embed jiebadata/jieba.dict.utf8 jiebadata/hmm_model.utf8 jiebadata/user.dict.utf8 jiebadata/idf.utf8 jiebadata/stop_words.utf8
var jiebaDictFS embed.FS

var jiebaDictNames = []string{
	"jieba.dict.utf8",
	"hmm_model.utf8",
	"user.dict.utf8",
	"idf.utf8",
	"stop_words.utf8",
}

// Jieba is a global instance of Chinese text segmentation tool
var Jieba *gojieba.Jieba = newJieba()

func newJieba() *gojieba.Jieba {
	if dictDir := os.Getenv("JIEBA_DICT_DIR"); dictDir != "" {
		return gojieba.NewJieba(dictPaths(dictDir)...)
	}

	dir, err := extractJiebaDicts()
	if err != nil {
		// 无法写用户目录时退回系统临时目录；两者都不可写属于环境级故障
		if dir2, err2 := extractJiebaDictsTo(os.TempDir()); err2 == nil {
			return gojieba.NewJieba(dictPaths(dir2)...)
		}
		panic(fmt.Sprintf("initialize jieba dictionaries: %v", err))
	}
	return gojieba.NewJieba(dictPaths(dir)...)
}

func dictPaths(dir string) []string {
	paths := make([]string, 0, len(jiebaDictNames))
	for _, name := range jiebaDictNames {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths
}

// extractJiebaDicts materializes the embedded dictionaries under the user
// config directory and returns that directory. Idempotent: existing files
// are left untouched so dictionaries can be customized in place.
func extractJiebaDicts() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return extractJiebaDictsTo(filepath.Join(base, "WeKnora Lite", "dict"))
}

func extractJiebaDictsTo(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, name := range jiebaDictNames {
		target := filepath.Join(dir, name)
		if _, err := os.Stat(target); err == nil {
			continue
		}
		data, err := jiebaDictFS.ReadFile("jiebadata/" + name)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
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
