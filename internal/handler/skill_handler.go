package handler

import (
	"context"
	"net/http"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// usableSkillLister returns the installed skills a chat turn can actually
// invoke on one sandbox config. The @ picker and the agent editor both read
// this set so they cannot offer a skill the running image does not carry.
type usableSkillLister interface {
	ListUsableSkills(ctx context.Context, tenantID uint64, configID string) []*types.TenantSkillEntity
}

// SkillHandler handles skill-related HTTP requests
type SkillHandler struct {
	usableSkills usableSkillLister
}

// NewSkillHandler creates a new skill handler
func NewSkillHandler(usableSkills usableSkillLister) *SkillHandler {
	return &SkillHandler{
		usableSkills: usableSkills,
	}
}

// SkillInfoResponse represents the skill info returned to frontend
type SkillInfoResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListSkills godoc
// @Summary      获取当前沙盒配置上可执行的 Skills
// @Description  返回指定沙盒配置镜像内、智能体实际能调用的已安装技能（ready 且启用）。不传 sandbox_config_id 时列表为空。
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Param        sandbox_config_id  query     string  false  "Sandbox config ID"
// @Success      200  {object}  map[string]interface{}  "Skills列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /skills [get]
func (h *SkillHandler) ListSkills(c *gin.Context) {
	configID := c.Query("sandbox_config_id")
	if configID == "" || h.usableSkills == nil {
		c.JSON(http.StatusOK, gin.H{
			"success":          true,
			"data":             []SkillInfoResponse{},
			"skills_available": false,
		})
		return
	}

	rows := h.usableSkills.ListUsableSkills(
		c.Request.Context(), sandboxConfigTenantID(c), configID,
	)
	response := make([]SkillInfoResponse, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		response = append(response, SkillInfoResponse{
			Name:        row.Name,
			Description: row.Description,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"data":             response,
		"skills_available": true,
	})
}
