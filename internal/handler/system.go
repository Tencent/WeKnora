package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// SystemHandler handles system-related requests
type SystemHandler struct {
	cfg            *config.Config
	neo4jDriver    neo4j.Driver
	documentReader interfaces.DocumentReader
}

// NewSystemHandler creates a new system handler
func NewSystemHandler(cfg *config.Config, neo4jDriver neo4j.Driver, documentReader interfaces.DocumentReader) *SystemHandler {
	return &SystemHandler{
		cfg:            cfg,
		neo4jDriver:    neo4jDriver,
		documentReader: documentReader,
	}
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

	var dbVersion string
	if ver, dirty, ok := database.CachedMigrationVersion(); ok {
		dbVersion = fmt.Sprintf("%d", ver)
		if dirty {
			dbVersion += " (dirty)"
		}
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
	docreaderAddr, docreaderTransport := h.getDocReaderConnInfo()
	connected := h.documentReader != nil && h.documentReader.IsConnected()

	var overrides map[string]string
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.ParserEngineConfig != nil {
			overrides = tenant.ParserEngineConfig.ToOverridesMap()
		}
	}

	remoteEngines := h.fetchRemoteEngines(c.Request.Context(), overrides)
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
		c.JSON(400, gin.H{"code": 1, "msg": fmt.Sprintf("地址未通过安全校验: %v", err)})
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
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.ParserEngineConfig != nil {
			overrides = tenant.ParserEngineConfig.ToOverridesMap()
		}
	}
	remoteEngines := h.fetchRemoteEngines(c.Request.Context(), overrides)
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
	docreaderAddr, docreaderTransport := h.getDocReaderConnInfo()
	connected := h.documentReader != nil && h.documentReader.IsConnected()

	var body types.ParserEngineConfig
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"code": 1, "msg": "请求体格式错误"})
		return
	}
	overrides := body.ToOverridesMap()
	remoteEngines := h.fetchRemoteEngines(c.Request.Context(), overrides)
	engines := docparser.ListAllEngines(connected, overrides, remoteEngines)
	c.JSON(200, gin.H{"code": 0, "msg": "success", "data": engines, "docreader_addr": docreaderAddr, "docreader_transport": docreaderTransport, "connected": connected})
}

// fetchRemoteEngines queries the remote docreader for its engine list.
// Returns nil on any error (e.g. not connected), letting the caller
// fall back to Go's static registry only.
func (h *SystemHandler) fetchRemoteEngines(ctx context.Context, overrides map[string]string) []types.ParserEngineInfo {
	if h.documentReader == nil || !h.documentReader.IsConnected() {
		return nil
	}
	engines, err := h.documentReader.ListEngines(ctx, overrides)
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

// isTOSEnvAvailable checks whether TOS env vars are set.
func (h *SystemHandler) isTOSEnvAvailable() bool {
	return os.Getenv("TOS_ENDPOINT") != "" &&
		os.Getenv("TOS_REGION") != "" &&
		os.Getenv("TOS_ACCESS_KEY") != "" &&
		os.Getenv("TOS_SECRET_KEY") != "" &&
		os.Getenv("TOS_BUCKET_NAME") != ""
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

// StorageEngineStatusItem describes one storage engine's availability and description.
type StorageEngineStatusItem struct {
	Name        string `json:"name"`        // "local", "minio", "cos", "tos"
	Available   bool   `json:"available"`   // whether the engine can be used
	Description string `json:"description"` // short description for UI
}

// GetStorageEngineStatusResponse is the response for GET /system/storage-engine-status.
type GetStorageEngineStatusResponse struct {
	Engines           []StorageEngineStatusItem `json:"engines"`
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
	engines := []StorageEngineStatusItem{
		{Name: "local", Available: true, Description: "本地文件系统存储，仅适合单机部署"},
		{Name: "minio", Available: minioConfigured || minioEnvAvailable, Description: "S3 兼容的自托管对象存储，适合内网和私有云部署"},
		{Name: "cos", Available: cosConfigured, Description: "腾讯云对象存储服务，适合公有云部署，支持 CDN 加速"},
		{Name: "tos", Available: tosConfigured, Description: "火山引擎对象存储服务，适合公有云部署"},
	}
	c.JSON(200, gin.H{
		"code": 0,
		"msg":  "success",
		"data": GetStorageEngineStatusResponse{Engines: engines, MinioEnvAvailable: minioEnvAvailable},
	})
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

	endpoint, accessKeyID, secretAccessKey := h.getMinioConfig(c)
	if endpoint == "" || accessKeyID == "" || secretAccessKey == "" {
		logger.Warn(ctx, "MinIO is not configured")
		c.JSON(400, gin.H{
			"code":    400,
			"msg":     "MinIO is not configured",
			"success": false,
		})
		return
	}

	useSSL := os.Getenv("MINIO_USE_SSL") == "true"
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.MinIO != nil {
			useSSL = tenant.StorageEngineConfig.MinIO.UseSSL
		}
	}

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

// --- Storage engine helpers ---

// cosFieldPattern validates COS region and bucket name format to prevent URL injection.
var cosFieldPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

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

// isBlockedStorageEndpoint checks whether a storage endpoint resolves to a dangerous
// address (cloud metadata, loopback, link-local). Unlike the stricter isSSRFSafeURL,
// this allows private IPs since MinIO is commonly deployed on internal networks.
// It also respects the SSRF_WHITELIST environment variable for whitelisted hosts.
func isBlockedStorageEndpoint(endpoint string) (bool, string) {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		host = endpoint
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
	Provider string                   `json:"provider"` // "minio", "cos", "tos", or "s3"
	MinIO    *types.MinIOEngineConfig `json:"minio,omitempty"`
	COS      *types.COSEngineConfig   `json:"cos,omitempty"`
	TOS      *types.TOSEngineConfig   `json:"tos,omitempty"`
	S3       *types.S3EngineConfig    `json:"s3,omitempty"`
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

	switch req.Provider {
	case "minio":
		h.checkMinio(c, ctx, req.MinIO)
	case "cos":
		h.checkCOS(c, ctx, req.COS)
	case "tos":
		h.checkTOS(c, ctx, req.TOS)
	case "s3":
		h.checkS3(c, ctx, req.S3)
	default:
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, Message: "本地存储无需检测"}})
	}
}

func (h *SystemHandler) checkMinio(c *gin.Context, ctx context.Context, cfg *types.MinIOEngineConfig) {
	if cfg == nil {
		c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: "未提供 MinIO 配置"}})
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
			logger.Warnf(ctx, "Storage check: MinIO endpoint blocked by SSRF protection", "endpoint", endpoint)
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: reason}})
			return
		}
	}

	err := file.CheckMinioConnectivity(ctx, endpoint, accessKeyID, secretAccessKey, cfg.BucketName, cfg.UseSSL)
	if err != nil {
		errMsg := err.Error()
		// If bucket does not exist, auto-create it with public-read policy
		if strings.Contains(errMsg, "does not exist") && cfg.BucketName != "" {
			logger.Info(ctx, "Storage check: bucket does not exist, attempting auto-creation", "bucket", cfg.BucketName)
			minioClient, clientErr := minio.New(endpoint, &minio.Options{
				Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
				Secure: cfg.UseSSL,
			})
			if clientErr != nil {
				c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("创建 MinIO 客户端失败: %s", sanitizeStorageCheckError(clientErr))}})
				return
			}
			if mkErr := minioClient.MakeBucket(ctx, cfg.BucketName, minio.MakeBucketOptions{}); mkErr != nil {
				logger.Error(ctx, "Storage check: failed to create bucket", "bucket", cfg.BucketName, "error", mkErr)
				c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: false, Message: fmt.Sprintf("自动创建 Bucket「%s」失败: %s", cfg.BucketName, sanitizeStorageCheckError(mkErr))}})
				return
			}
			// Set public-read policy
			publicReadPolicy := fmt.Sprintf(`{
				"Version": "2012-10-17",
				"Statement": [
					{
						"Effect": "Allow",
						"Principal": {"AWS": ["*"]},
						"Action": ["s3:GetBucketLocation", "s3:ListBucket"],
						"Resource": ["arn:aws:s3:::%s"]
					},
					{
						"Effect": "Allow",
						"Principal": {"AWS": ["*"]},
						"Action": ["s3:GetObject"],
						"Resource": ["arn:aws:s3:::%s/*"]
					}
				]
			}`, cfg.BucketName, cfg.BucketName)
			if policyErr := minioClient.SetBucketPolicy(ctx, cfg.BucketName, publicReadPolicy); policyErr != nil {
				logger.Error(ctx, "Storage check: bucket created but failed to set public-read policy", "bucket", cfg.BucketName, "error", policyErr)
				c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, BucketCreated: true, Message: fmt.Sprintf("Bucket「%s」已自动创建，但设置公有读策略失败，请手动配置权限", cfg.BucketName)}})
				return
			}
			logger.Info(ctx, "Storage check: bucket created with public-read policy", "bucket", cfg.BucketName)
			c.JSON(200, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, BucketCreated: true, Message: fmt.Sprintf("Bucket「%s」不存在，已自动创建并设置公有读权限", cfg.BucketName)}})
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
