// Package config 提供自研后端（custom-backend）的配置加载。
// 从环境变量读取，未设置时用默认值；与 WeKnora 官方 config 包解耦，
// 避免引入官方 server 的完整配置依赖。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config 自研后端总配置
type Config struct {
	Server                ServerConfig
	Database              DatabaseConfig
	WeKnora               WeKnoraConfig
	WikiGraph             WikiGraphConfig
	MinIO                 MinIOConfig
	Upload                UploadConfig
	Tongyi                TongyiConfig
	MPS                   MPSConfig
	TranscriptionProvider string
	LLM                   LLMConfig
	Worker                WorkerConfig
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Host string
	Port int
}

// DatabaseConfig 自研业务库配置（与 WeKnora 库隔离）
type DatabaseConfig struct {
	Driver   string
	Path     string
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

// WeKnoraConfig WeKnora 内容引擎连接配置
type WeKnoraConfig struct {
	BaseURL  string
	APIKey   string
	KBID     string // 字幕分块默认入库的目标 KB
	TenantID string // WeKnora 多租户场景
	AgentID  string // 视频问答使用的自定义智能体
}

// WikiGraphConfig is the isolated Neo4j projection used by the product graph.
// It may point at the same Neo4j server as WeKnora, but always uses its own label namespace.
type WikiGraphConfig struct {
	Enabled         bool
	URI             string
	Username        string
	Password        string
	Database        string
	Namespace       string
	KnowledgeBaseID string
}

// MinIOConfig 对象存储配置（presigned 直传 + 分片）
type MinIOConfig struct {
	Backend   string // minio / local
	Endpoint  string // 例：minio:9000
	AccessKey string
	SecretKey string
	Bucket    string // 视频 / 字幕 / 封面存放桶
	UseSSL    bool
	PublicURL string // 给前端展示用的公开地址（可能走 nginx 反代）
	UploadURL string // 浏览器 presigned 上传地址（必须是公网 MinIO/S3 endpoint）
	LocalDir  string // local backend 使用的本机对象存储目录
}

// UploadConfig 上传性能与 presigned 直传配置。
// PartSizeBytes 只控制服务端默认建议值；客户端会把最终值回传到 init，
// 便于在不重新构建前端的情况下按机器资源调优。
type UploadConfig struct {
	PartSizeBytes           int64
	LargeFileThresholdBytes int64
	InitialConcurrency      int
	MinConcurrency          int
	MaxConcurrency          int
	SignTTLSeconds          int
}

// TongyiConfig 通义听悟配置（视频转写）
type TongyiConfig struct {
	APIKey                  string // 已废弃：听悟改用 AccessKey 签名 + AppKey，保留兼容
	AccessKeyID             string // 阿里云 AccessKey ID（ROA 签名用）
	AccessKeySecret         string // 阿里云 AccessKey Secret（ROA 签名用）
	AppKey                  string // 听悟项目 AppKey（标识转写项目）
	Endpoint                string // 默认 https://tingwu.cn-beijing.aliyuncs.com
	CallbackURL             string // 转写完成回调地址（可选，留空走轮询）
	InternalFrontendBaseURL string // worker 在容器网络内校验视频源时使用的前端服务地址
}

// MPSConfig 腾讯云媒体处理智能字幕配置。
// MPS 输出必须落 COS；保留 SegmentSet 作为结构化结果，避免业务层重新解析字幕文件。
type MPSConfig struct {
	SecretID            string
	SecretKey           string
	Region              string
	Endpoint            string
	OutputBucket        string
	OutputRegion        string
	OutputDir           string
	InputBucket         string
	InputRegion         string
	InputDir            string
	TemplateID          uint64
	PollIntervalSeconds int
	TimeoutSeconds      int
}

type LLMConfig struct {
	Provider       string
	BaseURL        string
	APIKey         string
	Model          string
	PromptVersion  string
	TimeoutSeconds int
	MaxTokens      int
}

// WorkerConfig Worker 引擎配置（轮询周期 / 重试上限）
type WorkerConfig struct {
	PollIntervalSeconds int // 扫描周期（秒）
	MaxAttempts         int // 单个 job 默认重试上限
	Concurrency         int // 并发 worker 数
}

// DSN 返回 GORM 使用的 PostgreSQL 连接串
func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable TimeZone=Asia/Shanghai",
		d.Host, d.Port, d.User, d.Password, d.DBName,
	)
}

// MigrateURL 返回 golang-migrate 使用的 PostgreSQL URL。
// 用 url.UserPassword 对凭据做 URL 编码，避免密码含 @/# 等字符时 URL 解析错乱。
func (d DatabaseConfig) MigrateURL() string {
	userinfo := url.UserPassword(d.User, d.Password).String()
	return fmt.Sprintf(
		"postgres://%s@%s:%d/%s?sslmode=disable&x-migrations-table=custom_schema_migrations",
		userinfo, d.Host, d.Port, d.DBName,
	)
}

// Load 从环境变量加载配置，未设置时用默认值
func Load() *Config {
	provider, err := NormalizeTranscriptionProvider(getEnv("CUSTOM_TRANSCRIPTION_PROVIDER", "tingwu"))
	if err != nil {
		// 非法值安全回退到听悟，避免启动时误把任务路由到未配置的供应商。
		provider = "aliyun_tingwu"
	}
	return &Config{
		Server: ServerConfig{
			Host: getEnv("CUSTOM_SERVER_HOST", "0.0.0.0"),
			Port: getEnvInt("CUSTOM_SERVER_PORT", 8090),
		},
		Database: DatabaseConfig{
			Driver:   getEnv("CUSTOM_DB_DRIVER", "postgres"),
			Path:     getEnv("CUSTOM_DB_PATH", "./data/custom-backend.db"),
			Host:     getEnv("CUSTOM_DB_HOST", "localhost"),
			Port:     getEnvInt("CUSTOM_DB_PORT", 5432),
			User:     getEnv("CUSTOM_DB_USER", "postgres"),
			Password: getEnv("CUSTOM_DB_PASSWORD", "postgres"),
			DBName:   getEnv("CUSTOM_DB_NAME", "vidsage"),
		},
		WeKnora: WeKnoraConfig{
			BaseURL:  getEnv("WEKNORA_BASE_URL", "http://localhost:8080"),
			APIKey:   getEnv("WEKNORA_API_KEY", ""),
			KBID:     getEnv("WEKNORA_KB_ID", ""),
			TenantID: getEnv("WEKNORA_TENANT_ID", ""),
			AgentID:  getEnv("CUSTOM_CONTENT_AGENT_ID", ""),
		},
		WikiGraph: WikiGraphConfig{
			// The product graph must never inherit the official GraphRAG switch.
			// It is a separate Wiki -> Neo4j projection and must be enabled
			// explicitly to avoid two competing graph writers.
			Enabled:   getEnvBool("CUSTOM_WIKI_GRAPH_NEO4J_ENABLE", false),
			URI:       getEnv("CUSTOM_WIKI_GRAPH_NEO4J_URI", ""),
			Username:  getEnv("CUSTOM_WIKI_GRAPH_NEO4J_USERNAME", ""),
			Password:  getEnv("CUSTOM_WIKI_GRAPH_NEO4J_PASSWORD", ""),
			Database:  getEnv("CUSTOM_WIKI_GRAPH_NEO4J_DATABASE", ""),
			Namespace: getEnv("CUSTOM_WIKI_GRAPH_NEO4J_NAMESPACE", "VIDSAGE_KNOWLEDGE"),
			// 默认与内容流水线使用同一个真实 WeKnora 知识库；如需隔离，
			// 由 CUSTOM_WIKI_GRAPH_KB_ID 显式覆盖。
			KnowledgeBaseID: getEnv("CUSTOM_WIKI_GRAPH_KB_ID", getEnv("WEKNORA_KB_ID", "")),
		},
		MinIO: MinIOConfig{
			Backend:   getEnv("CUSTOM_STORAGE_BACKEND", "minio"),
			Endpoint:  getEnv("MINIO_ENDPOINT", "localhost:9000"),
			AccessKey: getEnv("MINIO_ACCESS_KEY", "minioadmin"),
			SecretKey: getEnv("MINIO_SECRET_KEY", "minioadmin"),
			Bucket:    getEnv("MINIO_BUCKET", "vidsage"),
			UseSSL:    getEnvBool("MINIO_USE_SSL", false),
			PublicURL: getEnv("MINIO_PUBLIC_URL", ""),
			UploadURL: getEnv("MINIO_UPLOAD_URL", ""),
			LocalDir:  getEnv("CUSTOM_STORAGE_DIR", "./data/custom-storage"),
		},
		Upload: UploadConfig{
			PartSizeBytes:           getEnvInt64("CUSTOM_UPLOAD_PART_SIZE_MB", 8) * 1024 * 1024,
			LargeFileThresholdBytes: getEnvInt64("CUSTOM_UPLOAD_LARGE_FILE_THRESHOLD_MB", 256) * 1024 * 1024,
			InitialConcurrency:      getEnvInt("CUSTOM_UPLOAD_INITIAL_CONCURRENCY", 2),
			MinConcurrency:          getEnvInt("CUSTOM_UPLOAD_MIN_CONCURRENCY", 1),
			MaxConcurrency:          getEnvInt("CUSTOM_UPLOAD_MAX_CONCURRENCY", 4),
			SignTTLSeconds:          getEnvInt("CUSTOM_UPLOAD_SIGN_TTL_SECONDS", 3600),
		},
		Tongyi: TongyiConfig{
			APIKey:                  getEnv("TONGYI_API_KEY", ""),
			AccessKeyID:             getEnv("TONGYI_ACCESS_KEY_ID", ""),
			AccessKeySecret:         getEnv("TONGYI_ACCESS_KEY_SECRET", ""),
			AppKey:                  getEnv("TONGYI_APP_KEY", ""),
			Endpoint:                getEnv("TONGYI_ENDPOINT", "https://tingwu.cn-beijing.aliyuncs.com"),
			CallbackURL:             getEnv("TONGYI_CALLBACK_URL", ""),
			InternalFrontendBaseURL: getEnv("INTERNAL_FRONTEND_BASE_URL", ""),
		},
		MPS: MPSConfig{
			SecretID:            getEnv("TENCENTCLOUD_SECRET_ID", ""),
			SecretKey:           getEnv("TENCENTCLOUD_SECRET_KEY", ""),
			Region:              getEnv("TENCENTCLOUD_REGION", "ap-guangzhou"),
			Endpoint:            getEnv("TENCENTCLOUD_MPS_ENDPOINT", "mps.tencentcloudapi.com"),
			OutputBucket:        getEnv("TENCENTCLOUD_MPS_OUTPUT_BUCKET", ""),
			OutputRegion:        getEnv("TENCENTCLOUD_MPS_OUTPUT_REGION", ""),
			OutputDir:           getEnv("TENCENTCLOUD_MPS_OUTPUT_DIR", "/subtitles/"),
			InputBucket:         getEnv("TENCENTCLOUD_MPS_INPUT_BUCKET", ""),
			InputRegion:         getEnv("TENCENTCLOUD_MPS_INPUT_REGION", ""),
			InputDir:            getEnv("TENCENTCLOUD_MPS_INPUT_DIR", "vidsage-mps-input/"),
			TemplateID:          uint64(getEnvInt64("TENCENTCLOUD_MPS_TEMPLATE_ID", 307)),
			PollIntervalSeconds: getEnvInt("TENCENTCLOUD_MPS_POLL_INTERVAL_SECONDS", 5),
			TimeoutSeconds:      getEnvInt("TENCENTCLOUD_MPS_TIMEOUT_SECONDS", 1800),
		},
		TranscriptionProvider: provider,
		LLM: LLMConfig{
			Provider:       getEnv("CUSTOM_LLM_PROVIDER", "openai-compatible"),
			BaseURL:        getEnv("CUSTOM_LLM_BASE_URL", ""),
			APIKey:         getEnv("CUSTOM_LLM_API_KEY", ""),
			Model:          getEnv("CUSTOM_LLM_MODEL", ""),
			PromptVersion:  getEnv("CUSTOM_LLM_PROMPT_VERSION", "direct-content-v3"),
			TimeoutSeconds: getEnvInt("CUSTOM_LLM_TIMEOUT_SECONDS", 180),
			MaxTokens:      getEnvInt("CUSTOM_LLM_MAX_TOKENS", 8192),
		},
		Worker: WorkerConfig{
			PollIntervalSeconds: getEnvInt("CUSTOM_WORKER_POLL_INTERVAL", 5),
			MaxAttempts:         getEnvInt("CUSTOM_WORKER_MAX_ATTEMPTS", 3),
			Concurrency:         getEnvInt("CUSTOM_WORKER_CONCURRENCY", 2),
		},
	}
}

// normalizeTranscriptionProvider 返回持久化使用的规范值。
// 兼容历史任务中的 aliyun_tingwu，同时允许配置使用 tingwu 别名。
func NormalizeTranscriptionProvider(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "tingwu", "aliyun_tingwu":
		return "aliyun_tingwu", nil
	case "tencent_mps", "mps":
		return "tencent_mps", nil
	default:
		return "", fmt.Errorf("CUSTOM_TRANSCRIPTION_PROVIDER must be tingwu or tencent_mps")
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getEnvInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
