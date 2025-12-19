package types

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	// KnowledgeTypeManual represents the manual knowledge type
	KnowledgeTypeManual = "manual"
	// KnowledgeTypeFAQ represents the FAQ knowledge type
	KnowledgeTypeFAQ = "faq"
)

// Knowledge parse status constants
const (
	// ParseStatusPending indicates the knowledge is waiting to be processed
	ParseStatusPending = "pending"
	// ParseStatusProcessing indicates the knowledge is being processed
	ParseStatusProcessing = "processing"
	// ParseStatusCompleted indicates the knowledge has been processed successfully
	ParseStatusCompleted = "completed"
	// ParseStatusFailed indicates the knowledge processing failed
	ParseStatusFailed = "failed"
	// ParseStatusDeleting indicates the knowledge is being deleted (used to prevent async task conflicts)
	ParseStatusDeleting = "deleting"
)

// Summary status constants for async summary generation
const (
	// SummaryStatusNone indicates no summary task is needed
	SummaryStatusNone = "none"
	// SummaryStatusPending indicates the summary task is waiting to be processed
	SummaryStatusPending = "pending"
	// SummaryStatusProcessing indicates the summary is being generated
	SummaryStatusProcessing = "processing"
	// SummaryStatusCompleted indicates the summary has been generated successfully
	SummaryStatusCompleted = "completed"
	// SummaryStatusFailed indicates the summary generation failed
	SummaryStatusFailed = "failed"
)

// ManualKnowledgeFormat represents the format of the manual knowledge
const (
	ManualKnowledgeFormatMarkdown = "markdown"
	ManualKnowledgeStatusDraft    = "draft"
	ManualKnowledgeStatusPublish  = "publish"
)

// Knowledge represents a knowledge entity in the system.
// It contains metadata about the knowledge source, its processing status,
// and references to the physical file if applicable.
type Knowledge struct {
	// Unique identifier of the knowledge
	ID string `json:"id"                 gorm:"type:varchar(36);primaryKey"`
	// Tenant ID
	TenantID uint64 `json:"tenant_id"`
	// ID of the knowledge base
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// Optional tag ID for categorization within a knowledge base
	TagID string `json:"tag_id"             gorm:"type:varchar(36);index"`
	// Type of the knowledge
	Type string `json:"type"`
	// Title of the knowledge
	Title string `json:"title"`
	// Description of the knowledge
	Description string `json:"description"`
	// Source of the knowledge
	Source string `json:"source"`
	// Parse status of the knowledge
	ParseStatus string `json:"parse_status"`
	// Summary status for async summary generation
	SummaryStatus string `json:"summary_status"     gorm:"type:varchar(32);default:none"`
	// Enable status of the knowledge
	EnableStatus string `json:"enable_status"`
	// ID of the embedding model
	EmbeddingModelID string `json:"embedding_model_id"`
	// File name of the knowledge
	FileName string `json:"file_name"`
	// File type of the knowledge
	FileType string `json:"file_type"`
	// File size of the knowledge
	FileSize int64 `json:"file_size"`
	// File hash of the knowledge
	FileHash string `json:"file_hash"`
	// File path of the knowledge
	FilePath string `json:"file_path"`
	// Storage size of the knowledge
	StorageSize int64 `json:"storage_size"`
	// Metadata of the knowledge
	Metadata JSON `json:"metadata"           gorm:"type:json"`
	// Creation time of the knowledge
	CreatedAt time.Time `json:"created_at"`
	// Last updated time of the knowledge
	UpdatedAt time.Time `json:"updated_at"`
	// Processed time of the knowledge
	ProcessedAt *time.Time `json:"processed_at"`
	// Error message of the knowledge
	ErrorMessage string `json:"error_message"`
	// Deletion time of the knowledge
	DeletedAt gorm.DeletedAt `json:"deleted_at"         gorm:"index"`
	// Knowledge base name (not stored in database, populated on query)
	KnowledgeBaseName string `json:"knowledge_base_name" gorm:"-"`
}

// GetMetadata returns the metadata as a map[string]string.
func (k *Knowledge) GetMetadata() map[string]string {
	metadata := make(map[string]string)
	metadataMap, err := k.Metadata.Map()
	if err != nil {
		return nil
	}
	for k, v := range metadataMap {
		metadata[k] = fmt.Sprintf("%v", v)
	}
	return metadata
}

// BeforeCreate hook generates a UUID for new Knowledge entities before they are created.
func (k *Knowledge) BeforeCreate(tx *gorm.DB) (err error) {
	if k.ID == "" {
		k.ID = uuid.New().String()
	}
	return nil
}

// ManualKnowledgeMetadata stores metadata for manual Markdown knowledge content.
type ManualKnowledgeMetadata struct {
	Content   string `json:"content"`
	Format    string `json:"format"`
	Status    string `json:"status"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at"`
}

// ManualKnowledgePayload represents the payload for manual knowledge operations.
type ManualKnowledgePayload struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// NewManualKnowledgeMetadata creates a new ManualKnowledgeMetadata instance.
func NewManualKnowledgeMetadata(content, status string, version int) *ManualKnowledgeMetadata {
	if version <= 0 {
		version = 1
	}
	return &ManualKnowledgeMetadata{
		Content:   content,
		Format:    ManualKnowledgeFormatMarkdown,
		Status:    status,
		Version:   version,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// ToJSON converts the metadata to JSON type.
func (m *ManualKnowledgeMetadata) ToJSON() (JSON, error) {
	if m == nil {
		return nil, nil
	}
	if m.Format == "" {
		m.Format = ManualKnowledgeFormatMarkdown
	}
	if m.Status == "" {
		m.Status = ManualKnowledgeStatusDraft
	}
	if m.Version <= 0 {
		m.Version = 1
	}
	if m.UpdatedAt == "" {
		m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	bytes, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return JSON(bytes), nil
}

// ManualMetadata parses and returns manual knowledge metadata.
func (k *Knowledge) ManualMetadata() (*ManualKnowledgeMetadata, error) {
	if len(k.Metadata) == 0 {
		return nil, nil
	}
	var metadata ManualKnowledgeMetadata
	if err := json.Unmarshal(k.Metadata, &metadata); err != nil {
		return nil, err
	}
	if metadata.Format == "" {
		metadata.Format = ManualKnowledgeFormatMarkdown
	}
	if metadata.Version <= 0 {
		metadata.Version = 1
	}
	return &metadata, nil
}

// SetManualMetadata sets manual knowledge metadata onto the knowledge instance.
func (k *Knowledge) SetManualMetadata(meta *ManualKnowledgeMetadata) error {
	if meta == nil {
		k.Metadata = nil
		return nil
	}
	jsonValue, err := meta.ToJSON()
	if err != nil {
		return err
	}
	k.Metadata = jsonValue
	return nil
}

// IsManual returns true if the knowledge item is manual Markdown knowledge.
func (k *Knowledge) IsManual() bool {
	return k != nil && k.Type == KnowledgeTypeManual
}

// EnsureManualDefaults sets default values for manual knowledge entries.
func (k *Knowledge) EnsureManualDefaults() {
	if k == nil {
		return
	}
	if k.Type == "" {
		k.Type = KnowledgeTypeManual
	}
	if k.FileType == "" {
		k.FileType = KnowledgeTypeManual
	}
	if k.Source == "" {
		k.Source = KnowledgeTypeManual
	}
}

// IsDraft returns whether the payload should be saved as draft.
func (p ManualKnowledgePayload) IsDraft() bool {
	return p.Status == "" || p.Status == ManualKnowledgeStatusDraft
}

// KnowledgeCheckParams defines parameters used to check if knowledge already exists.
type KnowledgeCheckParams struct {
	// File parameters
	FileName string
	FileSize int64
	FileHash string
	// URL parameters
	URL string
	// Text passage parameters
	Passages []string
	// Knowledge type
	Type string
}
