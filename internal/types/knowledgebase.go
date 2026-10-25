package types

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
)

const storageBackendScheme = "storage://"

func BuildStorageBackendPath(backendID, providerPath string) string {
	return storageBackendScheme + strings.TrimSpace(backendID) + "/" + providerPath
}

func ParseStorageBackendPath(path string) (backendID, providerPath string, ok bool) {
	if !strings.HasPrefix(path, storageBackendScheme) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, storageBackendScheme)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

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
	ID string `yaml:"id"                      json:"id"                      gorm:"type:varchar(36);primaryKey" extensions:"x-agent=knowledge_base_id,x-agent-response"`
	// Name of the knowledge base
	Name string `yaml:"name"                    json:"name"                    binding:"required" extensions:"x-agent,x-agent-required"`
	// Type of the knowledge base: document, faq or wiki. Empty means document.
	Type string `yaml:"type"                    json:"type"                    gorm:"type:varchar(32);default:'document'" enums:"document,faq,wiki" extensions:"x-agent"`
	// Whether this knowledge base is temporary (ephemeral) and should be hidden from UI
	IsTemporary bool `yaml:"is_temporary"            json:"is_temporary"            gorm:"default:false"`
	// Description of the knowledge base
	Description string `yaml:"description"             json:"description" extensions:"x-agent"`
	// Workspace ID
	TenantID uint64 `yaml:"tenant_id"               json:"tenant_id"`
	// CreatorID records the user ID of whoever originally created the KB.
	// Used by the workspace-level RBAC middleware to let Contributors edit
	// their own KBs without granting them access to everyone else's.
	// Nullable for backward compatibility with rows created before the
	// RBAC migration backfilled the column to the workspace Owner.
	CreatorID string `yaml:"creator_id"              json:"creator_id"              gorm:"type:varchar(36);index"`
	// Chunking configuration
	ChunkingConfig ChunkingConfig `yaml:"chunking_config"         json:"chunking_config"         gorm:"type:json" extensions:"x-agent,x-agent-detail"`
	// Image processing configuration
	ImageProcessingConfig ImageProcessingConfig `yaml:"image_processing_config" json:"image_processing_config" gorm:"type:json" extensions:"x-agent,x-agent-detail"`
	// ID of the embedding model. model.list returns the ids. Cannot change after the knowledge base has documents.
	EmbeddingModelID string `yaml:"embedding_model_id"      json:"embedding_model_id" extensions:"x-agent=embedding_model_id"`
	// Summary model ID. model.list returns the ids. Used for summaries, and as
	// the default for auto-tagging and the AI description.
	SummaryModelID string `yaml:"summary_model_id"        json:"summary_model_id" extensions:"x-agent=summary_model_id"`
	// VLM config
	VLMConfig VLMConfig `yaml:"vlm_config"              json:"vlm_config"              gorm:"type:json" extensions:"x-agent,x-agent-detail"`
	// ASR config (Automatic Speech Recognition)
	ASRConfig ASRConfig `yaml:"asr_config"              json:"asr_config"              gorm:"type:json" extensions:"x-agent,x-agent-detail"`
	// Storage provider config (new): only stores provider selection; credentials from workspace StorageEngineConfig
	StorageProviderConfig *StorageProviderConfig `yaml:"storage_provider_config" json:"storage_provider_config"  gorm:"column:storage_provider_config;type:jsonb"`
	// StorageBackendID binds this KB to one concrete storage instance. The
	// legacy provider field remains readable during migration only.
	StorageBackendID *string `yaml:"storage_backend_id" json:"storage_backend_id,omitempty" gorm:"column:storage_backend_id;type:varchar(36);default:null" extensions:"x-agent=storage_backend_id"`
	// Deprecated: legacy COS config column. Kept for backward compatibility with old data.
	StorageConfig StorageConfig `yaml:"-" json:"storage_config" gorm:"column:cos_config;type:json"`
	// VectorStoreID references the VectorStore this knowledge base is bound to.
	// When nil, the KB falls back to the workspace's effective engines derived from
	// the RETRIEVE_DRIVER environment variable (env store flow).
	// This field is set once at creation time and must not be modified afterwards;
	// enforcement lives at the GORM layer (`<-:create`) plus the service-layer
	// KB update path, which omits this field from its update DTO.
	VectorStoreID *string `yaml:"vector_store_id"         json:"vector_store_id,omitempty" gorm:"column:vector_store_id;type:varchar(36);<-:create" extensions:"x-agent=vector_store_id"`
	// Extract config
	ExtractConfig *ExtractConfig `yaml:"extract_config"          json:"extract_config"          gorm:"column:extract_config;type:json" extensions:"x-agent,x-agent-detail"`
	// FAQConfig stores FAQ specific configuration such as indexing strategy
	FAQConfig *FAQConfig `yaml:"faq_config"              json:"faq_config"              gorm:"column:faq_config;type:json" extensions:"x-agent,x-agent-detail"`
	// QuestionGenerationConfig stores question generation configuration for document knowledge bases
	QuestionGenerationConfig *QuestionGenerationConfig `yaml:"question_generation_config" json:"question_generation_config" gorm:"column:question_generation_config;type:json" extensions:"x-agent,x-agent-detail"`
	// AutoTagConfig controls asynchronous association of existing tags after parsing.
	AutoTagConfig *AutoTagConfig `yaml:"auto_tag_config" json:"auto_tag_config" gorm:"type:json" extensions:"x-agent,x-agent-detail"`
	// ProfileConfig controls automatic generation of the knowledge-base
	// description from per-document profiles (document knowledge bases only).
	ProfileConfig *KnowledgeBaseProfileConfig `yaml:"profile_config" json:"profile_config" gorm:"column:profile_config;type:json" extensions:"x-agent,x-agent-detail"` //nolint:lll // one-line struct tag
	// GeneratedProfile is the machine-generated description: a gist, merged
	// topics, typical questions and the aggregate snapshot they came from. It
	// never overwrites the user-authored Description; both are shown to agents.
	GeneratedProfile *KnowledgeBaseProfile `yaml:"generated_profile" json:"generated_profile,omitempty" gorm:"column:generated_profile;type:json"` //nolint:lll // one-line struct tag
	// WikiConfig stores wiki-specific configuration (only for wiki type knowledge bases)
	WikiConfig *WikiConfig `yaml:"wiki_config"             json:"wiki_config"             gorm:"column:wiki_config;type:json" extensions:"x-agent,x-agent-detail"`
	// IndexingStrategy controls which indexing pipelines are active for this knowledge base.
	// Pipelines: vector search, keyword search, wiki generation, knowledge graph extraction.
	IndexingStrategy IndexingStrategy `yaml:"indexing_strategy"       json:"indexing_strategy"       gorm:"column:indexing_strategy;type:json" extensions:"x-agent,x-agent-response"`
	// IsPinned and PinnedAt are computed per-caller from user_kb_pins
	// (see migration 000050). They used to be stored on the row itself,
	// which made pinning a workspace-wide ordering decision gated behind
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
	UpdatedAt time.Time `yaml:"updated_at"              json:"updated_at" extensions:"x-agent,x-agent-response"`
	// Deletion time of the knowledge base
	DeletedAt gorm.DeletedAt `yaml:"deleted_at"              json:"deleted_at"              gorm:"index"`
	// Knowledge count (not stored in database, calculated on query)
	KnowledgeCount int64 `yaml:"knowledge_count"         json:"knowledge_count"         gorm:"-" extensions:"x-agent,x-agent-response"`
	// Chunk count (not stored in database, calculated on query)
	ChunkCount int64 `yaml:"chunk_count"             json:"chunk_count"             gorm:"-" extensions:"x-agent,x-agent-response"`
	// IsProcessing indicates if there is a processing import task (for FAQ type knowledge bases)
	IsProcessing bool `yaml:"is_processing"           json:"is_processing"           gorm:"-" extensions:"x-agent,x-agent-response"`
	// ProcessingCount indicates the number of knowledge items being processed (for document type knowledge bases)
	ProcessingCount int64 `yaml:"processing_count"        json:"processing_count"        gorm:"-" extensions:"x-agent,x-agent-response"`
	// ShareCount indicates the number of organizations this knowledge base is shared with (not stored in database)
	ShareCount int64 `yaml:"share_count"             json:"share_count"             gorm:"-"`
	// CreatorName 是 CreatorID 对应用户的展示名（username / email 等），
	// 仅在列表场景由 handler 批量回填，不落库；为空表示创建者无法解析（用户已删除、
	// CreatorID 为空的老数据等）。前端用它在卡片来源徽章上做 mine vs workspace 的二分。
	CreatorName string `yaml:"-"                       json:"creator_name,omitempty"  gorm:"-"`
}

// KnowledgeBaseConfig represents the knowledge base configuration
type KnowledgeBaseConfig struct {
	// Chunking configuration
	ChunkingConfig ChunkingConfig `yaml:"chunking_config"         json:"chunking_config"`
	// Image processing configuration.
	//
	// nil means "no change" when updating, the same contract IndexingStrategy
	// uses. A request that does not mention this field must leave the image
	// settings alone: the attribute-observation switch and the attribute policy
	// are things a knowledge base accumulates, and a client that predates them
	// would otherwise reset the lot on any save. Sending an object — empty
	// included — replaces the whole configuration.
	ImageProcessingConfig *ImageProcessingConfig `yaml:"image_processing_config" json:"image_processing_config"`
	// FAQ configuration (only for FAQ type knowledge bases)
	FAQConfig *FAQConfig `yaml:"faq_config"              json:"faq_config"`
	// Wiki configuration (only for wiki-enabled knowledge bases)
	WikiConfig *WikiConfig `yaml:"wiki_config"             json:"wiki_config"`
	// AutoTagConfig controls optional automatic association of existing KB tags.
	AutoTagConfig *AutoTagConfig `yaml:"auto_tag_config" json:"auto_tag_config"`
	// ProfileConfig controls optional automatic knowledge-base description
	// generation. nil means "no change" when updating.
	ProfileConfig *KnowledgeBaseProfileConfig `yaml:"profile_config" json:"profile_config"`
	// IndexingStrategy controls which indexing pipelines are active.
	// nil means "no change" when updating (preserves existing strategy).
	IndexingStrategy *IndexingStrategy `yaml:"indexing_strategy"       json:"indexing_strategy"`
	// EmbeddingModelID changes the embedding model. nil means no change.
	// A knowledge base that already has documents cannot switch models.
	EmbeddingModelID *string `json:"embedding_model_id,omitempty"`
	// SummaryModelID changes the summary model. nil means no change.
	SummaryModelID *string `json:"summary_model_id,omitempty"`
	// StorageBackendID rebinds file storage. nil means no change.
	StorageBackendID *string `json:"storage_backend_id,omitempty"`
	// ASRConfig replaces speech-recognition settings when set. nil means no change.
	ASRConfig *ASRConfig `json:"asr_config,omitempty"`
	// ExtractConfig replaces graph extraction settings when set. nil means no change.
	ExtractConfig *ExtractConfig `json:"extract_config,omitempty"`
	// QuestionGenerationConfig replaces generated-question settings when set. nil means no change.
	QuestionGenerationConfig *QuestionGenerationConfig `json:"question_generation_config,omitempty"`
}

const (
	// DefaultAutoTagMaxTags is applied when max_tags is unset or non-positive.
	DefaultAutoTagMaxTags = 3
	// MaximumAutoTagMaxTags caps how many tags one document may auto-acquire.
	MaximumAutoTagMaxTags = 10
)

// AutoTagConfig controls asynchronous document auto-tagging. It is deliberately
// opt-in so upgrading an existing deployment does not add model calls or alter
// document tags unexpectedly.
type AutoTagConfig struct {
	// Enabled turns automatic tagging on. The model picks from tags that already exist.
	Enabled bool `yaml:"enabled" json:"enabled" extensions:"x-agent=auto_tag_enabled"`
	// ModelID overrides the summary model. Empty uses summary_model_id.
	ModelID string `yaml:"model_id,omitempty" json:"model_id,omitempty" extensions:"x-agent=auto_tag_model_id"`
	// MaxTags is how many tags one document may receive, from 1 to 10.
	MaxTags int `yaml:"max_tags,omitempty" json:"max_tags,omitempty" minimum:"1" maximum:"10" extensions:"x-agent=auto_tag_max_tags"`
	// SkipIfTagged leaves a document alone when it already has tags. Empty means yes.
	SkipIfTagged *bool `yaml:"skip_if_tagged,omitempty" json:"skip_if_tagged,omitempty" extensions:"x-agent=auto_tag_skip_if_tagged"`
}

// ShouldSkipIfTagged reports whether documents with existing tags are left
// alone. Defaults to true for nil receivers and unset values.
func (c *AutoTagConfig) ShouldSkipIfTagged() bool {
	if c == nil || c.SkipIfTagged == nil {
		return true
	}
	return *c.SkipIfTagged
}

// Value serializes the automatic-tagging configuration for database storage.
func (c AutoTagConfig) Value() (driver.Value, error) { return json.Marshal(c) }

// Scan deserializes the automatic-tagging configuration from a database value.
func (c *AutoTagConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		if s, ok := value.(string); ok {
			b = []byte(s)
		} else {
			return nil
		}
	}
	return json.Unmarshal(b, c)
}

// Normalize applies defaults and bounds to the automatic-tagging configuration.
func (c *AutoTagConfig) Normalize() {
	if c == nil {
		return
	}
	if c.MaxTags <= 0 {
		c.MaxTags = DefaultAutoTagMaxTags
	}
	if c.MaxTags > MaximumAutoTagMaxTags {
		c.MaxTags = MaximumAutoTagMaxTags
	}
	if c.SkipIfTagged == nil {
		skip := true
		c.SkipIfTagged = &skip
	}
}

// ParserEngineRule maps a set of file types to a specific parser engine.
type ParserEngineRule struct {
	FileTypes []string `yaml:"file_types" json:"file_types" extensions:"x-agent"`
	Engine    string   `yaml:"engine"     json:"engine" extensions:"x-agent"`
	// XLSXFirstRowAsHeader restores row-1 column context for flat XLSX tables.
	// nil preserves the parser default; an explicit false disables the mode.
	XLSXFirstRowAsHeader *bool `yaml:"xlsx_first_row_as_header,omitempty" json:"xlsx_first_row_as_header,omitempty" extensions:"x-agent"`
}

// ChunkingConfig represents the document splitting configuration
type ChunkingConfig struct {
	// Chunk size in characters. Empty on create uses the server default.
	ChunkSize int `yaml:"chunk_size"    json:"chunk_size" minimum:"100" maximum:"10000" extensions:"x-agent"`
	// Chunk overlap in characters.
	ChunkOverlap int `yaml:"chunk_overlap" json:"chunk_overlap" minimum:"0" maximum:"5000" extensions:"x-agent"`
	// Separators, in the order they are tried.
	Separators []string `yaml:"separators"    json:"separators" extensions:"x-agent,x-agent-maximum=20"`
	// ParserEngineRules configures which parser engine to use for each file type.
	// When empty, DefaultParserEngine is used (builtin/simple routing, except
	// types that only a specific engine can parse: ppt/pptx fall back to
	// markitdown). A linked anydoc binding is preferred for every type it
	// converts.
	ParserEngineRules []ParserEngineRule `yaml:"parser_engine_rules,omitempty" json:"parser_engine_rules,omitempty" extensions:"x-agent,x-agent-response"`
	// EnableParentChild splits into large parent chunks and small child chunks.
	// Search matches a child and returns its parent.
	EnableParentChild bool `yaml:"enable_parent_child,omitempty" json:"enable_parent_child,omitempty" extensions:"x-agent"`
	// ParentChunkSize is the size of parent chunks (default: 4096).
	// Only used when EnableParentChild is true.
	ParentChunkSize int `yaml:"parent_chunk_size,omitempty" json:"parent_chunk_size,omitempty" minimum:"1" maximum:"20000" extensions:"x-agent"`
	// ChildChunkSize is the size of child chunks used for embedding (default: 384).
	// Only used when EnableParentChild is true.
	ChildChunkSize int `yaml:"child_chunk_size,omitempty" json:"child_chunk_size,omitempty" minimum:"1" maximum:"10000" extensions:"x-agent"`
	// Strategy selects how documents are split: auto, heading, heuristic,
	// recursive or legacy. Empty keeps the server default.
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty" enums:"auto,heading,heuristic,recursive,legacy" extensions:"x-agent"`
	// TokenLimit caps chunk size in approximate tokens. 0 uses ChunkSize as a character count.
	TokenLimit int `yaml:"token_limit,omitempty" json:"token_limit,omitempty" minimum:"0" maximum:"100000" extensions:"x-agent"`
	// Languages hints the splitter, for example de or zh. Empty detects the language from the document.
	Languages []string `yaml:"languages,omitempty" json:"languages,omitempty" extensions:"x-agent,x-agent-maximum=20"`
	// TableMetadataInstructions is extra guidance for CSV and Excel summaries. The output format stays fixed.
	TableMetadataInstructions string `yaml:"table_metadata_instructions,omitempty" json:"table_metadata_instructions,omitempty" extensions:"x-agent,x-agent-detail,x-agent-max-bytes=4000"`
}

// defaultParserEngineByType maps file types the builtin/simple engines cannot
// parse to a docreader engine that can. Types omitted here keep empty-string
// routing (Go simple formats, otherwise docreader builtin) unless
// SetPreferParserEngine selects a linked in-process engine such as anydoc.
var defaultParserEngineByType = map[string]string{
	"ppt":  "markitdown",
	"pptx": "markitdown",
}

// preferParserEngine, if set, may override DefaultParserEngine. The
// docparser package registers anydoc here so types does not import the
// engine catalog.
var preferParserEngine func(fileType string) string

// SetPreferParserEngine registers a build-time preference used by
// DefaultParserEngine. Pass nil to clear. Intended to be called from init().
func SetPreferParserEngine(fn func(fileType string) string) {
	preferParserEngine = fn
}

// DefaultParserEngine returns the engine used when no parser_engine_rules
// match. Empty string means builtin (docreader) or Go simple-format routing.
// When the anydoc binding is linked it is preferred for every type it
// converts (except Go simple formats). ppt/pptx otherwise fall back to
// markitdown.
func DefaultParserEngine(fileType string) string {
	ft := normalizeParserFileType(fileType)
	if preferParserEngine != nil {
		if engine := preferParserEngine(ft); engine != "" {
			return engine
		}
	}
	return defaultParserEngineByType[ft]
}

// ResolveParserEngine returns the engine name for the given file type
// based on the configured rules. When no rule matches it returns the
// type-level default (see DefaultParserEngine).
func (c ChunkingConfig) ResolveParserEngine(fileType string) string {
	if rule := c.ResolveParserEngineRule(fileType); rule != nil {
		return rule.Engine
	}
	return DefaultParserEngine(fileType)
}

// ResolveParserEngineRule returns the parser rule for a file type.
func (c ChunkingConfig) ResolveParserEngineRule(fileType string) *ParserEngineRule {
	fileType = normalizeParserFileType(fileType)
	if fileType == "" {
		return nil
	}
	for i := range c.ParserEngineRules {
		for _, ft := range c.ParserEngineRules[i].FileTypes {
			if normalizeParserFileType(ft) == fileType {
				return &c.ParserEngineRules[i]
			}
		}
	}
	return nil
}

func normalizeParserFileType(fileType string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fileType)), ".")
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

// SharesStorageBackendWith compares concrete instance bindings first. Provider
// comparison is only a compatibility fallback for rows not yet backfilled.
func (kb *KnowledgeBase) SharesStorageBackendWith(other *KnowledgeBase, defaultBackendID, defaultProvider string) bool {
	if kb == nil || other == nil {
		return false
	}
	effectiveID := func(candidate *KnowledgeBase) string {
		if candidate.StorageBackendID != nil {
			if id := strings.TrimSpace(*candidate.StorageBackendID); id != "" {
				return id
			}
		}
		return strings.TrimSpace(defaultBackendID)
	}
	leftID, rightID := effectiveID(kb), effectiveID(other)
	if leftID != "" || rightID != "" {
		return leftID != "" && leftID == rightID
	}
	return kb.EffectiveStorageProvider(defaultProvider) == other.EffectiveStorageProvider(defaultProvider)
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
	if _, inner, ok := ParseStorageBackendPath(filePath); ok {
		filePath = inner
	}
	for _, provider := range []string{"local", "minio", "cos", "tos", "s3", "oss", "ks3", "obs", "dummy"} {
		if strings.HasPrefix(filePath, provider+"://") {
			return provider
		}
	}
	return ""
}

// ImageProcessingConfig represents the image processing configuration
type ImageProcessingConfig struct {
	// Model ID used when describing images. Empty uses the knowledge base's caption model.
	ModelID string `yaml:"model_id" json:"model_id" extensions:"x-agent=image_model_id"`
	// ImageActions overrides the built-in attribute→work table (see
	// DefaultImageActions). A nil value keeps the conservative default: run OCR
	// for block text and data visuals. What to do about an image whose
	// attributes were NOT observed is deliberately not part of this table — it
	// is a scheduling decision, so it belongs to the pipeline that observes
	// (`ocr_on_unobserved` in that pipeline's slot).
	ImageActions *ImageActionsConfig `yaml:"image_actions,omitempty" json:"image_actions,omitempty"`
	// ImageAttrsEnabled turns the attribute-observed image pipeline on for this
	// knowledge base. It is off by default on purpose, so upgrading an
	// existing deployment never changes what happens to documents already
	// being ingested: with it off every image is described and OCR'd exactly
	// as before. When on, the describe round also observes image attributes and
	// the attribute policy decides whether OCR runs for it. A single upload can
	// override it per document through
	// KnowledgeProcessOverrides.ImageAttrsEnabled.
	ImageAttrsEnabled bool `yaml:"image_attrs_enabled,omitempty" json:"image_attrs_enabled,omitempty" extensions:"x-agent"` //nolint:lll // one-line struct tag
	// ImagePipelineID selects the image pipeline by id, so the choice the
	// settings panel offers is the choice the backend runs. It is unset on
	// knowledge bases configured before the field existed; those follow
	// ImageAttrsEnabled — see ResolveImagePipelineID. An explicit id always
	// wins over the switch.
	ImagePipelineID string `yaml:"image_pipeline,omitempty" json:"image_pipeline,omitempty" enums:"default,smartocr" extensions:"x-agent"`
	// ImagePipelineParams holds the tunables of ImagePipelineID, keyed by each
	// field's slot — the pipeline id joined to the field's own key, built by
	// SlotKey and read back by ImagePipelineParam. The map travels whole: a key
	// a pipeline no longer declares is dropped when the run reads it, so an old
	// stored config stays loadable rather than being rewritten on save.
	ImagePipelineParams map[string]any `yaml:"image_pipeline_params,omitempty" json:"image_pipeline_params,omitempty" extensions:"x-agent,x-agent-response,x-agent-keys=default.caption_enabled|default.ocr_enabled"`
	// ImageVectorEnabled lets a knowledge base whose embedding model takes
	// images index each image by its own vector and recall it with one. It
	// is off by default on purpose: a model that takes images is also used
	// for plain text, and turning this on costs an embedding call per image
	// and widens vector recall. Existing knowledge bases stay off after an
	// upgrade because the stored JSON has no such key.
	ImageVectorEnabled bool `yaml:"image_vector_enabled,omitempty" json:"image_vector_enabled,omitempty" extensions:"x-agent"` //nolint:lll // one-line struct tag
}

// The modes the preprocessing action's own switch accepts, and what each means:
//
//	auto   the describe round also observes the page's geometry
//	       (layout.margin / layout.upright) and the page is straightened
//	       and cropped only when those say it is worth it;
//	off    (default) never preprocess, and leave the describe prompt exactly as it was
//	       before this feature existed;
//	always preprocess every readable page without asking, and likewise leave
//	       the describe prompt untouched.
//
// Only auto changes the describe prompt; off and always cannot disturb an
// observation pipeline that is already tuned. The switch is declared by the
// preprocessing action (types.ImageActionPreprocess) and stored in the slot of
// the pipeline that offers it, like every other pipeline setting.
//
// A mode is a SETTING — what the knowledge base asked for. What actually
// happened to an image is a different question with a different vocabulary, and
// it is recorded on the trace as preprocess.result.
const (
	PreprocessModeOff    = "off"
	PreprocessModeAuto   = "auto"
	PreprocessModeAlways = "always"
)

// SlotKey namespaces a pipeline-private key with the pipeline that owns it. A key
// already carrying this pipeline's own namespace — an action scoped to one
// pipeline, whose id is "<pipeline>.<action>" — is left alone, so the pipeline id
// is never doubled. The runtime lookup in the image service and the settings
// panel, through its own mirror, both go through it, so a stored key is read the
// way it was written.
func SlotKey(pipelineID, key string) string {
	if pipelineID == "" || strings.HasPrefix(key, pipelineID+".") {
		return key
	}
	return pipelineID + "." + key
}

// ImagePipelineParam reads one pipeline setting out of a params map. The key is
// the running pipeline's slot and nothing else: a params map with no such entry
// means the setting was never stored, and the caller falls back to what the
// field declares — which is the same answer the panel draws.
func ImagePipelineParam(params map[string]any, pipelineID, key string) (any, bool) {
	if params == nil {
		return nil, false
	}
	v, ok := params[SlotKey(pipelineID, key)]
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

// PreprocessModeKey is the preprocessing action's tunable, as the panel declares
// it. The setting lives in the pipeline's slot under this key.
const PreprocessModeKey = "preprocess.mode"

// PreprocessObservesGeometry reports whether the describe round should ask the
// geometry attributes; PreprocessRunsUnconditionally reports whether the page is
// straightened without consulting them. The three modes are mutually exclusive,
// so a caller reads exactly one of these.
//
// An empty mode answers false to both, which is the honest answer for a caller
// that read a switch nobody offers: a pipeline that does not declare the setting
// has no reason to ask the model about geometry it could not use.
func PreprocessObservesGeometry(mode string) bool { return mode == PreprocessModeAuto }

// PreprocessRunsUnconditionally is the other half of the pair above: the mode
// that straightens every page without asking, which is also why it leaves the
// describe prompt alone (there is no question to ask).
func PreprocessRunsUnconditionally(mode string) bool { return mode == PreprocessModeAlways }

// ResolveImagePipelineID returns the pipeline id a knowledge base asks for. An
// explicit ImagePipelineID wins; without one the old attribute-observation
// switch still decides, so a knowledge base saved before the selector existed
// runs the pipeline the switch pointed at. An id that no pipeline registers is
// ignored here and falls back to the switch, which is what keeps a stored
// config from pointing at a pipeline that has since been removed.
func ResolveImagePipelineID(cfg *ImageProcessingConfig) ImagePipelineID {
	if cfg == nil {
		return ImagePipelineIDFor(false)
	}
	if id := strings.TrimSpace(cfg.ImagePipelineID); id != "" {
		return NormalizeImagePipelineID(ImagePipelineID(id))
	}
	return ImagePipelineIDFor(cfg.ImageAttrsEnabled)
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
	// Enabled turns image captions on. Also send vlm_model_id.
	Enabled bool `yaml:"enabled" json:"enabled" extensions:"x-agent=vlm_enabled"`
	// Model that writes image captions. model.list returns the ids.
	ModelID string `yaml:"model_id" json:"model_id" extensions:"x-agent=vlm_model_id"`
	// DescriptionLanguage is the language of generated image captions. Empty follows the document language.
	DescriptionLanguage string `yaml:"description_language,omitempty" json:"description_language,omitempty" extensions:"x-agent=vlm_language"`
	// CustomInstructions adds image-reading guidance. The OCR and Markdown format stay fixed.
	CustomInstructions string `yaml:"custom_instructions,omitempty" json:"custom_instructions,omitempty" extensions:"x-agent=vlm_instructions,x-agent-detail,x-agent-max-bytes=4000"`

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
	// Enabled turns question generation on. Generated questions are indexed beside the chunk.
	Enabled bool `yaml:"enabled"  json:"enabled" extensions:"x-agent=question_generation_enabled"`
	// QuestionCount is how many questions to write per chunk, from 1 to 10.
	QuestionCount int `yaml:"question_count" json:"question_count" minimum:"1" maximum:"10" extensions:"x-agent=question_count"`
	// CustomInstructions describes who the questions are for. The question format stays fixed.
	CustomInstructions string `yaml:"custom_instructions,omitempty" json:"custom_instructions,omitempty" extensions:"x-agent=question_instructions,x-agent-detail,x-agent-max-bytes=4000"`
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
	// Enabled turns speech transcription on. Also send asr_model_id.
	Enabled bool `yaml:"enabled" json:"enabled" extensions:"x-agent=asr_enabled"`
	// Model that transcribes audio. model.list returns the ids.
	ModelID string `yaml:"model_id" json:"model_id" extensions:"x-agent=asr_model_id"`
	// Language is an optional hint for transcription, such as zh or en.
	Language string `yaml:"language" json:"language" extensions:"x-agent=asr_language"`
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
	// Enabled turns graph extraction on. Node and relation types are edited in the knowledge base editor.
	Enabled bool `yaml:"enabled"   json:"enabled" extensions:"x-agent=extract_enabled"`
	// Text is the extraction prompt. Required when extraction is on and no text is saved yet.
	Text string `yaml:"text"      json:"text,omitempty" extensions:"x-agent=extract_text,x-agent-detail,x-agent-max-bytes=8000"`
	// Tags are the entity labels extraction may use.
	Tags      []string         `yaml:"tags"      json:"tags,omitempty" extensions:"x-agent=extract_tags,x-agent-maximum=50"`
	Nodes     []*GraphNode     `yaml:"nodes"     json:"nodes,omitempty"`
	Relations []*GraphRelation `yaml:"relations" json:"relations,omitempty"`
	// CustomInstructions adds domain guidance. The graph output format stays fixed.
	CustomInstructions string `yaml:"custom_instructions,omitempty" json:"custom_instructions,omitempty" extensions:"x-agent=extract_instructions,x-agent-detail,x-agent-max-bytes=4000"`
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
	// IndexMode is question_only or question_answer.
	IndexMode FAQIndexMode `yaml:"index_mode"          json:"index_mode" enums:"question_only,question_answer" extensions:"x-agent=faq_index_mode"`
	// QuestionIndexMode is combined or separate.
	QuestionIndexMode FAQQuestionIndexMode `yaml:"question_index_mode" json:"question_index_mode" enums:"combined,separate" extensions:"x-agent=faq_question_index_mode"`
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
	if kb.Type != KnowledgeBaseTypeDocument {
		kb.AutoTagConfig = nil
		kb.ProfileConfig = nil
	} else if kb.AutoTagConfig != nil {
		kb.AutoTagConfig.Normalize()
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
//
// The legacy inline credentials (storage_config secret_id / secret_key and
// vlm_config api_key) are withheld. Their DB columns are written by
// StorageConfig.Value / VLMConfig.Value, so persistence is unaffected, while
// every JSON rendering of a KB is an API or tool response — including the
// lists shown to org-share receivers, who must never see the owner's keys.
// Clients only need these fields' presence, which /initialization/config
// reports separately.
func (kb *KnowledgeBase) MarshalJSON() ([]byte, error) {
	type alias KnowledgeBase
	redacted := *kb
	redacted.StorageConfig.SecretID = ""
	redacted.StorageConfig.SecretKey = ""
	redacted.VLMConfig.APIKey = ""
	aux := struct {
		*alias
		Capabilities KBCapabilities `json:"capabilities"`
	}{
		alias:        (*alias)(&redacted),
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

// IsWikiAutoGenerateEnabled controls background generation independently of
// access to existing pages and manual Wiki editing.
func (kb *KnowledgeBase) IsWikiAutoGenerateEnabled() bool {
	return kb.IsWikiEnabled() && kb.WikiConfig.IsAutoGenerateEnabled()
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

// IsImageVectorEnabled reports whether the knowledge base opted in to
// indexing and recalling images by their own vectors. The embedding model
// must also take images; callers check that separately, since it needs the
// model resolved.
func (kb *KnowledgeBase) IsImageVectorEnabled() bool {
	return kb.IsVectorEnabled() && kb.ImageProcessingConfig.ImageVectorEnabled
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
