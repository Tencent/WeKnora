// Package config 提供自研后端（custom-backend）的配置加载。
// 从环境变量读取，未设置时用默认值；与 WeKnora 官方 config 包解耦，
// 避免引入官方 server 的完整配置依赖。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
)

// Config 自研后端总配置
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	WeKnora  WeKnoraConfig
	MinIO    MinIOConfig
	Upload   UploadConfig
	Tongyi   TongyiConfig
	Worker   WorkerConfig
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
	APIKey          string // 已废弃：听悟改用 AccessKey 签名 + AppKey，保留兼容
	AccessKeyID     string // 阿里云 AccessKey ID（ROA 签名用）
	AccessKeySecret string // 阿里云 AccessKey Secret（ROA 签名用）
	AppKey          string // 听悟项目 AppKey（标识转写项目）
	Endpoint        string // 默认 https://tingwu.cn-beijing.aliyuncs.com
	CallbackURL     string // 转写完成回调地址（可选，留空走轮询）
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
		"postgres://%s@%s:%d/%s?sslmode=disable",
		userinfo, d.Host, d.Port, d.DBName,
	)
}

// Load 从环境变量加载配置，未设置时用默认值
func Load() *Config {
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
			APIKey:          getEnv("TONGYI_API_KEY", ""),
			AccessKeyID:     getEnv("TONGYI_ACCESS_KEY_ID", ""),
			AccessKeySecret: getEnv("TONGYI_ACCESS_KEY_SECRET", ""),
			AppKey:          getEnv("TONGYI_APP_KEY", ""),
			Endpoint:        getEnv("TONGYI_ENDPOINT", "https://tingwu.cn-beijing.aliyuncs.com"),
			CallbackURL:     getEnv("TONGYI_CALLBACK_URL", ""),
		},
		Worker: WorkerConfig{
			PollIntervalSeconds: getEnvInt("CUSTOM_WORKER_POLL_INTERVAL", 5),
			MaxAttempts:         getEnvInt("CUSTOM_WORKER_MAX_ATTEMPTS", 3),
			Concurrency:         getEnvInt("CUSTOM_WORKER_CONCURRENCY", 2),
		},
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
