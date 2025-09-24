package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Config 应用程序总配置
type Config struct {
	Conversation   *ConversationConfig   `yaml:"conversation" json:"conversation"`
	Server         *ServerConfig         `yaml:"server" json:"server"`
	KnowledgeBase  *KnowledgeBaseConfig  `yaml:"knowledge_base" json:"knowledge_base"`
	Tenant         *TenantConfig         `yaml:"tenant" json:"tenant"`
	Models         []ModelConfig         `yaml:"models" json:"models"`
	VectorDatabase *VectorDatabaseConfig `yaml:"vector_database" json:"vector_database"`
	DocReader      *DocReaderConfig      `yaml:"docreader" json:"docreader"`
	StreamManager  *StreamManagerConfig  `yaml:"stream_manager" json:"stream_manager"`
	ExtractManager *ExtractManagerConfig `yaml:"extract" json:"extract"`
}

type DocReaderConfig struct {
	Addr string `yaml:"addr" json:"addr"`
}

type VectorDatabaseConfig struct {
	Driver string `yaml:"driver" json:"driver"`
}

// ConversationConfig 对话服务配置
type ConversationConfig struct {
	MaxRounds                  int            `yaml:"max_rounds" json:"max_rounds"`
	KeywordThreshold           float64        `yaml:"keyword_threshold" json:"keyword_threshold"`
	EmbeddingTopK              int            `yaml:"embedding_top_k" json:"embedding_top_k"`
	VectorThreshold            float64        `yaml:"vector_threshold" json:"vector_threshold"`
	RerankTopK                 int            `yaml:"rerank_top_k" json:"rerank_top_k"`
	RerankThreshold            float64        `yaml:"rerank_threshold" json:"rerank_threshold"`
	FallbackStrategy           string         `yaml:"fallback_strategy" json:"fallback_strategy"`
	FallbackResponse           string         `yaml:"fallback_response" json:"fallback_response"`
	FallbackPrompt             string         `yaml:"fallback_prompt" json:"fallback_prompt"`
	EnableRewrite              bool           `yaml:"enable_rewrite" json:"enable_rewrite"`
	EnableRerank               bool           `yaml:"enable_rerank" json:"enable_rerank"`
	Summary                    *SummaryConfig `yaml:"summary" json:"summary"`
	GenerateSessionTitlePrompt string         `yaml:"generate_session_title_prompt" json:"generate_session_title_prompt"`
	GenerateSummaryPrompt      string         `yaml:"generate_summary_prompt" json:"generate_summary_prompt"`
	RewritePromptSystem        string         `yaml:"rewrite_prompt_system" json:"rewrite_prompt_system"`
	RewritePromptUser          string         `yaml:"rewrite_prompt_user" json:"rewrite_prompt_user"`
	SimplifyQueryPrompt        string         `yaml:"simplify_query_prompt" json:"simplify_query_prompt"`
	SimplifyQueryPromptUser    string         `yaml:"simplify_query_prompt_user" json:"simplify_query_prompt_user"`
	ExtractEntitiesPrompt      string         `yaml:"extract_entities_prompt" json:"extract_entities_prompt"`
	ExtractRelationshipsPrompt string         `yaml:"extract_relationships_prompt" json:"extract_relationships_prompt"`
}

// SummaryConfig 摘要配置
type SummaryConfig struct {
	MaxTokens           int     `yaml:"max_tokens" json:"max_tokens"`
	RepeatPenalty       float64 `yaml:"repeat_penalty" json:"repeat_penalty"`
	TopK                int     `yaml:"top_k" json:"top_k"`
	TopP                float64 `yaml:"top_p" json:"top_p"`
	FrequencyPenalty    float64 `yaml:"frequency_penalty" json:"frequency_penalty"`
	PresencePenalty     float64 `yaml:"presence_penalty" json:"presence_penalty"`
	Prompt              string  `yaml:"prompt" json:"prompt"`
	ContextTemplate     string  `yaml:"context_template" json:"context_template"`
	Temperature         float64 `yaml:"temperature" json:"temperature"`
	Seed                int     `yaml:"seed" json:"seed"`
	MaxCompletionTokens int     `yaml:"max_completion_tokens" json:"max_completion_tokens"`
	NoMatchPrefix       string  `yaml:"no_match_prefix" json:"no_match_prefix"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port            int           `yaml:"port" json:"port"`
	Host            string        `yaml:"host" json:"host"`
	LogPath         string        `yaml:"log_path" json:"log_path"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout" default:"30s"`
}

// KnowledgeBaseConfig 知识库配置
type KnowledgeBaseConfig struct {
	ChunkSize       int                    `yaml:"chunk_size" json:"chunk_size"`
	ChunkOverlap    int                    `yaml:"chunk_overlap" json:"chunk_overlap"`
	SplitMarkers    []string               `yaml:"split_markers" json:"split_markers"`
	KeepSeparator   bool                   `yaml:"keep_separator" json:"keep_separator"`
	ImageProcessing *ImageProcessingConfig `yaml:"image_processing" json:"image_processing"`
}

// ImageProcessingConfig 图像处理配置
type ImageProcessingConfig struct {
	EnableMultimodal bool `yaml:"enable_multimodal" json:"enable_multimodal"`
}

// TenantConfig 租户配置
type TenantConfig struct {
	DefaultSessionName        string `yaml:"default_session_name" json:"default_session_name"`
	DefaultSessionTitle       string `yaml:"default_session_title" json:"default_session_title"`
	DefaultSessionDescription string `yaml:"default_session_description" json:"default_session_description"`
}

// ModelConfig 模型配置
type ModelConfig struct {
	Type       string                 `yaml:"type" json:"type"`
	Source     string                 `yaml:"source" json:"source"`
	ModelName  string                 `yaml:"model_name" json:"model_name"`
	Parameters map[string]interface{} `yaml:"parameters" json:"parameters"`
}

// StreamManagerConfig 流管理器配置
type StreamManagerConfig struct {
	Type           string        `yaml:"type" json:"type"`                       // 类型: "memory" 或 "redis"
	Redis          RedisConfig   `yaml:"redis" json:"redis"`                     // Redis配置
	CleanupTimeout time.Duration `yaml:"cleanup_timeout" json:"cleanup_timeout"` // 清理超时，单位秒
}

// RedisConfig Redis配置
type RedisConfig struct {
	Address  string        `yaml:"address" json:"address"`   // Redis地址
	Password string        `yaml:"password" json:"password"` // Redis密码
	DB       int           `yaml:"db" json:"db"`             // Redis数据库
	Prefix   string        `yaml:"prefix" json:"prefix"`     // 键前缀
	TTL      time.Duration `yaml:"ttl" json:"ttl"`           // 过期时间(小时)
}

// ExtractManagerConfig 抽取管理器配置
type ExtractManagerConfig struct {
	ExtractGraph  *types.PromptTemplateStructured `yaml:"extract_graph" json:"extract_graph"`
	ExtractEntity *types.PromptTemplateStructured `yaml:"extract_entity" json:"extract_entity"`
	FabriText     *FebriText                      `yaml:"fabri_text" json:"fabri_text"`
}

type FebriText struct {
	WithTag   string `yaml:"with_tag" json:"with_tag"`
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
	return &cfg, nil
}
