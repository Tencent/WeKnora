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

// Channel constants identify through which channel a knowledge entry was ingested.
// Aligned with Message.Channel values ("web", "api", "im") but allows finer granularity.
const (
	ChannelWeb              = "web"               // Web UI (default)
	ChannelAPI              = "api"               // External API call
	ChannelBrowserExtension = "browser_extension" // Browser extension / plugin
	ChannelWechat           = "wechat"            // WeChat
	ChannelWecom            = "wecom"             // WeCom (企业微信)
	ChannelFeishu           = "feishu"            // Feishu / Lark
	ChannelDingtalk         = "dingtalk"          // DingTalk
	ChannelSlack            = "slack"             // Slack
	ChannelIM               = "im"                // Generic IM channel
	ChannelNotion           = "notion"            // Notion
	ChannelYuque            = "yuque"             // Yuque (语雀)
	ChannelRSS              = "rss"               // RSS / Atom feed
)

// Knowledge parse status constants
const (
	// ParseStatusPending indicates the knowledge is waiting to be processed
	ParseStatusPending = "pending"
	// ParseStatusProcessing indicates the knowledge is being processed
	// (DocReader / chunking / embedding stage).
	ParseStatusProcessing = "processing"
	// ParseStatusFinalizing indicates the primary parse has finished but
	// enrichment subtasks (summary, question generation, graph extract)
	// are still in flight. The user-facing intuition behind this state is
	// "the document is queryable for vector search but is still spending
	// resources" — cancel-parse can interrupt enrichment from here.
	// pending_subtasks_count holds the outstanding subtask count; the
	// last subtask to finish atomically promotes the row to completed.
	ParseStatusFinalizing = "finalizing"
	// ParseStatusCompleted indicates the knowledge has been processed
	// successfully AND every enrichment subtask has reached a terminal
	// state. No further resources will be spent on the document until
	// the user explicitly re-parses it.
	ParseStatusCompleted = "completed"
	// ParseStatusFailed indicates the knowledge processing failed
	ParseStatusFailed = "failed"
	// ParseStatusDeleting indicates the knowledge is being deleted (used to prevent async task conflicts)
	ParseStatusDeleting = "deleting"
	// ParseStatusCancelled indicates parsing was cancelled by the user.
	// Same short-circuit semantics as ParseStatusDeleting for in-flight and
	// queued downstream tasks, but the knowledge row and any already-written
	// chunks/index are kept so the user can re-trigger parsing via reparse.
	ParseStatusCancelled = "cancelled"
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

// KnowledgeListFilter aggregates optional filters for listing knowledge entries
// under a knowledge base. Empty / zero fields mean "no filter on that dimension".
type KnowledgeListFilter struct {
	// FolderIDSet distinguishes no folder filter from an explicit root filter.
	// A nil FolderID with FolderIDSet=true means the root directory.
	FolderIDSet              bool
	FolderID                 *string
	IncludeFolderDescendants bool
	// TagIDs filters by multiple tags (OR semantics: match any of the given tags).
	TagIDs []string
	// Keyword performs a LIKE match on file_name / title when non-empty.
	Keyword string
	// FileType filters by file_type, or by type for the special values "manual" / "url".
	FileType string
	// ParseStatus filters by parse_status when non-empty (e.g. pending, processing, completed, failed).
	ParseStatus string
	// Source filters by ingestion channel when non-empty (web, api, feishu, notion, wechat, ...).
	// The special values "manual" and "url" are routed to the `type` column to match
	// FileType semantics, so callers can filter "manually created" / "URL imported" entries.
	Source string
	// UpdatedFrom, when non-zero, keeps rows with updated_at >= UpdatedFrom.
	UpdatedFrom time.Time
	// UpdatedTo, when non-zero, keeps rows with updated_at <= UpdatedTo.
	UpdatedTo time.Time
}

// Knowledge represents a knowledge entity in the system.
// It contains metadata about the knowledge source, its processing status,
// and references to the physical file if applicable.
type Knowledge struct {
	// Unique identifier of the knowledge
	ID string `json:"id"                 gorm:"type:varchar(36);primaryKey"`
	// Workspace ID
	TenantID uint64 `json:"tenant_id"`
	// ID of the knowledge base
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// FolderID is nil for documents stored at the knowledge-base root.
	FolderID *string `json:"folder_id" gorm:"type:varchar(36);default:null"`
	// Tags holds the tags associated with this knowledge (populated on query, not persisted directly).
	Tags []*KnowledgeTag `json:"tags"               gorm:"-"`
	// Type of the knowledge
	Type string `json:"type"`
	// Title of the knowledge
	Title string `json:"title"`
	// Description of the knowledge
	Description string `json:"description"`
	// Source of the knowledge (e.g. URL address for url type, "manual" for manual type)
	Source string `json:"source"             gorm:"type:varchar(2048)"`
	// Channel indicates through which channel the knowledge was ingested (web, api, browser_extension, wechat, etc.)
	Channel string `json:"channel"            gorm:"type:varchar(50);default:'web'"`
	// Parse status of the knowledge
	ParseStatus string `json:"parse_status"`
	// PendingSubtasksCount is the outstanding enrichment subtask count
	// (summary + question + graph chunks). Only meaningful while
	// ParseStatus == "finalizing"; defaults to 0 in any terminal state.
	PendingSubtasksCount int `json:"pending_subtasks_count" gorm:"type:int;not null;default:0"`
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
	// Last FAQ import result (for FAQ type knowledge only)
	LastFAQImportResult JSON `json:"last_faq_import_result" gorm:"type:json"`
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
	if len(k.Metadata) == 0 {
		return metadata
	}
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
	Title         string                     `json:"title"`
	Content       string                     `json:"content"`
	Status        string                     `json:"status"`
	TagIDs        []string                   `json:"tag_ids"`
	Channel       string                     `json:"channel"`
	ProcessConfig *KnowledgeProcessOverrides `json:"process_config,omitempty"`
}

// KnowledgeSearchScope defines a (tenant_id, knowledge_base_id) scope for knowledge search (e.g. own KBs + shared KBs).
type KnowledgeSearchScope struct {
	TenantID uint64
	KBID     string
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

// SetLastFAQImportResult sets FAQ import result to the dedicated field.
func (k *Knowledge) SetLastFAQImportResult(result *FAQImportResult) error {
	if result == nil {
		k.LastFAQImportResult = nil
		return nil
	}
	jsonValue, err := result.ToJSON()
	if err != nil {
		return err
	}
	k.LastFAQImportResult = jsonValue
	return nil
}

// GetLastFAQImportResult parses and returns FAQ import result from the dedicated field.
func (k *Knowledge) GetLastFAQImportResult() (*FAQImportResult, error) {
	if len(k.LastFAQImportResult) == 0 {
		return nil, nil
	}
	var result FAQImportResult
	if err := json.Unmarshal(k.LastFAQImportResult, &result); err != nil {
		return nil, err
	}
	return &result, nil
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
	if k.Channel == "" {
		k.Channel = ChannelWeb
	}
}

// IsDraft returns whether the payload should be saved as draft.
func (p ManualKnowledgePayload) IsDraft() bool {
	return p.Status == "" || p.Status == ManualKnowledgeStatusDraft
}

const metadataKeyProcessOverrides = "process_overrides"

// ProcessOverrides parses process config overrides from knowledge metadata.
func (k *Knowledge) ProcessOverrides() (*KnowledgeProcessOverrides, error) {
	if k == nil || len(k.Metadata) == 0 {
		return nil, nil
	}
	metadataMap, err := k.Metadata.Map()
	if err != nil {
		return nil, err
	}
	raw, ok := metadataMap[metadataKeyProcessOverrides]
	if !ok || raw == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var overrides KnowledgeProcessOverrides
	if err := json.Unmarshal(bytes, &overrides); err != nil {
		return nil, err
	}
	return &overrides, nil
}

// SetProcessOverrides merges process config overrides into knowledge metadata.
func (k *Knowledge) SetProcessOverrides(o *KnowledgeProcessOverrides) error {
	if k == nil {
		return nil
	}
	metadataMap, err := k.Metadata.Map()
	if err != nil {
		return err
	}
	if o == nil {
		delete(metadataMap, metadataKeyProcessOverrides)
	} else {
		bytes, err := json.Marshal(o)
		if err != nil {
			return err
		}
		var value interface{}
		if err := json.Unmarshal(bytes, &value); err != nil {
			return err
		}
		metadataMap[metadataKeyProcessOverrides] = value
	}
	bytes, err := json.Marshal(metadataMap)
	if err != nil {
		return err
	}
	k.Metadata = JSON(bytes)
	return nil
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
