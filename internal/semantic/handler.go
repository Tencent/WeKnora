package semantic

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Handler serves the module REST API under /api/v1/semantic. Credentials
// never leave the server: connections serialize through ConnectionResponse.
type Handler struct {
	engine *Engine
}

// NewHandler builds the module handler.
func NewHandler(engine *Engine) *Handler { return &Handler{engine: engine} }

// ConnectionResponse is the credential-free connection shape. Non-secret
// connection params (host/port/database/username/extra) are included so the
// edit dialog can pre-fill; the password never leaves the server
// (has_password flags its presence instead).
type ConnectionResponse struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Type        string                 `json:"type"`
	Guided      bool                   `json:"guided"`
	Status      string                 `json:"status"`
	Host        string                 `json:"host,omitempty"`
	Port        int                    `json:"port,omitempty"`
	Database    string                 `json:"database,omitempty"`
	Username    string                 `json:"username,omitempty"`
	Extra       map[string]interface{} `json:"extra,omitempty"`
	LastTest    types.JSON             `json:"last_test_result,omitempty"`
	HasPassword bool                   `json:"has_password"`
	CreatedBy   string                 `json:"created_by"`
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
}

func newConnectionResponse(c *CubeConnection) *ConnectionResponse {
	cfg := decryptConfig(c)
	return &ConnectionResponse{
		ID: c.ID, Name: c.Name, Title: c.Title, Description: c.Description,
		Type: c.Type, Guided: IsGuidedType(c.Type), Status: c.Status,
		Host: cfg.Host, Port: cfg.Port, Database: cfg.Database,
		Username: cfg.Username, Extra: cfg.Extra,
		LastTest: c.LastTestResult, HasPassword: cfg.Password != "",
		CreatedBy: c.CreatedBy,
		CreatedAt: c.CreatedAt.Format(timeLayout), UpdatedAt: c.UpdatedAt.Format(timeLayout),
	}
}

const timeLayout = "2006-01-02 15:04:05"

func (h *Handler) tenantOf(c *gin.Context) (uint64, string, bool) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		return 0, "", false
	}
	userID, _ := c.Get(types.UserIDContextKey.String())
	uid, _ := userID.(string)
	return tenantID, uid, true
}

// respondError maps engine errors to appropriate HTTP status codes.
// ErrNotFound → 404; everything else → 500 with a generic message
// (the original error is logged server-side only, preventing internal
// details like hosts, usernames, or SQL fragments from reaching the client).
func (h *Handler) respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "resource not found"})
	case errors.Is(err, ErrConflict):
		// ConflictError 的具体消息是业务可读的，安全透出
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		logger.Errorf(c.Request.Context(), "[semantic] internal error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

func unauthorized(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: workspace context missing"})
}

// Info godoc
// @Summary Get semantic module info
// @Description Get semantic module info
// @Tags Semantic
// @Produce json
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/info [GET]
func (h *Handler) Info(c *gin.Context) {
	c.JSON(http.StatusOK, h.engine.Info(c.Request.Context()))
}

// ListConnections godoc
// @Summary List data source connections
// @Description List data source connections
// @Tags Semantic
// @Produce json
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/connections [GET]
func (h *Handler) ListConnections(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	list, err := h.engine.ListConnections(c.Request.Context(), tenant)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]*ConnectionResponse, 0, len(list))
	for _, conn := range list {
		out = append(out, newConnectionResponse(conn))
	}
	c.JSON(http.StatusOK, gin.H{"connections": out})
}

// GetConnection godoc
// @Summary Get a data source connection
// @Description Get a data source connection
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/connections/{id} [GET]
func (h *Handler) GetConnection(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	conn, err := h.engine.GetConnection(c.Request.Context(), tenant, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "connection not found"})
		return
	}
	c.JSON(http.StatusOK, newConnectionResponse(conn))
}

// CreateConnection godoc
// @Summary Create a data source connection
// @Description Create a data source connection
// @Tags Semantic
// @Produce json
// @Accept json
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/connections [POST]
func (h *Handler) CreateConnection(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var in ConnectionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	conn, err := h.engine.CreateConnection(c.Request.Context(), uid, tenant, &in)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, newConnectionResponse(conn))
}

// UpdateConnection godoc
// @Summary Update a data source connection
// @Description Update a data source connection
// @Tags Semantic
// @Produce json
// @Accept json
// @Param id path string true "Resource ID"
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/connections/{id} [PUT]
func (h *Handler) UpdateConnection(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var in ConnectionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	conn, err := h.engine.UpdateConnection(c.Request.Context(), uid, tenant, c.Param("id"), &in)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, newConnectionResponse(conn))
}

// DeleteConnection godoc
// @Summary Delete a data source connection
// @Description Delete a data source connection
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/connections/{id} [DELETE]
func (h *Handler) DeleteConnection(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	if err := h.engine.DeleteConnection(c.Request.Context(), uid, tenant, c.Param("id")); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// TestConnectionRaw godoc
// @Summary Test connection with raw params not persisted
// @Description Test connection with raw params not persisted
// @Tags Semantic
// @Produce json
// @Accept json
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/connections/test [POST]
func (h *Handler) TestConnectionRaw(c *gin.Context) {
	var in ConnectionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	result, err := h.engine.TestConnectionRaw(c.Request.Context(), &in)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"test": result, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"test": result})
}

// TestConnectionDraftByID POST /semantic/connections/:id/test-draft
// (unsaved form params + stored password)
func (h *Handler) TestConnectionDraftByID(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var in ConnectionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	result, err := h.engine.TestConnectionDraft(c.Request.Context(), tenant, c.Param("id"), &in)
	if err != nil && result == nil {
		h.respondError(c, err)
		return
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadRequest
	}
	c.JSON(status, gin.H{"test": result})
}

// TestConnectionByID godoc
// @Summary Test a saved connection
// @Description Test a saved connection
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/connections/{id}/test [POST]
func (h *Handler) TestConnectionByID(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	result, err := h.engine.TestConnectionByID(c.Request.Context(), tenant, c.Param("id"))
	if err != nil && result == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "connection not found"})
		return
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadRequest
	}
	c.JSON(status, gin.H{"test": result})
}

// ListTables godoc
// @Summary List tables of a guided connection
// @Description List tables of a guided connection
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/connections/{id}/tables [GET]
func (h *Handler) ListTables(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	tables, err := h.engine.ListTables(c.Request.Context(), tenant, c.Param("id"))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tables": tables})
}

// ListColumns godoc
// @Summary List columns of a table
// @Description List columns of a table
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/connections/{id}/columns [GET]
func (h *Handler) ListColumns(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	cols, err := h.engine.ListColumns(c.Request.Context(), tenant, c.Param("id"),
		c.Query("schema"), c.Query("table"))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"columns": cols})
}

// DraftCube godoc
// @Summary Generate a draft cube YAML from a table
// @Description Generate a draft cube YAML from a table
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/connections/{id}/draft [GET]
func (h *Handler) DraftCube(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	yamlText, err := h.engine.DraftCube(c.Request.Context(), tenant, c.Param("id"),
		c.Query("schema"), c.Query("table"))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"yaml": yamlText})
}

// ListModels godoc
// @Summary List semantic models
// @Description List semantic models
// @Tags Semantic
// @Produce json
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/models [GET]
func (h *Handler) ListModels(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	list, err := h.engine.ListModels(c.Request.Context(), tenant)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": list})
}

// GetModel godoc
// @Summary Get a semantic model
// @Description Get a semantic model
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/models/{id} [GET]
func (h *Handler) GetModel(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	m, err := h.engine.GetModel(c.Request.Context(), tenant, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
		return
	}
	c.JSON(http.StatusOK, m)
}

// CreateModel godoc
// @Summary Create a semantic model
// @Description Create a semantic model
// @Tags Semantic
// @Produce json
// @Accept json
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/models [POST]
func (h *Handler) CreateModel(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var in SaveModelInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	m, err := h.engine.CreateModel(c.Request.Context(), uid, tenant, &in)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, m)
}

// UpdateModel godoc
// @Summary Update a semantic model
// @Description Update a semantic model
// @Tags Semantic
// @Produce json
// @Accept json
// @Param id path string true "Resource ID"
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/models/{id} [PUT]
func (h *Handler) UpdateModel(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var in SaveModelInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	m, err := h.engine.UpdateModel(c.Request.Context(), uid, tenant, c.Param("id"), &in)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, m)
}

// DeleteModel godoc
// @Summary Delete a semantic model
// @Description Delete a semantic model
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/models/{id} [DELETE]
func (h *Handler) DeleteModel(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	if err := h.engine.DeleteModel(c.Request.Context(), uid, tenant, c.Param("id")); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// PublishModel godoc
// @Summary Publish a semantic model
// @Description Publish a semantic model
// @Tags Semantic
// @Produce json
// @Accept json
// @Param id path string true "Resource ID"
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/models/{id}/publish [POST]
func (h *Handler) PublishModel(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body)
	res, err := h.engine.Publish(c.Request.Context(), uid, tenant, c.Param("id"), body.Note)
	if err != nil {
		status := http.StatusBadRequest
		if res != nil {
			c.JSON(status, gin.H{"error": err.Error(), "result": res})
			return
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// UnpublishModel godoc
// @Summary Unpublish a semantic model
// @Description Unpublish a semantic model
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/models/{id}/unpublish [POST]
func (h *Handler) UnpublishModel(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	if err := h.engine.Unpublish(c.Request.Context(), uid, tenant, c.Param("id")); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": ModelStatusDraft})
}

// ListVersions godoc
// @Summary List publish version history
// @Description List publish version history
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/models/{id}/versions [GET]
func (h *Handler) ListVersions(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	list, err := h.engine.ListVersions(c.Request.Context(), tenant, c.Param("id"))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": list})
}

// RollbackModel godoc
// @Summary Roll back to a previous version
// @Description Roll back to a previous version
// @Tags Semantic
// @Produce json
// @Accept json
// @Param id path string true "Resource ID"
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/models/{id}/rollback [POST]
func (h *Handler) RollbackModel(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var body struct {
		VersionID string `json:"version_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.VersionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version_id is required"})
		return
	}
	res, err := h.engine.Rollback(c.Request.Context(), uid, tenant, c.Param("id"), body.VersionID)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// PreviewModel godoc
// @Summary Preview query data under caller identity
// @Description Preview query data under caller identity
// @Tags Semantic
// @Produce json
// @Accept json
// @Param id path string true "Resource ID"
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/models/{id}/preview [POST]
func (h *Handler) PreviewModel(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var q PreviewQuery
	if err := c.ShouldBindJSON(&q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query"})
		return
	}
	resp, err := h.engine.Preview(c.Request.Context(), uid, tenant, c.Param("id"), &q)
	if err != nil {
		h.respondError(c, err)
		return
	}
	meta, _ := h.engine.MetaForUser(c.Request.Context(), tenant, uid)
	hint := WarnDenied(meta, &q)
	c.JSON(http.StatusOK, gin.H{"data": resp.Data, "annotation": resp.Annotation, "hint": hint})
}

// Meta godoc
// @Summary Get visible Cube models for caller
// @Description Get visible Cube models for caller
// @Tags Semantic
// @Produce json
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/meta [GET]
func (h *Handler) Meta(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	meta, err := h.engine.MetaForUser(c.Request.Context(), tenant, uid)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, meta)
}

// ListGroups godoc
// @Summary List data groups
// @Description List data groups
// @Tags Semantic
// @Produce json
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/groups [GET]
func (h *Handler) ListGroups(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	list, err := h.engine.ListGroups(c.Request.Context(), tenant)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": list})
}

// CreateGroup godoc
// @Summary Create a data group
// @Description Create a data group
// @Tags Semantic
// @Produce json
// @Accept json
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/groups [POST]
func (h *Handler) CreateGroup(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var g DataGroup
	if err := c.ShouldBindJSON(&g); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	created, err := h.engine.CreateGroup(c.Request.Context(), uid, tenant, &g)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, created)
}

// UpdateGroup godoc
// @Summary Update a data group
// @Description Update a data group
// @Tags Semantic
// @Produce json
// @Accept json
// @Param id path string true "Resource ID"
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/groups/{id} [PUT]
func (h *Handler) UpdateGroup(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var in DataGroup
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	g, err := h.engine.UpdateGroup(c.Request.Context(), uid, tenant, c.Param("id"), &in)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, g)
}

// GroupUsage godoc
// @Summary Get how many models reference a group
// @Description Get how many models reference a group
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/groups/{id}/usage [GET]
func (h *Handler) GroupUsage(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	usage, err := h.engine.GetGroupUsage(c.Request.Context(), tenant, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	c.JSON(http.StatusOK, usage)
}

// DeleteGroup godoc
// @Summary Delete a data group
// @Description Delete a data group
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/groups/{id} [DELETE]
func (h *Handler) DeleteGroup(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	if err := h.engine.DeleteGroup(c.Request.Context(), uid, tenant, c.Param("id")); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// SetGroupMembers godoc
// @Summary Replace data group members
// @Description Replace data group members
// @Tags Semantic
// @Produce json
// @Accept json
// @Param id path string true "Resource ID"
// @Param request body object true "Request body"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/groups/{id}/members [PUT]
func (h *Handler) SetGroupMembers(c *gin.Context) {
	tenant, uid, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	var body struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := h.engine.SetGroupMembers(c.Request.Context(), uid, tenant, c.Param("id"), body.UserIDs); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": len(body.UserIDs)})
}

// ListGroupMembers godoc
// @Summary List data group members
// @Description List data group members
// @Tags Semantic
// @Produce json
// @Param id path string true "Resource ID"
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/groups/{id}/members [GET]
func (h *Handler) ListGroupMembers(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	list, err := h.engine.ListGroupMembers(c.Request.Context(), tenant, c.Param("id"))
	if err != nil {
		h.respondError(c, err)
		return
	}
	ids := make([]string, 0, len(list))
	for _, m := range list {
		ids = append(ids, m.UserID)
	}
	c.JSON(http.StatusOK, gin.H{"user_ids": ids})
}

// ListAudits godoc
// @Summary List recent audit logs
// @Description List recent audit logs
// @Tags Semantic
// @Produce json
// @Success 200 {object} object
// @Failure 400 {object} map[string]string
// @Router /semantic/audit [GET]
func (h *Handler) ListAudits(c *gin.Context) {
	tenant, _, ok := h.tenantOf(c)
	if !ok {
		unauthorized(c)
		return
	}
	list, err := h.engine.ListAudits(c.Request.Context(), tenant, h.engine.cfg.AuditLimit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"audit_logs": list})
}
