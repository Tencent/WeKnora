package types

import (
	"errors"
	"time"
)

// Learning algorithm and prompt versions identify the assessment rules in use.
const (
	LearningAlgorithmVersion = "bkt-v1"
	LearningPromptVersion    = "learning-quiz-v1"
)

// Learning errors are safe to return without exposing source or answer content.
var (
	ErrLearningForbidden   = errors.New("learning: forbidden")
	ErrLearningDisabled    = errors.New("learning: disabled")
	ErrLearningNotFound    = errors.New("learning: not found")
	ErrLearningStale       = errors.New("learning: stale evidence")
	ErrLearningNotReady    = errors.New("learning: quiz not ready")
	ErrLearningConflict    = errors.New("learning: conflicting attempt")
	ErrLearningInvalid     = errors.New("learning: invalid request")
	ErrLearningBusy        = errors.New("learning: busy")
	ErrLearningEvidence    = errors.New("learning: insufficient validated evidence")
	ErrLearningUnavailable = errors.New("learning: unavailable")
)

// LearningSettings records consent and the active assessment algorithm.
type LearningSettings struct {
	Enabled          bool   `json:"enabled"`
	AlgorithmVersion string `json:"algorithm_version"`
}

// LearningMasteryView exposes assessed mastery and review timing for a topic.
type LearningMasteryView struct {
	State              string     `json:"state"`
	PMastery           float64    `json:"p_mastery"`
	Attempts           int        `json:"attempts"`
	Correct            int        `json:"correct"`
	ConsecutiveCorrect int        `json:"consecutive_correct"`
	LastAssessedAt     *time.Time `json:"last_assessed_at,omitempty"`
	NextReviewAt       *time.Time `json:"next_review_at,omitempty"`
	SourceStale        bool       `json:"source_stale"`
}

// LearningNodeView combines a Wiki topic with familiarity and assessed mastery.
type LearningNodeView struct {
	PageID          string              `json:"page_id"`
	KnowledgeBaseID string              `json:"knowledge_base_id"`
	Slug            string              `json:"slug"`
	Title           string              `json:"title"`
	PageType        string              `json:"page_type"`
	Summary         string              `json:"summary"`
	Familiar        bool                `json:"familiar"`
	Mastery         LearningMasteryView `json:"mastery"`
}

// LearningCounts totals topics by assessment state.
type LearningCounts struct {
	Unseen    int `json:"unseen"`
	Learning  int `json:"learning"`
	Mastered  int `json:"mastered"`
	ReviewDue int `json:"review_due"`
}

// LearningOverview summarizes a learner's progress in one knowledge base.
type LearningOverview struct {
	Enabled          bool           `json:"enabled"`
	AlgorithmVersion string         `json:"algorithm_version"`
	KnowledgeBaseID  string         `json:"knowledge_base_id"`
	TotalNodes       int            `json:"total_nodes"`
	Counts           LearningCounts `json:"counts"`
}

// LearningScoreComponents contains the inputs to a recommendation's weighted score.
type LearningScoreComponents struct {
	ReviewNeed     float64 `json:"review_need"`
	GraphFrontier  float64 `json:"graph_frontier"`
	InterestMatch  float64 `json:"interest_match"`
	ContentQuality float64 `json:"content_quality"`
}

// LearningRecommendation is a ranked topic with its score and selection reasons.
type LearningRecommendation struct {
	LearningNodeView
	Score       float64                 `json:"score"`
	Components  LearningScoreComponents `json:"components"`
	ReasonCodes []string                `json:"reason_codes"`
}

// LearningOption is a selectable answer identified within a question.
type LearningOption struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// LearningEvidence identifies a source chunk and its supporting quote.
type LearningEvidence struct {
	ChunkID     string `json:"chunk_id"`
	KnowledgeID string `json:"knowledge_id"`
	Quote       string `json:"quote"`
}

// LearningQuestionView exposes a question and its result only after answering.
type LearningQuestionView struct {
	ID       string                `json:"id"`
	Prompt   string                `json:"prompt"`
	Options  []LearningOption      `json:"options"`
	Answered bool                  `json:"answered"`
	Result   *LearningAnswerResult `json:"result,omitempty"`
}

// LearningQuizView exposes quiz status and questions without unanswered keys.
type LearningQuizView struct {
	ID               string                 `json:"id"`
	PageID           string                 `json:"page_id"`
	KnowledgeBaseID  string                 `json:"knowledge_base_id"`
	Slug             string                 `json:"slug"`
	Title            string                 `json:"title"`
	Status           string                 `json:"status"`
	ErrorCode        string                 `json:"error_code,omitempty"`
	Questions        []LearningQuestionView `json:"questions"`
	AlgorithmVersion string                 `json:"algorithm_version"`
}

// LearningAnswer submits a selected option with an idempotent attempt ID.
type LearningAnswer struct {
	QuestionID string `json:"question_id"`
	OptionID   string `json:"option_id"`
	AttemptID  string `json:"attempt_id"`
}

// LearningAnswerResult contains grading, supporting evidence and updated mastery.
type LearningAnswerResult struct {
	AttemptID      string              `json:"attempt_id"`
	QuestionID     string              `json:"question_id"`
	SelectedOption string              `json:"selected_option"`
	Correct        bool                `json:"correct"`
	CorrectOption  string              `json:"correct_option"`
	Explanation    string              `json:"explanation"`
	Evidence       []LearningEvidence  `json:"evidence"`
	Mastery        LearningMasteryView `json:"mastery"`
}

// LearningClearResult reports how many learning records were deleted.
type LearningClearResult struct {
	DeletedAttempts int64 `json:"deleted_attempts"`
	DeletedMastery  int64 `json:"deleted_mastery"`
	DeletedQuizzes  int64 `json:"deleted_quizzes"`
}

// LearningExport never includes unanswered keys, even for the owner.
type LearningExport struct {
	Settings   LearningSettings        `json:"settings"`
	Nodes      []*LearningNodeView     `json:"nodes"`
	Quizzes    []*LearningQuizView     `json:"quizzes"`
	Attempts   []LearningAttemptExport `json:"attempts"`
	ExportedAt time.Time               `json:"exported_at"`
}

// LearningAttemptExport includes an attempt's result and assessment provenance.
type LearningAttemptExport struct {
	PageID           string               `json:"page_id"`
	KnowledgeBaseID  string               `json:"knowledge_base_id"`
	Fingerprint      string               `json:"fingerprint"`
	SourceStamp      string               `json:"source_stamp"`
	Before           float64              `json:"before"`
	After            float64              `json:"after"`
	Credited         bool                 `json:"credited"`
	AlgorithmVersion string               `json:"algorithm_version"`
	CreatedAt        time.Time            `json:"created_at"`
	Result           LearningAnswerResult `json:"result"`
}
