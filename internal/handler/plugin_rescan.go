package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/datasource"
	infraWebSearch "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	pluginPkg "github.com/Tencent/WeKnora/internal/plugin"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginHandler exposes plugin control-plane management endpoints.
type PluginHandler struct {
	manager           *pluginPkg.Manager
	connectorRegistry *datasource.ConnectorRegistry
	searchRegistry    *infraWebSearch.Registry
	retrieverRegistry *pluginPkg.RetrieverProviderRegistry
	settings          interfaces.SystemSettingService
}

// NewPluginHandler creates a PluginHandler. The manager and registries are the
// same singletons used at startup so a rescan operates on the live control
// plane rather than a detached copy.
func NewPluginHandler(
	manager *pluginPkg.Manager,
	connectorRegistry *datasource.ConnectorRegistry,
	searchRegistry *infraWebSearch.Registry,
	retrieverRegistry *pluginPkg.RetrieverProviderRegistry,
	settings interfaces.SystemSettingService,
) *PluginHandler {
	return &PluginHandler{
		manager:           manager,
		connectorRegistry: connectorRegistry,
		searchRegistry:    searchRegistry,
		retrieverRegistry: retrieverRegistry,
		settings:          settings,
	}
}

// Rescan re-runs plugin discovery over the configured directories and
// incrementally loads any newly appeared plugins. It never touches already
// loaded plugins, so it is safe to call repeatedly.
func (h *PluginHandler) Rescan(c *gin.Context) {
	report := pluginPkg.RescanExternalFromEnvWithRegistries(
		c.Request.Context(),
		h.manager,
		h.connectorRegistry,
		h.searchRegistry,
		h.retrieverRegistry,
	)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": report})
}

// PluginListEntry is the wire shape for the plugin management UI: one row per
// loaded plugin, plus its current deployment trust level and runtime state.
type PluginListEntry struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	ExtensionType string `json:"extension_type"`
	Description   string `json:"description,omitempty"`
	Entrypoint    string `json:"entrypoint"`
	TrustLevel    string `json:"trust_level"`
	State         string `json:"state"`
	// Pending is true when the plugin's configured trust level differs from the
	// level it was loaded with, i.e. there is a config change that only takes
	// effect after the next rescan/reload.
	Pending bool `json:"pending"`
}

// List returns every externally discovered plugin (loaded from a plugin
// directory) with its trust level and runtime state. Built-in components
// registered as BuiltinRuntime placeholders have an empty entrypoint and are
// omitted: the plugin management UI only manages external plugins, which the
// rescan report (DiscoverPackages) also covers.
func (h *PluginHandler) List(c *gin.Context) {
	infos := h.manager.List()
	entries := make([]PluginListEntry, 0, len(infos))
	for _, info := range infos {
		m := info.Manifest
		if m.Entrypoint == "" {
			continue // built-in placeholder, not an externally discovered plugin
		}
		desc := ""
		if m.Metadata != nil {
			if v, ok := m.Metadata["description"].(string); ok {
				desc = v
			}
		}
		trustLevel := h.manager.PluginTrustLevel(m.ID)
		entries = append(entries, PluginListEntry{
			ID:            m.ID,
			Name:          m.Name,
			Version:       m.Version,
			ExtensionType: m.ExtensionType,
			Description:   desc,
			Entrypoint:    m.Entrypoint,
			TrustLevel:    string(trustLevel),
			State:         string(info.State.State),
			Pending:       trustLevel != h.manager.LoadedTrustLevel(m.ID),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": entries})
}

// SetTrust updates and persists a single plugin's deployment trust level. The
// change applies to future loads/rescans; already-running plugins keep their
// current runtime plan until they are reloaded.
func (h *PluginHandler) SetTrust(c *gin.Context) {
	var req struct {
		PluginID   string `json:"plugin_id"`
		TrustLevel string `json:"trust_level"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	if req.PluginID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "plugin_id is required"})
		return
	}
	level := pluginPkg.PluginTrustLevel(req.TrustLevel)
	switch level {
	case pluginPkg.TrustOffline, pluginPkg.TrustTrusted, pluginPkg.TrustIsolated:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "unsupported trust level"})
		return
	}
	// Dry-run the trust matrix before persisting: an illegal combination (an
	// OCI "docker://" plugin set to "trusted", or a process plugin set to
	// "isolated") is rejected here with 400 instead of failing at the next
	// load — where the plugin card would vanish and lock the admin out.
	if info, ok := h.manager.Get(req.PluginID); ok && info.Manifest.Entrypoint != "" {
		if _, err := pluginPkg.ResolveExecutionPlan(info.Manifest, level); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   fmt.Sprintf("trust level %q is incompatible with plugin %q: %v", req.TrustLevel, req.PluginID, err),
			})
			return
		}
	}
	if h.settings == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "system settings service is unavailable"})
		return
	}
	const settingKey = "plugins.trust_levels"
	raw := h.settings.GetString(c.Request.Context(), settingKey, "WEKNORA_PLUGIN_TRUST_LEVELS", "")
	config, err := pluginPkg.ParsePluginTrustConfig(raw)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "stored plugin trust configuration is invalid"})
		return
	}
	if config == nil {
		config = pluginPkg.PluginTrustConfig{}
	}
	config[req.PluginID] = level
	encoded, err := json.Marshal(config)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "encode plugin trust configuration failed"})
		return
	}
	if _, err := h.settings.Update(c.Request.Context(), settingKey, string(encoded)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "persist plugin trust configuration failed"})
		return
	}
	h.manager.SetPluginTrustLevel(req.PluginID, level)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// Restart force-rebuilds one plugin's runtime (stop + start) without changing
// its trust configuration. It is the manual recovery entry point for an
// unhealthy plugin — e.g. its container was removed externally — and also
// picks up a freshly pulled OCI image on the next run.
func (h *PluginHandler) Restart(c *gin.Context) {
	var req struct {
		PluginID string `json:"plugin_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	if req.PluginID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "plugin_id is required"})
		return
	}
	if err := h.manager.Restart(c.Request.Context(), req.PluginID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
