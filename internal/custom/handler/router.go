// Package handler 提供自研后端的 Gin 路由与各资源 handler。
package handler

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
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
	DB    *gorm.DB
	Cfg   *config.Config
	MinIO *objstore.Client
	Wiki  *weknora.WikiClient
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
	return buildRouter(deps)
}

func buildRouter(deps *Deps) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	router.Use(corsMiddleware())

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
		uh := NewUploadHandler(deps.DB, deps.MinIO)
		uploads := api.Group("/uploads")
		uploads.POST("/presign", uh.Presign)
		uploads.POST("/confirm", uh.Confirm)
		uploads.POST("/direct", uh.Direct)
		uploads.POST("/multipart/init", uh.MultipartInit)
		uploads.POST("/multipart/sign", uh.MultipartSign)
		uploads.POST("/multipart/complete", uh.MultipartComplete)
		uploads.POST("/multipart/abort", uh.MultipartAbort)
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
		api.GET("/files/*objectKey", func(c *gin.Context) {
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
		})
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
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-API-Key, X-Tenant-ID")
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
