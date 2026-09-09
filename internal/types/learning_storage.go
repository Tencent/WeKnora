package types

import "time"

// Learning resource limits bound source selection and generation leases.
const (
	LearningMaxNodes      = 2000
	LearningMaxChunks     = 12
	LearningChunkBytes    = 2000
	LearningLeaseDuration = 210 * time.Second
)

// LearningProfile retains only an off switch and monotonic fence
// after a full clear. Removing the row would permit delete/re-enable ABA.
type LearningProfile struct {
	TenantID  uint64 `gorm:"primaryKey;autoIncrement:false" json:"-"`
	SubjectID string `gorm:"primaryKey;type:varchar(128)" json:"-"`
	Enabled   bool   `json:"-"`
	Epoch     int64  `json:"-"`
}

// LearningMastery stores assessed progress separately from viewing history.
type LearningMastery struct {
	TenantID           uint64     `gorm:"primaryKey;autoIncrement:false" json:"-"`
	SubjectID          string     `gorm:"primaryKey;type:varchar(128)" json:"-"`
	PageID             string     `gorm:"primaryKey;type:varchar(36)" json:"-"`
	KnowledgeBaseID    string     `gorm:"type:varchar(36);index" json:"-"`
	PMastery           float64    `json:"-"`
	Attempts           int        `json:"-"`
	Correct            int        `json:"-"`
	ConsecutiveCorrect int        `json:"-"`
	LastAssessedAt     *time.Time `json:"-"`
	NextReviewAt       *time.Time `json:"-"`
	ViewedAt           *time.Time `json:"-"`
	SourceStamp        string     `gorm:"type:varchar(64)" json:"-"`
}

// LearningQuiz stores generation state, source identity and the worker lease.
type LearningQuiz struct {
	ID                 string     `gorm:"primaryKey;type:varchar(36)" json:"-"`
	TenantID           uint64     `gorm:"index:idx_learning_quiz_scope,priority:1" json:"-"`
	SubjectID          string     `gorm:"type:varchar(128);index:idx_learning_quiz_scope,priority:2" json:"-"`
	PageID             string     `gorm:"type:varchar(36);index:idx_learning_quiz_scope,priority:3" json:"-"`
	KnowledgeBaseID    string     `gorm:"type:varchar(36);index" json:"-"`
	Epoch              int64      `json:"-"`
	SourceStamp        string     `gorm:"type:varchar(64)" json:"-"`
	SourceKnowledgeIDs []string   `gorm:"serializer:json;type:text" json:"-"`
	Status             string     `gorm:"type:varchar(16);index:idx_learning_quiz_recovery,priority:1" json:"-"`
	ErrorCode          string     `gorm:"type:varchar(32)" json:"-"`
	LeaseToken         string     `gorm:"type:varchar(36)" json:"-"`
	LeaseUntil         *time.Time `gorm:"index:idx_learning_quiz_recovery,priority:2" json:"-"`
	Claims             int        `json:"-"`
	ModelID            string     `gorm:"type:varchar(64)" json:"-"`
	PromptVersion      string     `gorm:"type:varchar(32)" json:"-"`
	CreatedAt          time.Time  `json:"-"`
	UpdatedAt          time.Time  `json:"-"`
}

// LearningQuestion must never be used as an HTTP DTO or logged. Even accidental
// JSON serialization omits the answer key, explanation and evidence.
type LearningQuestion struct {
	ID            string             `gorm:"primaryKey;type:varchar(36)" json:"-"`
	QuizID        string             `gorm:"type:varchar(36);index" json:"-"`
	Position      int                `json:"-"`
	Prompt        string             `json:"-"`
	Options       []LearningOption   `gorm:"serializer:json;type:text" json:"-"`
	CorrectOption string             `json:"-"`
	Explanation   string             `json:"-"`
	Evidence      []LearningEvidence `gorm:"serializer:json;type:text" json:"-"`
	Fingerprint   string             `gorm:"type:varchar(64)" json:"-"`
}

// LearningAttempt stores an idempotent answer and its effect on mastery.
type LearningAttempt struct {
	TenantID   uint64 `gorm:"primaryKey;autoIncrement:false;uniqueIndex:idx_learning_answer_once,priority:1" json:"-"`
	SubjectID  string `gorm:"primaryKey;type:varchar(128);uniqueIndex:idx_learning_answer_once,priority:2" json:"-"`
	AttemptID  string `gorm:"primaryKey;type:varchar(64)" json:"-"`
	QuestionID string `gorm:"type:varchar(36);uniqueIndex:idx_learning_answer_once,priority:3" json:"-"`

	QuizID           string               `gorm:"type:varchar(36);index" json:"-"`
	PageID           string               `gorm:"type:varchar(36);index" json:"-"`
	KnowledgeBaseID  string               `gorm:"type:varchar(36);index" json:"-"`
	Fingerprint      string               `gorm:"type:varchar(64)" json:"-"`
	SourceStamp      string               `gorm:"type:varchar(64)" json:"-"`
	Before           float64              `json:"-"`
	After            float64              `json:"-"`
	Credited         bool                 `json:"-"`
	AlgorithmVersion string               `gorm:"type:varchar(32)" json:"-"`
	Result           LearningAnswerResult `gorm:"serializer:json;type:text" json:"-"`
	CreatedAt        time.Time            `json:"-"`
}

// TableName returns the learning profile table.
func (LearningProfile) TableName() string { return "learning_profiles" }

// TableName returns the assessed mastery table.
func (LearningMastery) TableName() string { return "learning_mastery" }

// TableName returns the quiz generation table.
func (LearningQuiz) TableName() string { return "learning_quizzes" }

// TableName returns the private question table.
func (LearningQuestion) TableName() string { return "learning_questions" }

// TableName returns the answer attempt table.
func (LearningAttempt) TableName() string { return "learning_attempts" }

// LearningSource contains the admitted source material for quiz generation.
type LearningSource struct {
	Variation string
	Page      *WikiPage
	Chunks    []*Chunk
	Stamp     string
	ModelID   string
}

// LearningNode joins a Wiki page with its learner-specific assessment state.
type LearningNode struct {
	Page        *WikiPage
	Mastery     *LearningMastery
	SourceStamp string
	Related     bool
}

// LearningClaim binds a leased quiz to its validated source snapshot.
type LearningClaim struct {
	Quiz   LearningQuiz
	Source LearningSource
}

// LearningGeneratePayload contains no identity or source text; the database resolves the owner.
type LearningGeneratePayload struct {
	QuizID string `json:"quiz_id"`
	Epoch  int64  `json:"epoch"`
}
