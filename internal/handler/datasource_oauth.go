package handler

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// DataSourceOAuthHandler serves the OneDrive OAuth lifecycle endpoints.
type DataSourceOAuthHandler struct {
	manager   *datasource.OneDriveOAuthManager
	service   interfaces.DataSourceService
	kbService interfaces.KnowledgeBaseService
	content   *service.DataSourceContentManager
}

// NewDataSourceOAuthHandler constructs the OneDrive OAuth endpoint handler.
func NewDataSourceOAuthHandler(
	manager *datasource.OneDriveOAuthManager,
	service interfaces.DataSourceService,
	kbService interfaces.KnowledgeBaseService,
	content *service.DataSourceContentManager,
) *DataSourceOAuthHandler {
	return &DataSourceOAuthHandler{manager: manager, service: service, kbService: kbService, content: content}
}

func (h *DataSourceOAuthHandler) own(c *gin.Context) (*types.DataSource, bool) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return nil, false
	}
	ds, err := h.service.GetDataSource(c.Request.Context(), c.Param("id"))
	if err != nil || ds == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "data source not found"})
		return nil, false
	}
	kb, err := h.kbService.GetKnowledgeBaseByID(c.Request.Context(), ds.KnowledgeBaseID)
	if err != nil || kb == nil || kb.TenantID != tenantID {
		c.JSON(http.StatusNotFound, gin.H{"error": "data source not found"})
		return nil, false
	}
	if ds.Type != types.ConnectorTypeOneDrive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "data source does not use OneDrive OAuth"})
		return nil, false
	}
	return ds, true
}

// AuthorizeURL creates a short-lived Microsoft authorization URL.
func (h *DataSourceOAuthHandler) AuthorizeURL(c *gin.Context) {
	ds, ok := h.own(c)
	if !ok {
		return
	}
	var req struct {
		ReplaceConnection bool `json:"replace_connection"`
	}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
	}
	userID, _ := c.Get(types.UserIDContextKey.String())
	url, err := h.manager.AuthorizeURL(
		c.Request.Context(), ds.TenantID, ds.ID, stringValue(userID), req.ReplaceConnection,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, oauthErrorBody(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"authorization_url": url})
}

// Status returns the non-secret OneDrive connection state.
func (h *DataSourceOAuthHandler) Status(c *gin.Context) {
	ds, ok := h.own(c)
	if !ok {
		return
	}
	status, err := h.manager.Status(c.Request.Context(), ds.TenantID, ds.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, oauthErrorBody(err))
		return
	}
	c.JSON(http.StatusOK, status)
}

// Revoke disconnects OneDrive and removes knowledge owned by this data source.
func (h *DataSourceOAuthHandler) Revoke(c *gin.Context) {
	ds, ok := h.own(c)
	if !ok {
		return
	}
	if err := h.manager.Revoke(c.Request.Context(), ds.TenantID, ds.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to disconnect OneDrive"})
		return
	}
	if _, err := h.content.DeleteByDataSource(c.Request.Context(), ds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "OneDrive disconnected but failed to delete synced knowledge"})
		return
	}
	// The repository already persists paused state atomically with revocation;
	// this also removes the in-process cron registration immediately.
	if err := h.service.PauseDataSource(c.Request.Context(), ds.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "OneDrive disconnected but failed to pause schedule"})
		return
	}
	c.Status(http.StatusNoContent)
}

// Callback completes Microsoft OAuth and renders a closeable popup response.
func (h *DataSourceOAuthHandler) Callback(c *gin.Context) {
	status, err := h.manager.CompleteAuthorization(
		c.Request.Context(), c.Query("state"), c.Query("code"), c.Query("error"),
	)
	if err == nil && status != nil && status.ReplacedConnection {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 30*time.Minute)
		defer cancel()
		if dataSource, getErr := h.service.GetDataSource(cleanupCtx, status.DataSourceID); getErr != nil {
			err = getErr
		} else {
			_, err = h.content.DeleteByDataSource(cleanupCtx, dataSource)
			if err == nil {
				err = h.service.PauseDataSource(cleanupCtx, dataSource.ID)
			}
		}
		if err != nil {
			// Fail closed: a replacement whose cleanup did not finish must not
			// remain usable or appear connected through the status endpoint.
			if dataSource, getErr := h.service.GetDataSource(cleanupCtx, status.DataSourceID); getErr == nil {
				_ = h.manager.Revoke(cleanupCtx, dataSource.TenantID, dataSource.ID)
			}
		}
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	message := "Microsoft OneDrive connected. You can close this window."
	autoClose := true
	if err != nil {
		message = "OneDrive authorization failed: " + err.Error()
		autoClose = false
		c.Status(http.StatusBadRequest)
	} else {
		c.Status(http.StatusOK)
	}
	_ = oauthCallbackTemplate.Execute(c.Writer, struct {
		Message   string
		AutoClose bool
	}{Message: message, AutoClose: autoClose})
}

var oauthCallbackTemplate = template.Must(template.New("datasource-oauth-callback").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>OneDrive authorization</title></head>
<body><p>{{.Message}}</p><button type="button" onclick="window.close()">Close</button>
{{if .AutoClose}}<script>if (window.opener) { window.close(); }</script>{{end}}</body></html>`))

func oauthErrorBody(err error) gin.H {
	code := "oauth_request_failed"
	switch {
	case errors.Is(err, datasource.ErrOAuthNotConfigured):
		code = "oauth_not_configured"
	case errors.Is(err, datasource.ErrOAuthReauthorizationRequired):
		code = "oauth_reauthorization_required"
	case errors.Is(err, datasource.ErrOAuthAccountMismatch):
		code = "oauth_account_mismatch"
	case errors.Is(err, datasource.ErrOAuthConnectionChanged):
		code = "oauth_connection_changed"
	case errors.Is(err, datasource.ErrDataSourceNotFound):
		code = "data_source_not_found"
	}
	return gin.H{"code": code, "message": err.Error(), "error": err.Error()}
}

func stringValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}
