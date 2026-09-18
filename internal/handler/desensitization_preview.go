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
	Text                  string                      `json:"text"`
	DesensitizationConfig types.DesensitizationConfig `json:"desensitization_config"`
}

// PreviewDesensitization runs the builtin engine on supplied text so the KB
// editor can try rules without ingesting a document. Sidecar/LLM engines are
// not invoked here.
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
