// Package handler 提供自研后端的 Gin 路由与各资源 handler。
package handler

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	miniosdk "github.com/minio/minio-go/v7"
)

// Deps 路由依赖
type Deps struct {
	DB      *gorm.DB
	Cfg     *config.Config
	MinIO   *objstore.Client
	Wiki    *weknora.WikiClient
	WeKnora *weknora.Client
}

// NewRouter 构建自研后端路由。
// 自研服务走独立端口，业务 API 统一挂在 /api/custom/ 前缀下
// （与官方 /api/ 前缀分离，见个性化部署流程 §2.5 nginx 最长前缀优先匹配）。
func NewRouter(db *gorm.DB, cfg *config.Config) *gin.Engine {
	deps := &Deps{DB: db, Cfg: cfg}
	if m, err := objstore.New(cfg.MinIO); err == nil {
		deps.MinIO = m
	}
	deps.Wiki = weknora.NewWikiClient(cfg.WeKnora)
	deps.WeKnora = weknora.New(cfg.WeKnora)
	return buildRouter(deps)
}

func buildRouter(deps *Deps) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	router.Use(corsMiddleware())
	router.Use(uploadTraceMiddleware())

	// 健康检查：不挂 /api/custom，便于负载均衡探活
	router.GET("/healthz", healthCheck(deps.DB))

	// 上传测试页（本地/联调可视化验证）
	router.GET("/upload", uploadPage)

	// 业务路由分组
	api := router.Group("/api/custom")
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	if deps.MinIO != nil {
		uh := NewUploadHandler(deps.DB, deps.MinIO, deps.Cfg.Upload)
		transcriptImport := NewTranscriptImportHandler(deps.DB, deps.MinIO)
		uploads := api.Group("/uploads")
		uploads.POST("/presign", uh.Presign)
		uploads.POST("/confirm", uh.Confirm)
		uploads.POST("/direct", uh.Direct)
		uploads.POST("/multipart/init", uh.MultipartInit)
		uploads.POST("/multipart/sign", uh.MultipartSign)
		uploads.PUT("/multipart/part", uh.MultipartPart)
		uploads.POST("/multipart/complete", uh.MultipartComplete)
		uploads.POST("/multipart/abort", uh.MultipartAbort)
		api.POST("/videos/:id/retry-initial-processing", uh.RetryInitialProcessing)
		api.POST("/videos/:id/transcript/import", transcriptImport.Import)
		if deps.MinIO.IsLocal() {
			uploads.PUT("/local/:uploadID/parts/:partNumber", func(c *gin.Context) {
				partNumber, err := parsePositiveInt(c.Param("partNumber"))
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
					return
				}
				etag, err := deps.MinIO.WriteMultipartPart(c.Param("uploadID"), partNumber, c.Request.Body)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
				c.Header("ETag", etag)
				c.Status(http.StatusOK)
			})
		}
	}

	if deps.MinIO != nil && deps.MinIO.IsLocal() {
		localFileHandler := func(c *gin.Context) {
			objectKey := strings.TrimPrefix(c.Param("objectKey"), "/")
			if objectKey == "" {
				c.JSON(http.StatusNotFound, gin.H{"error": "object not found"})
				return
			}
			file, err := deps.MinIO.ServeLocalObject(objectKey)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "object not found"})
				return
			}
			defer file.Close()
			http.ServeFile(c.Writer, c.Request, file.Name())
		}
		// ValidateSourceFile 在创建听悟任务前发 HEAD 探活；Gin 不会自动将 HEAD 路由到 GET，
		// 必须显式注册 HEAD，否则容器内部直连（hairpin NAT 修复后的内部服务名）会 404
		api.GET("/files/*objectKey", localFileHandler)
		api.HEAD("/files/*objectKey", localFileHandler)
	} else if deps.MinIO != nil {
		remoteFileHandler := func(c *gin.Context) {
			objectKey := strings.TrimPrefix(c.Param("objectKey"), "/")
			if objectKey == "" {
				c.JSON(http.StatusNotFound, gin.H{"error": "object not found"})
				return
			}
			obj, err := deps.MinIO.OpenObject(c.Request.Context(), objectKey)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "object not found"})
				return
			}
			defer obj.Close()
			info, err := obj.Stat()
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "object not found"})
				return
			}
			http.ServeContent(c.Writer, c.Request, filepath.Base(objectKey), info.LastModified, obj)
		}
		api.GET("/files/*objectKey", remoteFileHandler)
		api.HEAD("/files/*objectKey", remoteFileHandler)
	}

	if deps.MinIO != nil {
		api.PUT("/videos/:id/poster", func(c *gin.Context) {
			videoID := c.Param("id")
			durationSeconds := 0
			if raw := strings.TrimSpace(c.Query("duration_seconds")); raw != "" {
				n, err := strconv.Atoi(raw)
				if err != nil || n < 0 {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid duration_seconds"})
					return
				}
				durationSeconds = n
			}

			var video struct {
				ID      string
				FileURL string
			}
			if err := deps.DB.Table("videos").Select("id", "file_url").Where("id = ?", videoID).First(&video).Error; err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
				return
			}

			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "read poster body: " + err.Error()})
				return
			}
			if len(body) == 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "poster body is empty"})
				return
			}
			objectKey := fmt.Sprintf("thumbnails/%s/cover.jpg", videoID)
			ct := c.GetHeader("Content-Type")
			if ct == "" {
				ct = "image/jpeg"
			}
			if _, err := deps.MinIO.PutObject(c.Request.Context(), objectKey, bytes.NewReader(body), int64(len(body)), miniosdk.PutObjectOptions{ContentType: ct}); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "store poster: " + err.Error()})
				return
			}
			thumbnailURL := deps.MinIO.PublicURL(objectKey)
			updates := map[string]any{
				"thumbnail_url":            thumbnailURL,
				"processing_error_summary": "",
			}
			if durationSeconds > 0 {
				updates["duration_seconds"] = durationSeconds
			}
			if strings.TrimSpace(video.FileURL) != "" {
				now := time.Now().UTC()
				updates["status"] = model.VideoStatusReady
				updates["ready_at"] = now
			}
			res := deps.DB.Table("videos").Where("id = ?", videoID).Updates(updates)
			if res.Error != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "update poster url: " + res.Error.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"video_id": videoID, "thumbnail_url": thumbnailURL, "status": updates["status"]})
		})
	}

	// 视频列表 / 详情
	vh := NewVideoHandler(deps.DB)
	api.GET("/videos", vh.List)
	api.GET("/videos/:id", vh.Detail)
	ph := NewProcessingHandler(deps.DB, ProcessingDependencies{Wiki: deps.Wiki, KBID: deps.Cfg.WeKnora.KBID})
	api.GET("/videos/:id/processing-status", ph.Status)
	api.POST("/videos/:id/processing-jobs/:jobType/retry", ph.Retry)
	graphHandler := NewEntityGraphHandler(deps.DB, deps.WeKnora, deps.Cfg.WeKnora.KBID)
	api.GET("/graph", graphHandler.Get)

	if deps.Wiki != nil {
		ch := NewContentHandler(deps.DB, deps.Wiki, deps.Cfg.WeKnora.KBID)
		videos := api.Group("/videos/:id")
		videos.GET("/related-knowledge", ch.RelatedKnowledge)
		videos.GET("/outline", ch.Outline)
		videos.GET("/overview", ch.Overview)
		videos.GET("/summary", ch.Summary)
		videos.GET("/transcript-page", ch.TranscriptPage)
	}

	return router
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-API-Key, X-Tenant-ID, X-Upload-Trace-ID, X-Upload-Attempt, X-Video-ID, X-Object-Key, X-Upload-ID, X-Part-Number")
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			c.Header("Access-Control-Expose-Headers", "ETag, Content-Length, Content-Type")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func uploadTraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/api/custom/uploads/") {
			c.Next()
			return
		}

		startedAt := time.Now()
		c.Next()

		fields := []any{
			"component", "custom-upload",
			"event", "http_request_completed",
			"trace_id", uploadTraceID(c),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"http_status", c.Writer.Status(),
			"elapsed_ms", time.Since(startedAt).Milliseconds(),
		}
		if videoID := strings.TrimSpace(c.GetHeader("X-Video-ID")); videoID != "" {
			fields = append(fields, "video_id", videoID)
		}
		if uploadID := strings.TrimSpace(c.GetHeader("X-Upload-ID")); uploadID != "" {
			fields = append(fields, "upload_id", uploadID)
		}
		if partNumber := strings.TrimSpace(c.GetHeader("X-Part-Number")); partNumber != "" {
			fields = append(fields, "part_number", partNumber)
		}
		if attempt := strings.TrimSpace(c.GetHeader(uploadAttemptHeader)); attempt != "" {
			fields = append(fields, "attempt", attempt)
		}
		if len(c.Errors) > 0 {
			fields = append(fields, "gin_errors", c.Errors.String())
		}
		slog.InfoContext(c.Request.Context(), "custom upload HTTP request", fields...)
	}
}

func parsePositiveInt(raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid positive integer: %q", raw)
	}
	return n, nil
}

// healthCheck 返回健康检查 handler，附带数据库连通性探测
func healthCheck(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		sqlDB, err := db.DB()
		if err != nil || sqlDB.Ping() != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "db": "down"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "db": "up"})
	}
}
