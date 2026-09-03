package handler

import (
	"context"
	stderrors "errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// meEnvVarService is the member-facing slice of the env var surface. Not one
// method takes an identity: the service derives it from the context, which is
// what makes it impossible for a request to select whose values are touched.
type meEnvVarService interface {
	ListMine(ctx context.Context) ([]service.ConfigEnvGroup, error)
	SetMineSkill(ctx context.Context, skillID, name, value string) error
	DeleteMineSkill(ctx context.Context, skillID, name string) error
	SetMineSandbox(ctx context.Context, configID, name, value string) error
	DeleteMineSandbox(ctx context.Context, configID, name string) error
}

// MeEnvVarHandler serves the /me/env-vars endpoints.
//
// These exist instead of relaxing /sandbox-configs/:id/skills*, which is Admin+
// even for reads on purpose: an upload there drives a root shell whose output is
// baked into the image every session of that config boots, and the listing names
// what that image carries. This handler returns declarations and set/unset
// status only — never a value.
type MeEnvVarHandler struct {
	service meEnvVarService
}

func NewMeEnvVarHandler(s *service.UserEnvService) *MeEnvVarHandler {
	return &MeEnvVarHandler{service: s}
}

// meEnvVarRequest is the body of every write endpoint. It has no principal
// field, deliberately: not an ignored one, an absent one. A field that could
// name an identity is one refactor away from being honoured, and honouring it
// would let any logged-in member write another member's values.
type meEnvVarRequest struct {
	SkillID         string `json:"skill_id"`
	SandboxConfigID string `json:"sandbox_config_id"`
	Name            string `json:"name"`
	// Value is unused by the delete endpoints. Clearing a value is a delete
	// rather than a write of "", so a member always has one unambiguous way to
	// revoke.
	Value string `json:"value"`
}

// respondEnvVarError renders a refusal. Nothing to delete is a 404; a rejected
// input is a 400; anything else is the service failing and stays a 500 so an
// internal message never reaches a member.
func respondEnvVarError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, types.ErrEnvVarNotFound):
		c.Error(apperrors.NewNotFoundError("this environment variable is not set"))
	default:
		c.Error(err)
	}
}

// List godoc
// @Summary      List my environment variables
// @Description  List every sandbox config of this workspace with the caller's own config-wide variables and the credentials its skills declared, each reporting whether it is unset, filled in workspace-wide, or filled in by the caller. Values are never returned.
// @Tags         Me
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "One group per sandbox config"
// @Failure      401  {object}  map[string]interface{}  "Unauthorized"
// @Security     Bearer
// @Router       /me/env-vars [get]
func (h *MeEnvVarHandler) List(c *gin.Context) {
	groups, err := h.service.ListMine(c.Request.Context())
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": groups})
}

// SetSkill godoc
// @Summary      Set one of my skill credentials
// @Description  Store the caller's own value for one variable the skill declared. It overrides the workspace-wide value for this caller only, and is injected only into executions that name this skill.
// @Tags         Me
// @Accept       json
// @Produce      json
// @Param        request  body      meEnvVarRequest         true  "Skill, variable name and value"
// @Success      200      {object}  map[string]interface{}  "Stored"
// @Failure      400      {object}  apperrors.AppError      "Invalid request"
// @Security     Bearer
// @Router       /me/env-vars/skill [put]
func (h *MeEnvVarHandler) SetSkill(c *gin.Context) {
	req, ok := bindEnvVarRequest(c, "skill_id", func(r meEnvVarRequest) string { return r.SkillID })
	if !ok {
		return
	}
	if err := h.service.SetMineSkill(c.Request.Context(), req.SkillID, req.Name, req.Value); err != nil {
		respondEnvVarError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteSkill godoc
// @Summary      Delete one of my skill credentials
// @Description  Remove the caller's own value. The workspace-wide value, if there is one, applies again afterwards.
// @Tags         Me
// @Accept       json
// @Produce      json
// @Param        request  body      meEnvVarRequest         true  "Skill and variable name"
// @Success      200      {object}  map[string]interface{}  "Deleted"
// @Failure      404      {object}  apperrors.AppError      "Not set"
// @Security     Bearer
// @Router       /me/env-vars/skill [delete]
func (h *MeEnvVarHandler) DeleteSkill(c *gin.Context) {
	req, ok := bindEnvVarRequest(c, "skill_id", func(r meEnvVarRequest) string { return r.SkillID })
	if !ok {
		return
	}
	if err := h.service.DeleteMineSkill(c.Request.Context(), req.SkillID, req.Name); err != nil {
		respondEnvVarError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SetSandbox godoc
// @Summary      Set one of my sandbox environment variables
// @Description  Store the caller's own value for one variable on a sandbox config. It is injected into every skill script and shell command this caller's turns run on that config.
// @Tags         Me
// @Accept       json
// @Produce      json
// @Param        request  body      meEnvVarRequest         true  "Sandbox config, variable name and value"
// @Success      200      {object}  map[string]interface{}  "Stored"
// @Failure      400      {object}  apperrors.AppError      "Invalid request"
// @Security     Bearer
// @Router       /me/env-vars/sandbox [put]
func (h *MeEnvVarHandler) SetSandbox(c *gin.Context) {
	req, ok := bindEnvVarRequest(c, "sandbox_config_id",
		func(r meEnvVarRequest) string { return r.SandboxConfigID })
	if !ok {
		return
	}
	err := h.service.SetMineSandbox(c.Request.Context(), req.SandboxConfigID, req.Name, req.Value)
	if err != nil {
		respondEnvVarError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteSandbox godoc
// @Summary      Delete one of my sandbox environment variables
// @Description  Remove the caller's own value for one variable on a sandbox config.
// @Tags         Me
// @Accept       json
// @Produce      json
// @Param        request  body      meEnvVarRequest         true  "Sandbox config and variable name"
// @Success      200      {object}  map[string]interface{}  "Deleted"
// @Failure      404      {object}  apperrors.AppError      "Not set"
// @Security     Bearer
// @Router       /me/env-vars/sandbox [delete]
func (h *MeEnvVarHandler) DeleteSandbox(c *gin.Context) {
	req, ok := bindEnvVarRequest(c, "sandbox_config_id",
		func(r meEnvVarRequest) string { return r.SandboxConfigID })
	if !ok {
		return
	}
	err := h.service.DeleteMineSandbox(c.Request.Context(), req.SandboxConfigID, req.Name)
	if err != nil {
		respondEnvVarError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// bindEnvVarRequest decodes a write body and refuses one missing its scope key
// or its name, so every endpoint reports the same two required fields the same
// way.
func bindEnvVarRequest(
	c *gin.Context, scopeField string, scope func(meEnvVarRequest) string,
) (meEnvVarRequest, bool) {
	var req meEnvVarRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return req, false
	}
	if scope(req) == "" || req.Name == "" {
		c.Error(apperrors.NewBadRequestError(scopeField + " and name are required"))
		return req, false
	}
	return req, true
}
