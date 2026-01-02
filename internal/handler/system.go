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

// SystemHandler 시스템 관련 요청 처리
type SystemHandler struct {
	cfg         *config.Config
	neo4jDriver neo4j.Driver
}

// NewSystemHandler 새로운 시스템 핸들러 생성
func NewSystemHandler(cfg *config.Config, neo4jDriver neo4j.Driver) *SystemHandler {
	return &SystemHandler{
		cfg:         cfg,
		neo4jDriver: neo4jDriver,
	}
}

// GetSystemInfoResponse 시스템 정보에 대한 응답 구조 정의
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

// 컴파일 시 주입되는 버전 정보
var (
	Version   = "unknown"
	CommitID  = "unknown"
	BuildTime = "unknown"
	GoVersion = "unknown"
)

// GetSystemInfo godoc
// @Summary      시스템 정보 가져오기
// @Description  시스템 버전, 빌드 정보 및 엔진 구성 가져오기
// @Tags         시스템
// @Accept       json
// @Produce      json
// @Success      200  {object}  GetSystemInfoResponse  "시스템 정보"
// @Router       /system/info [get]
func (h *SystemHandler) GetSystemInfo(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	// RETRIEVE_DRIVER에서 키워드 인덱스 엔진 가져오기
	keywordIndexEngine := h.getKeywordIndexEngine()

	// config 또는 RETRIEVE_DRIVER에서 벡터 저장소 엔진 가져오기
	vectorStoreEngine := h.getVectorStoreEngine()

	// NEO4J_ENABLE에서 그래프 데이터베이스 엔진 가져오기
	graphDatabaseEngine := h.getGraphDatabaseEngine()

	// MinIO 활성화 상태 가져오기
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

// getKeywordIndexEngine 키워드 인덱스 엔진 이름 반환
func (h *SystemHandler) getKeywordIndexEngine() string {
	retrieveDriver := os.Getenv("RETRIEVE_DRIVER")
	if retrieveDriver == "" {
		return "미구성"
	}

	drivers := strings.Split(retrieveDriver, ",")
	// 키워드 검색을 지원하는 엔진 필터링
	keywordEngines := []string{}
	for _, driver := range drivers {
		driver = strings.TrimSpace(driver)
		if driver == "postgres" || driver == "elasticsearch_v7" || driver == "elasticsearch_v8" {
			keywordEngines = append(keywordEngines, driver)
		}
	}

	if len(keywordEngines) == 0 {
		return "미구성"
	}
	return strings.Join(keywordEngines, ", ")
}

// getVectorStoreEngine 벡터 저장소 엔진 이름 반환
func (h *SystemHandler) getVectorStoreEngine() string {
	// 먼저 config.yaml 확인
	if h.cfg != nil && h.cfg.VectorDatabase != nil && h.cfg.VectorDatabase.Driver != "" {
		return h.cfg.VectorDatabase.Driver
	}

	// 벡터 지원을 위해 RETRIEVE_DRIVER로 대체
	retrieveDriver := os.Getenv("RETRIEVE_DRIVER")
	if retrieveDriver == "" {
		return "미구성"
	}

	drivers := strings.Split(retrieveDriver, ",")
	// 벡터 검색을 지원하는 엔진 필터링
	vectorEngines := []string{}
	for _, driver := range drivers {
		driver = strings.TrimSpace(driver)
		if driver == "postgres" || driver == "elasticsearch_v8" {
			vectorEngines = append(vectorEngines, driver)
		}
	}

	if len(vectorEngines) == 0 {
		return "미구성"
	}
	return strings.Join(vectorEngines, ", ")
}

// getGraphDatabaseEngine 그래프 데이터베이스 엔진 이름 반환
func (h *SystemHandler) getGraphDatabaseEngine() string {
	if h.neo4jDriver == nil {
		return "비활성화됨"
	}
	return "Neo4j"
}

// isMinioEnabled MinIO 활성화 여부 확인
func (h *SystemHandler) isMinioEnabled() bool {
	// 필수 MinIO 환경 변수가 모두 설정되었는지 확인
	endpoint := os.Getenv("MINIO_ENDPOINT")
	accessKeyID := os.Getenv("MINIO_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("MINIO_SECRET_ACCESS_KEY")

	return endpoint != "" && accessKeyID != "" && secretAccessKey != ""
}

// MinioBucketInfo 접근 정책이 포함된 버킷 정보
type MinioBucketInfo struct {
	Name      string `json:"name"`
	Policy    string `json:"policy"` // "public", "private", "custom"
	CreatedAt string `json:"created_at,omitempty"`
}

// ListMinioBucketsResponse 버킷 목록 응답 구조 정의
type ListMinioBucketsResponse struct {
	Buckets []MinioBucketInfo `json:"buckets"`
}

// ListMinioBuckets godoc
// @Summary      MinIO 버킷 목록 조회
// @Description  모든 MinIO 버킷 및 접근 권한 조회
// @Tags         시스템
// @Accept       json
// @Produce      json
// @Success      200  {object}  ListMinioBucketsResponse  "버킷 목록"
// @Failure      400  {object}  map[string]interface{}    "MinIO 비활성화됨"
// @Failure      500  {object}  map[string]interface{}    "서버 오류"
// @Router       /system/minio/buckets [get]
func (h *SystemHandler) ListMinioBuckets(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	// MinIO 활성화 여부 확인
	if !h.isMinioEnabled() {
		logger.Warn(ctx, "MinIO is not enabled")
		c.JSON(400, gin.H{
			"code":    400,
			"msg":     "MinIO is not enabled",
			"success": false,
		})
		return
	}

	// 환경 변수에서 MinIO 구성 가져오기
	endpoint := os.Getenv("MINIO_ENDPOINT")
	accessKeyID := os.Getenv("MINIO_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("MINIO_SECRET_ACCESS_KEY")
	useSSL := os.Getenv("MINIO_USE_SSL") == "true"

	// MinIO 클라이언트 생성
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

	// 모든 버킷 나열
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

	// 각 버킷의 정책 가져오기
	bucketInfos := make([]MinioBucketInfo, 0, len(buckets))
	for _, bucket := range buckets {
		policy := "private" // 기본값: 정책이 없으면 비공개

		// 버킷 정책 가져오기 시도
		policyStr, err := minioClient.GetBucketPolicy(context.Background(), bucket.Name)
		if err == nil && policyStr != "" {
			policy = parseBucketPolicy(policyStr)
		}
		// err != nil이거나 policyStr이 비어 있으면 버킷에 정책이 없음 (비공개)

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

// BucketPolicy S3 버킷 정책 구조
type BucketPolicy struct {
	Version   string            `json:"Version"`
	Statement []PolicyStatement `json:"Statement"`
}

// PolicyStatement 버킷 정책의 단일 문장
type PolicyStatement struct {
	Effect    string      `json:"Effect"`
	Principal interface{} `json:"Principal"` // "*" 또는 {"AWS": [...]} 가능
	Action    interface{} `json:"Action"`    // 문자열 또는 []문자열 가능
	Resource  interface{} `json:"Resource"`  // 문자열 또는 []문자열 가능
}

// parseBucketPolicy 정책 JSON을 파싱하고 접근 유형 결정
func parseBucketPolicy(policyStr string) string {
	var policy BucketPolicy
	if err := json.Unmarshal([]byte(policyStr), &policy); err != nil {
		// 정책을 파싱할 수 없는 경우 사용자 정의로 처리
		return "custom"
	}

	// 공개 읽기 액세스를 허용하는 문장이 있는지 확인
	hasPublicRead := false
	for _, stmt := range policy.Statement {
		if stmt.Effect != "Allow" {
			continue
		}

		// Principal이 "*" (공개)인지 확인
		if !isPrincipalPublic(stmt.Principal) {
			continue
		}

		// Action에 s3:GetObject가 포함되어 있는지 확인
		if !hasGetObjectAction(stmt.Action) {
			continue
		}

		hasPublicRead = true
		break
	}

	if hasPublicRead {
		return "public"
	}

	// 정책은 있지만 공개 읽기가 아님
	return "custom"
}

// isPrincipalPublic Principal이 공개 액세스를 허용하는지 확인
func isPrincipalPublic(principal interface{}) bool {
	switch p := principal.(type) {
	case string:
		return p == "*"
	case map[string]interface{}:
		// {"AWS": "*"} 또는 {"AWS": ["*"]} 확인
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

// hasGetObjectAction Action에 s3:GetObject가 포함되어 있는지 확인
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
