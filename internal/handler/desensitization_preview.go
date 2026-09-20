package handler

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/desensitization"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

const desensitizationPreviewMaxChars = 32 * 1024

// PreviewDesensitizationRequest is the body for POST /desensitization/preview.
type PreviewDesensitizationRequest struct {
	// Sample text to mask. Preview always uses the builtin engine.
	Text string `json:"text" example:"请拨打13800138000"`
	// Optional rules; engine is forced to builtin for this endpoint.
	DesensitizationConfig types.DesensitizationConfig `json:"desensitization_config"`
}

// PreviewDesensitization godoc
// @Summary      试跑内置脱敏规则
// @Description  对样例文本运行内置引擎。不调用 Presidio 或 LLM。
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        request  body      PreviewDesensitizationRequest  true  "样例文本与脱敏配置"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /desensitization/preview [post]
func PreviewDesensitization(c *gin.Context) {
	var req PreviewDesensitizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "text is required"})
		return
	}
	if utf8.RuneCountInString(text) > desensitizationPreviewMaxChars {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "text is too long"})
		return
	}
	cfg := req.DesensitizationConfig
	cfg.Enabled = true
	cfg.Engine = types.DesensitizationEngineBuiltin
	out, report, err := desensitization.Apply(c.Request.Context(), text, cfg, desensitization.Deps{})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"text":   out,
			"report": report,
		},
	})
}
