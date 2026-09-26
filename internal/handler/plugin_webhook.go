package handler

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/activate"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/plugin/webhook"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// maxWebhookBody bounds one inbound call.
const maxWebhookBody = 1 << 20

// Per-URL rate: a burst, then a steady rate.
const (
	webhookBurst  = 40
	webhookPerSec = 20
)

// PluginWebhookHandler receives third parties' calls to plugin webhooks and
// relays them to the plugin, and shows workspace admins their URLs.
type PluginWebhookHandler struct {
	registry *registry.Registry
	tenancy  *tenancy.Service
	invoker  *activate.Invoker
	tokens   *webhook.Tokens

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

// NewPluginWebhookHandler creates the handler.
func NewPluginWebhookHandler(
	reg *registry.Registry, t *tenancy.Service, iv *activate.Invoker, tokens *webhook.Tokens,
) *PluginWebhookHandler {
	return &PluginWebhookHandler{
		registry: reg,
		tenancy:  t,
		invoker:  iv,
		tokens:   tokens,
		limiters: map[string]*rate.Limiter{},
	}
}

func (h *PluginWebhookHandler) allow(token string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.limiters[token]
	if !ok {
		if len(h.limiters) > 10000 {
			h.limiters = map[string]*rate.Limiter{} // bounded memory; limits restart
		}
		l = rate.NewLimiter(webhookPerSec, webhookBurst)
		h.limiters[token] = l
	}
	return l.Allow()
}

// webhookOf finds webhook id among a loaded plugin's contributions.
func (h *PluginWebhookHandler) webhookOf(pluginID, id string) (*manifest.Manifest, *manifest.Contribution) {
	m, ok := h.registry.Plugin(pluginID)
	if !ok || !m.Runtime.Type.HasCode() {
		return nil, nil
	}
	for i, c := range m.Contributes[manifest.PointWebhooks] {
		if c.ID == id {
			return m, &m.Contributes[manifest.PointWebhooks][i]
		}
	}
	return nil, nil
}

// Receive relays one inbound call. It needs no login: the URL's token names
// the workspace and proves WeKnora issued the URL. Everything that is not
// a live webhook of a plugin the workspace has on answers 404.
func (h *PluginWebhookHandler) Receive(c *gin.Context) {
	pluginID, hookID, token := c.Param("id"), c.Param("hook"), c.Param("token")
	tenantID, ok := h.tokens.Verify(pluginID, hookID, token)
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	m, _ := h.webhookOf(pluginID, hookID)
	if m == nil {
		c.Status(http.StatusNotFound)
		return
	}
	ctx := c.Request.Context()
	if on, err := h.tenancy.PluginEnabled(ctx, tenantID, pluginID); err != nil || !on {
		c.Status(http.StatusNotFound)
		return
	}
	if !h.allow(token) {
		c.Status(http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBody+1))
	if err != nil || len(body) > maxWebhookBody {
		c.Status(http.StatusRequestEntityTooLarge)
		return
	}
	headers := map[string]string{}
	for k, v := range c.Request.Header {
		if len(v) > 0 && !strings.EqualFold(k, "Cookie") {
			headers[k] = v[0]
		}
	}
	path := c.Param("path")
	if path == "" {
		path = "/"
	}
	in := pluginapi.WebhookRequest{
		Method: c.Request.Method, Path: path, Query: c.Request.URL.RawQuery, Headers: headers, Body: body,
	}
	var out pluginapi.WebhookResponse
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	if err := h.invoker.Call(ctx, m, pluginapi.WebhookPath(hookID), nil, in, &out); err != nil {
		logger.Warnf(ctx, "[plugin] webhook %s/%s for tenant %d: %v", pluginID, hookID, tenantID, err)
		status := http.StatusBadGateway
		if pe, ok := pluginapi.AsError(err); ok && pe.Retryable {
			status = http.StatusServiceUnavailable
		}
		c.Status(status)
		return
	}
	status := out.Status
	if status < 100 || status > 599 {
		status = http.StatusOK
	}
	ctype := out.ContentType
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	c.Data(status, ctype, out.Body)
}

// PluginWebhookDTO is one webhook of a plugin, with the workspace's URL.
type PluginWebhookDTO struct {
	ID          string                 `json:"id"`
	Name        manifest.LocalizedText `json:"name"`
	Description manifest.LocalizedText `json:"description,omitzero"`
	// Path is the URL path; URL is the full URL when WeKnora knows its
	// public address (APP_EXTERNAL_URL).
	Path string `json:"path"`
	URL  string `json:"url,omitempty"`
}

// List godoc
// @Summary      插件 Webhook 地址
// @Description  返回本空间该插件各个 Webhook 的地址，供在第三方系统中配置；地址含密钥，仅管理员可见
// @Tags         Plugins
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /plugins/{id}/webhooks [get]
func (h *PluginWebhookHandler) List(c *gin.Context) {
	pluginID := c.Param("id")
	m, ok := h.registry.Plugin(pluginID)
	if !ok {
		_ = c.Error(errors.NewNotFoundError("plugin not found"))
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	base := webhook.PublicBase()
	out := []PluginWebhookDTO{}
	for _, w := range m.Contributes[manifest.PointWebhooks] {
		dto := PluginWebhookDTO{
			ID:          w.ID,
			Name:        w.Name,
			Description: w.Description,
			Path:        h.tokens.Path(m.ID, w.ID, tenantID),
		}
		if base != "" {
			dto.URL = base + dto.Path
		}
		out = append(out, dto)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}
