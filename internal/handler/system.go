package handler

import (
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
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
