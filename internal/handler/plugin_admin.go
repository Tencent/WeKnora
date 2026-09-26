package handler

import (
	"context"
	stderrors "errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/driver"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/market"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/types"
)

// PluginAdminHandler lets system administrators install and manage plugins
// for the whole platform.
type PluginAdminHandler struct {
	service *install.Service
	market  *market.Client
	tenants PluginAudienceTenants
	drivers *driver.Set
}

// PluginAudienceTenants finds the workspaces a plugin's audience can name.
type PluginAudienceTenants interface {
	SearchTenants(
		ctx context.Context, keyword string, tenantID uint64, page, pageSize int,
	) ([]*types.Tenant, int64, error)
	GetTenantsByIDs(ctx context.Context, ids []uint64) (map[uint64]*types.Tenant, error)
}

// WithTenants lets the handler look workspaces up for plugin audiences.
func (h *PluginAdminHandler) WithTenants(t PluginAudienceTenants) *PluginAdminHandler {
	h.tenants = t
	return h
}

// WithDrivers lets the handler report where each plugin runs.
func (h *PluginAdminHandler) WithDrivers(d *driver.Set) *PluginAdminHandler {
	h.drivers = d
	return h
}

// NewPluginAdminHandler creates a PluginAdminHandler.
func NewPluginAdminHandler(service *install.Service) *PluginAdminHandler {
	return &PluginAdminHandler{service: service}
}

// WithMarket sets the marketplace index administrators browse; nil leaves
// the platform without one.
func (h *PluginAdminHandler) WithMarket(m *market.Client) *PluginAdminHandler {
	h.market = m
	return h
}

// PluginPackageRequest locates a package by URL; uploads send the archive as
// the multipart "file" field and the digest as a form field instead.
type PluginPackageRequest struct {
	URL string `json:"url"`
	// Digest pins an install to the package reviewed with inspect.
	Digest string `json:"digest"`
	// RemoteURL is where a remote plugin's service runs.
	RemoteURL string `json:"remote_url"`
}

// packageInput is a package with what its install request said about it.
type packageInput struct {
	data      []byte
	source    install.Source
	digest    string
	remoteURL string
}

// readPackage returns the package archive from an upload or a URL.
func (h *PluginAdminHandler) readPackage(c *gin.Context) (packageInput, bool) {
	return readPluginPackage(c, h.service, h.fail)
}

// readPluginPackage returns the package archive from an upload or a URL.
func readPluginPackage(c *gin.Context, service *install.Service, fail func(*gin.Context, error)) (packageInput, bool) {
	if strings.HasPrefix(c.ContentType(), "application/json") {
		limitJSONBody(c, skillSourceJSONMaxBytes)
		var req PluginPackageRequest
		if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.URL) == "" {
			_ = c.Error(errors.NewBadRequestError("url or an uploaded file is required"))
			return packageInput{}, false
		}
		data, err := service.FetchURL(c.Request.Context(), strings.TrimSpace(req.URL))
		if err != nil {
			fail(c, err)
			return packageInput{}, false
		}
		return packageInput{
			data: data, source: install.Source{Kind: "url", URL: strings.TrimSpace(req.URL)},
			digest: req.Digest, remoteURL: strings.TrimSpace(req.RemoteURL),
		}, true
	}

	limitUploadBody(c, pkg.MaxArchiveBytes)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		if isRequestBodyTooLarge(err) {
			_ = c.Error(errors.NewBadRequestError("plugin package is too large"))
		} else {
			_ = c.Error(errors.NewBadRequestError("file is required"))
		}
		return packageInput{}, false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, pkg.MaxArchiveBytes+1))
	if err != nil {
		_ = c.Error(errors.NewBadRequestError("failed to read the uploaded package"))
		return packageInput{}, false
	}
	if len(data) > pkg.MaxArchiveBytes {
		_ = c.Error(errors.NewBadRequestError("plugin package is too large"))
		return packageInput{}, false
	}
	return packageInput{
		data: data, source: install.Source{Kind: "upload", URL: header.Filename},
		digest: c.PostForm("digest"), remoteURL: strings.TrimSpace(c.PostForm("remote_url")),
	}, true
}

func (h *PluginAdminHandler) fail(c *gin.Context, err error) {
	var invalid *install.InvalidError
	switch {
	case stderrors.As(err, &invalid):
		_ = c.Error(errors.NewBadRequestError(err.Error()))
	case stderrors.Is(err, install.ErrNotInstalled):
		_ = c.Error(errors.NewNotFoundError("plugin is not installed"))
	default:
		logger.Errorf(c.Request.Context(), "[plugin] admin request failed: %v", err)
		_ = c.Error(errors.NewInternalServerError("plugin operation failed"))
	}
}

func (h *PluginAdminHandler) ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// InspectPlugin godoc
// @Summary      检查插件包
// @Description  解析上传的插件包（multipart file）或 URL（JSON {url}），返回清单、摘要和安装后的变化，不做任何修改
// @Tags         System
// @Accept       multipart/form-data,json
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/inspect [post]
func (h *PluginAdminHandler) InspectPlugin(c *gin.Context) {
	in, ok := h.readPackage(c)
	if !ok {
		return
	}
	preview, err := h.service.Inspect(c.Request.Context(), in.data)
	if err != nil {
		h.fail(c, err)
		return
	}
	// A digest given up front (a marketplace listing's) must be what came.
	if in.digest != "" && in.digest != preview.Digest {
		_ = c.Error(errors.NewBadRequestError(
			"the package does not match its listed digest (got " + preview.Digest + ", listed " + in.digest + ")"))
		return
	}
	h.ok(c, preview)
}

// MarketPluginListing is a marketplace entry with what is installed.
type MarketPluginListing struct {
	market.Listing
	InstalledVersion string `json:"installedVersion,omitempty"`
}

// ListMarketPlugins godoc
// @Summary      浏览插件市场
// @Description  读取 WEKNORA_PLUGIN_INDEX_URL 指向的插件索引，列出插件、本平台可运行的最新版本和已安装版本。
// @Description  安装时走 inspect/install，并带上索引中的摘要
// @Tags         System
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/market [get]
func (h *PluginAdminHandler) ListMarketPlugins(c *gin.Context) {
	if h.market == nil {
		h.ok(c, gin.H{"configured": false, "plugins": []MarketPluginListing{}})
		return
	}
	listings, skipped, err := h.market.List(c.Request.Context())
	if err != nil {
		logger.Warnf(c.Request.Context(), "[plugin] read the marketplace index: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": gin.H{"message": err.Error()}})
		return
	}
	installed := map[string]string{}
	views, err := h.service.List(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	for _, v := range views {
		installed[v.ID] = v.ActiveVersion
	}
	out := make([]MarketPluginListing, 0, len(listings))
	for _, l := range listings {
		out = append(out, MarketPluginListing{Listing: l, InstalledVersion: installed[l.ID]})
	}
	h.ok(c, gin.H{"configured": true, "indexUrl": h.market.URL(), "plugins": out, "skipped": skipped})
}

// InstallPlugin godoc
// @Summary      安装或升级插件
// @Description  安装插件包（multipart file + digest，或 JSON {url, digest}）。digest 取自 inspect，
// @Description  保证安装的就是审阅过的包。安装后全平台可用，各空间需自行启用。
// @Description  remote 插件还需 remote_url（服务地址）；首次安装的响应里 issuedSecret 是签名密钥，只返回这一次
// @Tags         System
// @Accept       multipart/form-data,json
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins [post]
func (h *PluginAdminHandler) InstallPlugin(c *gin.Context) {
	in, ok := h.readPackage(c)
	if !ok {
		return
	}
	userID, _ := c.Request.Context().Value(types.UserIDContextKey).(string)
	view, err := h.service.Install(c.Request.Context(), install.Request{
		Data: in.data, Source: in.source, ExpectedDigest: in.digest, UserID: userID, RemoteURL: in.remoteURL,
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, h.withEgress(c.Request.Context(), view))
}

// ListInstalledPlugins godoc
// @Summary      列出已安装插件
// @Description  列出系统管理员安装的插件（不含内置插件），含版本和各节点加载状态
// @Tags         System
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins [get]
func (h *PluginAdminHandler) ListInstalledPlugins(c *gin.Context) {
	views, err := h.service.List(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	out := make([]InstalledPluginDTO, len(views))
	for i := range views {
		out[i] = h.withEgress(c.Request.Context(), &views[i])
	}
	h.ok(c, out)
}

// InstalledPluginDTO is an installed plugin as the admin console lists it.
type InstalledPluginDTO struct {
	*install.View
	// Egress is the least controlled egress among the plugin's instances,
	// so the console can flag a grant that is not enforced everywhere.
	// Empty for plugins without code or without instances.
	Egress driver.EgressMode `json:"egress,omitempty"`
}

// withEgress adds how the plugin's outbound traffic is controlled across
// the nodes and plugin hosts running it.
func (h *PluginAdminHandler) withEgress(ctx context.Context, v *install.View) InstalledPluginDTO {
	out := InstalledPluginDTO{View: v}
	if h.drivers == nil || v.DesiredState != types.PluginStateEnabled {
		return out
	}
	switch manifest.RuntimeType(v.Runtime) {
	case manifest.RuntimeBuiltin, manifest.RuntimeDeclarative:
		return out
	}
	d, err := h.drivers.For(manifest.RuntimeType(v.Runtime))
	if err != nil {
		return out
	}
	instances, err := d.Status(ctx, v.ID)
	if err != nil {
		logger.Warnf(ctx, "[plugin] instances of %s: %v", v.ID, err)
		return out
	}
	out.Egress = weakestEgress(instances)
	return out
}

// egressRank orders egress modes from enforced to not controlled.
var egressRank = map[driver.EgressMode]int{
	driver.EgressSandboxed:     1,
	driver.EgressNetworkPolicy: 2,
	driver.EgressProxy:         3,
	driver.EgressUnmanaged:     4,
}

// weakestEgress is the least controlled egress mode among instances.
func weakestEgress(instances []driver.InstanceStatus) driver.EgressMode {
	var out driver.EgressMode
	for _, in := range instances {
		if egressRank[in.Egress] > egressRank[out] {
			out = in.Egress
		}
	}
	return out
}

// GetInstalledPlugin godoc
// @Summary      获取已安装插件
// @Tags         System
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id} [get]
func (h *PluginAdminHandler) GetInstalledPlugin(c *gin.Context) {
	view, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, h.withEgress(c.Request.Context(), view))
}

// PluginInstancesDTO is where a plugin runs: its instances on each node.
type PluginInstancesDTO struct {
	Instances []driver.InstanceStatus `json:"instances"`
	// InstanceError explains an empty Instances (no driver, lookup failed).
	InstanceError string `json:"instanceError,omitempty"`
}

// GetPluginInstances godoc
// @Summary      获取插件的运行实例
// @Description  返回已安装插件在各节点上的实例状态，不受当前空间可见范围限制
// @Tags         System
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/instances [get]
func (h *PluginAdminHandler) GetPluginInstances(c *gin.Context) {
	view, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	out := PluginInstancesDTO{Instances: []driver.InstanceStatus{}}
	if h.drivers == nil {
		h.ok(c, out)
		return
	}
	d, err := h.drivers.For(manifest.RuntimeType(view.Runtime))
	if err == nil {
		out.Instances, err = d.Status(c.Request.Context(), view.ID)
	}
	if err != nil {
		out.InstanceError = err.Error()
		out.Instances = []driver.InstanceStatus{}
	} else if out.Instances == nil {
		out.Instances = []driver.InstanceStatus{}
	}
	h.ok(c, out)
}

// SetInstalledPluginEnabled godoc
// @Summary      全平台启用或停用插件
// @Description  停用后所有节点卸载该插件；各空间的开关和配置保留
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "插件 ID"
// @Param        request  body      SetPluginEnabledRequest  true  "开关"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/enabled [put]
func (h *PluginAdminHandler) SetInstalledPluginEnabled(c *gin.Context) {
	var req SetPluginEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("enabled is required"))
		return
	}
	view, err := h.service.SetEnabled(c.Request.Context(), c.Param("id"), *req.Enabled)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, h.withEgress(c.Request.Context(), view))
}

// ActivatePluginVersionRequest picks a stored version.
type ActivatePluginVersionRequest struct {
	Version string `json:"version" binding:"required"`
}

// ActivatePluginVersion godoc
// @Summary      切换插件版本
// @Description  把已存储的某个版本设为当前版本（回滚或重新升级）
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        id       path      string                        true  "插件 ID"
// @Param        request  body      ActivatePluginVersionRequest  true  "版本"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/active-version [put]
func (h *PluginAdminHandler) ActivatePluginVersion(c *gin.Context) {
	var req ActivatePluginVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("version is required"))
		return
	}
	view, err := h.service.Activate(c.Request.Context(), c.Param("id"), req.Version)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, h.withEgress(c.Request.Context(), view))
}

// SetPluginRemoteURLRequest moves a remote plugin.
type SetPluginRemoteURLRequest struct {
	URL string `json:"url" binding:"required"`
}

// SetPluginRemoteURL godoc
// @Summary      修改远程插件的服务地址
// @Description  私有网络中的地址需要加入 SSRF_WHITELIST
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        id       path      string                     true  "插件 ID"
// @Param        request  body      SetPluginRemoteURLRequest  true  "服务地址"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/remote-url [put]
func (h *PluginAdminHandler) SetPluginRemoteURL(c *gin.Context) {
	var req SetPluginRemoteURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("url is required"))
		return
	}
	view, err := h.service.SetRemoteURL(c.Request.Context(), c.Param("id"), strings.TrimSpace(req.URL))
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, h.withEgress(c.Request.Context(), view))
}

// RotatePluginSecret godoc
// @Summary      轮换远程插件的签名密钥
// @Description  新密钥只在本次响应的 issuedSecret 中返回；服务换上新密钥前调用会失败
// @Tags         System
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/secret/rotate [post]
func (h *PluginAdminHandler) RotatePluginSecret(c *gin.Context) {
	view, err := h.service.RotateSecret(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, h.withEgress(c.Request.Context(), view))
}

// SetPluginAudienceRequest limits a plugin to some workspaces.
type SetPluginAudienceRequest struct {
	// Tenants are the workspaces that see the plugin; null (or absent)
	// lets every workspace see it.
	Tenants *[]uint64 `json:"tenants"`
}

// SetPluginAudience godoc
// @Summary      设置插件的可见空间
// @Description  tenants 为空间 ID 列表时，只有这些空间能看到并启用该插件；为 null 时所有空间可见。
// @Description  范围外的空间保留原有开关和配置，重新纳入后恢复
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        id       path      string                    true  "插件 ID"
// @Param        request  body      SetPluginAudienceRequest  true  "可见空间"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/audience [put]
func (h *PluginAdminHandler) SetPluginAudience(c *gin.Context) {
	var req SetPluginAudienceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("tenants must be a list of workspace IDs or null"))
		return
	}
	var tenants []uint64
	if req.Tenants != nil {
		tenants = append([]uint64{}, *req.Tenants...)
	}
	view, err := h.service.SetAudience(c.Request.Context(), c.Param("id"), tenants)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, h.withEgress(c.Request.Context(), view))
}

// PluginAudienceTenant is a workspace as the audience picker shows it.
type PluginAudienceTenant struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

// ListPluginAudienceTenants godoc
// @Summary      查找可作为插件可见范围的空间
// @Description  按关键词（名称或 ID）搜索空间，或用 ids（逗号分隔）取回指定空间；只返回 ID 和名称，最多 50 个
// @Tags         System
// @Produce      json
// @Param        keyword  query     string  false  "关键词"
// @Param        ids      query     string  false  "空间 ID，逗号分隔"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/tenants [get]
func (h *PluginAdminHandler) ListPluginAudienceTenants(c *gin.Context) {
	if h.tenants == nil {
		h.ok(c, []PluginAudienceTenant{})
		return
	}
	ctx := c.Request.Context()
	out := []PluginAudienceTenant{}
	if raw := strings.TrimSpace(c.Query("ids")); raw != "" {
		var ids []uint64
		for _, part := range strings.Split(raw, ",") {
			if id, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
		if len(ids) > 200 {
			ids = ids[:200]
		}
		found, err := h.tenants.GetTenantsByIDs(ctx, ids)
		if err != nil {
			h.fail(c, err)
			return
		}
		for _, id := range ids {
			if t, ok := found[id]; ok && t != nil {
				out = append(out, PluginAudienceTenant{ID: t.ID, Name: t.Name})
			}
		}
		h.ok(c, out)
		return
	}
	keyword := strings.TrimSpace(c.Query("keyword"))
	var byID uint64
	if id, err := strconv.ParseUint(keyword, 10, 64); err == nil {
		byID, keyword = id, ""
	}
	found, _, err := h.tenants.SearchTenants(ctx, keyword, byID, 1, 50)
	if err != nil {
		h.fail(c, err)
		return
	}
	for _, t := range found {
		out = append(out, PluginAudienceTenant{ID: t.ID, Name: t.Name})
	}
	h.ok(c, out)
}

// UninstallPlugin godoc
// @Summary      卸载插件
// @Description  删除插件及其全部版本；各空间的开关和配置保留，重新安装后恢复
// @Tags         System
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id} [delete]
func (h *PluginAdminHandler) UninstallPlugin(c *gin.Context) {
	if err := h.service.Uninstall(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetPluginSystemConfig godoc
// @Summary      获取插件的平台配置
// @Description  返回插件的平台级配置 Schema 和当前值（密钥脱敏为 ***）
// @Tags         System
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/config [get]
func (h *PluginAdminHandler) GetPluginSystemConfig(c *gin.Context) {
	cfg, err := h.service.GetSystemConfig(c.Request.Context(), c.Param("id"))
	if err != nil {
		configError(c, err)
		return
	}
	h.ok(c, cfg)
}

// UpdatePluginSystemConfig godoc
// @Summary      保存插件的平台配置
// @Description  按插件声明的 Schema 校验并保存平台级配置，对所有空间生效
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        id       path      string               true  "插件 ID"
// @Param        request  body      PluginConfigRequest  true  "配置"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/config [put]
func (h *PluginAdminHandler) UpdatePluginSystemConfig(c *gin.Context) {
	var req PluginConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("values are required"))
		return
	}
	cfg, err := h.service.SetSystemConfig(c.Request.Context(), c.Param("id"), req.Values)
	if err != nil {
		configError(c, err)
		return
	}
	h.ok(c, cfg)
}
