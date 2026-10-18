package handler

import (
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/gin-gonic/gin"
)

// ListSystemSettingSummaries godoc
// @Summary 查询系统设置
// @Description 返回已注册的系统设置及脱敏值，可按完整设置键过滤
// @Tags System Admin
// @Produce json
// @Param key query string false "完整设置键；省略返回所有已注册设置" extensions(x-agent)
// @Success 200 {array} types.SystemSettingSummary
// @x-agent {"id":"system_admin.list_settings","risk":"read","system_admin":true,"max_items":100}
// @x-agent-hint "Reads registered platform settings, optionally selecting an exact key from this list. value_json encodes the settings page value, including service fallbacks; it is not proof that a running process has reloaded a restart-required change. value_hidden means secret, invalid or oversized and never means empty/default. Unknown out-of-band database rows are excluded. Read-only: cannot change or reset settings."
// @Router /system/admin/settings-summary [get]
func (h *SystemHandler) ListSystemSettingSummaries(c *gin.Context) {
	rows, err := h.systemSettingSvc.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list system settings"})
		return
	}
	key := strings.TrimSpace(c.Query("key"))
	result := service.SummarizeSystemSettings(rows, key)
	if key != "" && len(result) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown setting key"})
		return
	}
	c.JSON(http.StatusOK, result)
}

// Return categories only. Never echo a substring of an upstream error: it may
// include credentials, signed URLs or document content. Categories are hints.
func runtimeErrorCategory(raw string) string {
	if raw == "" {
		return "none"
	}
	s := strings.ToLower(raw)
	switch {
	case strings.Contains(s, "rate limit"), strings.Contains(s, "too many requests"), strings.Contains(s, "status 429"):
		return "rate_limited"
	case strings.Contains(s, "deadline exceeded"), strings.Contains(s, "timeout"), strings.Contains(s, "timed out"):
		return "timeout"
	case strings.Contains(s, "unauthorized"),
		strings.Contains(s, "authentication"),
		strings.Contains(s, "permission denied"),
		strings.Contains(s, "forbidden"):
		return "access_denied"
	case strings.Contains(s, "connection refused"),
		strings.Contains(s, "no such host"),
		strings.Contains(s, "network unreachable"):
		return "connection_failed"
	case strings.Contains(s, "not found"), strings.Contains(s, "no such file"):
		return "not_found"
	default:
		return "unknown"
	}
}
