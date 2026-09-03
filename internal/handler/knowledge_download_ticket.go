package handler

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	downloadTicketHeader     = "X-WeKnora-Download-Ticket"
	downloadTicketRateLimit  = 60
	downloadTicketRateWindow = time.Minute
)

type KnowledgeDownloadTicketHandler struct {
	knowledge interfaces.KnowledgeService
	redis     *redis.Client
}

func NewKnowledgeDownloadTicketHandler(
	knowledge interfaces.KnowledgeService,
	redisClient *redis.Client,
) *KnowledgeDownloadTicketHandler {
	return &KnowledgeDownloadTicketHandler{knowledge: knowledge, redis: redisClient}
}

func (h *KnowledgeDownloadTicketHandler) Download(c *gin.Context) {
	h.serve(c, false)
}

func (h *KnowledgeDownloadTicketHandler) Head(c *gin.Context) {
	h.serve(c, true)
}

func (h *KnowledgeDownloadTicketHandler) serve(c *gin.Context, headOnly bool) {
	ctx := c.Request.Context()
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "error": "knowledge id required"})
		return
	}
	if !h.allowIP(c) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"success": false, "error": "rate limited"})
		return
	}
	claims, err := h.parseTicket(c, id, false)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "invalid ticket"})
		return
	}
	k, err := h.knowledge.GetKnowledgeByIDOnly(ctx, id)
	if err != nil || k == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"success": false, "error": "not found"})
		return
	}
	if k.TenantID != claims.TenantID {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "invalid ticket"})
		return
	}
	if k.FilePath == "" && !k.IsManual() {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"success": false, "error": "not found"})
		return
	}
	fileCtx := context.WithValue(ctx, types.TenantIDContextKey, k.TenantID)
	file, filename, err := h.knowledge.GetKnowledgeFile(fileCtx, id)
	if err != nil {
		logger.Warnf(ctx, "knowledge download ticket get file: %v", err)
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"success": false, "error": "not found"})
		return
	}
	defer file.Close()
	cd := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	c.Header("Content-Disposition", cd)
	c.Header("Content-Type", "application/octet-stream")
	if headOnly {
		c.Status(http.StatusOK)
		return
	}
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, file); err != nil {
		logger.Warnf(ctx, "knowledge download ticket stream: %v", err)
	}
}

func (h *KnowledgeDownloadTicketHandler) Renew(c *gin.Context) {
	ctx := c.Request.Context()
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "error": "knowledge id required"})
		return
	}
	if !h.allowIP(c) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"success": false, "error": "rate limited"})
		return
	}
	claims, err := h.parseTicket(c, id, true)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "invalid ticket"})
		return
	}
	grace := secutils.TicketRenewGrace()
	if time.Now().Unix() > claims.Expires+int64(grace.Seconds()) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "ticket expired"})
		return
	}
	k, err := h.knowledge.GetKnowledgeByIDOnly(ctx, id)
	if err != nil || k == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"success": false, "error": "not found"})
		return
	}
	if k.TenantID != claims.TenantID {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "invalid ticket"})
		return
	}
	ticket, exp, err := secutils.SignKnowledgeDownloadTicket(id, k.TenantID, time.Now())
	if err != nil {
		c.Error(apperrors.NewInternalServerError("failed to renew ticket"))
		return
	}
	c.JSON(http.StatusOK, dto.WebhookRenewResponse{
		Ticket:          ticket,
		TicketExpiresAt: exp.UTC().Format(time.RFC3339),
		Path:            types.WebhookDownloadPathPrefix + id,
		TicketHeader:    types.WebhookDownloadTicketHeader,
	})
}

func (h *KnowledgeDownloadTicketHandler) parseTicket(c *gin.Context, knowledgeID string, expiredOK bool) (*secutils.DownloadTicketClaims, error) {
	raw := strings.TrimSpace(c.GetHeader(downloadTicketHeader))
	if raw == "" {
		return nil, apperrors.NewUnauthorizedError("missing ticket")
	}
	claims, err := secutils.ParseKnowledgeDownloadTicket(raw, time.Now(), expiredOK)
	if err != nil {
		return nil, err
	}
	if claims.KnowledgeID != knowledgeID {
		return nil, apperrors.NewUnauthorizedError("ticket knowledge mismatch")
	}
	return claims, nil
}

func (h *KnowledgeDownloadTicketHandler) allowIP(c *gin.Context) bool {
	if h.redis == nil {
		return true
	}
	ip := c.ClientIP()
	key := "weknora:webhook:wdt:" + ip
	n, err := h.redis.Incr(c.Request.Context(), key).Result()
	if err != nil {
		return true
	}
	if n == 1 {
		h.redis.Expire(c.Request.Context(), key, downloadTicketRateWindow)
	}
	return n <= downloadTicketRateLimit
}
