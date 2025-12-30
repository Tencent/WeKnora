package handler

import (
	"context"
	"encoding/json"
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
		policy := "private" // default: no policy means private

		// Try to get bucket policy
		policyStr, err := minioClient.GetBucketPolicy(context.Background(), bucket.Name)
		if err == nil && policyStr != "" {
			policy = parseBucketPolicy(policyStr)
		}
		// If err != nil or policyStr is empty, bucket has no policy (private)

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

// BucketPolicy represents the S3 bucket policy structure
type BucketPolicy struct {
	Version   string            `json:"Version"`
	Statement []PolicyStatement `json:"Statement"`
}

// PolicyStatement represents a single statement in the bucket policy
type PolicyStatement struct {
	Effect    string      `json:"Effect"`
	Principal interface{} `json:"Principal"` // Can be "*" or {"AWS": [...]}
	Action    interface{} `json:"Action"`    // Can be string or []string
	Resource  interface{} `json:"Resource"`  // Can be string or []string
}

// parseBucketPolicy parses the policy JSON and determines the access type
func parseBucketPolicy(policyStr string) string {
	var policy BucketPolicy
	if err := json.Unmarshal([]byte(policyStr), &policy); err != nil {
		// If we can't parse the policy, treat it as custom
		return "custom"
	}

	// Check if any statement grants public read access
	hasPublicRead := false
	for _, stmt := range policy.Statement {
		if stmt.Effect != "Allow" {
			continue
		}

		// Check if Principal is "*" (public)
		if !isPrincipalPublic(stmt.Principal) {
			continue
		}

		// Check if Action includes s3:GetObject
		if !hasGetObjectAction(stmt.Action) {
			continue
		}

		hasPublicRead = true
		break
	}

	if hasPublicRead {
		return "public"
	}

	// Has policy but not public read
	return "custom"
}

// isPrincipalPublic checks if the principal allows public access
func isPrincipalPublic(principal interface{}) bool {
	switch p := principal.(type) {
	case string:
		return p == "*"
	case map[string]interface{}:
		// Check for {"AWS": "*"} or {"AWS": ["*"]}
		if aws, ok := p["AWS"]; ok {
			switch a := aws.(type) {
			case string:
				return a == "*"
			case []interface{}:
				for _, v := range a {
					if s, ok := v.(string); ok && s == "*" {
						return true
					}
				}
			}
		}
	}
	return false
}

// hasGetObjectAction checks if the action includes s3:GetObject
func hasGetObjectAction(action interface{}) bool {
	checkAction := func(a string) bool {
		a = strings.ToLower(a)
		return a == "s3:getobject" || a == "s3:*" || a == "*"
	}

	switch act := action.(type) {
	case string:
		return checkAction(act)
	case []interface{}:
		for _, v := range act {
			if s, ok := v.(string); ok && checkAction(s) {
				return true
			}
		}
	}
	return false
}
