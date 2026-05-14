package handler

import (
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// SkillHandler handles skill-related HTTP requests
type SkillHandler struct {
	skillService interfaces.SkillService
}

// NewSkillHandler creates a new skill handler
func NewSkillHandler(skillService interfaces.SkillService) *SkillHandler {
	return &SkillHandler{
		skillService: skillService,
	}
}

// SkillInfoResponse represents the skill info returned to frontend
type SkillInfoResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source,omitempty"`
}

// SkillDetailResponse represents the skill detail returned to frontend
type SkillDetailResponse struct {
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Source       string        `json:"source"`
	Instructions string        `json:"instructions"`
	Files        []string      `json:"files"`
	Docs         []DocResponse `json:"docs"`
}

// DocResponse represents the doc content returned to frontend
type DocResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ListSkills godoc
// @Summary      获取Skills列表
// @Description  获取所有可用的Agent Skills（包括预装和已安装的）
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

	allSkills, err := h.skillService.ListAllSkills(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to list skills: " + err.Error()))
		return
	}

	// Convert to response format
	var response []SkillInfoResponse
	for _, s := range allSkills {
		response = append(response, SkillInfoResponse{
			Name:        s.Name,
			Description: s.Description,
			Source:      s.Source,
		})
	}

	// skills_available: true only when sandbox is enabled (docker or local), so frontend can hide/disable Skills UI
	sandboxMode := os.Getenv("WEKNORA_SANDBOX_MODE")
	skillsAvailable := sandboxMode != "" && sandboxMode != "disabled"

	skillsHubAvailable := true

	c.JSON(http.StatusOK, gin.H{
		"success":              true,
		"data":                 response,
		"skills_available":     skillsAvailable,
		"skills_hub_available": skillsHubAvailable,
	})
}

// GetSkillDetail godoc
// @Summary      获取Skill详情
// @Description  获取指定Skill的完整详情
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Param        name  path  string  true  "Skill名称"
// @Success      200  {object}  map[string]interface{}  "Skill详情"
// @Failure      404  {object}  errors.AppError         "Skill不存在"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /skills/{name} [get]
func (h *SkillHandler) GetSkillDetail(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	skill, err := h.skillService.GetSkillDetail(ctx, name)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewNotFoundError("Skill not found: " + name))
		return
	}

	files, _ := h.skillService.ListSkillFiles(ctx, name)

	var docs []DocResponse
	for _, d := range skill.Docs {
		docs = append(docs, DocResponse{
			Path:    d.Path,
			Content: d.Content,
		})
	}

	response := SkillDetailResponse{
		Name:         skill.Summary.Name,
		Description:  skill.Summary.Description,
		Source:       skill.Summary.Source,
		Instructions: skill.Instructions,
		Files:        files,
		Docs:         docs,
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    response,
	})
}

// InstallSkillRequest install skill request
type InstallSkillRequest struct {
	Name string `json:"name" binding:"required"`
	URL  string `json:"url" binding:"required"`
}

// InstallSkill godoc
// @Summary      从URL安装Skill
// @Description  从指定URL下载并安装Skill
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Param        body  body  InstallSkillRequest  true  "安装请求"
// @Success      200  {object}  map[string]interface{}  "安装成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /skills/install [post]
func (h *SkillHandler) InstallSkill(c *gin.Context) {
	ctx := c.Request.Context()

	var req InstallSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	if err := h.skillService.InstallSkillFromURL(ctx, req.Name, req.URL); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to install skill: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Skill installed successfully",
	})
}

// UploadSkill godoc
// @Summary      上传安装Skill
// @Description  通过上传文件安装Skill
// @Tags         Skills
// @Accept       multipart/form-data
// @Produce      json
// @Param        name  formData  string  true  "Skill名称"
// @Param        file  formData  file    true  "Skill归档文件(zip/tar.gz)"
// @Success      200  {object}  map[string]interface{}  "安装成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /skills/upload [post]
func (h *SkillHandler) UploadSkill(c *gin.Context) {
	ctx := c.Request.Context()

	name := c.PostForm("name")
	if name == "" {
		c.Error(errors.NewBadRequestError("Skill name is required"))
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.Error(errors.NewBadRequestError("File is required: " + err.Error()))
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		c.Error(errors.NewInternalServerError("Failed to read file: " + err.Error()))
		return
	}

	if err := h.skillService.InstallSkillFromUpload(ctx, name, data, header.Filename); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to install skill: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Skill uploaded and installed successfully",
	})
}

// UninstallSkill godoc
// @Summary      卸载Skill
// @Description  卸载指定的Skill（仅限用户安装的）
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Param        name  path  string  true  "Skill名称"
// @Success      200  {object}  map[string]interface{}  "卸载成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /skills/{name} [delete]
func (h *SkillHandler) UninstallSkill(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	if err := h.skillService.UninstallSkill(ctx, name); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to uninstall skill: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Skill uninstalled successfully",
	})
}

// RefreshSkills godoc
// @Summary      刷新Skills
// @Description  重新扫描并刷新Skills索引
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "刷新成功"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /skills/refresh [post]
func (h *SkillHandler) RefreshSkills(c *gin.Context) {
	ctx := c.Request.Context()

	if err := h.skillService.RefreshSkills(ctx); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to refresh skills: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Skills refreshed successfully",
	})
}

// ExportSkill godoc
// @Summary      导出Skill
// @Description  将Skill导出为zip归档文件
// @Tags         Skills
// @Accept       json
// @Produce      application/zip
// @Param        name  path  string  true  "Skill名称"
// @Success      200  {file}  binary  "Skill归档文件"
// @Failure      404  {object}  errors.AppError  "Skill不存在"
// @Failure      500  {object}  errors.AppError  "服务器错误"
// @Security     Bearer
// @Router       /skills/{name}/export [get]
func (h *SkillHandler) ExportSkill(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	data, err := h.skillService.ExportSkill(ctx, name)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to export skill: " + err.Error()))
		return
	}

	c.Header("Content-Disposition", "attachment; filename="+name+".zip")
	c.Header("Content-Type", "application/zip")
	c.Data(http.StatusOK, "application/zip", data)
}

// ListSkillFiles godoc
// @Summary      列出Skill文件
// @Description  列出指定Skill目录中的所有文件
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Param        name  path  string  true  "Skill名称"
// @Success      200  {object}  map[string]interface{}  "文件列表"
// @Failure      404  {object}  errors.AppError         "Skill不存在"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /skills/{name}/files [get]
func (h *SkillHandler) ListSkillFiles(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	files, err := h.skillService.ListSkillFiles(ctx, name)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewNotFoundError("Skill not found: " + name))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    files,
	})
}

// --- Artifact 产物相关接口 ---

// ListArtifacts godoc
// @Summary      列出产物
// @Description  列出指定会话中的所有产物
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Param        session_id  query  string  true  "会话ID"
// @Success      200  {object}  map[string]interface{}  "产物列表"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /skills/artifacts [get]
func (h *SkillHandler) ListArtifacts(c *gin.Context) {
	ctx := c.Request.Context()

	userID := c.Query("user_id")
	if userID == "" {
		userID = "default"
	}

	sessionInfo := skills.ArtifactSessionInfo{
		AppName:   "weknora",
		UserID:    userID,
		SessionID: c.Query("session_id"),
	}

	if sessionInfo.SessionID == "" {
		c.Error(errors.NewBadRequestError("session_id is required"))
		return
	}

	artifacts, err := h.skillService.ListArtifacts(ctx, sessionInfo)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to list artifacts: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    artifacts,
	})
}

// SaveArtifactRequest 保存产物的请求体
type SaveArtifactRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	UserID    string `json:"user_id"`
	Filename  string `json:"filename" binding:"required"`
	MimeType  string `json:"mime_type"`
	Name      string `json:"name"`
	Data      string `json:"data"` // base64 编码的数据
}

// SaveArtifact godoc
// @Summary      保存产物
// @Description  保存Skill执行产生的产物
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Param        body  body  SaveArtifactRequest  true  "产物数据"
// @Success      200  {object}  map[string]interface{}  "保存成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /skills/artifacts [post]
func (h *SkillHandler) SaveArtifact(c *gin.Context) {
	ctx := c.Request.Context()

	var req SaveArtifactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	userID := req.UserID
	if userID == "" {
		userID = "default"
	}

	sessionInfo := skills.ArtifactSessionInfo{
		AppName:   "weknora",
		UserID:    userID,
		SessionID: req.SessionID,
	}

	artifact := &skills.Artifact{
		Data:     []byte(req.Data),
		MimeType: req.MimeType,
		Name:     req.Name,
	}

	version, err := h.skillService.SaveArtifact(ctx, sessionInfo, req.Filename, artifact)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to save artifact: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"version": version,
	})
}

// ExportArtifact godoc
// @Summary      导出产物
// @Description  下载指定的产物文件
// @Tags         Skills
// @Accept       json
// @Produce      application/octet-stream
// @Param        session_id  query  string  true   "会话ID"
// @Param        filename    query  string  true   "文件名"
// @Param        version     query  int     false  "版本号"
// @Success      200  {file}  binary  "产物文件"
// @Failure      400  {object}  errors.AppError  "请求参数错误"
// @Failure      404  {object}  errors.AppError  "产物不存在"
// @Failure      500  {object}  errors.AppError  "服务器错误"
// @Security     Bearer
// @Router       /skills/artifacts/export [get]
func (h *SkillHandler) ExportArtifact(c *gin.Context) {
	ctx := c.Request.Context()

	userID := c.Query("user_id")
	if userID == "" {
		userID = "default"
	}

	sessionInfo := skills.ArtifactSessionInfo{
		AppName:   "weknora",
		UserID:    userID,
		SessionID: c.Query("session_id"),
	}

	filename := c.Query("filename")
	if sessionInfo.SessionID == "" || filename == "" {
		c.Error(errors.NewBadRequestError("session_id and filename are required"))
		return
	}

	var versionPtr *int
	if v := c.Query("version"); v != "" {
		ver, err := strconv.Atoi(v)
		if err == nil {
			versionPtr = &ver
		}
	}

	data, mimeType, err := h.skillService.ExportArtifact(ctx, sessionInfo, filename, versionPtr)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewNotFoundError("Artifact not found: " + filename))
		return
	}

	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Header("Content-Type", mimeType)
	c.Data(http.StatusOK, mimeType, data)
}

// DeleteArtifact godoc
// @Summary      删除产物
// @Description  删除指定的产物
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Param        session_id  query  string  true  "会话ID"
// @Param        filename    query  string  true  "文件名"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /skills/artifacts [delete]
func (h *SkillHandler) DeleteArtifact(c *gin.Context) {
	ctx := c.Request.Context()

	userID := c.Query("user_id")
	if userID == "" {
		userID = "default"
	}

	sessionInfo := skills.ArtifactSessionInfo{
		AppName:   "weknora",
		UserID:    userID,
		SessionID: c.Query("session_id"),
	}

	filename := c.Query("filename")
	if sessionInfo.SessionID == "" || filename == "" {
		c.Error(errors.NewBadRequestError("session_id and filename are required"))
		return
	}

	if err := h.skillService.DeleteArtifact(ctx, sessionInfo, filename); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to delete artifact: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Artifact deleted successfully",
	})
}
