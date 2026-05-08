package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
)

// SkillHandler handles skill-related HTTP requests
type SkillHandler struct {
	runtime skills.SkillRuntime
}

// NewSkillHandler creates a new skill handler backed by the global SkillRuntime
func NewSkillHandler(runtime skills.SkillRuntime) *SkillHandler {
	return &SkillHandler{runtime: runtime}
}

// SkillInfoResponse represents the skill info returned to frontend
type SkillInfoResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListSkills godoc
// @Summary      获取预装Skills列表
// @Description  获取所有预装的Agent Skills元数据
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "Skills列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /skills [get]
func (h *SkillHandler) ListSkills(c *gin.Context) {
	ctx := c.Request.Context()

	if h.runtime == nil {
		c.Error(errors.NewInternalServerError("skill runtime is not configured"))
		return
	}

	// Ensure metadata cache is warm. Initialize is idempotent.
	if err := h.runtime.Initialize(ctx); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to initialize skill runtime: " + err.Error()))
		return
	}

	skillsMetadata := h.runtime.ListMetadata(ctx)

	// Convert to response format
	response := make([]SkillInfoResponse, 0, len(skillsMetadata))
	for _, meta := range skillsMetadata {
		response = append(response, SkillInfoResponse{
			Name:        meta.Name,
			Description: meta.Description,
		})
	}

	// skills_available reflects whether the sandbox can actually execute
	// scripts. The runtime owns this fact (no more env reading here).
	skillsAvailable := h.runtime.SandboxAvailable()
	logger.Infof(ctx, "skills_available: %v, count: %d", skillsAvailable, len(response))

	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"data":             response,
		"skills_available": skillsAvailable,
	})
}
