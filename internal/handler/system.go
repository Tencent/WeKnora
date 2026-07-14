package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/database"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	modellimiter "github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

type runtimeKnowledgeCanceller interface {
	CancelKnowledgeParse(ctx context.Context, knowledgeID string) (*types.Knowledge, error)
}

// SystemHandler handles system-related requests
type SystemHandler struct {
	cfg              *config.Config
	neo4jDriver      neo4j.Driver
	documentReader   interfaces.DocumentReader
	tenantSvc        interfaces.TenantService
	userSvc          interfaces.UserService
	systemSettingSvc interfaces.SystemSettingService
	// auditSvc is optional — when nil, emitAdminAudit no-ops so unit
	// tests that wire a partial container still compile. In production
	// the dig graph always provides one.
	auditSvc interfaces.AuditLogService
	// taskInspector backs the SystemAdmin runtime queue dashboard. Always
	// provided by the container (asynq-backed in Redis mode, a no-op in
	// Lite mode), so GetRuntimeQueues can distinguish "no queues in this
	// deployment" from "queues are empty".
	taskInspector interfaces.TaskInspector
	// knowledgeSvc supplies the domain-level cancellation path used by the
	// runtime task console. It updates business state and tracing before queue
	// records are removed, unlike a raw Redis deletion.
	knowledgeSvc runtimeKnowledgeCanceller
}

// NewSystemHandler creates a new system handler
func NewSystemHandler(cfg *config.Config,
	neo4jDriver neo4j.Driver,
	documentReader interfaces.DocumentReader,
	tenantSvc interfaces.TenantService,
	userSvc interfaces.UserService,
	systemSettingSvc interfaces.SystemSettingService,
	auditSvc interfaces.AuditLogService,
	taskInspector interfaces.TaskInspector,
	knowledgeSvc interfaces.KnowledgeService,
) *SystemHandler {
	return &SystemHandler{
		cfg:              cfg,
		neo4jDriver:      neo4jDriver,
		documentReader:   documentReader,
		tenantSvc:        tenantSvc,
		userSvc:          userSvc,
		systemSettingSvc: systemSettingSvc,
		auditSvc:         auditSvc,
		taskInspector:    taskInspector,
		knowledgeSvc:     knowledgeSvc,
	}
}

// emitAdminAudit writes one audit row for a system-admin lifecycle event
// (promote / revoke). Best-effort — a nil audit service or a write
// failure does not bubble up to the caller. Mirrors the failure
// semantics of tenantMemberService.emitAudit and the system settings
// audit hook.
//
// `details` may be nil; the JSON `{}` default applies.
func (h *SystemHandler) emitAdminAudit(
	ctx context.Context,
	action types.AuditAction,
	target *types.User,
	details map[string]any,
) {
	if h.auditSvc == nil {
		return
	}
	actorID, _ := types.UserIDFromContext(ctx)
	var detailsJSON types.JSON
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			detailsJSON = types.JSON(b)
		}
	}
	entry := &types.AuditLog{
		// tenant_id=0 marks the row as system-scope. The audit_logs
		// table is tenant-scoped; 0 is the convention for platform-wide
		// events (matches AuditActionSystemSettingChanged).
		TenantID:    0,
		ActorUserID: actorID,
		ActorRole:   "system_admin",
		Action:      action,
		TargetType:  "user",
		Outcome:     types.AuditOutcomeSuccess,
		Details:     detailsJSON,
	}
	if target != nil {
		entry.TargetID = target.ID
		entry.TargetUserID = target.ID
	}
	_ = h.auditSvc.Log(ctx, entry)
}

// GetSystemInfoResponse defines the response structure for system info
type GetSystemInfoResponse struct {
	Version             string `json:"version"`
	Edition             string `json:"edition"`
	CommitID            string `json:"commit_id,omitempty"`
	BuildTime           string `json:"build_time,omitempty"`
	GoVersion           string `json:"go_version,omitempty"`
	KeywordIndexEngine  string `json:"keyword_index_engine,omitempty"`
	VectorStoreEngine   string `json:"vector_store_engine,omitempty"`
	GraphDatabaseEngine string `json:"graph_database_engine,omitempty"`
	MinioEnabled        bool   `json:"minio_enabled,omitempty"`
	DBVersion           string `json:"db_version,omitempty"`
	// DBMigrationError carries the human-readable error message recorded when
	// the most recent startup migration attempt failed. Empty when migrations
	// succeeded; non-empty values let the frontend surface a troubleshooting
	// banner instead of silently hiding the DB version row (see issue #1319).
	DBMigrationError string `json:"db_migration_error,omitempty"`
	// StartedAt is the server process boot time (RFC3339, UTC).
	StartedAt string `json:"started_at,omitempty"`
	// UptimeSeconds is seconds elapsed since process start.
	UptimeSeconds int64 `json:"uptime_seconds,omitempty"`
}

// 编译时注入的版本信息
var (
	Version   = "unknown"
	Edition   = "standard"
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
	minioEnabled := h.isMinioConfigured(c)

	dbMigrationErr := database.CachedMigrationError()
	var dbVersion string
	if ver, dirty, ok := database.CachedMigrationVersion(); ok {
		dbVersion = fmt.Sprintf("%d", ver)
		if dirty {
			dbVersion += " (dirty)"
		}
		if dbMigrationErr != "" {
			dbVersion += " (failed)"
		}
	} else if dbMigrationErr != "" {
		// Failure happened before m.Version() could be read (e.g. could not
		// open the database). Still emit a placeholder so the frontend renders
		// the row and shows the troubleshooting banner.
		dbVersion = "unknown"
	}

	var startedAt string
	var uptimeSec int64
	if boot := runtime.ServerStartedAt(); !boot.IsZero() {
		startedAt = boot.UTC().Format(time.RFC3339)
		uptimeSec = int64(runtime.ServerUptime().Seconds())
	}

	response := GetSystemInfoResponse{
		Version:             Version,
		Edition:             Edition,
		CommitID:            CommitID,
		BuildTime:           BuildTime,
		GoVersion:           GoVersion,
		KeywordIndexEngine:  keywordIndexEngine,
		VectorStoreEngine:   vectorStoreEngine,
		GraphDatabaseEngine: graphDatabaseEngine,
		MinioEnabled:        minioEnabled,
		DBVersion:           dbVersion,
		DBMigrationError:    dbMigrationErr,
		StartedAt:           startedAt,
		UptimeSeconds:       uptimeSec,
	}

	logger.Info(ctx, "System info retrieved successfully")
	c.JSON(200, gin.H{
		"code": 0,
		"msg":  "success",
		"data": response,
	})
}

func (h *SystemHandler) getDocReaderConnInfo() (addr, transport string) {
	addr = strings.TrimSpace(os.Getenv("DOCREADER_ADDR"))
	transport = strings.TrimSpace(os.Getenv("DOCREADER_TRANSPORT"))
	if transport == "" {
		transport = "grpc"
	}
	transport = strings.ToLower(transport)
	return addr, transport
}

// ListParserEngines returns available document parser engines.
// Merges Go-native static engines with engines discovered from the remote
// docreader service, so newly added Python engines are auto-discovered.
// @Summary      列出可用的文档解析引擎
// @Tags         系统
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "解析引擎列表"
// @Router       /system/parser-engines [get]
func (h *SystemHandler) ListParserEngines(c *gin.Context) {
	var overrides map[string]string
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil {
			if tenant.ParserEngineConfig != nil {
				overrides = tenant.ParserEngineConfig.ToOverridesMap()
			}
			if creds := tenant.Credentials.GetWeKnoraCloud(); creds != nil {
				if overrides == nil {
					overrides = make(map[string]string)
				}
				overrides["weknoracloud_app_id"] = creds.AppID
			}
		}
	}

	reader, docreaderAddr, docreaderTransport := h.resolveDocReader(c.Request.Context(), overrides)
	connected := reader != nil && reader.IsConnected()
	remoteEngines := h.fetchRemoteEngines(c.Request.Context(), reader, overrides)
	engines := docparser.ListAllEngines(connected, overrides, remoteEngines)
	c.JSON(200, gin.H{"code": 0, "msg": "success", "data": engines, "docreader_addr": docreaderAddr, "docreader_transport": docreaderTransport, "connected": connected})
}

// ReconnectDocReader reconnects the document converter to a new (or same) DocReader address.
// @Summary      重连文档解析服务
// @Tags         系统
// @Accept       json
// @Produce      json
// @Param        request  body  object{addr string} true "DocReader 地址"
// @Success      200
// @Router       /system/docreader/reconnect [post]
func (h *SystemHandler) ReconnectDocReader(c *gin.Context) {
	var req struct {
		Addr string `json:"addr" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"code": 1, "msg": "请提供 addr 参数"})
		return
	}
	addr := strings.TrimSpace(req.Addr)
	if addr == "" {
		c.JSON(400, gin.H{"code": 1, "msg": "addr 不能为空"})
		return
	}

	// SSRF validation for docreader address
	if err := secutils.ValidateURLForSSRF(addr); err != nil {
		logger.Warnf(c.Request.Context(), "SSRF validation failed for docreader addr: %v", err)
		c.JSON(400, gin.H{"code": 1, "msg": secutils.FormatSSRFError("DocReader 地址", addr, err)})
		return
	}

	if h.documentReader == nil {
		c.JSON(500, gin.H{"code": 1, "msg": "document converter not initialized"})
		return
	}

	if err := h.documentReader.Reconnect(addr); err != nil {
		logger.Errorf(c.Request.Context(), "Failed to reconnect docreader to %s: %v", addr, err)
		c.JSON(200, gin.H{"code": 1, "msg": fmt.Sprintf("连接失败: %v", err)})
		return
	}

	var overrides map[string]string
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil {
			if tenant.ParserEngineConfig != nil {
				overrides = tenant.ParserEngineConfig.ToOverridesMap()
			}
			if creds := tenant.Credentials.GetWeKnoraCloud(); creds != nil {
				if overrides == nil {
					overrides = make(map[string]string)
				}
				overrides["weknoracloud_app_id"] = creds.AppID
			}
		}
	}
	remoteEngines := h.fetchRemoteEngines(c.Request.Context(), h.documentReader, overrides)
	engines := docparser.ListAllEngines(true, overrides, remoteEngines)

	_, docreaderTransport := h.getDocReaderConnInfo()
	c.JSON(200, gin.H{"code": 0, "msg": "连接成功", "data": engines, "docreader_addr": addr, "docreader_transport": docreaderTransport, "connected": true})
}

// CheckParserEngines runs availability check with the given config overrides (e.g. current form values).
// Used to test engine availability without saving; body shape matches ParserEngineConfig.
// @Summary      使用当前参数检测解析引擎可用性
// @Tags         系统
// @Accept       json
// @Produce      json
// @Param        body  body  object  true  "解析引擎配置（与保存接口同结构）"
// @Success      200
// @Router       /system/parser-engines/check [post]
func (h *SystemHandler) CheckParserEngines(c *gin.Context) {
	var body types.ParserEngineConfig
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"code": 1, "msg": "请求体格式错误"})
		return
	}
	var existing *types.ParserEngineConfig
	var tenant *types.Tenant
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if t, ok := v.(*types.Tenant); ok && t != nil {
			tenant = t
			existing = t.ParserEngineConfig
		}
	}
	merged := types.MergeParserEngineConfigForUpdate(&body, existing)
	overrides := merged.ToOverridesMap()
	if tenant != nil {
		if creds := tenant.Credentials.GetWeKnoraCloud(); creds != nil {
			if overrides == nil {
				overrides = make(map[string]string)
			}
			overrides["weknoracloud_app_id"] = creds.AppID
		}
	}
	reader, docreaderAddr, docreaderTransport := h.resolveDocReader(c.Request.Context(), overrides)
	connected := reader != nil && reader.IsConnected()
	remoteEngines := h.fetchRemoteEngines(c.Request.Context(), reader, overrides)
	engines := docparser.ListAllEngines(connected, overrides, remoteEngines)
	c.JSON(200, gin.H{"code": 0, "msg": "success", "data": engines, "docreader_addr": docreaderAddr, "docreader_transport": docreaderTransport, "connected": connected})
}

func (h *SystemHandler) resolveDocReader(ctx context.Context, overrides map[string]string) (interfaces.DocumentReader, string, string) {
	if len(overrides) > 0 {
		if addr := strings.TrimSpace(overrides["docreader_addr"]); addr != "" && service.IsWeKnoraCloudDocReaderAddr(addr) {
			reader := h.ResolveDocumentReader(ctx, addr)
			return reader, addr, transportFromDocReaderAddr(addr)
		}
	}

	addr, transport := h.getDocReaderConnInfo()
	return h.documentReader, addr, transport
}

func transportFromDocReaderAddr(addr string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(addr)), "https://") {
		return "https"
	}
	return "http"
}

// fetchRemoteEngines queries the remote docreader for its engine list.
// Returns nil on any error (e.g. not connected), letting the caller
// fall back to Go's static registry only.
func (h *SystemHandler) fetchRemoteEngines(ctx context.Context, reader interfaces.DocumentReader, overrides map[string]string) []types.ParserEngineInfo {
	if reader == nil || !reader.IsConnected() {
		return nil
	}
	engines, err := reader.ListEngines(ctx, overrides)
	if err != nil {
		logger.Warnf(ctx, "Failed to fetch remote engines from docreader: %v", err)
		return nil
	}
	return engines
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
		if h.supportsRetrieverType(driver, types.KeywordsRetrieverType) {
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
		if h.supportsRetrieverType(driver, types.VectorRetrieverType) {
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
		return "Not Enabled"
	}
	return "Neo4j"
}

// supportsRetrieverType checks if a driver supports a specific retriever type
// by looking up the retrieverEngineMapping from types package
func (h *SystemHandler) supportsRetrieverType(driver string, retrieverType types.RetrieverType) bool {
	// Get the mapping of all supported drivers and their capabilities
	mapping := types.GetRetrieverEngineMapping()

	// Check if the driver exists in the mapping
	engines, exists := mapping[driver]
	if !exists {
		return false
	}

	// Check if any of the engine configurations support the requested retriever type
	for _, engine := range engines {
		if engine.RetrieverType == retrieverType {
			return true
		}
	}
	return false
}

// getMinioConfig resolves MinIO connection parameters from tenant config (if mode=remote) or env vars (mode=docker/default).
func (h *SystemHandler) getMinioConfig(c *gin.Context) (endpoint, accessKeyID, secretAccessKey string) {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.MinIO != nil {
			m := tenant.StorageEngineConfig.MinIO
			if m.Mode == "remote" {
				return m.Endpoint, m.AccessKeyID, m.SecretAccessKey
			}
		}
	}
	endpoint = os.Getenv("MINIO_ENDPOINT")
	accessKeyID = os.Getenv("MINIO_ACCESS_KEY_ID")
	secretAccessKey = os.Getenv("MINIO_SECRET_ACCESS_KEY")
	return
}

// isMinioConfigured checks whether MinIO connection info is available (from tenant config or env).
func (h *SystemHandler) isMinioConfigured(c *gin.Context) bool {
	endpoint, accessKeyID, secretAccessKey := h.getMinioConfig(c)
	return endpoint != "" && accessKeyID != "" && secretAccessKey != ""
}

// isMinioEnvAvailable checks whether MinIO env vars (MINIO_ENDPOINT etc.) are set.
func (h *SystemHandler) isMinioEnvAvailable() bool {
	return os.Getenv("MINIO_ENDPOINT") != "" &&
		os.Getenv("MINIO_ACCESS_KEY_ID") != "" &&
		os.Getenv("MINIO_SECRET_ACCESS_KEY") != ""
}

// isCOSConfigured checks whether COS connection info is available from tenant config.
func (h *SystemHandler) isCOSConfigured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.COS != nil {
			cosConf := tenant.StorageEngineConfig.COS
			return cosConf.SecretID != "" && cosConf.SecretKey != "" && cosConf.Region != "" && cosConf.BucketName != ""
		}
	}
	return false
}

// isTOSConfigured checks whether TOS connection info is available from tenant config or env.
func (h *SystemHandler) isTOSConfigured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.TOS != nil {
			tosConf := tenant.StorageEngineConfig.TOS
			return tosConf.Endpoint != "" && tosConf.Region != "" && tosConf.AccessKey != "" && tosConf.SecretKey != "" && tosConf.BucketName != ""
		}
	}
	return h.isTOSEnvAvailable()
}

// isOSSConfigured checks whether OSS connection info is available from tenant config.
func (h *SystemHandler) isOSSConfigured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.OSS != nil {
			ossConf := tenant.StorageEngineConfig.OSS
			return ossConf.Endpoint != "" && ossConf.Region != "" && ossConf.AccessKey != "" && ossConf.SecretKey != "" && ossConf.BucketName != ""
		}
	}
	return false
}

// isKS3Configured checks whether KS3 connection info is available from tenant config.
func (h *SystemHandler) isKS3Configured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.KS3 != nil {
			ks3Conf := tenant.StorageEngineConfig.KS3
			return ks3Conf.Endpoint != "" && ks3Conf.Region != "" && ks3Conf.AccessKey != "" && ks3Conf.SecretKey != "" && ks3Conf.BucketName != ""
		}
	}
	return false
}

// isTOSEnvAvailable checks whether TOS env vars are set.
func (h *SystemHandler) isTOSEnvAvailable() bool {
	return os.Getenv("TOS_ENDPOINT") != "" &&
		os.Getenv("TOS_REGION") != "" &&
		os.Getenv("TOS_ACCESS_KEY") != "" &&
		os.Getenv("TOS_SECRET_KEY") != "" &&
		os.Getenv("TOS_BUCKET_NAME") != ""
}

// StorageEngineStatusItem describes one storage engine's availability and description.
type StorageEngineStatusItem struct {
	Name        string `json:"name"` // "local", "minio", "cos", "tos", "s3", "oss", "ks3"
	Allowed     bool   `json:"allowed"`
	Available   bool   `json:"available"`   // whether the engine can be used
	Description string `json:"description"` // short description for UI
}

// GetStorageEngineStatusResponse is the response for GET /system/storage-engine-status.
type GetStorageEngineStatusResponse struct {
	Engines           []StorageEngineStatusItem `json:"engines"`
	AllowedProviders  []string                  `json:"allowed_providers"`
	MinioEnvAvailable bool                      `json:"minio_env_available"`
}

// GetStorageEngineStatus godoc
// @Summary      获取存储引擎状态
// @Description  返回 Local、MinIO、COS 各存储引擎的可用状态及说明，供全局设置与知识库选择使用
// @Tags         系统
// @Produce      json
// @Success      200  {object}  GetStorageEngineStatusResponse
// @Router       /system/storage-engine-status [get]
func (h *SystemHandler) GetStorageEngineStatus(c *gin.Context) {
	minioConfigured := h.isMinioConfigured(c)
	minioEnvAvailable := h.isMinioEnvAvailable()
	cosConfigured := h.isCOSConfigured(c)
	tosConfigured := h.isTOSConfigured(c)
	s3Configured := h.isS3Configured(c)
	ossConfigured := h.isOSSConfigured(c)
	ks3Configured := h.isKS3Configured(c)
	allowed := getAllowedStorageProviders()
	allowedProviders := make([]string, 0, len(supportedStorageProviders))
	for _, provider := range getSupportedStorageProviders() {
		if allowed[provider] {
			allowedProviders = append(allowedProviders, provider)
		}
	}
	engines := []StorageEngineStatusItem{
		{Name: "local", Allowed: allowed["local"], Available: true, Description: "本地文件系统存储，仅适合单机部署"},
		{Name: "minio", Allowed: allowed["minio"], Available: minioConfigured || minioEnvAvailable, Description: "S3 兼容的自托管对象存储，适合内网和私有云部署"},
		{Name: "cos", Allowed: allowed["cos"], Available: cosConfigured, Description: "腾讯云对象存储服务，适合公有云部署，支持 CDN 加速"},
		{Name: "tos", Allowed: allowed["tos"], Available: tosConfigured, Description: "火山引擎对象存储服务，适合公有云部署"},
		{Name: "s3", Allowed: allowed["s3"], Available: s3Configured, Description: "AWS S3 与兼容对象存储服务，适合公有云与混合云部署"},
		{Name: "oss", Allowed: allowed["oss"], Available: ossConfigured, Description: "阿里云对象存储服务，适合公有云部署，支持 S3 兼容协议"},
		{Name: "ks3", Allowed: allowed["ks3"], Available: ks3Configured, Description: "金山云对象存储服务，适合公有云部署"},
	}
	c.JSON(200, gin.H{
		"code": 0,
		"msg":  "success",
		"data": GetStorageEngineStatusResponse{Engines: engines, AllowedProviders: allowedProviders, MinioEnvAvailable: minioEnvAvailable},
	})
}

// --- Storage engine helpers ---
// cosFieldPattern validates COS region and bucket name format to prevent URL injection.
var cosFieldPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

// ossFieldPattern validates OSS region and bucket name format to prevent URL injection.
var ossFieldPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

// sanitizeStorageCheckError converts a raw storage connectivity error into a safe
// user-facing message that does not leak internal network details (hostnames, IPs, ports).
func sanitizeStorageCheckError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Endpoint url cannot have fully qualified paths"):
		return "Endpoint 地址格式错误：请去除 http:// 或 https:// 前缀，只填写域名或 IP 地址和端口（例如：minio.example.com:9000）"
	case strings.Contains(msg, "no such host"):
		return "DNS 解析失败，请检查地址是否正确"
	case strings.Contains(msg, "connection refused"):
		return "连接被拒绝，请确认服务已启动且端口正确"
	case strings.Contains(msg, "no route to host"):
		return "无法路由到目标地址，请检查网络配置"
	case strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context deadline"):
		return "连接超时，请检查网络或服务状态"
	case strings.Contains(msg, "403") || strings.Contains(msg, "AccessDenied") || strings.Contains(msg, "access denied"):
		return "认证失败，请检查访问凭证是否正确"
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "tls") || strings.Contains(msg, "x509"):
		return "TLS/SSL 证书错误，请检查 SSL 配置"
	case strings.Contains(msg, "404") || strings.Contains(msg, "NoSuchBucket"):
		return "Bucket 不存在，请检查名称和 Region"
	default:
		return "连接失败，请检查配置参数是否正确"
	}
}

// storageEndpointHost extracts the hostname from a storage endpoint string.
// Endpoints may be bare host:port, hostnames, or full URLs with a scheme
// (e.g. "http://127.0.0.1:9000" for S3-compatible stores).
func storageEndpointHost(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if strings.Contains(endpoint, "://") {
		if u, err := url.Parse(endpoint); err == nil {
			if h := u.Hostname(); h != "" {
				return h
			}
		}
	}
	if host, _, err := net.SplitHostPort(endpoint); err == nil {
		return host
	}
	return endpoint
}

// isBlockedStorageEndpoint checks whether a storage endpoint resolves to a dangerous
// address (cloud metadata, loopback, link-local). Unlike the stricter isSSRFSafeURL,
// this allows private IPs since MinIO is commonly deployed on internal networks.
// It also respects the SSRF_WHITELIST environment variable for whitelisted hosts.
func isBlockedStorageEndpoint(endpoint string) (bool, string) {
	host := storageEndpointHost(endpoint)
	if host == "" {
		return true, "无效的地址"
	}

	// Check SSRF whitelist first – whitelisted hosts bypass the block check.
	if secutils.IsSSRFWhitelisted(host) {
		return false, ""
	}

	hostLower := strings.ToLower(host)

	blockedHosts := []string{
		"metadata.google.internal",
		"metadata.tencentyun.com",
		"metadata.aws.internal",
	}
	for _, bh := range blockedHosts {
		if hostLower == bh {
			return true, "该地址不允许访问"
		}
	}

	checkIP := func(ip net.IP) (bool, string) {
		if ip.IsLoopback() {
			return true, "不允许访问本地回环地址"
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return true, "不允许访问链路本地地址"
		}
		if ip.IsUnspecified() {
			return true, "无效的地址"
		}
		return false, ""
	}

	if ip := net.ParseIP(host); ip != nil {
		return checkIP(ip)
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return false, ""
	}
	for _, ip := range ips {
		if blocked, reason := checkIP(ip); blocked {
			return blocked, reason
		}
	}
	return false, ""
}

// --- Storage engine connectivity check ---

// StorageCheckRequest is the body for POST /system/storage-engine-check.
type StorageCheckRequest struct {
	Provider string                   `json:"provider"` // "minio", "cos", "tos", "s3", "oss", "ks3", "obs"
	MinIO    *types.MinIOEngineConfig `json:"minio,omitempty"`
	COS      *types.COSEngineConfig   `json:"cos,omitempty"`
	TOS      *types.TOSEngineConfig   `json:"tos,omitempty"`
	S3       *types.S3EngineConfig    `json:"s3,omitempty"`
	OSS      *types.OSSEngineConfig   `json:"oss,omitempty"`
	KS3      *types.KS3EngineConfig   `json:"ks3,omitempty"`
	OBS      *types.OBSEngineConfig   `json:"obs,omitempty"`
}

// StorageCheckResponse is the response for a single-engine connectivity check.
type StorageCheckResponse struct {
	OK            bool   `json:"ok"`
	Message       string `json:"message"`
	BucketCreated bool   `json:"bucket_created,omitempty"`
}

// CheckStorageEngine tests connectivity for a single storage engine using the provided config.
// @Summary      测试存储引擎连通性
// @Description  使用当前填写的参数测试 MinIO/COS 连通性，不保存配置
// @Tags         系统
// @Accept       json
// @Produce      json
// @Param        body  body  StorageCheckRequest  true  "存储引擎配置"
// @Success      200   {object}  StorageCheckResponse
// @Router       /system/storage-engine-check [post]
func (h *SystemHandler) CheckStorageEngine(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req StorageCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"code": 1, "msg": "请求体格式错误"})
		return
	}
	if !isStorageProviderAllowed(req.Provider) {
		c.JSON(403, gin.H{"code": 1, "msg": "该存储引擎已被禁用"})
		return
	}

	switch req.Provider {
	case "minio":
		h.checkMinio(c, ctx, req.MinIO)
	case "cos":
		h.checkCOS(c, ctx, req.COS)
	case "tos":
		h.checkTOS(c, ctx, req.TOS)
	case "s3":
		h.checkS3(c, ctx, req.S3)
	case "oss":
		h.checkOSS(c, ctx, req.OSS)
	case "ks3":
		h.checkKS3(c, ctx, req.KS3)
	case "obs":
		h.checkOBS(c, ctx, req.OBS)
	default:
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, Message: "本地存储无需检测"}})
	}
}

func (h *SystemHandler) isS3Configured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.S3 != nil {
			s3Conf := tenant.StorageEngineConfig.S3
			return s3Conf.Endpoint != "" && s3Conf.Region != "" && s3Conf.AccessKey != "" && s3Conf.SecretKey != "" && s3Conf.BucketName != ""
		}
	}
	return false
}

func (h *SystemHandler) checkMinio(c *gin.Context, ctx context.Context, cfg *types.MinIOEngineConfig) {
	if cfg == nil {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "未提供 MinIO 配置"}})
		return
	}

	if cfg.BucketName != "" && !cosFieldPattern.MatchString(cfg.BucketName) {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Bucket 名称格式不正确，仅允许字母、数字、点、连字符"}})
		return
	}

	endpoint, accessKeyID, secretAccessKey := cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey
	if cfg.Mode != "remote" {
		endpoint = os.Getenv("MINIO_ENDPOINT")
		accessKeyID = os.Getenv("MINIO_ACCESS_KEY_ID")
		secretAccessKey = os.Getenv("MINIO_SECRET_ACCESS_KEY")
	}
	if endpoint == "" || accessKeyID == "" || secretAccessKey == "" {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Endpoint、Access Key、Secret Key 不能为空"}})
		return
	}

	if cfg.Mode == "remote" {
		if blocked, reason := isBlockedStorageEndpoint(endpoint); blocked {
			logger.Warnf(ctx, "Storage check: MinIO endpoint blocked by SSRF protection, endpoint: %s", endpoint)
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: reason}})
			return
		}
	}

	err := file.CheckMinioConnectivity(ctx, endpoint, accessKeyID, secretAccessKey, cfg.BucketName, cfg.UseSSL)
	if err != nil {
		errMsg := err.Error()
		// If bucket does not exist, auto-create it
		if strings.Contains(errMsg, "does not exist") && cfg.BucketName != "" {
			logger.Info(ctx, "Storage check: bucket does not exist, attempting auto-creation", "bucket", cfg.BucketName)
			minioClient, clientErr := minio.New(endpoint, &minio.Options{
				Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
				Secure: cfg.UseSSL,
			})
			if clientErr != nil {
				c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("Failed to create MinIO client: %s", sanitizeStorageCheckError(clientErr))}})
				return
			}
			if mkErr := minioClient.MakeBucket(ctx, cfg.BucketName, minio.MakeBucketOptions{}); mkErr != nil {
				logger.Error(ctx, "Storage check: failed to create bucket", "bucket", cfg.BucketName, "error", mkErr)
				c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("Failed to auto-create Bucket '%s': %s", cfg.BucketName, sanitizeStorageCheckError(mkErr))}})
				return
			}
			logger.Info(ctx, "Storage check: bucket created", "bucket", cfg.BucketName)
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, BucketCreated: true, Message: fmt.Sprintf("Bucket '%s' does not exist, and has been automatically created", cfg.BucketName)}})
			return
		}
		logger.Error(ctx, "Storage check: MinIO connectivity failed", "error", err)
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: sanitizeStorageCheckError(err)}})
		return
	}

	msg := "连接成功"
	if cfg.BucketName != "" {
		msg = fmt.Sprintf("连接成功，Bucket「%s」已确认存在", cfg.BucketName)
	}
	c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, Message: msg}})
}

func (h *SystemHandler) checkCOS(c *gin.Context, ctx context.Context, cfg *types.COSEngineConfig) {
	if cfg == nil {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "未提供 COS 配置"}})
		return
	}
	if cfg.SecretID == "" || cfg.SecretKey == "" || cfg.Region == "" || cfg.BucketName == "" {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Secret ID、Secret Key、Region、Bucket 名称不能为空"}})
		return
	}
	if !cosFieldPattern.MatchString(cfg.Region) {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Region 格式不正确，仅允许字母、数字、点、连字符"}})
		return
	}
	if !cosFieldPattern.MatchString(cfg.BucketName) {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Bucket 名称格式不正确，仅允许字母、数字、点、连字符"}})
		return
	}

	err := file.CheckCosConnectivity(ctx, cfg.BucketName, cfg.Region, cfg.SecretID, cfg.SecretKey)
	if err != nil {
		logger.Errorf(ctx, "Storage check: COS connectivity failed, bucket: %s, error: %v", cfg.BucketName, err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") {
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "认证失败，请检查 Secret ID / Secret Key 是否正确"}})
			return
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "NoSuchBucket") {
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("Bucket「%s」不存在，请检查名称和 Region", cfg.BucketName)}})
			return
		}
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: sanitizeStorageCheckError(err)}})
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, Message: fmt.Sprintf("连接成功，Bucket「%s」已确认存在", cfg.BucketName)}})
}

func (h *SystemHandler) checkTOS(c *gin.Context, ctx context.Context, cfg *types.TOSEngineConfig) {
	if cfg == nil {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "未提供 TOS 配置"}})
		return
	}
	if cfg.Endpoint == "" || cfg.Region == "" || cfg.AccessKey == "" || cfg.SecretKey == "" || cfg.BucketName == "" {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Endpoint、Region、Access Key、Secret Key、Bucket 名称不能为空"}})
		return
	}

	if blocked, reason := isBlockedStorageEndpoint(cfg.Endpoint); blocked {
		logger.Warnf(ctx, "Storage check: TOS endpoint blocked by SSRF protection, endpoint: %s", cfg.Endpoint)
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: reason}})
		return
	}

	err := file.CheckTosConnectivity(ctx, cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey, cfg.BucketName)
	if err != nil {
		logger.Errorf(ctx, "Storage check: TOS connectivity failed, bucket: %s, error: %v", cfg.BucketName, err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") {
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "认证失败，请检查 Access Key / Secret Key 是否正确"}})
			return
		}
		if strings.Contains(errMsg, "404") {
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("Bucket「%s」不存在，请检查名称和 Region", cfg.BucketName)}})
			return
		}
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: sanitizeStorageCheckError(err)}})
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, Message: fmt.Sprintf("连接成功，Bucket「%s」已确认存在", cfg.BucketName)}})
}

func (h *SystemHandler) checkS3(c *gin.Context, ctx context.Context, cfg *types.S3EngineConfig) {
	if cfg == nil {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "未提供 S3 配置"}})
		return
	}
	if cfg.Endpoint == "" || cfg.Region == "" || cfg.AccessKey == "" || cfg.SecretKey == "" || cfg.BucketName == "" {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Endpoint、Region、Access Key、Secret Key、Bucket 名称不能为空"}})
		return
	}

	if blocked, reason := isBlockedStorageEndpoint(cfg.Endpoint); blocked {
		logger.Warnf(ctx, "Storage check: S3 endpoint blocked by SSRF protection, endpoint: %s", cfg.Endpoint)
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: reason}})
		return
	}

	err := file.CheckS3Connectivity(ctx, cfg.Endpoint, cfg.AccessKey, cfg.SecretKey, cfg.BucketName, cfg.Region)
	if err != nil {
		logger.Errorf(ctx, "Storage check: S3 connectivity failed, bucket: %s, error: %v", cfg.BucketName, err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") {
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "认证失败，请检查 Access Key / Secret Key 是否正确"}})
			return
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "NotFound") {
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("Bucket「%s」不存在，请检查名称和 Region", cfg.BucketName)}})
			return
		}
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: sanitizeStorageCheckError(err)}})
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, Message: fmt.Sprintf("连接成功，Bucket「%s」已确认存在", cfg.BucketName)}})
}

func (h *SystemHandler) checkOSS(c *gin.Context, ctx context.Context, cfg *types.OSSEngineConfig) {
	if cfg == nil {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "未提供 OSS 配置"}})
		return
	}

	endpoint, accessKey, secretKey := cfg.Endpoint, cfg.AccessKey, cfg.SecretKey
	if endpoint == "" || accessKey == "" || secretKey == "" || cfg.BucketName == "" {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Endpoint、Access Key、Secret Key、Bucket Name 不能为空"}})
		return
	}

	// Strip URL scheme before SSRF check — OSS endpoint may include http:// or https://
	ssrfEndpoint := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	if blocked, reason := isBlockedStorageEndpoint(ssrfEndpoint); blocked {
		logger.Warnf(ctx, "Storage check: OSS endpoint blocked by SSRF protection, endpoint: %s", endpoint)
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: reason}})
		return
	}
	if !ossFieldPattern.MatchString(cfg.Region) {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Region 格式不正确，仅允许字母、数字、点、连字符"}})
		return
	}
	if !ossFieldPattern.MatchString(cfg.BucketName) {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Bucket 名称格式不正确，仅允许字母、数字、点、连字符"}})
		return
	}

	err := file.CheckOssConnectivity(ctx, endpoint, cfg.Region, accessKey, secretKey, cfg.BucketName)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") || strings.Contains(errMsg, "AccessDenied") {
			logger.Errorf(ctx, "Storage check: OSS auth failed, endpoint: %s, bucket: %s", endpoint, cfg.BucketName)
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "认证失败，请检查 Access Key / Secret Key 是否正确"}})
			return
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "NoSuchBucket") {
			logger.Errorf(ctx, "Storage check: OSS bucket not found, bucket: %s", cfg.BucketName)
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("Bucket「%s」不存在", cfg.BucketName)}})
			return
		}
		logger.Errorf(ctx, "Storage check: OSS connectivity failed, endpoint: %s, bucket: %s, error: %v", endpoint, cfg.BucketName, err)
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("OSS 连通性检测失败: %s", sanitizeStorageCheckError(err))}})
		return
	}

	c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, Message: fmt.Sprintf("连接成功，Bucket「%s」已确认存在", cfg.BucketName)}})
}

func (h *SystemHandler) checkKS3(c *gin.Context, ctx context.Context, cfg *types.KS3EngineConfig) {
	if cfg == nil {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "未提供 KS3 配置"}})
		return
	}

	endpoint, region, accessKey, secretKey := cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey
	if endpoint == "" || region == "" || accessKey == "" || secretKey == "" || cfg.BucketName == "" {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Endpoint、Region、Access Key、Secret Key、Bucket 名称不能为空"}})
		return
	}

	if blocked, reason := isBlockedStorageEndpoint(endpoint); blocked {
		logger.Warnf(ctx, "Storage check: KS3 endpoint blocked by SSRF protection, endpoint: %s", endpoint)
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: reason}})
		return
	}

	err := file.CheckKS3Connectivity(ctx, endpoint, region, accessKey, secretKey, cfg.BucketName)
	if err != nil {
		logger.Errorf(ctx, "Storage check: KS3 connectivity failed, bucket: %s, error: %v", cfg.BucketName, err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") || strings.Contains(errMsg, "AccessDenied") {
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "认证失败，请检查 Access Key / Secret Key 是否正确"}})
			return
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "NoSuchBucket") {
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("Bucket「%s」不存在，请检查名称和 Region", cfg.BucketName)}})
			return
		}
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: sanitizeStorageCheckError(err)}})
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, Message: fmt.Sprintf("连接成功，Bucket「%s」已确认存在", cfg.BucketName)}})
}

func (h *SystemHandler) checkOBS(c *gin.Context, ctx context.Context, cfg *types.OBSEngineConfig) {
	if cfg == nil {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "未提供 OBS 配置"}})
		return
	}

	endpoint, region, accessKey, secretKey := cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey
	if endpoint == "" || region == "" || accessKey == "" || secretKey == "" || cfg.BucketName == "" {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Endpoint、Region、Access Key、Secret Key、Bucket 名称不能为空"}})
		return
	}

	ssrfEndpoint := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	if blocked, reason := isBlockedStorageEndpoint(ssrfEndpoint); blocked {
		logger.Warnf(ctx, "Storage check: OBS endpoint blocked by SSRF protection, endpoint: %s", endpoint)
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: reason}})
		return
	}

	if !ossFieldPattern.MatchString(cfg.Region) {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Region 格式不正确，仅允许字母、数字、点、连字符"}})
		return
	}
	if !ossFieldPattern.MatchString(cfg.BucketName) {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "Bucket 名称格式不正确，仅允许字母、数字、点、连字符"}})
		return
	}

	err := file.CheckObsConnectivity(ctx, endpoint, region, accessKey, secretKey, cfg.BucketName)
	if err != nil {
		logger.Errorf(ctx, "Storage check: OBS connectivity failed, bucket: %s, error: %v", cfg.BucketName, err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") || strings.Contains(errMsg, "AccessDenied") {
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "认证失败，请检查 Access Key / Secret Key 是否正确"}})
			return
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "NoSuchBucket") {
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("Bucket「%s」不存在，请检查名称和 Region", cfg.BucketName)}})
			return
		}
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: sanitizeStorageCheckError(err)}})
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, Message: fmt.Sprintf("连接成功，Bucket「%s」已确认存在", cfg.BucketName)}})
}

func (h *SystemHandler) ResolveDocumentReader(ctx context.Context, addr string) interfaces.DocumentReader {
	if addr == "" {
		return h.documentReader
	}

	if service.IsWeKnoraCloudDocReaderAddr(addr) {
		creds := h.tenantSvc.GetWeKnoraCloudCredentials(ctx)
		if creds == nil {
			return nil
		}
		reader, err := docparser.NewWeKnoraCloudSignedDocumentReader(creds.AppID, creds.AppSecret)
		if err != nil {
			return nil
		}
		return reader
	}

	reader, err := docparser.NewHTTPDocumentReader(addr)
	if err != nil || reader == nil {
		return reader
	}
	return reader
}

// PromoteUserToSystemAdminRequest defines the request for promoting a user to system admin.
//
// Either user_id (UUID) or email must be supplied. When both are present
// user_id wins — explicit IDs are unambiguous, an email collision (extremely
// rare in practice but possible during a tenant merge) would otherwise
// silently target the wrong row.
//
// We don't expose a `binding:"required"` tag on either field because gin's
// validator can't express the OR constraint; the handler does the check
// manually and returns 400 with a specific message for each branch.
type PromoteUserToSystemAdminRequest struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// PromoteUserToSystemAdmin godoc
// @Summary      Promote a user to system administrator
// @Description  Grant system administrator privileges to a user (SystemAdmin only).
// @Description  Idempotent: re-promoting an existing system admin returns 200 with no DB write.
// @Description  Identify the user by email (preferred for human operators) or user_id (UUID, for API clients).
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        request body PromoteUserToSystemAdminRequest true "User promotion request"
// @Success      200  {object}  types.UserInfo  "User promoted successfully"
// @Failure      400  {object}  map[string]interface{}  "Bad request"
// @Failure      403  {object}  map[string]interface{}  "Forbidden: not a system admin"
// @Failure      404  {object}  map[string]interface{}  "User not found"
// @Router       /system/admin/promote [post]
func (h *SystemHandler) PromoteUserToSystemAdmin(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req PromoteUserToSystemAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	userID := strings.TrimSpace(req.UserID)
	email := strings.TrimSpace(req.Email)
	if userID == "" && email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Either user_id or email is required"})
		return
	}

	// Resolve user. user_id takes priority when both are sent (see request
	// struct doc); otherwise we look up by email. Both branches funnel
	// into the same {nil-user / error} 404 so we don't leak whether a
	// given email exists in the system to non-admins — though SystemAdmin
	// is already a high-trust role, the parity keeps the surface clean.
	var (
		user *types.User
		err  error
	)
	switch {
	case userID != "":
		user, err = h.userSvc.GetUserByID(ctx, userID)
	default:
		user, err = h.userSvc.GetUserByEmail(ctx, email)
	}
	if err != nil {
		logger.Errorf(ctx, "Error fetching user (id=%q email=%q): %v", userID, email, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	if user.IsSystemAdmin {
		// Idempotent: re-promoting an existing system admin is a no-op
		// success. We still emit an audit row so probing the endpoint
		// leaves a forensic trail (idempotent=true marks it as noop).
		h.emitAdminAudit(ctx, types.AuditActionSystemAdminPromoted, user, map[string]any{
			"target_email":    user.Email,
			"target_username": user.Username,
			"idempotent":      true,
		})
		c.JSON(http.StatusOK, user.ToUserInfo())
		return
	}
	user.IsSystemAdmin = true
	if err := h.userSvc.UpdateUser(ctx, user); err != nil {
		logger.Errorf(ctx, "Error promoting user %s to system admin: %v", req.UserID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to promote user"})
		return
	}

	logger.Infof(ctx, "User %s (ID: %s) promoted to system admin", user.Username, user.ID)
	h.emitAdminAudit(ctx, types.AuditActionSystemAdminPromoted, user, map[string]any{
		"target_email":    user.Email,
		"target_username": user.Username,
		"idempotent":      false,
	})
	c.JSON(http.StatusOK, user.ToUserInfo())
}

// RevokeSystemAdminRequest defines the request for revoking system admin privileges
type RevokeSystemAdminRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

// RevokeSystemAdmin godoc
// @Summary      Revoke system administrator privileges from a user
// @Description  Remove system administrator privileges from a user (SystemAdmin only).
// @Description  Two safety guards: the caller cannot revoke their own privileges,
// @Description  and revoking the last remaining system admin is rejected — both
// @Description  prevent a SystemAdmin from accidentally locking the platform out
// @Description  of system-level administration. Idempotent on already-non-admin users.
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        request body RevokeSystemAdminRequest true "User revocation request"
// @Success      200  {object}  types.UserInfo  "Privileges revoked successfully"
// @Failure      400  {object}  map[string]interface{}  "Bad request / would remove last admin / self-revoke"
// @Failure      403  {object}  map[string]interface{}  "Forbidden: not a system admin"
// @Failure      404  {object}  map[string]interface{}  "User not found"
// @Router       /system/admin/revoke [post]
func (h *SystemHandler) RevokeSystemAdmin(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req RevokeSystemAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	callerID, _ := types.UserIDFromContext(ctx)
	user, err := h.userSvc.RevokeSystemAdmin(ctx, req.UserID, callerID)
	switch {
	case err == nil:
		// Real revoke — privileges were actually removed.
		logger.Infof(ctx, "System admin privileges revoked from user %s (ID: %s)", user.Username, user.ID)
		h.emitAdminAudit(ctx, types.AuditActionSystemAdminRevoked, user, map[string]any{
			"target_email":    user.Email,
			"target_username": user.Username,
			"changed":         true,
		})
		c.JSON(http.StatusOK, user.ToUserInfo())
		return
	case errors.Is(err, repository.ErrUserNotSystemAdmin):
		// Idempotent: target was already not an admin. Return 200 so
		// callers that re-issue the request after a partial failure
		// don't get a confusing 4xx, but mark `changed=false` in the
		// audit row so a forensic reader can tell a real revoke from a
		// noop probe.
		if user == nil {
			logger.Errorf(ctx, "RevokeSystemAdmin returned nil user with ErrUserNotSystemAdmin for %s", req.UserID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke system admin privileges"})
			return
		}
		logger.Infof(ctx, "Revoke noop (user %s was not a system admin)", user.ID)
		h.emitAdminAudit(ctx, types.AuditActionSystemAdminRevoked, user, map[string]any{
			"target_email":    user.Email,
			"target_username": user.Username,
			"changed":         false,
		})
		c.JSON(http.StatusOK, user.ToUserInfo())
		return
	case errors.Is(err, repository.ErrCannotRevokeSelf):
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Cannot revoke your own system admin privileges",
		})
		return
	case errors.Is(err, repository.ErrLastSystemAdmin):
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Cannot revoke the last remaining system administrator",
		})
		return
	case errors.Is(err, repository.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	default:
		logger.Errorf(ctx, "Error revoking system admin from user %s: %v", req.UserID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke system admin privileges"})
		return
	}
}

// ListSystemAdminsResponse defines the response structure for listing system admins.
// Total reflects the underlying COUNT(*), not just the page size, so the front
// end can render pagination metadata without a follow-up call.
type ListSystemAdminsResponse struct {
	Total  int64             `json:"total"`
	Admins []*types.UserInfo `json:"admins"`
}

// ListSystemAdmins godoc
// @Summary      List all system administrators
// @Description  Retrieve a paginated list of users with system administrator
// @Description  privileges (SystemAdmin only). Supports `offset` (default 0)
// @Description  and `limit` (default 50, max 200) query parameters. Walks the
// @Description  partial-friendly idx_users_is_system_admin index.
// @Tags         System Admin
// @Produce      json
// @Param        offset query int false "Page offset" default(0)
// @Param        limit  query int false "Page size (max 200)" default(50)
// @Success      200  {object}  ListSystemAdminsResponse  "System admins retrieved successfully"
// @Failure      403  {object}  map[string]interface{}  "Forbidden: not a system admin"
// @Router       /system/admin/list [get]
func (h *SystemHandler) ListSystemAdmins(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	// Best-effort pagination parsing — a malformed `limit=foo` falls back
	// to defaults rather than 400-ing, since the call is still safe and a
	// failed-page is more user-hostile than a soft default.
	offset := 0
	limit := 50
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	// Cap so a client can't ask for the entire table.
	if limit > 200 {
		limit = 200
	}

	users, total, err := h.userSvc.ListSystemAdmins(ctx, offset, limit)
	if err != nil {
		logger.Errorf(ctx, "Error listing system admins: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list system admins"})
		return
	}

	// Always emit a non-nil slice so the JSON serialises to `[]` rather
	// than `null` for an empty page — front-end iteration is safer.
	infos := make([]*types.UserInfo, 0, len(users))
	for _, u := range users {
		infos = append(infos, u.ToUserInfo())
	}

	c.JSON(http.StatusOK, ListSystemAdminsResponse{
		Total:  total,
		Admins: infos,
	})
}

// ResetUserPasswordRequest defines the system-administrator password-reset
// payload. Email is intentionally the only user-facing identifier: the UI is
// an operator tool and emails are easier to verify than UUIDs. The password is
// never written to logs or audit details.
type ResetUserPasswordRequest struct {
	Email       string `json:"email" binding:"required,email"`
	NewPassword string `json:"new_password" binding:"required"`
}

// ResetUserPassword godoc
// @Summary      Reset another user's password
// @Description  Replace another user's local password and revoke all of their existing sessions (SystemAdmin only).
// @Description  A system administrator cannot reset their own password through this endpoint; self-service password change still requires the old password.
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        request body ResetUserPasswordRequest true "Password reset request"
// @Success      200  {object}  map[string]interface{}  "Password reset successfully"
// @Failure      400  {object}  map[string]interface{}  "Invalid request, weak password, or self reset"
// @Failure      403  {object}  map[string]interface{}  "Forbidden: not a system admin"
// @Failure      404  {object}  map[string]interface{}  "User not found"
// @Router       /system/admin/users/reset-password [post]
func (h *SystemHandler) ResetUserPassword(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req ResetUserPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid password reset request"})
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if err := service.ValidatePasswordPolicy(req.NewPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := h.userSvc.GetUserByEmail(ctx, req.Email)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	callerID, _ := types.UserIDFromContext(ctx)
	if callerID == user.ID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot reset your own password here"})
		return
	}

	if err := h.userSvc.AdminResetPassword(ctx, user.ID, req.NewPassword); err != nil {
		if errors.Is(err, service.ErrPasswordPolicy) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		logger.Errorf(ctx, "Failed to reset password for user %s: %v", user.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reset user password"})
		return
	}

	logger.Infof(ctx, "Password reset by system administrator for user ID: %s", user.ID)
	h.emitAdminAudit(ctx, types.AuditActionSystemUserPasswordReset, user, map[string]any{
		"target_email":     user.Email,
		"target_username":  user.Username,
		"sessions_revoked": true,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Password reset successfully"})
}

// ============================================================================
// System Settings (P1)
// ----------------------------------------------------------------------------
// Endpoints below are mounted under /api/v1/system/admin/settings*, all
// gated to SystemAdmin via the route group's middleware. Every response
// is the raw model — no `gin.H{"data": ...}` wrapping — to match the
// project's axios interceptor contract (response.data is unwrapped at the
// HTTP layer; see frontend/src/utils/request.ts:97). The P0 ListSystemAdmins
// already follows this; do not break the convention.
// ============================================================================

// ListSystemSettings godoc
// @Summary      List all system settings
// @Description  Return every row in the system_settings table (system-scope,
// @Description  not workspace-scoped). SystemAdmin only.
// @Tags         System Admin
// @Produce      json
// @Success      200 {array} types.SystemSetting "list of settings"
// @Failure      403 {object} map[string]interface{} "Forbidden: not a system admin"
// @Router       /system/admin/settings [get]
// RuntimeQueuesResponse is the payload for the SystemAdmin runtime queue
// dashboard. `available` is false in Lite mode (no Redis/asynq) so the
// UI can render an "unavailable in this deployment" state instead of an
// empty table. Each pool reports configured per-process concurrency plus live
// cluster capacity/active workers aggregated from asynq server heartbeats.
type RuntimeWorkerPool struct {
	Name            string  `json:"name"`
	Concurrency     int     `json:"concurrency"` // configured per instance
	QueueCount      int     `json:"queue_count"`
	Instances       int     `json:"instances"`        // live asynq server heartbeats
	ClusterCapacity int     `json:"cluster_capacity"` // sum of live server concurrency
	Active          int     `json:"active"`           // live workers assigned to this pool
	Utilization     float64 `json:"utilization"`      // Active / ClusterCapacity
}

type RuntimeQueuesResponse struct {
	Available             bool                       `json:"available"`
	UpstreamConcurrency   int                        `json:"upstream_concurrency"`
	ParseConcurrency      int                        `json:"parse_concurrency"` // compatibility alias for upstream_concurrency
	WikiConcurrency       int                        `json:"wiki_concurrency"`  // compatibility field
	Pools                 []RuntimeWorkerPool        `json:"pools"`
	Queues                []types.QueueStat          `json:"queues"`
	ModelLimiterAvailable bool                       `json:"model_limiter_available"`
	Models                []modellimiter.RuntimeStat `json:"models"`
	Timestamp             int64                      `json:"timestamp"`
}

func aggregateRuntimeWorkerPools(pools []RuntimeWorkerPool, servers []types.WorkerServerStat) {
	queueConfigs := map[string]map[string]int{
		types.WorkerPoolCore:        types.QueueWeightsForPool(types.WorkerPoolCore),
		types.WorkerPoolPostProcess: types.QueueWeightsForPool(types.WorkerPoolPostProcess),
		types.WorkerPoolEnrichment:  types.QueueWeightsForPool(types.WorkerPoolEnrichment),
		types.WorkerPoolMaintenance: types.QueueWeightsForPool(types.WorkerPoolMaintenance),
		types.WorkerPoolShared:      types.QueueWeightsForSharedPool(),
		types.WorkerPoolWiki:        types.QueueWeightsForPool(types.WorkerPoolWiki),
	}
	indexes := make(map[string]int, len(pools))
	for i := range pools {
		indexes[pools[i].Name] = i
	}
	for _, server := range servers {
		if server.Status != "active" {
			continue
		}
		for poolName, expectedQueues := range queueConfigs {
			if !sameQueueWeights(server.Queues, expectedQueues) {
				continue
			}
			idx, ok := indexes[poolName]
			if !ok {
				break
			}
			pools[idx].Instances++
			pools[idx].ClusterCapacity += server.Concurrency
			pools[idx].Active += server.Active
			break
		}
	}
	for i := range pools {
		if pools[i].ClusterCapacity > 0 {
			pools[i].Utilization = float64(pools[i].Active) / float64(pools[i].ClusterCapacity)
		}
	}
}

func sameQueueWeights(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for name, weight := range left {
		if right[name] != weight {
			return false
		}
	}
	return true
}

// GetRuntimeQueues godoc
// @Summary      获取解析任务队列运行时状态
// @Description  返回各 asynq 队列的实时深度（pending/active/scheduled/retry 等）与 worker 并发配置，仅系统管理员可见
// @Tags         系统管理
// @Produce      json
// @Success      200  {object}  RuntimeQueuesResponse
// @Router       /system/admin/runtime/queues [get]
func (h *SystemHandler) GetRuntimeQueues(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	var allocation types.WorkerPoolConcurrency
	if h.systemSettingSvc != nil {
		allocation = types.ResolveWorkerPoolConcurrency(func(key, env string, fallback int) int {
			configured := h.systemSettingSvc.GetInt(ctx, key, env, int64(fallback))
			return int(configured)
		})
	} else {
		allocation = types.DefaultWorkerPoolConcurrency()
	}
	queueCounts := make(map[string]int)
	for _, definition := range types.QueueDefinitions() {
		queueCounts[definition.Pool]++
	}
	resp := RuntimeQueuesResponse{
		UpstreamConcurrency: allocation.UpstreamTotal(),
		ParseConcurrency:    allocation.UpstreamTotal(),
		WikiConcurrency:     allocation.Wiki,
		Pools: []RuntimeWorkerPool{
			{Name: types.WorkerPoolCore, Concurrency: allocation.Core, QueueCount: queueCounts[types.WorkerPoolCore]},
			{Name: types.WorkerPoolPostProcess, Concurrency: allocation.PostProcess, QueueCount: queueCounts[types.WorkerPoolPostProcess]},
			{Name: types.WorkerPoolEnrichment, Concurrency: allocation.Enrichment, QueueCount: queueCounts[types.WorkerPoolEnrichment]},
			{Name: types.WorkerPoolMaintenance, Concurrency: allocation.Maintenance, QueueCount: queueCounts[types.WorkerPoolMaintenance]},
			{Name: types.WorkerPoolShared, Concurrency: allocation.Shared, QueueCount: len(types.QueueWeightsForSharedPool())},
			{Name: types.WorkerPoolWiki, Concurrency: allocation.Wiki, QueueCount: queueCounts[types.WorkerPoolWiki]},
		},
		Timestamp: time.Now().Unix(),
	}

	if h.taskInspector != nil {
		stats, supported, err := h.taskInspector.QueueStats(ctx)
		if err != nil {
			logger.Errorf(ctx, "get queue stats failed: %v", err)
		}
		resp.Available = supported
		if stats != nil {
			resp.Queues = stats
		}
		serverStats, _, serverErr := h.taskInspector.WorkerServerStats(ctx)
		if serverErr != nil {
			logger.Errorf(ctx, "get worker server stats failed: %v", serverErr)
		} else {
			aggregateRuntimeWorkerPools(resp.Pools, serverStats)
		}
	}
	// Always emit a non-nil array so the JSON serialises to `[]` rather
	// than `null`, keeping the frontend iteration simple.
	if resp.Queues == nil {
		resp.Queues = []types.QueueStat{}
	}
	modelStats, modelSupported, modelErr := modellimiter.RuntimeStats(ctx)
	if modelErr != nil {
		logger.Errorf(ctx, "get model concurrency stats failed: %v", modelErr)
	}
	resp.ModelLimiterAvailable = modelSupported
	resp.Models = modelStats

	c.JSON(http.StatusOK, resp)
}

type RuntimeTasksResponse struct {
	Available  bool                    `json:"available"`
	Tasks      []types.RuntimeTaskInfo `json:"tasks"`
	PageSize   int                     `json:"page_size"`
	HasMore    bool                    `json:"has_more"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

func isKnownRuntimeQueue(name string) bool {
	for _, definition := range types.QueueDefinitions() {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func runtimeTaskPageSize(c *gin.Context) int {
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return pageSize
}

func (h *SystemHandler) listRuntimeTasks(c *gin.Context, state types.RuntimeTaskState) {
	queue := c.Param("queue")
	if !isKnownRuntimeQueue(queue) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown task queue"})
		return
	}
	if !state.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown task state"})
		return
	}
	pageSize := runtimeTaskPageSize(c)
	cursor := c.Query("cursor")
	inspector, ok := h.taskInspector.(interfaces.RuntimeTaskInspector)
	if !ok {
		c.JSON(http.StatusOK, RuntimeTasksResponse{
			Available: false,
			Tasks:     []types.RuntimeTaskInfo{},
			PageSize:  pageSize,
		})
		return
	}
	page, supported, err := inspector.ListRuntimeTasks(c.Request.Context(), queue, state, cursor, pageSize)
	if err != nil {
		if errors.Is(err, types.ErrInvalidRuntimeTaskCursor) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Invalid task list cursor",
				"code":  "runtime_task_cursor_invalid",
			})
			return
		}
		if errors.Is(err, types.ErrExpiredRuntimeTaskCursor) {
			c.JSON(http.StatusConflict, gin.H{
				"error": "Task list changed; refresh from the beginning",
				"code":  "runtime_task_cursor_expired",
			})
			return
		}
		logger.Errorf(c.Request.Context(), "list runtime queue tasks queue=%s state=%s: %v", queue, state, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list queue tasks"})
		return
	}
	if page.Tasks == nil {
		page.Tasks = []types.RuntimeTaskInfo{}
	}
	c.JSON(http.StatusOK, RuntimeTasksResponse{
		Available:  supported,
		Tasks:      page.Tasks,
		PageSize:   pageSize,
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	})
}

// ListRuntimeTasks returns one live task-state page for the unified operator
// drawer. Raw task payloads are never included.
// @Summary      List runtime queue tasks by state
// @Tags         System Admin
// @Produce      json
// @Param        queue path string true "Queue name"
// @Param        state query string true "Task state" Enums(pending,active,scheduled,retry,archived,completed)
// @Param        cursor query string false "Opaque continuation cursor"
// @Param        page_size query int false "Page size" default(20)
// @Success      200 {object} RuntimeTasksResponse
// @Router       /system/admin/runtime/queues/{queue}/tasks [get]
func (h *SystemHandler) ListRuntimeTasks(c *gin.Context) {
	h.listRuntimeTasks(c, types.RuntimeTaskState(c.Query("state")))
}

func (h *SystemHandler) emitQueueTaskAudit(
	ctx context.Context,
	action types.AuditAction,
	queue, taskID string,
	extra map[string]string,
) {
	if h.auditSvc == nil {
		return
	}
	actorID, _ := types.UserIDFromContext(ctx)
	detailMap := map[string]string{"queue": queue, "task_id": taskID}
	for key, value := range extra {
		detailMap[key] = value
	}
	details, _ := json.Marshal(detailMap)
	targetType := "queue_task"
	targetID := taskID
	if targetID == "" {
		targetType = "task_queue"
		targetID = queue
	}
	_ = h.auditSvc.Log(ctx, &types.AuditLog{
		TenantID:    0,
		ActorUserID: actorID,
		ActorRole:   "system_admin",
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		Outcome:     types.AuditOutcomeSuccess,
		Details:     types.JSON(details),
	})
}

func runtimeTaskParams(c *gin.Context) (string, string, bool) {
	queue := c.Param("queue")
	taskID := c.Param("task_id")
	if !isKnownRuntimeQueue(queue) || taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid queue or task ID"})
		return "", "", false
	}
	return queue, taskID, true
}

func (h *SystemHandler) mutateRuntimeTask(c *gin.Context, action types.RuntimeTaskAction) {
	queue, taskID, ok := runtimeTaskParams(c)
	if !ok {
		return
	}
	inspector, supported := h.taskInspector.(interfaces.RuntimeTaskInspector)
	if !supported {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Task queue is unavailable"})
		return
	}
	task, available, err := inspector.GetRuntimeTask(c.Request.Context(), queue, taskID)
	if !available {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Task queue is unavailable"})
		return
	}
	if err != nil || task == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Task is no longer available"})
		return
	}
	if !task.Allows(action) {
		c.JSON(http.StatusConflict, gin.H{"error": "Action is not allowed for the current task state"})
		return
	}

	var auditAction types.AuditAction
	auditDetails := map[string]string{
		"task_type":  task.Type,
		"from_state": string(task.State),
		"action":     string(action),
	}
	switch action {
	case types.RuntimeTaskActionCancel:
		if h.knowledgeSvc == nil || task.TenantID == 0 || task.KnowledgeID == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Business cancellation is unavailable"})
			return
		}
		cancelCtx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, task.TenantID)
		if _, err = h.knowledgeSvc.CancelKnowledgeParse(cancelCtx, task.KnowledgeID); err != nil {
			if isKnowledgeGone(err) {
				if h.purgeOrphanRuntimeTask(c.Request.Context(), inspector, queue, taskID, task.KnowledgeID) {
					logger.Warnf(c.Request.Context(),
						"cancel runtime queue task queue=%s task=%s: knowledge gone, purged queue record",
						queue, taskID)
					auditDetails["orphan_purge"] = "true"
					auditAction = types.AuditActionSystemQueueTaskCancelled
					break
				}
			}
			logger.Errorf(c.Request.Context(), "cancel runtime queue task queue=%s task=%s: %v", queue, taskID, err)
			c.JSON(http.StatusConflict, gin.H{"error": "Task can no longer be cancelled"})
			return
		}
		auditAction = types.AuditActionSystemQueueTaskCancelled
	case types.RuntimeTaskActionRunNow:
		available, err = inspector.RunRuntimeTask(c.Request.Context(), queue, taskID)
		if !available {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Task queue is unavailable"})
			return
		}
		if err != nil {
			logger.Errorf(c.Request.Context(), "run queue task now queue=%s task=%s: %v", queue, taskID, err)
			c.JSON(http.StatusConflict, gin.H{"error": "Task is no longer available to run"})
			return
		}
		auditAction = types.AuditActionSystemQueueTaskRunNow
	case types.RuntimeTaskActionDelete:
		available, err = inspector.DeleteRuntimeTask(c.Request.Context(), queue, taskID)
		if !available {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Task queue is unavailable"})
			return
		}
		if err != nil {
			logger.Errorf(c.Request.Context(), "delete queue task queue=%s task=%s: %v", queue, taskID, err)
			c.JSON(http.StatusConflict, gin.H{"error": "Task is no longer available for deletion"})
			return
		}
		auditAction = types.AuditActionSystemQueueTaskDeleted
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown task action"})
		return
	}
	h.emitQueueTaskAudit(c.Request.Context(), auditAction, queue, taskID, auditDetails)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func isKnowledgeGone(err error) bool {
	if errors.Is(err, repository.ErrKnowledgeNotFound) {
		return true
	}
	var appErr *apperrors.AppError
	return errors.As(err, &appErr) && appErr.Code == apperrors.ErrNotFound
}

// purgeOrphanRuntimeTask removes queue records for a task whose knowledge row
// is already gone. CancelTasksForKnowledge sweeps sibling tasks best-effort;
// success is reported only once the requested task ID is gone.
func (h *SystemHandler) purgeOrphanRuntimeTask(
	ctx context.Context,
	inspector interfaces.RuntimeTaskInspector,
	queue, taskID, knowledgeID string,
) bool {
	if h.taskInspector != nil && knowledgeID != "" {
		if _, _, err := h.taskInspector.CancelTasksForKnowledge(ctx, knowledgeID); err != nil {
			logger.Warnf(ctx, "purge orphan tasks for knowledge %s: %v", knowledgeID, err)
		}
	}
	supported, err := inspector.ForceDeleteRuntimeTask(ctx, queue, taskID)
	if !supported {
		return false
	}
	if err == nil {
		return true
	}
	// The knowledge-wide sweep may have already removed this task ID.
	if _, available, getErr := inspector.GetRuntimeTask(ctx, queue, taskID); available && getErr != nil {
		return true
	}
	logger.Warnf(ctx, "force delete orphan task queue=%s task=%s: %v", queue, taskID, err)
	return false
}

// MutateRuntimeTask executes a backend-advertised action after re-reading the
// current task state to close click/race windows.
// @Summary      Run a safe runtime task action
// @Tags         System Admin
// @Produce      json
// @Param        queue path string true "Queue name"
// @Param        task_id path string true "Task ID"
// @Param        action path string true "Action" Enums(cancel,run_now,delete)
// @Success      200 {object} map[string]bool
// @Router       /system/admin/runtime/queues/{queue}/tasks/{task_id}/actions/{action} [post]
func (h *SystemHandler) MutateRuntimeTask(c *gin.Context) {
	h.mutateRuntimeTask(c, types.RuntimeTaskAction(c.Param("action")))
}

func (h *SystemHandler) ListSystemSettings(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	rows, err := h.systemSettingSvc.List(ctx)
	if err != nil {
		logger.Errorf(ctx, "list system settings failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list system settings"})
		return
	}
	if rows == nil {
		// Always emit a non-nil array so the JSON serialises to `[]`
		// rather than `null` on an empty table — front-end iteration
		// is safer.
		rows = []*types.SystemSetting{}
	}
	h.enrichSettingsModifiedBy(ctx, rows)
	c.JSON(http.StatusOK, rows)
}

// enrichSettingsModifiedBy resolves LastModifiedBy (UUID) → display name
// in a single batch lookup. Failures degrade silently: the UI already
// falls back to the UUID prefix when the name is empty, so a transient
// userSvc error must not break the settings page entirely.
func (h *SystemHandler) enrichSettingsModifiedBy(ctx context.Context, rows []*types.SystemSetting) {
	if len(rows) == 0 || h.userSvc == nil {
		return
	}
	idSet := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		if r != nil && strings.TrimSpace(r.LastModifiedBy) != "" {
			idSet[r.LastModifiedBy] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	users, err := h.userSvc.GetUsersByIDs(ctx, ids)
	if err != nil {
		logger.Warnf(ctx, "enrichSettingsModifiedBy: GetUsersByIDs failed: %v", err)
		return
	}
	for _, r := range rows {
		if r == nil {
			continue
		}
		u, ok := users[r.LastModifiedBy]
		if !ok || u == nil {
			continue
		}
		// Username is the canonical display label; fall back to email
		// for older rows where username may be empty (legacy seeded
		// admins). Both empty → leave LastModifiedByName empty so the
		// UI's UUID-prefix fallback kicks in.
		switch {
		case strings.TrimSpace(u.Username) != "":
			r.LastModifiedByName = u.Username
		case strings.TrimSpace(u.Email) != "":
			r.LastModifiedByName = u.Email
		}
	}
}

// GetSystemSetting godoc
// @Summary      Get a single system setting by key
// @Description  Returns the row matching :key. 404 when the key is unknown
// @Description  to the registry; 200 with the row when known.
// @Tags         System Admin
// @Produce      json
// @Param        key path string true "Setting key (e.g. file.max_size_mb)"
// @Success      200 {object} types.SystemSetting "the setting row"
// @Failure      400 {object} map[string]interface{} "Unknown key"
// @Failure      404 {object} map[string]interface{} "Key registered but DB row absent"
// @Router       /system/admin/settings/{key} [get]
func (h *SystemHandler) GetSystemSetting(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	key := c.Param("key")
	row, err := h.systemSettingSvc.Get(ctx, key)
	if err != nil {
		// Service-layer "unknown key" surfaces as a generic error here;
		// distinguish via the error string rather than typed errors so
		// we don't grow a sentinel package for a single error class.
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if row == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "setting not yet persisted"})
		return
	}
	c.JSON(http.StatusOK, row)
}

// UpdateSystemSettingRequest is the body for PUT /system/admin/settings/:key.
// `value` carries the new value as raw JSON — int / string / bool depending
// on the registry-declared value_type. The service validates the type
// strictly and rejects mismatches with 400.
type UpdateSystemSettingRequest struct {
	// Value is intentionally `any` (decoded as float64 / string / bool /
	// etc. by the JSON unmarshaller). Service.encodeForType normalises
	// these against the registry's declared type and rejects mismatches.
	Value any `json:"value"`
}

// UpdateSystemSetting godoc
// @Summary      Update a system setting value
// @Description  Persist a new value for :key. Service validates the
// @Description  rawValue against the registry's declared value_type and
// @Description  rejects mismatches with 400. SystemAdmin only. Emits an
// @Description  audit row (action=system.setting_changed) on success.
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        key     path string                       true "Setting key"
// @Param        request body UpdateSystemSettingRequest   true "New value"
// @Success      200 {object} types.SystemSetting "the updated row"
// @Failure      400 {object} map[string]interface{} "Bad request / unknown key / type mismatch"
// @Failure      403 {object} map[string]interface{} "Forbidden: not a system admin"
// @Router       /system/admin/settings/{key} [put]
func (h *SystemHandler) UpdateSystemSetting(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	key := c.Param("key")

	var req UpdateSystemSettingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}
	if req.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "value is required"})
		return
	}

	row, err := h.systemSettingSvc.Update(ctx, key, req.Value)
	if err != nil {
		// Whether this is "unknown key" / "type mismatch" / "DB error"
		// is encoded in the error message at the service layer; surface
		// it verbatim. UI captures it as the toast text.
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.enrichSettingsModifiedBy(ctx, []*types.SystemSetting{row})
	c.JSON(http.StatusOK, row)
}

// ApplyDefaultStorageQuotaToAllTenants godoc
// @Summary      Apply the default storage quota to every existing workspace
// @Description  Reads the current value of `tenant.default_storage_quota_gb`
// @Description  (3-tier resolver: DB > ENV > default) and writes that many
// @Description  GiB into storage_quota for every row in tenants. Bypasses
// @Description  the per-workspace PUT whitelist, which forbids storage_quota
// @Description  edits by Owners. SystemAdmin only.
// @Description  Idempotent — running twice with the same setting is a no-op.
// @Tags         System Admin
// @Produce      json
// @Success      200 {object} map[string]interface{} "{ affected: int64, quota_bytes: int64 }"
// @Failure      500 {object} map[string]interface{} "DB write failed"
// @Router       /system/admin/tenants/apply-default-storage-quota [post]
func (h *SystemHandler) ApplyDefaultStorageQuotaToAllTenants(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	// Resolve via the same 3-tier path the CreateTenant handler uses
	// so the action's effect mirrors what new tenants would receive.
	gb := h.systemSettingSvc.GetInt(
		ctx,
		"tenant.default_storage_quota_gb",
		"WEKNORA_TENANT_DEFAULT_STORAGE_QUOTA_GB",
		10,
	)
	if gb <= 0 {
		gb = 10
	}
	quotaBytes := gb * 1024 * 1024 * 1024

	affected, err := h.tenantSvc.BulkSetStorageQuota(ctx, quotaBytes)
	if err != nil {
		logger.Errorf(ctx, "BulkSetStorageQuota failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to apply storage quota"})
		return
	}

	// Audit. We capture quota_bytes (the actual value written) rather
	// than the GB literal so the audit row is unambiguous if someone
	// later changes the setting's units.
	if h.auditSvc != nil {
		actorID, _ := types.UserIDFromContext(ctx)
		details, _ := json.Marshal(map[string]any{
			"quota_bytes": quotaBytes,
			"quota_gb":    gb,
			"affected":    affected,
		})
		_ = h.auditSvc.Log(ctx, &types.AuditLog{
			TenantID:    0,
			ActorUserID: actorID,
			ActorRole:   "system_admin",
			Action:      types.AuditActionSystemSettingChanged,
			TargetType:  "tenant_storage_quota",
			TargetID:    "all",
			Outcome:     types.AuditOutcomeSuccess,
			Details:     types.JSON(details),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"affected":    affected,
		"quota_bytes": quotaBytes,
		"quota_gb":    gb,
	})
}

// ResetSystemSetting godoc
// @Summary      Reset a system setting to ENV / built-in default
// @Description  Removes the DB override for :key so the 3-tier resolver
// @Description  falls back to the environment variable (when configured)
// @Description  or the in-code default. Idempotent — resetting a key
// @Description  that was never persisted returns 200.
// @Tags         System Admin
// @Param        key path string true "Setting key"
// @Success      200 {object} map[string]interface{} "Reset acknowledged"
// @Failure      400 {object} map[string]interface{} "Unknown key"
// @Failure      500 {object} map[string]interface{} "DB write failed"
// @Router       /system/admin/settings/{key} [delete]
func (h *SystemHandler) ResetSystemSetting(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	key := c.Param("key")
	if err := h.systemSettingSvc.Reset(ctx, key); err != nil {
		// Service surfaces "unknown key" as a plain error string; treat
		// every error as 400 so the UI captures it as a toast. A real
		// DB failure here is rare and indistinguishable from "bad key"
		// at this layer — operators see the message either way.
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
