package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// EmbedChannelHandler manages web embed channel CRUD and public embed endpoints.
type EmbedChannelHandler struct {
	embedSvc          interfaces.EmbedChannelService
	sessionService    interfaces.SessionService
	sessionHandler    *session.Handler
	messageHandler    *MessageHandler
	suggestionHandler *MessageSuggestionHandler
	mcpOAuthHandler   *MCPOAuthHandler
	mcpServiceHandler *MCPServiceHandler
	redis             *redis.Client
}

func NewEmbedChannelHandler(
	embedSvc interfaces.EmbedChannelService,
	sessionService interfaces.SessionService,
	sessionHandler *session.Handler,
	messageHandler *MessageHandler,
	suggestionHandler *MessageSuggestionHandler,
	mcpOAuthHandler *MCPOAuthHandler,
	mcpServiceHandler *MCPServiceHandler,
	redisClient *redis.Client,
) *EmbedChannelHandler {
	return &EmbedChannelHandler{
		embedSvc:          embedSvc,
		sessionService:    sessionService,
		sessionHandler:    sessionHandler,
		messageHandler:    messageHandler,
		suggestionHandler: suggestionHandler,
		mcpOAuthHandler:   mcpOAuthHandler,
		mcpServiceHandler: mcpServiceHandler,
		redis:             redisClient,
	}
}

type embedChannelRequest struct {
	Name                   string   `json:"name"`
	Enabled                *bool    `json:"enabled"`
	AllowedOrigins         []string `json:"allowed_origins"`
	WelcomeMessage         string   `json:"welcome_message"`
	RateLimitPerMinute     int      `json:"rate_limit_per_minute"`
	RateLimitPerDay        int      `json:"rate_limit_per_day"`
	PrimaryColor           string   `json:"primary_color"`
	PageTitle              string   `json:"page_title"`
	HeaderTitleMode        string   `json:"header_title_mode"`
	ShowSuggestedQuestions *bool    `json:"show_suggested_questions"`
	WidgetPosition         string   `json:"widget_position"`
	AllowWebSearch         *bool    `json:"allow_web_search"`
	AllowFileUpload        *bool    `json:"allow_file_upload"`
	DefaultLocale          *string  `json:"default_locale"`
	WebhookURL             *string  `json:"webhook_url"`
	WebhookSecret          *string  `json:"webhook_secret"`
	AgentID                *string  `json:"agent_id"`
}

// isProductionMode reports whether the server runs in a hardened (release) mode.
func isProductionMode() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("GIN_MODE")), "release")
}

func stringOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// validateAllowedOrigins enforces that a public embed channel declares an
// explicit origin allowlist. An empty list means "allow any origin" in the
// auth middleware, which is unsafe for a publicly reachable widget, so it is
// rejected. In production a wildcard ("*") is also rejected; each entry must be
// a well-formed http(s) origin (optionally a "*." subdomain wildcard).
func validateAllowedOrigins(origins []string) error {
	cleaned := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		cleaned = append(cleaned, o)
	}
	if len(cleaned) == 0 {
		return fmt.Errorf("at least one allowed origin is required")
	}
	for _, o := range cleaned {
		if o == "*" {
			if isProductionMode() {
				return fmt.Errorf("wildcard origin '*' is not allowed in production")
			}
			continue
		}
		host := o
		if strings.HasPrefix(o, "*.") {
			host = "https://" + strings.TrimPrefix(o, "*.")
		}
		u, err := url.Parse(host)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("invalid allowed origin: %q", o)
		}
	}
	return nil
}

func (h *EmbedChannelHandler) CreateEmbedChannel(c *gin.Context) {
	agentID := secutils.SanitizeForLog(c.Param("id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	var req embedChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateAllowedOrigins(req.AllowedOrigins); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	originsJSON, _ := json.Marshal(req.AllowedOrigins)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	showSuggested := true
	if req.ShowSuggestedQuestions != nil {
		showSuggested = *req.ShowSuggestedQuestions
	}
	allowWebSearch := false
	if req.AllowWebSearch != nil {
		allowWebSearch = *req.AllowWebSearch
	}
	allowFileUpload := false
	if req.AllowFileUpload != nil {
		allowFileUpload = *req.AllowFileUpload
	}
	ch, token, err := h.embedSvc.Create(c.Request.Context(), tenantID, agentID, &types.EmbedChannel{
		Name:                   req.Name,
		Enabled:                enabled,
		AllowedOrigins:         originsJSON,
		WelcomeMessage:         req.WelcomeMessage,
		RateLimitPerMinute:     req.RateLimitPerMinute,
		RateLimitPerDay:        req.RateLimitPerDay,
		PrimaryColor:           req.PrimaryColor,
		PageTitle:              req.PageTitle,
		HeaderTitleMode:        req.HeaderTitleMode,
		ShowSuggestedQuestions: showSuggested,
		WidgetPosition:         req.WidgetPosition,
		AllowWebSearch:         allowWebSearch,
		AllowFileUpload:        allowFileUpload,
		DefaultLocale:          types.NormalizeEmbedDefaultLocale(stringOrEmpty(req.DefaultLocale)),
	})
	if err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    embedChannelResponse(ch, token),
	})
}

func (h *EmbedChannelHandler) ListEmbedChannels(c *gin.Context) {
	agentID := secutils.SanitizeForLog(c.Param("id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	rows, err := h.embedSvc.ListByAgent(c.Request.Context(), tenantID, agentID)
	if err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": embedChannelsResponse(rows)})
}

// ListAllEmbedChannels lists every embed channel in the current tenant, across
// agents, for sidebar session grouping. Publish tokens are never included.
func (h *EmbedChannelHandler) ListAllEmbedChannels(c *gin.Context) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	rows, err := h.embedSvc.ListByTenant(c.Request.Context(), tenantID)
	if err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": embedChannelsResponse(rows)})
}

func embedChannelsResponse(rows []*types.EmbedChannel) []gin.H {
	data := make([]gin.H, 0, len(rows))
	for _, ch := range rows {
		data = append(data, embedChannelResponse(ch, ""))
	}
	return data
}

func (h *EmbedChannelHandler) UpdateEmbedChannel(c *gin.Context) {
	channelID := secutils.SanitizeForLog(c.Param("channel_id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	var req embedChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Only validate when the caller intends to change the allowlist. A nil slice
	// means "leave unchanged"; a present slice must still be a valid allowlist.
	if req.AllowedOrigins != nil {
		if err := validateAllowedOrigins(req.AllowedOrigins); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if req.WebhookURL != nil {
		if err := service.ValidateEmbedWebhookURL(*req.WebhookURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	originsJSON, _ := json.Marshal(req.AllowedOrigins)
	update := &types.EmbedChannel{
		Name:               req.Name,
		AllowedOrigins:     originsJSON,
		WelcomeMessage:     req.WelcomeMessage,
		RateLimitPerMinute: req.RateLimitPerMinute,
		RateLimitPerDay:    req.RateLimitPerDay,
		PrimaryColor:       req.PrimaryColor,
		PageTitle:          req.PageTitle,
		HeaderTitleMode:    req.HeaderTitleMode,
		WidgetPosition:     req.WidgetPosition,
	}
	if req.AgentID != nil {
		update.AgentID = strings.TrimSpace(*req.AgentID)
	}
	ch, err := h.embedSvc.Update(c.Request.Context(), tenantID, channelID, update, req.Enabled, req.ShowSuggestedQuestions, req.AllowWebSearch, req.AllowFileUpload, req.DefaultLocale, req.WebhookURL, req.WebhookSecret)
	if err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": embedChannelResponse(ch, "")})
}

func (h *EmbedChannelHandler) DeleteEmbedChannel(c *gin.Context) {
	channelID := secutils.SanitizeForLog(c.Param("channel_id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if err := h.embedSvc.Delete(c.Request.Context(), tenantID, channelID); err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *EmbedChannelHandler) RotateEmbedToken(c *gin.Context) {
	channelID := secutils.SanitizeForLog(c.Param("channel_id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	ch, token, err := h.embedSvc.RotateToken(c.Request.Context(), tenantID, channelID)
	if err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": embedChannelResponse(ch, token)})
}

func (h *EmbedChannelHandler) IssuePreviewSession(c *gin.Context) {
	channelID := secutils.SanitizeForLog(c.Param("channel_id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	sessionToken, expiresIn, err := h.embedSvc.IssuePreviewSession(c.Request.Context(), tenantID, channelID)
	if err != nil {
		if errors.Is(err, service.ErrEmbedChannelDisabled) {
			c.JSON(http.StatusForbidden, gin.H{"error": "embed channel is disabled"})
			return
		}
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"session_token": sessionToken,
			"expires_in":    expiresIn,
		},
	})
}

func (h *EmbedChannelHandler) ExchangeEmbedSession(c *gin.Context) {
	ctx := c.Request.Context()
	ch, ok := middleware.EmbedChannelFromContext(ctx)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	// Only the long-lived publish token may mint session tokens. Accepting a
	// session token here would let a holder renew it indefinitely without ever
	// re-presenting the publish token.
	if auth := strings.TrimSpace(c.GetHeader("Authorization")); !strings.HasPrefix(auth, "Embed ") ||
		service.IsEmbedSessionToken(strings.TrimPrefix(auth, "Embed ")) {
		c.JSON(http.StatusForbidden, gin.H{"error": "publish token required"})
		return
	}
	sessionToken, expiresIn, err := h.embedSvc.IssueSessionToken(ctx, ch.ID)
	if err != nil {
		if errors.Is(err, service.ErrEmbedSessionUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session tokens unavailable"})
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue session token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"session_token": sessionToken,
			"expires_in":    expiresIn,
		},
	})
}

func (h *EmbedChannelHandler) GetEmbedConfig(c *gin.Context) {
	ch, ok := middleware.EmbedChannelFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.embedSvc.PublicConfig(c.Request.Context(), ch)})
}

func (h *EmbedChannelHandler) GetEmbedChunk(c *gin.Context) {
	ch, ok := middleware.EmbedChannelFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	chunkID := secutils.SanitizeForLog(c.Param("chunk_id"))
	if chunkID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "chunk_id is required"})
		return
	}
	chunk, err := h.embedSvc.EmbedChunk(c.Request.Context(), ch, chunkID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEmbedChunkForbidden):
			c.JSON(http.StatusForbidden, gin.H{"error": "chunk not accessible"})
		case errors.Is(err, service.ErrEmbedChunkNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "chunk not found"})
		default:
			logger.Error(c.Request.Context(), "embed chunk lookup failed", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load chunk"})
		}
		return
	}
	if chunk.Content != "" {
		chunk.Content = secutils.SanitizeForDisplay(chunk.Content)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": chunk})
}

func (h *EmbedChannelHandler) GetEmbedSuggestedQuestions(c *gin.Context) {
	ch, ok := middleware.EmbedChannelFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !ch.ShowSuggestedQuestions {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"questions": []types.SuggestedQuestion{}}})
		return
	}
	limit := 6
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
		if limit > 12 {
			limit = 12
		}
	}
	questions, err := h.embedSvc.SuggestedQuestions(c.Request.Context(), ch, limit)
	if err != nil {
		logger.Error(c.Request.Context(), "embed suggested questions failed", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load suggested questions"})
		return
	}
	if questions == nil {
		questions = []types.SuggestedQuestion{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"questions": questions}})
}

func (h *EmbedChannelHandler) CreateEmbedSession(c *gin.Context) {
	ctx := c.Request.Context()
	ch, ok := middleware.EmbedChannelFromContext(ctx)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	// Leave Title empty so the first visitor message triggers the same async
	// title generation as normal chat (see setupSSEStream in session/qa.go).
	// Channel display name belongs on the embed page chrome, not every session row.
	createdSession := &types.Session{
		TenantID:    tenantID,
		Title:       "",
		Description: service.EmbedSessionDescription(ch.ID),
	}
	created, err := h.sessionService.CreateSession(ctx, createdSession)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}
	ownerID := types.EmbedSessionPrincipal(tenantID, ch.ID, created.ID).StorageID()
	if err := h.sessionService.SetSessionOwnerID(ctx, tenantID, created.ID, ownerID); err != nil {
		logger.Warnf(ctx, "failed to assign embed session owner for %s: %v", created.ID, err)
	} else {
		created.UserID = ownerID
	}
	// Hand back a signed handle bound to this session; the widget must echo it
	// (X-Embed-Session header) on every subsequent load/chat call.
	sig := service.SignEmbedSessionHandle(ch, created.ID)
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"id": created.ID, "sig": sig}})
}

func (h *EmbedChannelHandler) EmbedKnowledgeChat(c *gin.Context) {
	h.delegateEmbedChat(c, false)
}

func (h *EmbedChannelHandler) EmbedAgentChat(c *gin.Context) {
	h.delegateEmbedChat(c, true)
}

func (h *EmbedChannelHandler) EmbedLoadMessages(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	h.messageHandler.LoadMessages(c)
}

func (h *EmbedChannelHandler) EmbedStopSession(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	h.sessionHandler.StopSession(c)
}

func (h *EmbedChannelHandler) EmbedEnsureMessageSuggestions(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	ch, _ := middleware.EmbedChannelFromContext(c.Request.Context())
	if ch == nil || !ch.ShowSuggestedQuestions || h.suggestionHandler == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"status": "suppressed", "suppression_reason": "channel_disabled", "questions": []any{},
		}})
		return
	}
	h.suggestionHandler.Ensure(c)
}

func (h *EmbedChannelHandler) EmbedGetMessageSuggestions(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	ch, _ := middleware.EmbedChannelFromContext(c.Request.Context())
	if ch == nil || !ch.ShowSuggestedQuestions || h.suggestionHandler == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"status": "suppressed", "suppression_reason": "channel_disabled", "questions": []any{},
		}})
		return
	}
	h.suggestionHandler.Get(c)
}

func (h *EmbedChannelHandler) EmbedRecordSuggestionEvent(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	if h.suggestionHandler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "suggestion service unavailable"})
		return
	}
	h.suggestionHandler.RecordEvent(c)
}

func (h *EmbedChannelHandler) EmbedResolveMCPOAuth(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	if h.mcpOAuthHandler == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "oauth handler unavailable"})
		return
	}
	h.mcpOAuthHandler.ResolveMCPOAuth(c)
}

func (h *EmbedChannelHandler) EmbedCancelMCPOAuth(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	if h.mcpOAuthHandler == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "oauth handler unavailable"})
		return
	}
	h.mcpOAuthHandler.CancelMCPOAuth(c)
}

func (h *EmbedChannelHandler) EmbedMCPOAuthAuthorizeURL(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	if h.mcpOAuthHandler == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "oauth handler unavailable"})
		return
	}
	h.mcpOAuthHandler.AuthorizeURL(c)
}

func (h *EmbedChannelHandler) EmbedMCPOAuthStatus(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	if h.mcpOAuthHandler == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "oauth handler unavailable"})
		return
	}
	h.mcpOAuthHandler.Status(c)
}

func (h *EmbedChannelHandler) EmbedResolveToolApproval(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	if h.mcpServiceHandler == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "tool approval handler unavailable"})
		return
	}
	h.mcpServiceHandler.ResolveToolApproval(c)
}

type embedWebhookEventRequest struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Query     string `json:"query"`
	Content   string `json:"content"`
}

// EmbedRelayWebhookEvent forwards a visitor chat event to the channel webhook URL.
func (h *EmbedChannelHandler) EmbedRelayWebhookEvent(c *gin.Context) {
	ch, ok := middleware.EmbedChannelFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	var req embedWebhookEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	eventType := strings.TrimSpace(req.Type)
	switch eventType {
	case "message_sent", "message_received":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported event type"})
		return
	}
	payload := map[string]any{}
	if q := strings.TrimSpace(req.Query); q != "" {
		payload["query"] = q
	}
	if content := strings.TrimSpace(req.Content); content != "" {
		payload["content"] = content
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = secutils.SanitizeForLog(c.Param("session_id"))
	}
	service.DispatchEmbedWebhook(ch, eventType, sessionID, payload)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *EmbedChannelHandler) delegateEmbedChat(c *gin.Context, agentMode bool) {
	ch, ok := middleware.EmbedChannelFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	patched, err := patchEmbedChatPayload(c.Request.Body, ch, agentMode)
	if err != nil {
		switch {
		case errors.Is(err, errInvalidEmbedChatBody):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		case errors.Is(err, errInvalidEmbedChatJSON):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare request"})
		}
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(patched))
	c.Request.ContentLength = int64(len(patched))
	if agentMode && ch.AgentID != types.BuiltinQuickAnswerID {
		h.sessionHandler.AgentQA(c)
		return
	}
	h.sessionHandler.KnowledgeQA(c)
}

func (h *EmbedChannelHandler) ensureEmbedSession(c *gin.Context) error {
	ch, ok := middleware.EmbedChannelFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return apperrors.NewUnauthorizedError("unauthorized")
	}
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return apperrors.NewBadRequestError("session_id is required")
	}
	sess, err := h.sessionService.GetSessionByID(c.Request.Context(), ch.TenantID, sessionID)
	if err != nil || sess == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return apperrors.NewNotFoundError("session not found")
	}
	marker := service.EmbedSessionDescription(ch.ID)
	if sess.TenantID != ch.TenantID || sess.Description != marker {
		c.JSON(http.StatusForbidden, gin.H{"error": "session not allowed for this embed channel"})
		return apperrors.NewForbiddenError("session not allowed")
	}
	ownerID := types.EmbedSessionPrincipal(ch.TenantID, ch.ID, sessionID).StorageID()
	if strings.TrimSpace(sess.UserID) == "" {
		if err := h.sessionService.SetSessionOwnerID(c.Request.Context(), ch.TenantID, sessionID, ownerID); err != nil {
			logger.Warnf(c.Request.Context(), "failed to backfill embed session owner for %s: %v", sessionID, err)
		}
	}
	// Require the signed handle minted at creation. This is the per-visitor
	// authorization secret: knowing the session id alone (e.g. from a leaked
	// access log) is insufficient without the matching signature.
	sig := c.GetHeader("X-Embed-Session")
	if !service.VerifyEmbedSessionHandle(ch, sessionID, sig) {
		c.JSON(http.StatusForbidden, gin.H{"error": "session signature invalid"})
		return apperrors.NewForbiddenError("session signature invalid")
	}
	principal := types.EmbedSessionPrincipal(ch.TenantID, ch.ID, sessionID)
	ctx := c.Request.Context()
	if visitorID := strings.TrimSpace(c.GetHeader(types.EmbedVisitorHeader)); visitorID != "" {
		if err := types.ValidateEmbedVisitorID(visitorID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid embed visitor id"})
			return apperrors.NewBadRequestError("invalid embed visitor id")
		}
		ctx = types.WithEmbedVisitorID(ctx, visitorID)
	}
	c.Set(types.PrincipalContextKey.String(), principal)
	c.Request = c.Request.WithContext(types.WithPrincipal(ctx, principal))
	return nil
}

var (
	errInvalidEmbedChatBody = errors.New("invalid embed chat request body")
	errInvalidEmbedChatJSON = errors.New("invalid embed chat json")
)

// patchEmbedChatPayload merges embed-channel constraints into the client QA body.
func patchEmbedChatPayload(body io.Reader, ch *types.EmbedChannel, agentMode bool) ([]byte, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidEmbedChatBody, err)
	}
	var payload map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("%w: %v", errInvalidEmbedChatJSON, err)
		}
	}
	if payload == nil {
		payload = make(map[string]any)
	}
	payload["agent_id"] = ch.AgentID
	payload["knowledge_base_ids"] = []string{}
	clientWebSearch := false
	if v, ok := payload["web_search_enabled"].(bool); ok {
		clientWebSearch = v
	}
	// Channel allow_web_search only exposes the visitor toggle; the client must opt in.
	payload["web_search_enabled"] = ch.AllowWebSearch && clientWebSearch
	if !ch.AllowFileUpload {
		delete(payload, "images")
		delete(payload, "attachment_uploads")
		delete(payload, "attachment_ids")
	}
	payload["mcp_service_ids"] = []string{}
	if agentMode {
		payload["agent_enabled"] = true
	} else {
		payload["agent_enabled"] = false
	}
	patched, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return patched, nil
}

// GetEmbedChannel returns a single embed channel for management, including the
// publish token so admins can copy deploy snippets at any time.
func (h *EmbedChannelHandler) GetEmbedChannel(c *gin.Context) {
	channelID := strings.TrimSpace(c.Param("channel_id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	ch, err := h.embedSvc.GetOwnedChannel(c.Request.Context(), tenantID, channelID)
	if err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    embedChannelResponse(ch, ch.PublishToken),
	})
}

// GetEmbedChannelStats returns lightweight usage stats for an embed channel.
func (h *EmbedChannelHandler) GetEmbedChannelStats(c *gin.Context) {
	channelID := strings.TrimSpace(c.Param("channel_id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	ctx := c.Request.Context()

	if _, err := h.embedSvc.GetOwnedChannel(ctx, tenantID, channelID); err != nil {
		writeEmbedMgmtError(c, err)
		return
	}

	result, err := h.sessionService.CountSessionsBySource(ctx, &types.SessionListQuery{
		TenantID: tenantID,
		Source:   "embed:" + channelID,
		Page:     1,
		PageSize: 1,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	total := result
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"session_count": total,
		},
	})
}

func embedChannelResponse(ch *types.EmbedChannel, publishToken string) gin.H {
	row := gin.H{
		"id":                       ch.ID,
		"tenant_id":                ch.TenantID,
		"agent_id":                 ch.AgentID,
		"name":                     ch.Name,
		"enabled":                  ch.Enabled,
		"allowed_origins":          ch.AllowedOriginsList(),
		"welcome_message":          ch.WelcomeMessage,
		"rate_limit_per_minute":    ch.RateLimitPerMinute,
		"rate_limit_per_day":       ch.RateLimitPerDay,
		"primary_color":            ch.PrimaryColor,
		"page_title":               ch.PageTitle,
		"header_title_mode":        types.NormalizeEmbedHeaderTitleMode(ch.HeaderTitleMode),
		"show_suggested_questions": ch.ShowSuggestedQuestions,
		"widget_position":          ch.WidgetPosition,
		"allow_web_search":         ch.AllowWebSearch,
		"allow_file_upload":        ch.AllowFileUpload,
		"default_locale":           ch.DefaultLocale,
		"webhook_url":              ch.WebhookURL,
		"has_webhook_secret":       ch.WebhookSecret != "",
		"created_at":               ch.CreatedAt,
		"updated_at":               ch.UpdatedAt,
	}
	if publishToken != "" {
		row["publish_token"] = publishToken
	}
	return row
}

func writeEmbedMgmtError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrEmbedChannelNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "embed channel not found"})
	case errors.Is(err, service.ErrEmbedWebhookURLInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		var appErr *apperrors.AppError
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": appErr.Message})
			return
		}
		logger.Error(c.Request.Context(), "embed channel management failed", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation failed"})
	}
}
