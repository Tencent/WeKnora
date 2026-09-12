package handler

import (
	"context"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
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
	catalog      skillCatalogService
}

type skillCatalogService interface {
	ListCatalog(ctx context.Context, tenantID uint64) ([]service.SkillCatalogView, error)
	RegisterCatalogFromArchive(ctx context.Context, tenantID uint64, archive []byte) (*types.TenantSkillCatalogEntity, error)
	RegisterCatalogFromSource(ctx context.Context, tenantID uint64, source string) (*types.TenantSkillCatalogEntity, error)
	InstallCatalogToConfigs(ctx context.Context, tenantID uint64, catalogID string, configIDs []string) (*service.CatalogInstallResult, error)
	DeleteCatalog(ctx context.Context, tenantID uint64, catalogID string) error
	ListCatalogFiles(ctx context.Context, tenantID uint64, catalogID string) ([]service.SkillFileEntry, error)
	ReadCatalogFile(ctx context.Context, tenantID uint64, catalogID, relativePath string) (*service.SkillFileContent, error)
}

// NewSkillHandler creates a new skill handler. catalog may be nil in tests
// that only exercise the chat picker.
func NewSkillHandler(usableSkills usableSkillLister, catalog skillCatalogService) *SkillHandler {
	return &SkillHandler{
		usableSkills: usableSkills,
		catalog:      catalog,
	}
}

// SkillInfoResponse represents the skill info returned to frontend
type SkillInfoResponse struct {
	Source  string `json:"source,omitempty"`
	Version string `json:"version,omitempty"`

	Name        string `json:"name"`
	Description string `json:"description"`
}

// BuiltinSkillsSummary reports compatible preinstalled resources independently
// of same-named workspace overrides in the usable-skill list.
type BuiltinSkillsSummary struct {
	Known       bool                `json:"known"`
	Version     string              `json:"version,omitempty"`
	Skills      []SkillInfoResponse `json:"skills"`
	Unavailable int                 `json:"unavailable"`
}

// ListSkills godoc
// @Summary      获取当前沙箱配置上可执行的 Skills
// @Description  返回指定沙箱配置镜像内、智能体实际能调用的已安装技能（ready 且启用）。不传 sandbox_config_id 时列表为空。
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Param        sandbox_config_id  query     string  false  "Sandbox config ID"
// @Param        session_id query string false "Session ID for the chat picker (ownership required)"
// @Success      200  {object}  map[string]interface{}  "Skills列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /skills [get]
func (h *SkillHandler) ListSkills(c *gin.Context) {
	configID := c.Query("sandbox_config_id")
	sessionID := c.Query("session_id")
	if (configID == "" && sessionID == "") || h.usableSkills == nil {
		c.JSON(http.StatusOK, gin.H{
			"success":          true,
			"data":             []SkillInfoResponse{},
			"skills_available": false,
		})
		return
	}

	var rows []*types.TenantSkillEntity
	var sessionManifest *types.BuiltinSkillsManifest
	if sessionID != "" {
		reader, ok := h.usableSkills.(interface {
			ListSessionSkillResources(
				context.Context, uint64, string, string,
			) ([]*types.TenantSkillEntity, *types.BuiltinSkillsManifest, error)
		})
		if !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session skills unavailable"})
			return
		}
		var err error
		rows, sessionManifest, err = reader.ListSessionSkillResources(
			c.Request.Context(), sandboxConfigTenantID(c), sessionID, configID,
		)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "session skills unavailable"})
			return
		}
	} else {
		rows = h.usableSkills.ListUsableSkills(c.Request.Context(), sandboxConfigTenantID(c), configID)
	}
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

	var entries []builtin.Entry
	builtinSummary := BuiltinSkillsSummary{Skills: []SkillInfoResponse{}}
	if sessionID != "" {
		if sessionManifest != nil {
			builtinSummary.Known = true
			builtinSummary.Version = sessionManifest.Version
			entries = builtin.CompatibleEntries(sessionManifest)
			builtinSummary.Unavailable = len(sessionManifest.Skills) - len(entries)
		}
	} else if reader, ok := h.usableSkills.(interface {
		GetBuiltinSkillsManifest(context.Context, uint64, string) *types.BuiltinSkillsManifest
	}); ok {
		manifest := reader.GetBuiltinSkillsManifest(c.Request.Context(), sandboxConfigTenantID(c), configID)
		if manifest != nil {
			builtinSummary.Known = true
			builtinSummary.Version = manifest.Version
			entries = builtin.CompatibleEntries(manifest)
			builtinSummary.Unavailable = len(manifest.Skills) - len(entries)
		}
	} else if lister, ok := h.usableSkills.(interface {
		ListBuiltinSkills(context.Context, uint64, string) []builtin.Entry
	}); ok {
		entries = lister.ListBuiltinSkills(c.Request.Context(), sandboxConfigTenantID(c), configID)
		builtinSummary.Known = len(entries) > 0
	}
	seen := map[string]bool{}
	for _, row := range response {
		seen[row.Name] = true
	}
	for _, entry := range entries {
		info := SkillInfoResponse{
			Name: entry.Name, Description: entry.Description["en-US"], Source: "builtin", Version: entry.Version,
		}
		builtinSummary.Skills = append(builtinSummary.Skills, info)
		if !seen[entry.Name] {
			response = append(response, info)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"data":             response,
		"builtin_skills":   builtinSummary,
		"skills_available": true,
	})
}
