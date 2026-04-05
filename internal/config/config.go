package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// Config 应用程序总配置
type Config struct {
	Conversation    *ConversationConfig    `yaml:"conversation"     json:"conversation"`
	Server          *ServerConfig          `yaml:"server"           json:"server"`
	KnowledgeBase   *KnowledgeBaseConfig   `yaml:"knowledge_base"   json:"knowledge_base"`
	Tenant          *TenantConfig          `yaml:"tenant"           json:"tenant"`
	OIDCAuth        *OIDCAuthConfig        `yaml:"oidc_auth"        json:"oidc_auth"`
	Models          []ModelConfig          `yaml:"models"           json:"models"`
	VectorDatabase  *VectorDatabaseConfig  `yaml:"vector_database"  json:"vector_database"`
	DocReader       *DocReaderConfig       `yaml:"docreader"        json:"docreader"`
	StreamManager   *StreamManagerConfig   `yaml:"stream_manager"   json:"stream_manager"`
	ExtractManager  *ExtractManagerConfig  `yaml:"extract"          json:"extract"`
	WebSearch       *WebSearchConfig       `yaml:"web_search"       json:"web_search"`
	PromptTemplates *PromptTemplatesConfig `yaml:"prompt_templates" json:"prompt_templates"`
	IM              *IMConfig              `yaml:"im"               json:"im"`
	Agent           *AgentConfig           `yaml:"agent"            json:"agent"`
}

// AgentConfig represents the global agent settings.
type AgentConfig struct {
	// LLMCallTimeout is the default timeout for a single LLM call in seconds.
	// Default: 120 (standard agents) or 300 (can be overridden by Env).
	LLMCallTimeout int `yaml:"llm_call_timeout" json:"llm_call_timeout"`
}

// IMConfig configures the IM integration service.
// All fields are optional — zero values fall back to built-in defaults so
// existing deployments need no config changes.
type IMConfig struct {
	// Workers is the number of concurrent QA worker goroutines per instance.
	// Default: 5.
	Workers int `yaml:"workers" json:"workers"`
	// GlobalMaxWorkers is the maximum number of QA requests that can execute
	// concurrently across ALL instances. Enforced via a Redis counter; when the
	// global limit is reached, local workers wait until a slot opens.
	// Requires Redis — ignored in single-instance mode.
	// 0 (default) means no global limit.
	GlobalMaxWorkers int `yaml:"global_max_workers" json:"global_max_workers"`
	// MaxQueueSize is the maximum number of pending QA requests per instance.
	// Default: 50.
	MaxQueueSize int `yaml:"max_queue_size" json:"max_queue_size"`
	// MaxPerUser limits how many requests a single user can have queued globally.
	// Default: 3.
	MaxPerUser int `yaml:"max_per_user" json:"max_per_user"`
	// RateLimitWindow is the sliding window duration for per-user rate limiting.
	// Default: 60s.
	RateLimitWindow time.Duration `yaml:"rate_limit_window" json:"rate_limit_window"`
	// RateLimitMax is the maximum number of requests allowed per window per user.
	// Default: 10.
	RateLimitMax int `yaml:"rate_limit_max" json:"rate_limit_max"`
}

// DocReaderConfig configures the document parser client (gRPC or HTTP).
type DocReaderConfig struct {
	// Addr: for gRPC it is the server address (e.g. "localhost:50051"); for HTTP it is the base URL (e.g. "http://localhost:8080").
	Addr string `yaml:"addr" json:"addr"`
	// Transport: "grpc" (default) or "http"
	Transport string `yaml:"transport" json:"transport"`
}

type VectorDatabaseConfig struct {
	Driver string `yaml:"driver" json:"driver"`
}

// ConversationConfig 对话服务配置
type ConversationConfig struct {
	MaxRounds            int            `yaml:"max_rounds"                       json:"max_rounds"`
	KeywordThreshold     float64        `yaml:"keyword_threshold"                json:"keyword_threshold"`
	EmbeddingTopK        int            `yaml:"embedding_top_k"                  json:"embedding_top_k"`
	VectorThreshold      float64        `yaml:"vector_threshold"                 json:"vector_threshold"`
	RerankTopK           int            `yaml:"rerank_top_k"                     json:"rerank_top_k"`
	RerankThreshold      float64        `yaml:"rerank_threshold"                 json:"rerank_threshold"`
	FallbackStrategy     string         `yaml:"fallback_strategy"                json:"fallback_strategy"`
	FallbackResponse     string         `yaml:"fallback_response"                json:"fallback_response"`
	EnableRewrite        bool           `yaml:"enable_rewrite"                   json:"enable_rewrite"`
	EnableQueryExpansion bool           `yaml:"enable_query_expansion"           json:"enable_query_expansion"`
	EnableRerank         bool           `yaml:"enable_rerank"                    json:"enable_rerank"`
	Summary              *SummaryConfig `yaml:"summary"                          json:"summary"`

	// Prompt template ID fields — resolved to text by backfillConversationDefaults
	FallbackPromptID             string `yaml:"fallback_prompt_id"                json:"fallback_prompt_id"`
	RewritePromptID              string `yaml:"rewrite_prompt_id"                 json:"rewrite_prompt_id"`
	GenerateSessionTitlePromptID string `yaml:"generate_session_title_prompt_id"  json:"generate_session_title_prompt_id"`
	GenerateSummaryPromptID      string `yaml:"generate_summary_prompt_id"        json:"generate_summary_prompt_id"`
	ExtractEntitiesPromptID      string `yaml:"extract_entities_prompt_id"        json:"extract_entities_prompt_id"`
	ExtractRelationshipsPromptID string `yaml:"extract_relationships_prompt_id"   json:"extract_relationships_prompt_id"`
	GenerateQuestionsPromptID    string `yaml:"generate_questions_prompt_id"      json:"generate_questions_prompt_id"`

	// Resolved prompt text fields (populated by backfill, not from YAML)
	FallbackPrompt             string `yaml:"-" json:"fallback_prompt"`
	RewritePromptSystem        string `yaml:"-" json:"rewrite_prompt_system"`
	RewritePromptUser          string `yaml:"-" json:"rewrite_prompt_user"`
	GenerateSessionTitlePrompt string `yaml:"-" json:"generate_session_title_prompt"`
	GenerateSummaryPrompt      string `yaml:"-" json:"generate_summary_prompt"`
	ExtractEntitiesPrompt      string `yaml:"-" json:"extract_entities_prompt"`
	ExtractRelationshipsPrompt string `yaml:"-" json:"extract_relationships_prompt"`
	GenerateQuestionsPrompt    string `yaml:"-" json:"generate_questions_prompt"`

	// IntentSystemPrompts maps intent values (e.g. "greeting", "chitchat") to
	// system prompt text. Populated by backfill from IntentPrompts templates.
	IntentSystemPrompts map[string]string `yaml:"-" json:"-"`
}

// SummaryConfig 摘要配置
type SummaryConfig struct {
	MaxInputChars       int     `yaml:"max_input_chars"       json:"max_input_chars"` // Max input characters for summary generation (default: 16384)
	MaxTokens           int     `yaml:"max_tokens"            json:"max_tokens"`
	RepeatPenalty       float64 `yaml:"repeat_penalty"        json:"repeat_penalty"`
	TopK                int     `yaml:"top_k"                 json:"top_k"`
	TopP                float64 `yaml:"top_p"                 json:"top_p"`
	FrequencyPenalty    float64 `yaml:"frequency_penalty"     json:"frequency_penalty"`
	PresencePenalty     float64 `yaml:"presence_penalty"      json:"presence_penalty"`
	Temperature         float64 `yaml:"temperature"           json:"temperature"`
	Seed                int     `yaml:"seed"                  json:"seed"`
	MaxCompletionTokens int     `yaml:"max_completion_tokens" json:"max_completion_tokens"`
	NoMatchPrefix       string  `yaml:"no_match_prefix"       json:"no_match_prefix"`
	Thinking            *bool   `yaml:"thinking"              json:"thinking"`

	// Prompt template ID fields — resolved to text by backfillConversationDefaults
	PromptID          string `yaml:"prompt_id"           json:"prompt_id"`
	ContextTemplateID string `yaml:"context_template_id" json:"context_template_id"`

	// Resolved prompt text fields (populated by backfill, not from YAML)
	Prompt          string `yaml:"-" json:"prompt"`
	ContextTemplate string `yaml:"-" json:"context_template"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port            int           `yaml:"port"             json:"port"`
	Host            string        `yaml:"host"             json:"host"`
	LogPath         string        `yaml:"log_path"         json:"log_path"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout" default:"30s"`
}

// KnowledgeBaseConfig 知识库配置
type KnowledgeBaseConfig struct {
	ChunkSize       int                    `yaml:"chunk_size"       json:"chunk_size"`
	ChunkOverlap    int                    `yaml:"chunk_overlap"    json:"chunk_overlap"`
	SplitMarkers    []string               `yaml:"split_markers"    json:"split_markers"`
	KeepSeparator   bool                   `yaml:"keep_separator"   json:"keep_separator"`
	ImageProcessing *ImageProcessingConfig `yaml:"image_processing" json:"image_processing"`
}

// ImageProcessingConfig 图像处理配置
type ImageProcessingConfig struct {
	EnableMultimodal bool `yaml:"enable_multimodal" json:"enable_multimodal"`
}

// TenantConfig 租户配置
type TenantConfig struct {
	DefaultSessionName        string `yaml:"default_session_name"        json:"default_session_name"`
	DefaultSessionTitle       string `yaml:"default_session_title"       json:"default_session_title"`
	DefaultSessionDescription string `yaml:"default_session_description" json:"default_session_description"`
	// EnableCrossTenantAccess enables cross-tenant access for users with permission
	EnableCrossTenantAccess bool `yaml:"enable_cross_tenant_access" json:"enable_cross_tenant_access"`
}

type OIDCUserInfoMapping struct {
	Username string `yaml:"username" json:"username"`
	Email    string `yaml:"email"    json:"email"`
}

type OIDCAuthConfig struct {
	Enable                bool                 `yaml:"enable"                 json:"enable"`
	IssuerURL             string               `yaml:"issuer_url"             json:"issuer_url"`
	DiscoveryURL          string               `yaml:"discovery_url"          json:"discovery_url"`
	ProviderDisplayName   string               `yaml:"provider_display_name"  json:"provider_display_name"`
	ClientID              string               `yaml:"client_id"              json:"client_id"`
	ClientSecret          string               `yaml:"client_secret"          json:"-"`
	AuthorizationEndpoint string               `yaml:"authorization_endpoint" json:"authorization_endpoint"`
	TokenEndpoint         string               `yaml:"token_endpoint"         json:"token_endpoint"`
	UserInfoEndpoint      string               `yaml:"user_info_endpoint"     json:"user_info_endpoint"`
	Scopes                []string             `yaml:"scopes"                 json:"scopes"`
	UserInfoMapping       *OIDCUserInfoMapping `yaml:"user_info_mapping"      json:"user_info_mapping"`
}

// PromptTemplateI18n holds localized name and description for a prompt template.
type PromptTemplateI18n struct {
	Name        string `yaml:"name"        json:"name"`
	Description string `yaml:"description" json:"description"`
}

// PromptTemplate 提示词模板
//
// 字段设计：每个模板最多由两部分组成 —— 系统侧 (content) 和用户侧 (user)。
//   - content: 主要内容 / 系统 Prompt（所有模板都使用此字段）
//   - user:    用户侧 Prompt（仅在需要 system+user 配对的模板中使用，如 rewrite、keywords_extraction）
//   - i18n:    多语言 name/description，键为 locale（如 "zh-CN"、"en-US"、"ko-KR"），后端根据请求语言替换 Name/Description 再返回
type PromptTemplate struct {
	ID               string                        `yaml:"id"                 json:"id"`
	Name             string                        `yaml:"name"               json:"name"`
	Description      string                        `yaml:"description"        json:"description"`
	Content          string                        `yaml:"content"            json:"content"`
	User             string                        `yaml:"user"               json:"user,omitempty"`
	HasKnowledgeBase bool                          `yaml:"has_knowledge_base" json:"has_knowledge_base,omitempty"`
	HasWebSearch     bool                          `yaml:"has_web_search"     json:"has_web_search,omitempty"`
	Default          bool                          `yaml:"default"            json:"default,omitempty"`
	Mode             string                        `yaml:"mode"               json:"mode,omitempty"`
	I18n             map[string]PromptTemplateI18n `yaml:"i18n"               json:"-"`
}

// PromptTemplatesConfig 提示词模板配置
//
// 每种 Prompt 类型对应一个 YAML 文件，所有模板都在同一个字段（文件）中管理。
// 每个模板使用 content (system prompt) + user (user prompt) 两个字段。
type PromptTemplatesConfig struct {
	SystemPrompt    []PromptTemplate `yaml:"system_prompt"    json:"system_prompt"`
	ContextTemplate []PromptTemplate `yaml:"context_template" json:"context_template"`
	// Rewrite 合并了前端可选模板和运行时默认模板，每个模板同时包含 content + user
	Rewrite []PromptTemplate `yaml:"rewrite" json:"rewrite"`
	// Fallback 合并了固定回复模板和模型兜底 prompt（通过 mode:"model" 区分）
	Fallback []PromptTemplate `yaml:"fallback" json:"fallback"`

	GenerateSessionTitle []PromptTemplate `yaml:"generate_session_title" json:"generate_session_title,omitempty"`
	GenerateSummary      []PromptTemplate `yaml:"generate_summary"       json:"generate_summary,omitempty"`
	KeywordsExtraction   []PromptTemplate `yaml:"keywords_extraction"    json:"keywords_extraction,omitempty"`
	AgentSystemPrompt    []PromptTemplate `yaml:"agent_system_prompt"    json:"agent_system_prompt,omitempty"`
	GraphExtraction      []PromptTemplate `yaml:"graph_extraction"       json:"graph_extraction,omitempty"`
	GenerateQuestions    []PromptTemplate `yaml:"generate_questions"     json:"generate_questions,omitempty"`
	// IntentPrompts holds per-intent system prompt overrides (template ID = intent value).
	IntentPrompts []PromptTemplate `yaml:"intent_prompts" json:"intent_prompts,omitempty"`
}

// DefaultTemplate returns the first template marked as default in the list,
// or the first template if none is marked, or nil if the list is empty.
func DefaultTemplate(templates []PromptTemplate) *PromptTemplate {
	for i := range templates {
		if templates[i].Default {
			return &templates[i]
		}
	}
	if len(templates) > 0 {
		return &templates[0]
	}
	return nil
}

// DefaultTemplateByMode returns the default template filtered by mode.
func DefaultTemplateByMode(templates []PromptTemplate, mode string) *PromptTemplate {
	for i := range templates {
		if templates[i].Mode == mode && templates[i].Default {
			return &templates[i]
		}
	}
	for i := range templates {
		if templates[i].Mode == mode {
			return &templates[i]
		}
	}
	return DefaultTemplate(templates)
}

// LocalizeTemplates returns a deep copy of the template list with Name and
// Description replaced according to the given locale.  Fallback chain:
//   locale → primary language (e.g. "zh" from "zh-CN") → original Name/Description.
// The returned slice is safe to serialise directly; it never mutates the original.
func LocalizeTemplates(templates []PromptTemplate, locale string) []PromptTemplate {
	if len(templates) == 0 {
		return templates
	}
	out := make([]PromptTemplate, len(templates))
	copy(out, templates)
	for i := range out {
		if len(out[i].I18n) == 0 {
			continue
		}
		// Try exact match first (e.g. "zh-CN"), then primary subtag (e.g. "zh")
		l10n, ok := out[i].I18n[locale]
		if !ok {
			if idx := strings.IndexByte(locale, '-'); idx > 0 {
				l10n, ok = out[i].I18n[locale[:idx]]
			}
		}
		if !ok {
			continue
		}
		if l10n.Name != "" {
			out[i].Name = l10n.Name
		}
		if l10n.Description != "" {
			out[i].Description = l10n.Description
		}
	}
	return out
}

// ModelConfig 模型配置
type ModelConfig struct {
	Type       string                 `yaml:"type"       json:"type"`
	Source     string                 `yaml:"source"     json:"source"`
	ModelName  string                 `yaml:"model_name" json:"model_name"`
	Parameters map[string]interface{} `yaml:"parameters" json:"parameters"`
}

// StreamManagerConfig 流管理器配置
type StreamManagerConfig struct {
	Type           string        `yaml:"type"            json:"type"`            // 类型: "memory" 或 "redis"
	Redis          RedisConfig   `yaml:"redis"           json:"redis"`           // Redis配置
	CleanupTimeout time.Duration `yaml:"cleanup_timeout" json:"cleanup_timeout"` // 清理超时，单位秒
}

// RedisConfig Redis配置
type RedisConfig struct {
	Address  string        `yaml:"address"  json:"address"`  // Redis地址
	Username string        `yaml:"username" json:"username"` // Redis用户名
	Password string        `yaml:"password" json:"password"` // Redis密码
	DB       int           `yaml:"db"       json:"db"`       // Redis数据库
	Prefix   string        `yaml:"prefix"   json:"prefix"`   // 键前缀
	TTL      time.Duration `yaml:"ttl"      json:"ttl"`      // 过期时间(小时)
}

// ExtractManagerConfig 抽取管理器配置
type ExtractManagerConfig struct {
	ExtractGraph  *types.PromptTemplateStructured `yaml:"extract_graph"  json:"extract_graph"`
	ExtractEntity *types.PromptTemplateStructured `yaml:"extract_entity" json:"extract_entity"`
	FabriText     *FebriText                      `yaml:"fabri_text"     json:"fabri_text"`
}

type FebriText struct {
	WithTag   string `yaml:"with_tag"    json:"with_tag"`
	WithNoTag string `yaml:"with_no_tag" json:"with_no_tag"`
}

// LoadConfig 从配置文件加载配置
func LoadConfig() (*Config, error) {
	// 设置配置文件名和路径
	viper.SetConfigName("config")         // 配置文件名称(不带扩展名)
	viper.SetConfigType("yaml")           // 配置文件类型
	viper.AddConfigPath(".")              // 当前目录
	viper.AddConfigPath("./config")       // config子目录
	viper.AddConfigPath("$HOME/.appname") // 用户目录
	viper.AddConfigPath("/etc/appname/")  // etc目录

	// 启用环境变量替换
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// 读取配置文件
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// 替换配置中的环境变量引用
	configFileContent, err := os.ReadFile(viper.ConfigFileUsed())
	if err != nil {
		return nil, fmt.Errorf("error reading config file content: %w", err)
	}

	// 替换${ENV_VAR}格式的环境变量引用
	re := regexp.MustCompile(`\${([^}]+)}`)
	result := re.ReplaceAllStringFunc(string(configFileContent), func(match string) string {
		// 提取环境变量名称（去掉${}部分）
		envVar := match[2 : len(match)-1]
		// 获取环境变量值，如果不存在则保持原样
		if value := os.Getenv(envVar); value != "" {
			return value
		}
		return match
	})

	// 使用处理后的配置内容
	viper.ReadConfig(strings.NewReader(result))

	// 解析配置到结构体
	var cfg Config
	if err := viper.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.TagName = "yaml"
	}); err != nil {
		return nil, fmt.Errorf("unable to decode config into struct: %w", err)
	}
	fmt.Printf("Using configuration file: %s\n", viper.ConfigFileUsed())

	// 加载提示词模板（从目录或配置文件）
	configDir := filepath.Dir(viper.ConfigFileUsed())
	promptTemplates, err := loadPromptTemplates(configDir)
	if err != nil {
		fmt.Printf("Warning: failed to load prompt templates from directory: %v\n", err)
		// 如果目录加载失败，使用配置文件中的模板（如果有）
	} else if promptTemplates != nil {
		cfg.PromptTemplates = promptTemplates
	}

	// Back-fill conversation config from prompt templates defaults
	// (so config.yaml can omit large prompt blocks and rely on template files)
	if cfg.PromptTemplates != nil && cfg.Conversation != nil {
		backfillConversationDefaults(&cfg)
	}

	// Load built-in agent definitions (i18n-aware) from builtin_agents.yaml
	if err := types.LoadBuiltinAgentsConfig(configDir); err != nil {
		fmt.Printf("Warning: failed to load builtin agents config: %v\n", err)
	}

	// Resolve prompt template ID references in builtin agent configs
	// (e.g. system_prompt_id -> actual content from agent_system_prompt.yaml)
	if cfg.PromptTemplates != nil {
		resolveBuiltinAgentPromptIDs(cfg.PromptTemplates)
	}

	// Validate configuration values
	applyOIDCEnvOverrides(&cfg)
	applyAgentEnvOverrides(&cfg)

	if err := ValidateConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ValidateConfig performs basic validation of the loaded configuration.
// It checks for obviously invalid or missing values that would cause runtime failures.
func ValidateConfig(cfg *Config) error {
	var errs []string

	if cfg.OIDCAuth != nil && cfg.OIDCAuth.Enable {
		if strings.TrimSpace(cfg.OIDCAuth.ClientID) == "" {
			errs = append(errs, "oidc_auth.client_id is required when OIDC is enabled")
		}
		if strings.TrimSpace(cfg.OIDCAuth.ClientSecret) == "" {
			errs = append(errs, "oidc_auth.client_secret is required when OIDC is enabled")
		}
		if strings.TrimSpace(cfg.OIDCAuth.DiscoveryURL) == "" &&
			(strings.TrimSpace(cfg.OIDCAuth.AuthorizationEndpoint) == "" || strings.TrimSpace(cfg.OIDCAuth.TokenEndpoint) == "") {
			errs = append(errs, "oidc_auth.discovery_url or both oidc_auth.authorization_endpoint and oidc_auth.token_endpoint are required when OIDC is enabled")
		}
	}

	if cfg.Conversation != nil {
		if cfg.Conversation.EmbeddingTopK < 0 {
			errs = append(errs, "conversation.embedding_top_k must be >= 0")
		}
		if cfg.Conversation.RerankTopK < 0 {
			errs = append(errs, "conversation.rerank_top_k must be >= 0")
		}
		if cfg.Conversation.VectorThreshold < 0 || cfg.Conversation.VectorThreshold > 1 {
			errs = append(errs, "conversation.vector_threshold must be between 0 and 1")
		}
		if cfg.Conversation.RerankThreshold < -10 || cfg.Conversation.RerankThreshold > 10 {
			errs = append(errs, "conversation.rerank_threshold must be between -10 and 10")
		}
	}

	if cfg.KnowledgeBase != nil {
		if cfg.KnowledgeBase.ChunkSize <= 0 {
			errs = append(errs, "knowledge_base.chunk_size must be > 0")
		}
		if cfg.KnowledgeBase.ChunkOverlap < 0 {
			errs = append(errs, "knowledge_base.chunk_overlap must be >= 0")
		}
		if cfg.KnowledgeBase.ChunkOverlap >= cfg.KnowledgeBase.ChunkSize {
			errs = append(errs, "knowledge_base.chunk_overlap must be less than chunk_size")
		}
	}

	if cfg.Server != nil {
		if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
			errs = append(errs, "server.port must be between 1 and 65535")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func applyOIDCEnvOverrides(cfg *Config) {
	if cfg.OIDCAuth == nil {
		cfg.OIDCAuth = &OIDCAuthConfig{}
	}
	if cfg.OIDCAuth.UserInfoMapping == nil {
		cfg.OIDCAuth.UserInfoMapping = &OIDCUserInfoMapping{}
	}

	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_ENABLE")); value != "" {
		cfg.OIDCAuth.Enable = strings.EqualFold(value, "true")
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_ISSUER_URL")); value != "" {
		cfg.OIDCAuth.IssuerURL = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_DISCOVERY_URL")); value != "" {
		cfg.OIDCAuth.DiscoveryURL = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_PROVIDER_DISPLAY_NAME")); value != "" {
		cfg.OIDCAuth.ProviderDisplayName = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_CLIENT_ID")); value != "" {
		cfg.OIDCAuth.ClientID = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_CLIENT_SECRET")); value != "" {
		cfg.OIDCAuth.ClientSecret = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_AUTHORIZATION_ENDPOINT")); value != "" {
		cfg.OIDCAuth.AuthorizationEndpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_TOKEN_ENDPOINT")); value != "" {
		cfg.OIDCAuth.TokenEndpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_USER_INFO_ENDPOINT")); value != "" {
		cfg.OIDCAuth.UserInfoEndpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_SCOPES")); value != "" {
		cfg.OIDCAuth.Scopes = strings.Fields(strings.ReplaceAll(value, ",", " "))
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_USER_INFO_MAPPING_USER_NAME")); value != "" {
		cfg.OIDCAuth.UserInfoMapping.Username = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_USER_INFO_MAPPING_EMAIL")); value != "" {
		cfg.OIDCAuth.UserInfoMapping.Email = value
	}

	if cfg.OIDCAuth.ProviderDisplayName == "" {
		cfg.OIDCAuth.ProviderDisplayName = "OIDC"
	}
	if len(cfg.OIDCAuth.Scopes) == 0 {
		cfg.OIDCAuth.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.OIDCAuth.UserInfoMapping.Username == "" {
		cfg.OIDCAuth.UserInfoMapping.Username = "name"
	}
	if cfg.OIDCAuth.UserInfoMapping.Email == "" {
		cfg.OIDCAuth.UserInfoMapping.Email = "email"
	}
	if cfg.OIDCAuth.DiscoveryURL == "" && cfg.OIDCAuth.IssuerURL != "" {
		cfg.OIDCAuth.DiscoveryURL = strings.TrimRight(cfg.OIDCAuth.IssuerURL, "/") + "/.well-known/openid-configuration"
	}
}

func applyAgentEnvOverrides(cfg *Config) {
	if cfg.Agent == nil {
		cfg.Agent = &AgentConfig{}
	}
	if value := strings.TrimSpace(os.Getenv("WEKNORA_AGENT_LLM_TIMEOUT")); value != "" {
		if timeout, err := time.ParseDuration(value); err == nil {
			cfg.Agent.LLMCallTimeout = int(timeout.Seconds())
		} else if sec, err := time.ParseDuration(value + "s"); err == nil {
			// Handle case where user just provides a number like "300"
			cfg.Agent.LLMCallTimeout = int(sec.Seconds())
		}
	}
}

// backfillConversationDefaults resolves prompt template ID references
// into actual prompt text content. Only xxx_id fields are used;
// no fallback to default templates.
func backfillConversationDefaults(cfg *Config) {
	pt := cfg.PromptTemplates
	conv := cfg.Conversation

	if conv.FallbackPromptID != "" {
		if t := FindTemplateByID(pt, conv.FallbackPromptID); t != nil {
			conv.FallbackPrompt = t.Content
		} else {
			fmt.Printf("Warning: fallback_prompt_id %q not found\n", conv.FallbackPromptID)
		}
	}
	if conv.RewritePromptID != "" {
		if t := FindTemplateByID(pt, conv.RewritePromptID); t != nil {
			conv.RewritePromptSystem = t.Content
			conv.RewritePromptUser = t.User
		} else {
			fmt.Printf("Warning: rewrite_prompt_id %q not found\n", conv.RewritePromptID)
		}
	}
	if conv.GenerateSessionTitlePromptID != "" {
		if t := FindTemplateByID(pt, conv.GenerateSessionTitlePromptID); t != nil {
			conv.GenerateSessionTitlePrompt = t.Content
		} else {
			fmt.Printf("Warning: generate_session_title_prompt_id %q not found\n", conv.GenerateSessionTitlePromptID)
		}
	}
	if conv.GenerateSummaryPromptID != "" {
		if t := FindTemplateByID(pt, conv.GenerateSummaryPromptID); t != nil {
			conv.GenerateSummaryPrompt = t.Content
		} else {
			fmt.Printf("Warning: generate_summary_prompt_id %q not found\n", conv.GenerateSummaryPromptID)
		}
	}
	if conv.ExtractEntitiesPromptID != "" {
		if t := FindTemplateByID(pt, conv.ExtractEntitiesPromptID); t != nil {
			conv.ExtractEntitiesPrompt = t.Content
		} else {
			fmt.Printf("Warning: extract_entities_prompt_id %q not found\n", conv.ExtractEntitiesPromptID)
		}
	}
	if conv.ExtractRelationshipsPromptID != "" {
		if t := FindTemplateByID(pt, conv.ExtractRelationshipsPromptID); t != nil {
			conv.ExtractRelationshipsPrompt = t.Content
		} else {
			fmt.Printf("Warning: extract_relationships_prompt_id %q not found\n", conv.ExtractRelationshipsPromptID)
		}
	}
	if conv.GenerateQuestionsPromptID != "" {
		if t := FindTemplateByID(pt, conv.GenerateQuestionsPromptID); t != nil {
			conv.GenerateQuestionsPrompt = t.Content
		} else {
			fmt.Printf("Warning: generate_questions_prompt_id %q not found\n", conv.GenerateQuestionsPromptID)
		}
	}
	if conv.Summary != nil {
		if conv.Summary.PromptID != "" {
			if t := FindTemplateByID(pt, conv.Summary.PromptID); t != nil {
				conv.Summary.Prompt = t.Content
			} else {
				fmt.Printf("Warning: summary.prompt_id %q not found\n", conv.Summary.PromptID)
			}
		}
		if conv.Summary.ContextTemplateID != "" {
			if t := FindTemplateByID(pt, conv.Summary.ContextTemplateID); t != nil {
				conv.Summary.ContextTemplate = t.Content
			} else {
				fmt.Printf("Warning: summary.context_template_id %q not found\n", conv.Summary.ContextTemplateID)
			}
		}
	}

	// Build intent→system-prompt map from IntentPrompts templates.
	// Template ID must equal the QueryIntent string value (e.g. "greeting").
	if len(pt.IntentPrompts) > 0 {
		conv.IntentSystemPrompts = make(map[string]string, len(pt.IntentPrompts))
		for _, t := range pt.IntentPrompts {
			if t.ID != "" && t.Content != "" {
				conv.IntentSystemPrompts[t.ID] = t.Content
			}
		}
	}
}

// FindTemplateByID searches across all template lists for a template with the given ID.
// It returns the template if found, or nil otherwise.
func FindTemplateByID(pt *PromptTemplatesConfig, id string) *PromptTemplate {
	if pt == nil || id == "" {
		return nil
	}
	// Search all template collections
	for _, list := range [][]PromptTemplate{
		pt.SystemPrompt,
		pt.ContextTemplate,
		pt.Rewrite,
		pt.Fallback,
		pt.GenerateSessionTitle,
		pt.GenerateSummary,
		pt.KeywordsExtraction,
		pt.AgentSystemPrompt,
		pt.GraphExtraction,
		pt.GenerateQuestions,
		pt.IntentPrompts,
	} {
		for i := range list {
			if list[i].ID == id {
				return &list[i]
			}
		}
	}
	return nil
}

// resolveBuiltinAgentPromptIDs resolves system_prompt_id and context_template_id
// references in builtin agent configs by looking up the actual content from
// prompt template YAML files.
func resolveBuiltinAgentPromptIDs(pt *PromptTemplatesConfig) {
	types.ResolveBuiltinAgentPromptRefs(func(id string) string {
		if t := FindTemplateByID(pt, id); t != nil {
			return t.Content
		}
		return ""
	})
}

// promptTemplateFile 用于解析模板文件
type promptTemplateFile struct {
	Templates []PromptTemplate `yaml:"templates"`
}

// loadPromptTemplates 从目录加载提示词模板
func loadPromptTemplates(configDir string) (*PromptTemplatesConfig, error) {
	templatesDir := filepath.Join(configDir, "prompt_templates")

	// 检查目录是否存在
	if _, err := os.Stat(templatesDir); os.IsNotExist(err) {
		return nil, nil // 目录不存在，返回nil让调用者使用配置文件中的模板
	}

	config := &PromptTemplatesConfig{}

	// 定义模板文件映射
	templateFiles := map[string]*[]PromptTemplate{
		"system_prompt.yaml":          &config.SystemPrompt,
		"context_template.yaml":       &config.ContextTemplate,
		"rewrite.yaml":                &config.Rewrite,
		"fallback.yaml":               &config.Fallback,
		"generate_session_title.yaml": &config.GenerateSessionTitle,
		"generate_summary.yaml":       &config.GenerateSummary,
		"keywords_extraction.yaml":    &config.KeywordsExtraction,
		"agent_system_prompt.yaml":    &config.AgentSystemPrompt,
		"graph_extraction.yaml":       &config.GraphExtraction,
		"generate_questions.yaml":     &config.GenerateQuestions,
		"intent_prompts.yaml":         &config.IntentPrompts,
	}

	// 加载每个模板文件
	for filename, target := range templateFiles {
		filePath := filepath.Join(templatesDir, filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			continue // 文件不存在，跳过
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", filename, err)
		}

		var file promptTemplateFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", filename, err)
		}

		*target = file.Templates
	}

	return config, nil
}

// WebSearchConfig represents the web search configuration
type WebSearchConfig struct {
	Timeout int `yaml:"timeout" json:"timeout"` // 超时时间（秒）
}
