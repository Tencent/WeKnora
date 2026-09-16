package types

import "time"

// EvaluationHumanRatingRevision is one append-only human judgment revision.
type EvaluationHumanRatingRevision struct {
	ID       string `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"not null;uniqueIndex:uidx_evaluation_human_rating_revision"`
	//nolint:lll // The composite GORM index declaration is an atomic schema tag.
	TaskID         string    `json:"task_id" gorm:"type:varchar(128);not null;uniqueIndex:uidx_evaluation_human_rating_revision"`
	SampleIndex    int       `json:"sample_index" gorm:"not null;uniqueIndex:uidx_evaluation_human_rating_revision"`
	Revision       int       `json:"revision" gorm:"not null;uniqueIndex:uidx_evaluation_human_rating_revision"`
	RaterID        string    `json:"rater_id" gorm:"type:varchar(128);not null"`
	RubricKey      string    `json:"rubric_key" gorm:"type:varchar(64);not null"`
	RubricVersion  string    `json:"rubric_version" gorm:"type:varchar(32);not null"`
	RubricSnapshot JSON      `json:"rubric_snapshot" gorm:"type:jsonb;not null"`
	Score          int       `json:"score" gorm:"not null"`
	Comment        string    `json:"comment,omitempty" gorm:"not null;default:''"`
	SupersedesID   *string   `json:"supersedes_id,omitempty" gorm:"type:varchar(36);uniqueIndex"`
	CreatedAt      time.Time `json:"created_at" gorm:"not null"`
}

// TableName returns the append-only human rating table name.
func (EvaluationHumanRatingRevision) TableName() string { return "evaluation_human_ratings" }

// EvaluationHumanRatingInput carries one requested revision before server-owned identity fields are assigned.
type EvaluationHumanRatingInput struct {
	RubricKey      string
	RubricVersion  string
	RubricSnapshot JSON
	Score          int
	Comment        string
}
