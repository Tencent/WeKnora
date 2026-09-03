package handler

import (
	stderrors "errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// ListCatalog godoc
// @Summary      List workspace skills
// @Description  Returns every skill definition in this workspace and which sandbox configs it is installed on.
// @Tags         Skills
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /skills/catalog [get]
func (h *SkillHandler) ListCatalog(c *gin.Context) {
	if h.catalog == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": []any{}})
		return
	}
	rows, err := h.catalog.ListCatalog(c.Request.Context(), sandboxConfigTenantID(c))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rows})
}

// RegisterCatalog godoc
// @Summary      Add a skill to the workspace catalog
// @Description  Records a skill without installing it. Send a zip as multipart field "file", or JSON {"source":"..."}.
// @Tags         Skills
// @Accept       json
// @Accept       multipart/form-data
// @Produce      json
// @Success      201  {object}  map[string]interface{}
// @Router       /skills/catalog [post]
func (h *SkillHandler) RegisterCatalog(c *gin.Context) {
	if h.catalog == nil {
		_ = c.Error(apperrors.NewInternalServerError("skill catalog is not configured"))
		return
	}
	maxBytes := secutils.GetMaxSkillBundleSize()
	limitSkillUploadBody(c, maxBytes)

	if strings.HasPrefix(c.ContentType(), "application/json") {
		h.registerCatalogFromSource(c)
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		if isRequestBodyTooLarge(err) {
			_ = c.Error(skillTooLargeError())
			return
		}
		_ = c.Error(apperrors.NewBadRequestError("file is required"))
		return
	}
	defer func() { _ = file.Close() }()
	if header.Size > maxBytes {
		_ = c.Error(skillTooLargeError())
		return
	}
	archive, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError("failed to read the uploaded skill bundle"))
		return
	}
	if int64(len(archive)) > maxBytes {
		_ = c.Error(skillTooLargeError())
		return
	}

	cat, err := h.catalog.RegisterCatalogFromArchive(
		c.Request.Context(), sandboxConfigTenantID(c), archive,
	)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": catalogIDResponse(cat)})
}

func (h *SkillHandler) registerCatalogFromSource(c *gin.Context) {
	var req skillSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if stderrors.As(err, &tooLarge) {
			_ = c.Error(skillSourceRequestTooLargeError())
			return
		}
		_ = c.Error(apperrors.NewBadRequestError("invalid skill source request"))
		return
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		_ = c.Error(apperrors.NewBadRequestError("source is required"))
		return
	}
	cat, err := h.catalog.RegisterCatalogFromSource(
		c.Request.Context(), sandboxConfigTenantID(c), source,
	)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": catalogIDResponse(cat)})
}

type catalogInstallRequest struct {
	SandboxConfigIDs []string `json:"sandbox_config_ids"`
}

// InstallCatalog godoc
// @Summary      Install a catalog skill onto sandboxes
// @Description  Runs the existing snapshot install onto each named sandbox config.
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Param        id   path  string  true  "Catalog skill ID"
// @Success      202  {object}  map[string]interface{}
// @Router       /skills/catalog/{id}/install [post]
func (h *SkillHandler) InstallCatalog(c *gin.Context) {
	if h.catalog == nil {
		_ = c.Error(apperrors.NewInternalServerError("skill catalog is not configured"))
		return
	}
	limitJSONBody(c, skillSourceJSONMaxBytes)
	var req catalogInstallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if isRequestBodyTooLarge(err) {
			_ = c.Error(skillJSONRequestTooLargeError())
			return
		}
		_ = c.Error(apperrors.NewBadRequestError("invalid install request"))
		return
	}
	result, err := h.catalog.InstallCatalogToConfigs(
		c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"), req.SandboxConfigIDs,
	)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	if result == nil {
		result = &service.CatalogInstallResult{}
	}
	data := gin.H{"installs": result.Installs}
	if len(result.Errors) > 0 {
		data["errors"] = result.Errors
	}
	c.JSON(http.StatusAccepted, gin.H{
		"success": len(result.Errors) == 0,
		"data":    data,
	})
}

// DeleteCatalog godoc
// @Summary      Delete a catalog skill
// @Description  Refused while any sandbox still has an installation of this skill.
// @Tags         Skills
// @Param        id   path  string  true  "Catalog skill ID"
// @Success      200  {object}  map[string]interface{}
// @Router       /skills/catalog/{id} [delete]
func (h *SkillHandler) DeleteCatalog(c *gin.Context) {
	if h.catalog == nil {
		_ = c.Error(apperrors.NewInternalServerError("skill catalog is not configured"))
		return
	}
	if err := h.catalog.DeleteCatalog(
		c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"),
	); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListCatalogFiles godoc
// @Summary      List files of a catalog skill
// @Description  Lists the stored catalog bundle. Files belong to the skill definition, not a sandbox install.
// @Tags         Skills
// @Produce      json
// @Param        id   path  string  true  "Catalog skill ID"
// @Success      200  {object}  map[string]interface{}
// @Router       /skills/catalog/{id}/files [get]
func (h *SkillHandler) ListCatalogFiles(c *gin.Context) {
	if h.catalog == nil {
		_ = c.Error(apperrors.NewInternalServerError("skill catalog is not configured"))
		return
	}
	files, err := h.catalog.ListCatalogFiles(
		c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"),
	)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": files})
}

// GetCatalogFile godoc
// @Summary      Read one file of a catalog skill
// @Tags         Skills
// @Produce      json
// @Param        id    path   string  true  "Catalog skill ID"
// @Param        path  query  string  true  "Skill-root-relative file path"
// @Success      200   {object}  map[string]interface{}
// @Router       /skills/catalog/{id}/files/content [get]
func (h *SkillHandler) GetCatalogFile(c *gin.Context) {
	if h.catalog == nil {
		_ = c.Error(apperrors.NewInternalServerError("skill catalog is not configured"))
		return
	}
	file, err := h.catalog.ReadCatalogFile(
		c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"), c.Query("path"),
	)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": file})
}

func catalogIDResponse(cat *types.TenantSkillCatalogEntity) gin.H {
	if cat == nil {
		return gin.H{}
	}
	return gin.H{
		"id":          cat.ID,
		"name":        cat.Name,
		"version":     cat.Version,
		"description": cat.Description,
	}
}
