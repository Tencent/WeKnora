package types

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
)

// KnowledgeBaseType represents the type of the knowledge base
const (
	// KnowledgeBaseTypeDocument represents the document knowledge base type
	KnowledgeBaseTypeDocument = "document"
	KnowledgeBaseTypeFAQ      = "faq"
)

// FAQIndexMode represents the FAQ index mode: only index questions or index questions and answers
type FAQIndexMode string

const (
	// FAQIndexModeQuestionOnly only index questions and similar questions
	FAQIndexModeQuestionOnly FAQIndexMode = "question_only"
	// FAQIndexModeQuestionAnswer index questions and answers together
	FAQIndexModeQuestionAnswer FAQIndexMode = "question_answer"
)

// FAQQuestionIndexMode represents the FAQ question index mode: index together or index separately
type FAQQuestionIndexMode string

const (
	// FAQQuestionIndexModeCombined index questions and similar questions together
	FAQQuestionIndexModeCombined FAQQuestionIndexMode = "combined"
	// FAQQuestionIndexModeSeparate index questions and similar questions separately
	FAQQuestionIndexModeSeparate FAQQuestionIndexMode = "separate"
)

// KnowledgeBase represents a knowledge base entity
type KnowledgeBase struct {
	// Unique identifier of the knowledge base
	ID string `yaml:"id"                      json:"id"                      gorm:"type:varchar(36);primaryKey"`
	// Name of the knowledge base
	Name string `yaml:"name"                    json:"name"`
	// Type of the knowledge base (document, faq, etc.)
	Type string `yaml:"type"                    json:"type"                    gorm:"type:varchar(32);default:'document'"`
	// Whether this knowledge base is temporary (ephemeral) and should be hidden from UI
	IsTemporary bool `yaml:"is_temporary"            json:"is_temporary"            gorm:"default:false"`
	// Description of the knowledge base
	Description string `yaml:"description"             json:"description"`
	// Tenant ID
	TenantID uint64 `yaml:"tenant_id"               json:"tenant_id"`
	// Chunking configuration
	ChunkingConfig ChunkingConfig `yaml:"chunking_config"         json:"chunking_config"         gorm:"type:json"`
	// Image processing configuration
	ImageProcessingConfig ImageProcessingConfig `yaml:"image_processing_config" json:"image_processing_config" gorm:"type:json"`
	// ID of the embedding model
	EmbeddingModelID string `yaml:"embedding_model_id"      json:"embedding_model_id"`
	// Summary model ID
	SummaryModelID string `yaml:"summary_model_id"        json:"summary_model_id"`
	// VLM config
	VLMConfig VLMConfig `yaml:"vlm_config"              json:"vlm_config"              gorm:"type:json"`
	// ASR config (Automatic Speech Recognition)
	ASRConfig ASRConfig `yaml:"asr_config"              json:"asr_config"              gorm:"type:json"`
	// Storage provider config (new): only stores provider selection; credentials from tenant StorageEngineConfig
	StorageProviderConfig *StorageProviderConfig `yaml:"storage_provider_config" json:"storage_provider_config"  gorm:"column:storage_provider_config;type:jsonb"`
	// Deprecated: legacy COS config column. Kept for backward compatibility with old data.
	StorageConfig StorageConfig `yaml:"-" json:"storage_config" gorm:"column:cos_config;type:json"`
	// Extract config
	ExtractConfig *ExtractConfig `yaml:"extract_config"          json:"extract_config"          gorm:"column:extract_config;type:json"`
	// FAQConfig stores FAQ specific configuration such as indexing strategy
	FAQConfig *FAQConfig `yaml:"faq_config"              json:"faq_config"              gorm:"column:faq_config;type:json"`
	// QuestionGenerationConfig stores question generation configuration for document knowledge bases
	QuestionGenerationConfig *QuestionGenerationConfig `yaml:"question_generation_config" json:"question_generation_config" gorm:"column:question_generation_config;type:json"`
	// Whether this knowledge base is pinned to the top of the list
	IsPinned bool `yaml:"is_pinned"               json:"is_pinned"               gorm:"default:false"`
	// Time when the knowledge base was pinned (nil if not pinned)
	PinnedAt *time.Time `yaml:"pinned_at"               json:"pinned_at"`
	// Creation time of the knowledge base
	CreatedAt time.Time `yaml:"created_at"              json:"created_at"`
	// Last updated time of the knowledge base
	UpdatedAt time.Time `yaml:"updated_at"              json:"updated_at"`
	// Deletion time of the knowledge base
	DeletedAt gorm.DeletedAt `yaml:"deleted_at"              json:"deleted_at"              gorm:"index"`
	// Knowledge count (not stored in database, calculated on query)
	KnowledgeCount int64 `yaml:"knowledge_count"         json:"knowledge_count"         gorm:"-"`
	// Chunk count (not stored in database, calculated on query)
	ChunkCount int64 `yaml:"chunk_count"             json:"chunk_count"             gorm:"-"`
	// IsProcessing indicates if there is a processing import task (for FAQ type knowledge bases)
	IsProcessing bool `yaml:"is_processing"           json:"is_processing"           gorm:"-"`
	// ProcessingCount indicates the number of knowledge items being processed (for document type knowledge bases)
	ProcessingCount int64 `yaml:"processing_count"        json:"processing_count"        gorm:"-"`
	// ShareCount indicates the number of organizations this knowledge base is shared with (not stored in database)
	ShareCount int64 `yaml:"share_count"             json:"share_count"             gorm:"-"`
}

// KnowledgeBaseConfig represents the knowledge base configuration
type KnowledgeBaseConfig struct {
	// Chunking configuration
	ChunkingConfig ChunkingConfig `yaml:"chunking_config"         json:"chunking_config"`
	// Image processing configuration
	ImageProcessingConfig ImageProcessingConfig `yaml:"image_processing_config" json:"image_processing_config"`
	// FAQ configuration (only for FAQ type knowledge bases)
	FAQConfig *FAQConfig `yaml:"faq_config"              json:"faq_config"`
}

// ParserEngineRule maps a set of file types to a specific parser engine.
type ParserEngineRule struct {
	FileTypes []string `yaml:"file_types" json:"file_types"`
	Engine    string   `yaml:"engine"     json:"engine"`
}

// ChunkingConfig represents the document splitting configuration
type ChunkingConfig struct {
	// Chunk size
	ChunkSize int `yaml:"chunk_size"    json:"chunk_size"`
	// Chunk overlap
	ChunkOverlap int `yaml:"chunk_overlap" json:"chunk_overlap"`
	// Separators
	Separators []string `yaml:"separators"    json:"separators"`
	// EnableMultimodal (deprecated, kept for backward compatibility with old data)
	EnableMultimodal bool `yaml:"enable_multimodal,omitempty" json:"enable_multimodal,omitempty"`
	// ParserEngineRules configures which parser engine to use for each file type.
	// When empty, the builtin engine is used for all types.
	ParserEngineRules []ParserEngineRule `yaml:"parser_engine_rules,omitempty" json:"parser_engine_rules,omitempty"`
	// EnableParentChild enables two-level parent-child chunking strategy.
	// When enabled, large parent chunks provide context while small child chunks
	// are used for vector matching. Retrieval matches on child but returns parent content.
	EnableParentChild bool `yaml:"enable_parent_child,omitempty" json:"enable_parent_child,omitempty"`
	// ParentChunkSize is the size of parent chunks (default: 4096).
	// Only used when EnableParentChild is true.
	ParentChunkSize int `yaml:"parent_chunk_size,omitempty" json:"parent_chunk_size,omitempty"`
	// ChildChunkSize is the size of child chunks used for embedding (default: 384).
	// Only used when EnableParentChild is true.
	ChildChunkSize int `yaml:"child_chunk_size,omitempty" json:"child_chunk_size,omitempty"`
}

// ResolveParserEngine returns the engine name for the given file type
// based on the configured rules. Returns empty string (builtin) when
// no rule matches.
func (c ChunkingConfig) ResolveParserEngine(fileType string) string {
	for _, rule := range c.ParserEngineRules {
		for _, ft := range rule.FileTypes {
			if ft == fileType {
				return rule.Engine
			}
		}
	}
	return ""
}

// StorageProviderConfig stores the KB-level storage provider selection.
// Credentials are managed at the tenant level (StorageEngineConfig).
type StorageProviderConfig struct {
	Provider string `yaml:"provider" json:"provider"` // "local", "minio", "cos", "tos"
}

func (c StorageProviderConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *StorageProviderConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Deprecated: StorageConfig is the legacy COS configuration stored in the cos_config column.
// New code should use StorageProviderConfig. Kept for backward compatibility with old data.
type StorageConfig struct {
	SecretID   string `yaml:"secret_id"   json:"secret_id"`
	SecretKey  string `yaml:"secret_key"  json:"secret_key"`
	Region     string `yaml:"region"      json:"region"`
	BucketName string `yaml:"bucket_name" json:"bucket_name"`
	AppID      string `yaml:"app_id"      json:"app_id"`
	PathPrefix string `yaml:"path_prefix" json:"path_prefix"`
	Provider   string `yaml:"provider"    json:"provider"`
}

func (c StorageConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *StorageConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// UnmarshalJSON keeps backward compatibility for legacy clients that still send
// `cos_config` or `storage_config`, while migrating to `storage_provider_config`.
func (kb *KnowledgeBase) UnmarshalJSON(data []byte) error {
	type alias KnowledgeBase
	aux := struct {
		*alias
		LegacyStorageConfig *StorageConfig `json:"cos_config"`
	}{
		alias: (*alias)(kb),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	// Backward compat: populate legacy StorageConfig from cos_config
	if aux.LegacyStorageConfig != nil && kb.StorageConfig == (StorageConfig{}) {
		kb.StorageConfig = *aux.LegacyStorageConfig
	}
	// Auto-populate StorageProviderConfig from legacy StorageConfig if not set
	if kb.StorageProviderConfig == nil && kb.StorageConfig.Provider != "" {
		kb.StorageProviderConfig = &StorageProviderConfig{Provider: kb.StorageConfig.Provider}
	}
	return nil
}

// GetStorageProvider returns the effective storage provider for this KB.
// Priority: StorageProviderConfig (new) > StorageConfig.Provider (legacy cos_config).
func (kb *KnowledgeBase) GetStorageProvider() string {
	if kb == nil {
		return ""
	}
	if kb.StorageProviderConfig != nil {
		p := strings.ToLower(strings.TrimSpace(kb.StorageProviderConfig.Provider))
		if p != "" && p != "__pending_env__" {
			return p
		}
	}
	return strings.ToLower(strings.TrimSpace(kb.StorageConfig.Provider))
}

// SetStorageProvider writes the provider to the new StorageProviderConfig field.
func (kb *KnowledgeBase) SetStorageProvider(provider string) {
	if kb == nil {
		return
	}
	kb.StorageProviderConfig = &StorageProviderConfig{Provider: provider}
}

// InferStorageFromFilePath deduces the storage provider from a file path format.
// Used as a safety fallback when the KB's configured provider doesn't match the data.
// Supports provider:// scheme (local://, minio://, cos://, tos://),
// unified /files/{provider}/... format, and legacy formats.
func InferStorageFromFilePath(filePath string) string {
	// Provider scheme format: provider://...
	if p := ParseProviderScheme(filePath); p != "" {
		return p
	}
	// Legacy formats
	switch {
	case strings.HasPrefix(filePath, "https://") && strings.Contains(filePath, ".cos."):
		return "cos"
	default:
		return ""
	}
}

// ParseProviderScheme extracts the provider from a provider:// scheme path.
// e.g. "minio://bucket/key" → "minio", "local://tenant/file.pdf" → "local"
// Returns "" if the path does not use a known provider scheme.
func ParseProviderScheme(filePath string) string {
	for _, provider := range []string{"local", "minio", "cos", "tos", "s3"} {
		if strings.HasPrefix(filePath, provider+"://") {
			return provider
		}
	}
	return ""
}

// ImageProcessingConfig represents the image processing configuration
type ImageProcessingConfig struct {
	// Model ID
	ModelID string `yaml:"model_id" json:"model_id"`
}

// Value implements the driver.Valuer interface, used to convert ChunkingConfig to database value
func (c ChunkingConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to ChunkingConfig
func (c *ChunkingConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Value implements the driver.Valuer interface, used to convert ImageProcessingConfig to database value
func (c ImageProcessingConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to ImageProcessingConfig
func (c *ImageProcessingConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// VLMConfig represents the VLM configuration
type VLMConfig struct {
	Enabled bool   `yaml:"enabled"  json:"enabled"`
	ModelID string `yaml:"model_id" json:"model_id"`

	// 兼容老版本
	// Model Name
	ModelName string `yaml:"model_name" json:"model_name"`
	// Base URL
	BaseURL string `yaml:"base_url" json:"base_url"`
	// API Key
	APIKey string `yaml:"api_key" json:"api_key"`
	// Interface Type: "ollama" or "openai"
	InterfaceType string `yaml:"interface_type" json:"interface_type"`
}

// IsEnabled 判断多模态是否启用（兼容新老版本）
// 新版本：Enabled && ModelID != ""
// 老版本：ModelName != "" && BaseURL != ""
func (c VLMConfig) IsEnabled() bool {
	// 新版本配置
	if c.Enabled && c.ModelID != "" {
		return true
	}
	// 兼容老版本配置
	if c.ModelName != "" && c.BaseURL != "" {
		return true
	}
	return false
}

// QuestionGenerationConfig represents the question generation configuration for document knowledge bases
// When enabled, the system will use LLM to generate questions for each chunk during document parsing
// These generated questions will be indexed separately to improve recall
type QuestionGenerationConfig struct {
	Enabled bool `yaml:"enabled"  json:"enabled"`
	// Number of questions to generate per chunk (default: 3, max: 10)
	QuestionCount int `yaml:"question_count" json:"question_count"`
}

// Value implements the driver.Valuer interface
func (c QuestionGenerationConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface
func (c *QuestionGenerationConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Value implements the driver.Valuer interface, used to convert VLMConfig to database value
func (c VLMConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to VLMConfig
func (c *VLMConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// ASRConfig represents the ASR (Automatic Speech Recognition) configuration
type ASRConfig struct {
	Enabled  bool   `yaml:"enabled"  json:"enabled"`
	ModelID  string `yaml:"model_id" json:"model_id"`
	Language string `yaml:"language" json:"language"` // optional: language hint for transcription
}

// IsASREnabled checks if ASR is enabled with a valid model
func (c ASRConfig) IsASREnabled() bool {
	return c.Enabled && c.ModelID != ""
}

// Value implements the driver.Valuer interface, used to convert ASRConfig to database value
func (c ASRConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to ASRConfig
func (c *ASRConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// ExtractConfig represents the extract configuration for a knowledge base
type ExtractConfig struct {
	Enabled   bool             `yaml:"enabled"   json:"enabled"`
	Text      string           `yaml:"text"      json:"text,omitempty"`
	Tags      []string         `yaml:"tags"      json:"tags,omitempty"`
	Nodes     []*GraphNode     `yaml:"nodes"     json:"nodes,omitempty"`
	Relations []*GraphRelation `yaml:"relations" json:"relations,omitempty"`
}

// Value implements the driver.Valuer interface, used to convert ExtractConfig to database value
func (e ExtractConfig) Value() (driver.Value, error) {
	return json.Marshal(e)
}

// Scan implements the sql.Scanner interface, used to convert database value to ExtractConfig
func (e *ExtractConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, e)
}

// FAQConfig 存储 FAQ 知识库的特有配置
type FAQConfig struct {
	IndexMode         FAQIndexMode         `yaml:"index_mode"          json:"index_mode"`
	QuestionIndexMode FAQQuestionIndexMode `yaml:"question_index_mode" json:"question_index_mode"`
}

// Value implements driver.Valuer
func (f FAQConfig) Value() (driver.Value, error) {
	return json.Marshal(f)
}

// Scan implements sql.Scanner
func (f *FAQConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, f)
}

// EnsureDefaults 确保类型与配置具备默认值
func (kb *KnowledgeBase) EnsureDefaults() {
	if kb == nil {
		return
	}
	if kb.Type == "" {
		kb.Type = KnowledgeBaseTypeDocument
	}
	if kb.Type != KnowledgeBaseTypeFAQ {
		kb.FAQConfig = nil
		return
	}
	if kb.FAQConfig == nil {
		kb.FAQConfig = &FAQConfig{
			IndexMode:         FAQIndexModeQuestionAnswer,
			QuestionIndexMode: FAQQuestionIndexModeCombined,
		}
		return
	}
	if kb.FAQConfig.IndexMode == "" {
		kb.FAQConfig.IndexMode = FAQIndexModeQuestionAnswer
	}
	if kb.FAQConfig.QuestionIndexMode == "" {
		kb.FAQConfig.QuestionIndexMode = FAQQuestionIndexModeCombined
	}
}

// IsMultimodalEnabled 判断多模态是否启用（兼容新老版本配置）
// 新版本：VLMConfig.IsEnabled()
// 老版本：ChunkingConfig.EnableMultimodal
func (kb *KnowledgeBase) IsMultimodalEnabled() bool {
	if kb == nil {
		return false
	}
	// 新版本配置优先
	if kb.VLMConfig.IsEnabled() {
		return true
	}
	// 兼容老版本：chunking_config 中的 enable_multimodal 字段
	if kb.ChunkingConfig.EnableMultimodal {
		return true
	}
	return false
}
