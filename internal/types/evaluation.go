package types

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/yanyiwu/gojieba"
	"gorm.io/gorm"
)

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

type EvaluationMetricStatus string

const (
	EvaluationMetricScored        EvaluationMetricStatus = "scored"
	EvaluationMetricNotApplicable EvaluationMetricStatus = "not_applicable"
	EvaluationMetricFailed        EvaluationMetricStatus = "failed"
	EvaluationMetricSkipped       EvaluationMetricStatus = "skipped"
)

type EvaluationRunStatus string

const (
	EvaluationRunPending   EvaluationRunStatus = "pending"
	EvaluationRunRunning   EvaluationRunStatus = "running"
	EvaluationRunCompleted EvaluationRunStatus = "completed"
	EvaluationRunFailed    EvaluationRunStatus = "failed"
)

type EvaluationResultStatus string

const (
	EvaluationResultPending   EvaluationResultStatus = "pending"
	EvaluationResultRunning   EvaluationResultStatus = "running"
	EvaluationResultCompleted EvaluationResultStatus = "completed"
	EvaluationResultFailed    EvaluationResultStatus = "failed"
)

type EvaluationReferenceContext struct {
	Text        string `json:"text"`
	KnowledgeID string `json:"knowledge_id,omitempty"`
	ChunkID     string `json:"chunk_id,omitempty"`
}

type EvaluationRetrievedContext struct {
	Text        string  `json:"text"`
	KnowledgeID string  `json:"knowledge_id,omitempty"`
	ChunkID     string  `json:"chunk_id,omitempty"`
	Score       float64 `json:"score"`
	Rank        int     `json:"rank"`
}

type EvaluationMetricSelection struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type EvaluationMetricDefinition struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	Category        string `json:"category"`
	HigherIsBetter  bool   `json:"higher_is_better"`
	RequiresAnswer  bool   `json:"requires_reference_answer"`
	RequiresContext bool   `json:"requires_reference_contexts"`
}

type EvaluationMetricScore struct {
	Name              string                 `json:"name"`
	Version           string                 `json:"version"`
	Category          string                 `json:"category"`
	Score             *float64               `json:"score"`
	Status            EvaluationMetricStatus `json:"status"`
	HigherIsBetter    bool                   `json:"higher_is_better"`
	Reason            string                 `json:"reason"`
	Error             string                 `json:"error"`
	ScoredSampleCount int                    `json:"scored_sample_count,omitempty"`
	TotalSampleCount  int                    `json:"total_sample_count,omitempty"`
}

type EvaluationMetricScores map[string]EvaluationMetricScore

type EvaluationConfigSnapshot struct {
	KnowledgeBaseID  string                      `json:"knowledge_base_id"`
	ChatModelID      string                      `json:"chat_model_id"`
	RerankModelID    string                      `json:"rerank_model_id"`
	VectorThreshold  float64                     `json:"vector_threshold"`
	KeywordThreshold float64                     `json:"keyword_threshold"`
	EmbeddingTopK    int                         `json:"embedding_top_k"`
	RerankTopK       int                         `json:"rerank_top_k"`
	RerankThreshold  float64                     `json:"rerank_threshold"`
	SummaryConfig    SummaryConfig               `json:"summary_config"`
	FallbackStrategy FallbackStrategy            `json:"fallback_strategy"`
	FallbackResponse string                      `json:"fallback_response"`
	Metrics          []EvaluationMetricSelection `json:"metrics"`
}

type EvaluationDataset struct {
	ID          string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID    uint64         `json:"tenant_id" gorm:"index;not null"`
	Name        string         `json:"name" gorm:"type:varchar(255);not null"`
	Description string         `json:"description" gorm:"type:text;not null;default:''"`
	SampleCount int            `json:"sample_count" gorm:"-"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

func (*EvaluationDataset) TableName() string { return "evaluation_datasets" }
func (d *EvaluationDataset) BeforeCreate(*gorm.DB) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	return nil
}

type EvaluationSample struct {
	ID                string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID          uint64         `json:"tenant_id" gorm:"index;not null"`
	DatasetID         string         `json:"dataset_id" gorm:"type:varchar(36);index;not null"`
	Question          string         `json:"question" gorm:"type:text;not null"`
	ReferenceAnswer   string         `json:"reference_answer" gorm:"type:text;not null"`
	ReferenceContexts JSON           `json:"reference_contexts" gorm:"type:jsonb;not null"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `json:"-" gorm:"index"`
}

func (*EvaluationSample) TableName() string { return "evaluation_samples" }
func (s *EvaluationSample) BeforeCreate(*gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	return nil
}

type EvaluationRun struct {
	ID                    string              `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID              uint64              `json:"tenant_id" gorm:"index;not null"`
	DatasetID             string              `json:"dataset_id" gorm:"type:varchar(36);index;not null"`
	DatasetName           string              `json:"dataset_name" gorm:"type:varchar(255);not null"`
	Status                EvaluationRunStatus `json:"status" gorm:"type:varchar(32);index;not null"`
	ConfigSnapshot        JSON                `json:"config_snapshot" gorm:"type:jsonb;not null"`
	AggregateMetricScores JSON                `json:"aggregate_metric_scores" gorm:"type:jsonb;not null"`
	TotalSamples          int                 `json:"total_samples"`
	FinishedSamples       int                 `json:"finished_samples"`
	FailedSamples         int                 `json:"failed_samples"`
	Error                 string              `json:"error" gorm:"type:text;not null;default:''"`
	StartedAt             *time.Time          `json:"started_at"`
	CompletedAt           *time.Time          `json:"completed_at"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
}

func (*EvaluationRun) TableName() string { return "evaluation_runs" }
func (r *EvaluationRun) BeforeCreate(*gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	return nil
}

type EvaluationRunResult struct {
	ID                   string                 `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID             uint64                 `json:"tenant_id" gorm:"index;not null"`
	RunID                string                 `json:"run_id" gorm:"type:varchar(36);index;not null"`
	SampleID             string                 `json:"sample_id" gorm:"type:varchar(36);index;not null"`
	SampleIndex          int                    `json:"sample_index" gorm:"not null"`
	Question             string                 `json:"question" gorm:"type:text;not null"`
	ReferenceAnswer      string                 `json:"reference_answer" gorm:"type:text;not null"`
	ReferenceContexts    JSON                   `json:"reference_contexts" gorm:"type:jsonb;not null"`
	RetrievedContexts    JSON                   `json:"retrieved_contexts" gorm:"type:jsonb;not null"`
	GeneratedAnswer      string                 `json:"generated_answer" gorm:"type:text;not null;default:''"`
	Status               EvaluationResultStatus `json:"status" gorm:"type:varchar(32);index;not null"`
	Error                string                 `json:"error" gorm:"type:text;not null;default:''"`
	MetricScores         JSON                   `json:"metric_scores" gorm:"type:jsonb;not null"`
	DurationMilliseconds int64                  `json:"duration_ms"`
	CreatedAt            time.Time              `json:"created_at"`
	UpdatedAt            time.Time              `json:"updated_at"`
}

func (*EvaluationRunResult) TableName() string { return "evaluation_run_results" }
func (r *EvaluationRunResult) BeforeCreate(*gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	return nil
}

type CreateEvaluationDatasetRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}
type UpdateEvaluationDatasetRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}
type CreateEvaluationSampleRequest struct {
	Question          string                       `json:"question" binding:"required"`
	ReferenceAnswer   string                       `json:"reference_answer" binding:"required"`
	ReferenceContexts []EvaluationReferenceContext `json:"reference_contexts"`
}
type UpdateEvaluationSampleRequest struct {
	Question          *string                       `json:"question"`
	ReferenceAnswer   *string                       `json:"reference_answer"`
	ReferenceContexts *[]EvaluationReferenceContext `json:"reference_contexts"`
}

type CreateEvaluationRunRequest struct {
	DatasetID        string                      `json:"dataset_id" binding:"required"`
	KnowledgeBaseID  string                      `json:"knowledge_base_id" binding:"required"`
	ChatModelID      string                      `json:"chat_model_id" binding:"required"`
	RerankModelID    string                      `json:"rerank_model_id"`
	VectorThreshold  *float64                    `json:"vector_threshold"`
	KeywordThreshold *float64                    `json:"keyword_threshold"`
	EmbeddingTopK    *int                        `json:"embedding_top_k"`
	RerankTopK       *int                        `json:"rerank_top_k"`
	RerankThreshold  *float64                    `json:"rerank_threshold"`
	SummaryConfig    *SummaryConfig              `json:"summary_config"`
	FallbackStrategy *FallbackStrategy           `json:"fallback_strategy"`
	FallbackResponse *string                     `json:"fallback_response"`
	Metrics          []EvaluationMetricSelection `json:"metrics"`
}

type EvaluationMetricDelta struct {
	Name                  string                        `json:"name"`
	Version               string                        `json:"version"`
	BaselineScore         float64                       `json:"baseline_score"`
	CandidateScore        float64                       `json:"candidate_score"`
	Delta                 float64                       `json:"delta"`
	Improved              bool                          `json:"improved"`
	ComparableSampleCount int                           `json:"comparable_sample_count"`
	SampleDeltas          []EvaluationSampleMetricDelta `json:"sample_deltas"`
}
type EvaluationSampleMetricDelta struct {
	SampleID       string  `json:"sample_id"`
	BaselineScore  float64 `json:"baseline_score"`
	CandidateScore float64 `json:"candidate_score"`
	Delta          float64 `json:"delta"`
}
type EvaluationComparison struct {
	BaselineRunID  string                  `json:"baseline_run_id"`
	CandidateRunID string                  `json:"candidate_run_id"`
	DatasetID      string                  `json:"dataset_id"`
	Metrics        []EvaluationMetricDelta `json:"metrics"`
}

// Legacy V1 projection types.
type EvaluationStatue int

const (
	EvaluationStatuePending EvaluationStatue = iota
	EvaluationStatueRunning
	EvaluationStatueSuccess
	EvaluationStatueFailed
)

type EvaluationTask struct {
	ID        string           `json:"id"`
	TenantID  uint64           `json:"tenant_id"`
	DatasetID string           `json:"dataset_id"`
	StartTime time.Time        `json:"start_time"`
	Status    EvaluationStatue `json:"status"`
	ErrMsg    string           `json:"err_msg,omitempty"`
	Total     int              `json:"total,omitempty"`
	Finished  int              `json:"finished,omitempty"`
}
type EvaluationDetail struct {
	Task   *EvaluationTask `json:"task"`
	Params *ChatManage     `json:"params"`
	Metric *MetricResult   `json:"metric,omitempty"`
}

func (e *EvaluationTask) String() string { b, _ := json.Marshal(e); return string(b) }

type MetricInput struct {
	RetrievalGT    [][]int
	RetrievalIDs   []int
	GeneratedTexts string
	GeneratedGT    string
}
type MetricResult struct {
	RetrievalMetrics  RetrievalMetrics  `json:"retrieval_metrics"`
	GenerationMetrics GenerationMetrics `json:"generation_metrics"`
}
type RetrievalMetrics struct {
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	NDCG3     float64 `json:"ndcg3"`
	NDCG10    float64 `json:"ndcg10"`
	MRR       float64 `json:"mrr"`
	MAP       float64 `json:"map"`
}
type GenerationMetrics struct {
	BLEU1  float64 `json:"bleu1"`
	BLEU2  float64 `json:"bleu2"`
	BLEU4  float64 `json:"bleu4"`
	ROUGE1 float64 `json:"rouge1"`
	ROUGE2 float64 `json:"rouge2"`
	ROUGEL float64 `json:"rougel"`
}
type EvalState int

const (
	StateBegin EvalState = iota
	StateAfterQaPairs
	StateAfterDataset
	StateAfterEmbedding
	StateAfterVectorSearch
	StateAfterRerank
	StateAfterComplete
	StateEnd
)
