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
	KnowledgeBaseTypeWiki     = "wiki"
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
	// CreatorID records the user ID of whoever originally created the KB.
	// Used by the tenant-level RBAC middleware to let Contributors edit
	// their own KBs without granting them access to everyone else's.
	// Nullable for backward compatibility with rows created before the
	// RBAC migration backfilled the column to the tenant Owner.
	CreatorID string `yaml:"creator_id"              json:"creator_id"              gorm:"type:varchar(36);index"`
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
	// VectorStoreID references the VectorStore this knowledge base is bound to.
	// When nil, the KB falls back to the tenant's effective engines derived from
	// the RETRIEVE_DRIVER environment variable (env store flow).
	// This field is set once at creation time and must not be modified afterwards;
	// enforcement lives at the GORM layer (`<-:create`) plus the service-layer
	// KB update path, which omits this field from its update DTO.
	VectorStoreID *string `yaml:"vector_store_id"         json:"vector_store_id,omitempty" gorm:"column:vector_store_id;type:varchar(36);<-:create"`
	// Extract config
	ExtractConfig *ExtractConfig `yaml:"extract_config"          json:"extract_config"          gorm:"column:extract_config;type:json"`
	// FAQConfig stores FAQ specific configuration such as indexing strategy
	FAQConfig *FAQConfig `yaml:"faq_config"              json:"faq_config"              gorm:"column:faq_config;type:json"`
	// QuestionGenerationConfig stores question generation configuration for document knowledge bases
	QuestionGenerationConfig *QuestionGenerationConfig `yaml:"question_generation_config" json:"question_generation_config" gorm:"column:question_generation_config;type:json"`
	// WikiConfig stores wiki-specific configuration (only for wiki type knowledge bases)
	WikiConfig *WikiConfig `yaml:"wiki_config"             json:"wiki_config"             gorm:"column:wiki_config;type:json"`
	// IndexingStrategy controls which indexing pipelines are active for this knowledge base.
	// Pipelines: vector search, keyword search, wiki generation, knowledge graph extraction.
	IndexingStrategy IndexingStrategy `yaml:"indexing_strategy"       json:"indexing_strategy"       gorm:"column:indexing_strategy;type:json"`
	// IsPinned and PinnedAt are computed per-caller from user_kb_pins
	// (see migration 000050). They used to be stored on the row itself,
	// which made pinning a tenant-wide ordering decision gated behind
	// the kb-edit RBAC guard. The columns are still present in legacy
	// schemas for rollback safety but are no longer read or written by
	// the application — both fields are tagged `gorm:"-"` so GORM
	// ignores them on every CRUD call and the list handler stamps them
	// after enriching with the caller's pin set.
	IsPinned bool `yaml:"is_pinned"               json:"is_pinned"               gorm:"-"`
	// PinnedAt records when the current caller pinned this knowledge
	// base; nil when they have not.
	PinnedAt *time.Time `yaml:"pinned_at"               json:"pinned_at"               gorm:"-"`
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
	// CreatorName 是 CreatorID 对应用户的展示名（username / email 等），
	// 仅在列表场景由 handler 批量回填，不落库；为空表示创建者无法解析（用户已删除、
	// CreatorID 为空的老数据等）。前端用它在卡片来源徽章上做 mine vs tenant 的二分。
	CreatorName string `yaml:"-"                       json:"creator_name,omitempty"  gorm:"-"`
}

// KnowledgeBaseConfig represents the knowledge base configuration
type KnowledgeBaseConfig struct {
	// Chunking configuration
	ChunkingConfig ChunkingConfig `yaml:"chunking_config"         json:"chunking_config"`
	// Image processing configuration
	ImageProcessingConfig ImageProcessingConfig `yaml:"image_processing_config" json:"image_processing_config"`
	// FAQ configuration (only for FAQ type knowledge bases)
	FAQConfig *FAQConfig `yaml:"faq_config"              json:"faq_config"`
	// Wiki configuration (only for wiki-enabled knowledge bases)
	WikiConfig *WikiConfig `yaml:"wiki_config"             json:"wiki_config"`
	// IndexingStrategy controls which indexing pipelines are active.
	// nil means "no change" when updating (preserves existing strategy).
	IndexingStrategy *IndexingStrategy `yaml:"indexing_strategy"       json:"indexing_strategy"`
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
	// Strategy selects the adaptive chunking tier. Empty / "legacy" preserves
	// the historical recursive splitter; "auto" lets a profiler pick between
	// heading-aware, heuristic and recursive tiers; "heading" / "heuristic" /
	// "recursive" pin the tier explicitly.
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	// TokenLimit caps chunk size in approximate tokens. 0 = use ChunkSize
	// as a character count.
	TokenLimit int `yaml:"token_limit,omitempty" json:"token_limit,omitempty"`
	// Languages hints the heuristic patterns. Empty = auto-detect from content.
	// Examples: ["de"], ["en", "zh"].
	Languages []string `yaml:"languages,omitempty" json:"languages,omitempty"`
	// TableMetadataInstructions contains optional business guidance used when
	// generating searchable summaries for CSV/Excel tables. The system-owned
	// output contract remains fixed; these instructions only add domain context.
	TableMetadataInstructions string `yaml:"table_metadata_instructions,omitempty" json:"table_metadata_instructions,omitempty"`
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
	Provider string `yaml:"provider" json:"provider"` // "local", "minio", "cos", "tos", "s3", "oss", "ks3", "obs"
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
	// Secret ID (COS) / Access Key ID (S3, MinIO)
	SecretID string `yaml:"secret_id"   json:"secret_id"`
	// Secret Key (COS) / Secret Access Key (S3, MinIO)
	SecretKey string `yaml:"secret_key"  json:"secret_key"`
	// Region
	Region string `yaml:"region"      json:"region"`
	// Bucket Name
	BucketName string `yaml:"bucket_name" json:"bucket_name"`
	// App ID (COS specific)
	AppID string `yaml:"app_id"      json:"app_id"`
	// Path Prefix
	PathPrefix string `yaml:"path_prefix" json:"path_prefix"`
	// Provider: "cos", "minio", "s3"
	Provider string `yaml:"provider"    json:"provider"`
	// Endpoint (S3 specific) - e.g., s3.amazonaws.com, oss-cn-hangzhou.aliyuncs.com
	Endpoint string `yaml:"endpoint"    json:"endpoint,omitempty"`
	// UseSSL (S3 specific) - whether to use HTTPS
	UseSSL bool `yaml:"use_ssl"     json:"use_ssl,omitempty"`
	// ForcePathStyle (S3 specific) - whether to use path-style URLs
	ForcePathStyle bool `yaml:"force_path_style" json:"force_path_style,omitempty"`
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

// EffectiveStorageProvider returns the KB's storage provider, falling back to
// the supplied tenant default when the KB does not pin one. This mirrors the
// selection logic in resolveFileService and is used by clone preflight checks
// to detect cross-storage-backend clones (which are not supported).
func (kb *KnowledgeBase) EffectiveStorageProvider(tenantDefault string) string {
	if p := kb.GetStorageProvider(); p != "" {
		return p
	}
	return strings.ToLower(strings.TrimSpace(tenantDefault))
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
	for _, provider := range []string{"local", "minio", "cos", "tos", "s3", "oss", "ks3", "obs"} {
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
	// DescriptionLanguage controls the language used for generated image
	// captions. Empty means follow the document/request language.
	DescriptionLanguage string `yaml:"description_language,omitempty" json:"description_language,omitempty"`
	// CustomInstructions adds KB-specific image interpretation guidance without
	// replacing the system-owned OCR and Markdown output contract.
	CustomInstructions string `yaml:"custom_instructions,omitempty" json:"custom_instructions,omitempty"`

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
	// CustomInstructions describes the intended audience or question style.
	// It is appended to the stable system question-generation template.
	CustomInstructions string `yaml:"custom_instructions,omitempty" json:"custom_instructions,omitempty"`
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
	// CustomInstructions adds domain-specific extraction guidance while the
	// system keeps ownership of the structured graph output protocol.
	CustomInstructions string `yaml:"custom_instructions,omitempty" json:"custom_instructions,omitempty"`
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
	// Clear type-specific configs that don't belong
	if kb.Type != KnowledgeBaseTypeFAQ {
		kb.FAQConfig = nil
	}
	// Set defaults for FAQ
	if kb.Type == KnowledgeBaseTypeFAQ {
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

	// Ensure IndexingStrategy has defaults.
	// For existing rows where indexing_strategy is NULL, GORM Scan() returns
	// DefaultIndexingStrategy() (vector+keyword=true). This block handles the
	// case where a fresh struct was created in-memory without touching DB.
	if kb.IndexingStrategy.IsZero() {
		kb.IndexingStrategy = DefaultIndexingStrategy()
	}
	// Sync legacy ExtractConfig.Enabled → IndexingStrategy.GraphEnabled
	if kb.ExtractConfig != nil && kb.ExtractConfig.Enabled && !kb.IndexingStrategy.GraphEnabled {
		kb.IndexingStrategy.GraphEnabled = true
	}
}

// KBCapabilities describes the functional features a knowledge base exposes.
// It is computed from the KB's configuration (IndexingStrategy, Type, WikiConfig, …)
// and surfaced in the JSON representation of a KnowledgeBase so that the frontend
// can filter / enable / disable KB options based on what the selected agent type needs.
type KBCapabilities struct {
	// Vector means semantic (embedding) search is indexed.
	Vector bool `json:"vector"`
	// Keyword means BM25 / sparse keyword search is indexed.
	Keyword bool `json:"keyword"`
	// Wiki means the wiki feature is enabled and authored pages exist / will be generated.
	Wiki bool `json:"wiki"`
	// Graph means knowledge-graph extraction is enabled.
	Graph bool `json:"graph"`
	// FAQ means the KB is a FAQ-type KB (Q/A pairs).
	FAQ bool `json:"faq"`
}

// Capabilities returns the computed capability flags for this KB.
// Safe to call on a nil KB (returns zero value).
func (kb *KnowledgeBase) Capabilities() KBCapabilities {
	if kb == nil {
		return KBCapabilities{}
	}
	return KBCapabilities{
		Vector:  kb.IsVectorEnabled(),
		Keyword: kb.IsKeywordEnabled(),
		Wiki:    kb.IsWikiEnabled(),
		Graph:   kb.IsGraphEnabled(),
		FAQ:     kb.Type == KnowledgeBaseTypeFAQ,
	}
}

// MarshalJSON augments the default JSON encoding of KnowledgeBase with a computed
// `capabilities` field so clients (agent editor) can filter KBs by feature.
// It preserves all existing fields verbatim.
func (kb *KnowledgeBase) MarshalJSON() ([]byte, error) {
	type alias KnowledgeBase
	aux := struct {
		*alias
		Capabilities KBCapabilities `json:"capabilities"`
	}{
		alias:        (*alias)(kb),
		Capabilities: kb.Capabilities(),
	}
	return json.Marshal(aux)
}

// IsWikiEnabled checks if the wiki feature is enabled for this knowledge base.
// Wiki enablement is the single source of truth on IndexingStrategy.WikiEnabled.
func (kb *KnowledgeBase) IsWikiEnabled() bool {
	if kb == nil {
		return false
	}
	return kb.IndexingStrategy.WikiEnabled
}

// IsVectorEnabled checks if vector (semantic) search is enabled.
func (kb *KnowledgeBase) IsVectorEnabled() bool {
	return kb != nil && kb.IndexingStrategy.VectorEnabled
}

// IsKeywordEnabled checks if keyword (BM25) search is enabled.
func (kb *KnowledgeBase) IsKeywordEnabled() bool {
	return kb != nil && kb.IndexingStrategy.KeywordEnabled
}

// IsGraphEnabled checks if knowledge graph extraction is enabled.
// Requires both the IndexingStrategy flag and a valid ExtractConfig.
func (kb *KnowledgeBase) IsGraphEnabled() bool {
	return kb != nil && kb.IndexingStrategy.GraphEnabled &&
		kb.ExtractConfig != nil && kb.ExtractConfig.Enabled
}

// NeedsEmbeddingModel returns true if any enabled pipeline requires an embedding model.
// Currently only vector and keyword search need embeddings.
func (kb *KnowledgeBase) NeedsEmbeddingModel() bool {
	return kb != nil && kb.IndexingStrategy.NeedsEmbedding()
}

// IsMultimodalEnabled 判断多模态是否启用，由 VLMConfig.IsEnabled() 决定。
func (kb *KnowledgeBase) IsMultimodalEnabled() bool {
	if kb == nil {
		return false
	}
	return kb.VLMConfig.IsEnabled()
}

// HasVectorStore reports whether the KB is bound to a DB-managed VectorStore
// (as opposed to the tenant's env-store fallback).
//
// Safe to call on a nil receiver (returns false). Mirrors the convention of
// other Is*Enabled / Capabilities accessors in this file.
func (kb *KnowledgeBase) HasVectorStore() bool {
	return kb != nil && kb.VectorStoreID != nil && *kb.VectorStoreID != ""
}

// Normalize folds the empty-string vector store id into nil so a single
// representation reaches both the DB and the retrieve-engine factory, which
// treats nil and `&""` as the same "no binding" signal. Idempotent and safe
// to call repeatedly.
//
// Callers that accept unvalidated user input (CreateKnowledgeBase, async
// payload decoders) should invoke this before persistence or validation.
// Safe to call on a nil receiver (no-op).
func (kb *KnowledgeBase) Normalize() {
	if kb == nil {
		return
	}
	if kb.VectorStoreID != nil && *kb.VectorStoreID == "" {
		kb.VectorStoreID = nil
	}
}

// SharesStoreWith reports whether two knowledge bases are bound to the same
// vector store. Both env-fallback (nil) → true; both same UUID → true;
// otherwise false. Safe to call when either receiver or argument is nil
// (returns true iff both are nil).
//
// Empty-string VectorStoreID is treated as equivalent to nil so that rows
// persisted by callers that did not run Normalize first (raw-SQL writes,
// external migrations, ops scripts) still compare correctly. The alternative
// — treating `&""` as a distinct binding — would reject otherwise valid
// CopyKnowledgeBase clones with a confusing "different vector stores" 400.
// This normalization is read-only; it does not mutate the receivers.
//
// Lives on *KnowledgeBase next to HasVectorStore() so the binding semantics
// stay co-located with the type they describe.
func (kb *KnowledgeBase) SharesStoreWith(other *KnowledgeBase) bool {
	if kb == nil || other == nil {
		return kb == other
	}
	a, b := kb.VectorStoreID, other.VectorStoreID
	if a != nil && *a == "" {
		a = nil
	}
	if b != nil && *b == "" {
		b = nil
	}
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}
