package handler

import (
	"context"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// SystemHandler handles system-related requests
type SystemHandler struct {
	cfg         *config.Config
	neo4jDriver neo4j.Driver
}

// NewSystemHandler creates a new system handler
func NewSystemHandler(cfg *config.Config, neo4jDriver neo4j.Driver) *SystemHandler {
	return &SystemHandler{
		cfg:         cfg,
		neo4jDriver: neo4jDriver,
	}
}

// GetSystemInfoResponse defines the response structure for system info
type GetSystemInfoResponse struct {
	Version             string `json:"version"`
	CommitID            string `json:"commit_id,omitempty"`
	BuildTime           string `json:"build_time,omitempty"`
	GoVersion           string `json:"go_version,omitempty"`
	KeywordIndexEngine  string `json:"keyword_index_engine,omitempty"`
	VectorStoreEngine   string `json:"vector_store_engine,omitempty"`
	GraphDatabaseEngine string `json:"graph_database_engine,omitempty"`
	MinioEnabled        bool   `json:"minio_enabled,omitempty"`
}

// 编译时注入的版本信息
var (
	Version   = "unknown"
	CommitID  = "unknown"
	BuildTime = "unknown"
	GoVersion = "unknown"
)

// GetSystemInfo godoc
// @Summary      获取系统信息
// @Description  获取系统版本、构建信息和引擎配置
// @Tags         系统
// @Accept       json
// @Produce      json
// @Success      200  {object}  GetSystemInfoResponse  "系统信息"
// @Router       /system/info [get]
func (h *SystemHandler) GetSystemInfo(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	// Get keyword index engine from RETRIEVE_DRIVER
	keywordIndexEngine := h.getKeywordIndexEngine()

	// Get vector store engine from config or RETRIEVE_DRIVER
	vectorStoreEngine := h.getVectorStoreEngine()

	// Get graph database engine from NEO4J_ENABLE
	graphDatabaseEngine := h.getGraphDatabaseEngine()

	// Get MinIO enabled status
	minioEnabled := h.isMinioEnabled()

	response := GetSystemInfoResponse{
		Version:             Version,
		CommitID:            CommitID,
		BuildTime:           BuildTime,
		GoVersion:           GoVersion,
		KeywordIndexEngine:  keywordIndexEngine,
		VectorStoreEngine:   vectorStoreEngine,
		GraphDatabaseEngine: graphDatabaseEngine,
		MinioEnabled:        minioEnabled,
	}

	logger.Info(ctx, "System info retrieved successfully")
	c.JSON(200, gin.H{
		"code": 0,
		"msg":  "success",
		"data": response,
	})
}

// getKeywordIndexEngine returns the keyword index engine name
func (h *SystemHandler) getKeywordIndexEngine() string {
	retrieveDriver := os.Getenv("RETRIEVE_DRIVER")
	if retrieveDriver == "" {
		return "未配置"
	}

	drivers := strings.Split(retrieveDriver, ",")
	// Filter out engines that support keyword retrieval
	keywordEngines := []string{}
	for _, driver := range drivers {
		driver = strings.TrimSpace(driver)
		if driver == "postgres" || driver == "elasticsearch_v7" || driver == "elasticsearch_v8" {
			keywordEngines = append(keywordEngines, driver)
		}
	}

	if len(keywordEngines) == 0 {
		return "未配置"
	}
	return strings.Join(keywordEngines, ", ")
}

// getVectorStoreEngine returns the vector store engine name
func (h *SystemHandler) getVectorStoreEngine() string {
	// First check config.yaml
	if h.cfg != nil && h.cfg.VectorDatabase != nil && h.cfg.VectorDatabase.Driver != "" {
		return h.cfg.VectorDatabase.Driver
	}

	// Fallback to RETRIEVE_DRIVER for vector support
	retrieveDriver := os.Getenv("RETRIEVE_DRIVER")
	if retrieveDriver == "" {
		return "未配置"
	}

	drivers := strings.Split(retrieveDriver, ",")
	// Filter out engines that support vector retrieval
	vectorEngines := []string{}
	for _, driver := range drivers {
		driver = strings.TrimSpace(driver)
		if driver == "postgres" || driver == "elasticsearch_v8" {
			vectorEngines = append(vectorEngines, driver)
		}
	}

	if len(vectorEngines) == 0 {
		return "未配置"
	}
	return strings.Join(vectorEngines, ", ")
}

// getGraphDatabaseEngine returns the graph database engine name
func (h *SystemHandler) getGraphDatabaseEngine() string {
	if h.neo4jDriver == nil {
		return "未启用"
	}
	return "Neo4j"
}

// isMinioEnabled checks if MinIO is enabled
func (h *SystemHandler) isMinioEnabled() bool {
	// Check if all required MinIO environment variables are set
	endpoint := os.Getenv("MINIO_ENDPOINT")
	accessKeyID := os.Getenv("MINIO_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("MINIO_SECRET_ACCESS_KEY")

	return endpoint != "" && accessKeyID != "" && secretAccessKey != ""
}

// MinioBucketInfo represents bucket information with access policy
type MinioBucketInfo struct {
	Name      string `json:"name"`
	Policy    string `json:"policy"` // "public", "private", "custom"
	CreatedAt string `json:"created_at,omitempty"`
}

// ListMinioBucketsResponse defines the response structure for listing buckets
type ListMinioBucketsResponse struct {
	Buckets []MinioBucketInfo `json:"buckets"`
}

// ListMinioBuckets godoc
// @Summary      列出 MinIO 存储桶
// @Description  获取所有 MinIO 存储桶及其访问权限
// @Tags         系统
// @Accept       json
// @Produce      json
// @Success      200  {object}  ListMinioBucketsResponse  "存储桶列表"
// @Failure      400  {object}  map[string]interface{}    "MinIO 未启用"
// @Failure      500  {object}  map[string]interface{}    "服务器错误"
// @Router       /system/minio/buckets [get]
func (h *SystemHandler) ListMinioBuckets(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	// Check if MinIO is enabled
	if !h.isMinioEnabled() {
		logger.Warn(ctx, "MinIO is not enabled")
		c.JSON(400, gin.H{
			"code":    400,
			"msg":     "MinIO is not enabled",
			"success": false,
		})
		return
	}

	// Get MinIO configuration from environment
	endpoint := os.Getenv("MINIO_ENDPOINT")
	accessKeyID := os.Getenv("MINIO_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("MINIO_SECRET_ACCESS_KEY")
	useSSL := os.Getenv("MINIO_USE_SSL") == "true"

	// Create MinIO client
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		logger.Error(ctx, "Failed to create MinIO client", "error", err)
		c.JSON(500, gin.H{
			"code":    500,
			"msg":     "Failed to connect to MinIO",
			"success": false,
		})
		return
	}

	// List all buckets
	buckets, err := minioClient.ListBuckets(context.Background())
	if err != nil {
		logger.Error(ctx, "Failed to list MinIO buckets", "error", err)
		c.JSON(500, gin.H{
			"code":    500,
			"msg":     "Failed to list buckets",
			"success": false,
		})
		return
	}

	// Get policy for each bucket
	bucketInfos := make([]MinioBucketInfo, 0, len(buckets))
	for _, bucket := range buckets {
		policy := "private" // default

		// Try to get bucket policy
		policyStr, err := minioClient.GetBucketPolicy(context.Background(), bucket.Name)
		if err == nil && policyStr != "" {
			// Check if policy contains public read access
			if strings.Contains(policyStr, `"Effect":"Allow"`) &&
				strings.Contains(policyStr, `"Principal":"*"`) &&
				strings.Contains(policyStr, `"s3:GetObject"`) {
				policy = "public"
			} else {
				policy = "custom"
			}
		}

		bucketInfos = append(bucketInfos, MinioBucketInfo{
			Name:      bucket.Name,
			Policy:    policy,
			CreatedAt: bucket.CreationDate.Format("2006-01-02 15:04:05"),
		})
	}

	logger.Info(ctx, "Listed MinIO buckets successfully", "count", len(bucketInfos))
	c.JSON(200, gin.H{
		"code":    0,
		"msg":     "success",
		"success": true,
		"data":    ListMinioBucketsResponse{Buckets: bucketInfos},
	})
}
