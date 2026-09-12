package types

import (
	"time"
)

// Evaluation dataset scopes control cross-tenant visibility.
const (
	// EvaluationDatasetScopeSystem marks a built-in dataset readable by every tenant.
	EvaluationDatasetScopeSystem = "system"
	// EvaluationDatasetScopeTenant marks a dataset owned by exactly one tenant.
	EvaluationDatasetScopeTenant = "tenant"
)

// EvaluationDatasetSchemaVersion is the canonical content schema frozen for dataset versions.
const EvaluationDatasetSchemaVersion = 1

// EvaluationDataset is the database identity of one evaluation dataset.
type EvaluationDataset struct {
	ID               string    `json:"id" gorm:"type:varchar(64);primaryKey"`
	Scope            string    `json:"scope" gorm:"type:varchar(16);not null"`
	OwnerTenantID    *uint64   `json:"owner_tenant_id,omitempty"`
	Name             string    `json:"name" gorm:"type:varchar(255);not null"`
	Description      string    `json:"description" gorm:"not null;default:''"`
	CurrentVersionID string    `json:"current_version_id,omitempty" gorm:"type:varchar(64)"`
	CreatedAt        time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt        time.Time `json:"updated_at" gorm:"not null"`
}

// TableName binds EvaluationDataset to the registry table.
func (EvaluationDataset) TableName() string { return "evaluation_datasets" }

// EvaluationDatasetVersion is one immutable dataset version. ID is the globally
// unique dataset_version_id used by APIs, task records, and metric plans;
// VersionNumber is only a human-readable per-dataset sequence.
type EvaluationDatasetVersion struct {
	ID             string    `json:"id" gorm:"type:varchar(64);primaryKey"`
	DatasetID      string    `json:"dataset_id" gorm:"type:varchar(64);not null"`
	VersionNumber  int       `json:"version_number" gorm:"not null"`
	SchemaVersion  int       `json:"schema_version" gorm:"not null"`
	ArtifactSHA256 string    `json:"artifact_sha256" gorm:"type:char(64);not null"`
	ContentSHA256  string    `json:"content_sha256" gorm:"type:char(64);not null"`
	Manifest       JSON      `json:"manifest" gorm:"not null"`
	PassageCount   int       `json:"passage_count" gorm:"not null"`
	QuestionCount  int       `json:"question_count" gorm:"not null"`
	RelevanceCount int       `json:"relevance_count" gorm:"not null"`
	CreatedAt      time.Time `json:"created_at" gorm:"not null"`
}

// TableName binds EvaluationDatasetVersion to the version table.
func (EvaluationDatasetVersion) TableName() string { return "evaluation_dataset_versions" }

// EvaluationDatasetPassage is one corpus passage of an immutable version.
type EvaluationDatasetPassage struct {
	DatasetVersionID string `json:"dataset_version_id" gorm:"type:varchar(64);primaryKey"`
	PID              string `json:"pid" gorm:"column:pid;type:varchar(128);primaryKey"`
	Content          string `json:"content" gorm:"not null"`
	Metadata         JSON   `json:"metadata" gorm:"not null"`
}

// TableName binds EvaluationDatasetPassage to the passage table.
func (EvaluationDatasetPassage) TableName() string { return "evaluation_dataset_passages" }

// EvaluationDatasetQuestion is one question of an immutable version with a
// stable sample_index assigned at creation time.
type EvaluationDatasetQuestion struct {
	DatasetVersionID string `json:"dataset_version_id" gorm:"type:varchar(64);primaryKey"`
	QID              string `json:"qid" gorm:"column:qid;type:varchar(128);primaryKey"`
	SampleIndex      int    `json:"sample_index" gorm:"not null"`
	Question         string `json:"question" gorm:"not null"`
	Answer           string `json:"answer" gorm:"not null;default:''"`
}

// TableName binds EvaluationDatasetQuestion to the question table.
func (EvaluationDatasetQuestion) TableName() string { return "evaluation_dataset_questions" }

// EvaluationDatasetRelevance is one graded question-passage relevance edge.
type EvaluationDatasetRelevance struct {
	DatasetVersionID string `json:"dataset_version_id" gorm:"type:varchar(64);primaryKey"`
	QID              string `json:"qid" gorm:"column:qid;type:varchar(128);primaryKey"`
	PID              string `json:"pid" gorm:"column:pid;type:varchar(128);primaryKey"`
	Grade            int    `json:"grade" gorm:"not null"`
}

// TableName binds EvaluationDatasetRelevance to the relevance table.
func (EvaluationDatasetRelevance) TableName() string { return "evaluation_dataset_relevance" }

// EvaluationDatasetPassageInput is one passage in a version-creation request.
type EvaluationDatasetPassageInput struct {
	PID      string `json:"pid"`
	Content  string `json:"content"`
	Metadata JSON   `json:"metadata,omitempty"`
}

// EvaluationDatasetQuestionInput is one question in a version-creation request.
// The position in the request array fixes the stable sample_index.
type EvaluationDatasetQuestionInput struct {
	QID      string `json:"qid"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// EvaluationDatasetRelevanceInput is one relevance edge in a version-creation request.
type EvaluationDatasetRelevanceInput struct {
	QID   string `json:"qid"`
	PID   string `json:"pid"`
	Grade int    `json:"grade"`
}

// EvaluationDatasetVersionInput is the structured content of one new immutable version.
type EvaluationDatasetVersionInput struct {
	Passages  []EvaluationDatasetPassageInput   `json:"passages"`
	Questions []EvaluationDatasetQuestionInput  `json:"questions"`
	Relevance []EvaluationDatasetRelevanceInput `json:"relevance"`
}

// EvaluationDatasetVersionContent is the full ordered content of one version
// read back from the registry for execution or hashing.
type EvaluationDatasetVersionContent struct {
	Version   *EvaluationDatasetVersion
	Passages  []EvaluationDatasetPassage
	Questions []EvaluationDatasetQuestion
	Relevance []EvaluationDatasetRelevance
}
