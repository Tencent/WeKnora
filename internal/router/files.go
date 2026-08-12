package router

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// files.go hosts every file-proxy surface the router exposes:
//
//   - /files                              tenant-scoped raw storage proxy
//   - /api/v1/knowledge-bases/:id/files   KB-scoped proxy (shared-KB images)
//   - /api/v1/sessions/:id/messages/:message_id/files
//                                          message-scoped proxy (shared-agent output)
//   - /api/v1/files/presigned             HMAC-signed anonymous access (IM)
//   - /api/v1/files/presigned-preview     Admin-only URL diagnostics
//   - /r/:token                           short-lived capability URLs
//
// The handlers differ in how they authenticate and which tenant owns the
// object, but share the same storage plumbing — the helpers directly below
// are that shared plumbing.

// getRouteRegistrar is the minimal registration surface serveFiles* needs;
// both *gin.Engine and *gin.RouterGroup satisfy it, which keeps the file
// routes testable without building a full engine.
type getRouteRegistrar interface {
	GET(string, ...gin.HandlerFunc) gin.IRoutes
}

// messageFileLookup is the narrow message-service surface needed by the
// message-scoped file proxy. Keeping it small makes the authorization boundary
// independently testable.
type messageFileLookup interface {
	GetMessage(ctx context.Context, sessionID, messageID string) (*types.Message, error)
}

// sharedAgentFileLookup verifies that a source workspace's agent is still
// shared to the caller. Revoking the share therefore also revokes historical
// message-file access.
type sharedAgentFileLookup interface {
	GetSharedAgentForTenant(
		ctx context.Context,
		tenantID uint64,
		callerTenantRole types.TenantRole,
		agentID string,
		sourceTenantID ...uint64,
	) (*types.CustomAgent, error)
}

// localStorageBaseDir resolves LOCAL_STORAGE_BASE_DIR with the container
// default.
func localStorageBaseDir() string {
	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	if baseDir == "" {
		baseDir = "/data/files"
	}
	return baseDir
}

// localStorageAbsDir is localStorageBaseDir made absolute — the form the
// storage resolvers expect.
func localStorageAbsDir() string {
	absDir, _ := filepath.Abs(localStorageBaseDir())
	return absDir
}

// parseStorageTarget splits a stored file path into its optional storage
// backend ID and provider scheme (e.g. "backend://3/local://7/x.png" ->
// ("3", "local"); "cos://7/x.png" -> ("", "cos")).
func parseStorageTarget(filePath string) (backendID, provider string) {
	backendID, providerPath, scoped := types.ParseStorageBackendPath(filePath)
	if !scoped {
		providerPath = filePath
	}
	return backendID, types.ParseProviderScheme(providerPath)
}

// requireFilePathQuery validates the file_path query parameter every proxy
// route accepts. On failure the 400 response has been written and ok=false.
func requireFilePathQuery(c *gin.Context) (string, bool) {
	filePath := strings.TrimSpace(c.Query("file_path"))
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing required parameter: file_path"})
		return "", false
	}
	if strings.Contains(filePath, "..") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path"})
		return "", false
	}
	return filePath, true
}

// resolveCatalogResource maps a logical resource path onto its physical path
// when the catalog knows it, enforcing that the resource belongs to
// ownerTenantID. isResource reports whether the path resolved to a registered
// resource (whose tenant is then authoritative). On !ok the response has been
// written.
func resolveCatalogResource(
	c *gin.Context,
	catalog interfaces.ResourceCatalog,
	filePath string,
	ownerTenantID uint64,
) (resolved string, isResource, ok bool) {
	if catalog == nil {
		return filePath, false, true
	}
	resolvedPath, resource, err := catalog.ResolvePath(c.Request.Context(), filePath)
	if err != nil {
		c.Status(http.StatusNotFound)
		return "", false, false
	}
	if resource == nil {
		return filePath, false, true
	}
	if resource.TenantID != ownerTenantID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: resource not accessible"})
		return "", false, false
	}
	return resolvedPath, true, true
}

// resolveFileService picks the file service for (tenant, backendID, provider)
// — via the storage resolver when wired, else directly from the tenant's
// storage config. No fallback; used by the presigned surfaces where a
// missing tenant config must surface as an error.
func resolveFileService(
	ctx context.Context,
	tenant *types.Tenant,
	backendID, provider, absDir string,
	storageResolver interfaces.StorageBackendResolver,
) (interfaces.FileService, string, error) {
	if storageResolver != nil {
		return storageResolver.ResolveFileService(ctx, tenant, backendID, provider, absDir)
	}
	return filesvc.NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, absDir)
}

// resolveTenantFileServiceWithFallback is resolveFileService plus the
// fallback rule shared by /files and the KB-scoped proxy: when tenant-level
// resolution fails and the requested provider matches the process-global
// STORAGE_TYPE, serve through the global file service instead of failing.
// Returns ok=false (already logged) when no service can be resolved; the
// caller decides the HTTP status.
func resolveTenantFileServiceWithFallback(
	ctx context.Context,
	logTag string,
	tenant *types.Tenant,
	backendID, provider, absDir string,
	storageResolver interfaces.StorageBackendResolver,
	globalFileService interfaces.FileService,
) (fileSvc interfaces.FileService, resolvedProvider string, ok bool) {
	var err error
	if storageResolver != nil {
		fileSvc, resolvedProvider, err = storageResolver.ResolveFileService(ctx, tenant, backendID, provider, absDir)
	} else if tenant.StorageEngineConfig != nil {
		fileSvc, resolvedProvider, err = filesvc.NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, absDir)
	} else {
		err = http.ErrMissingFile
	}
	if err == nil {
		return fileSvc, resolvedProvider, true
	}

	globalStorageType := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_TYPE")))
	if globalStorageType == "" {
		globalStorageType = "local"
	}
	if provider == globalStorageType && globalFileService != nil {
		logger.Warnf(ctx, "[Router] %s tenant storage config missing or invalid, fallback to global file service: tenant_id=%d provider=%s err=%v",
			logTag, tenant.ID, provider, err)
		return globalFileService, globalStorageType, true
	}
	logger.Warnf(ctx, "[Router] %s resolve file service failed without fallback: tenant_id=%d provider=%s global_storage_type=%s err=%v",
		logTag, tenant.ID, provider, globalStorageType, err)
	return nil, "", false
}

// streamStoredFile writes the shared success response of every file proxy:
// safe content type, nosniff, disposition for non-inline types, the route's
// cache policy, then the body (skipped for HEAD). Closes reader.
func streamStoredFile(c *gin.Context, reader io.ReadCloser, contentType string, inline bool, cacheControl, logTag string) {
	defer reader.Close()
	c.Header("Content-Type", contentType)
	c.Header("X-Content-Type-Options", "nosniff")
	if !inline {
		c.Header("Content-Disposition", "attachment")
	}
	c.Header("Cache-Control", cacheControl)
	c.Status(http.StatusOK)
	if c.Request.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(c.Writer, reader); err != nil {
		logger.Warnf(c.Request.Context(), "[Router] %s write response failed: %v", logTag, err)
	}
}

// newFileServeHandler builds the file-proxy handler. It reads the tenant from
// the request context (set by whichever auth middleware precedes it), so the
// same handler backs both the authenticated /files route and the embed route
// (where EmbedAuth injects the channel's tenant). Tenant ownership of the
// requested path is enforced via ValidateStoragePathTenant either way.
func newFileServeHandler(
	globalFileService interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	resourceCatalogs ...interfaces.ResourceCatalog,
) gin.HandlerFunc {
	resourceCatalog := firstResourceCatalog(resourceCatalogs)
	absDir := localStorageAbsDir()
	if info, err := os.Stat(absDir); err != nil || !info.IsDir() {
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			logger.Warnf(context.Background(), "[Router] Cannot create local storage dir %s: %v", absDir, err)
		}
	}

	return func(c *gin.Context) {
		filePath, ok := requireFilePathQuery(c)
		if !ok {
			return
		}

		tenant, _ := c.Request.Context().Value(types.TenantInfoContextKey).(*types.Tenant)
		if tenant == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: workspace context missing"})
			return
		}
		filePath, resourceResolved, ok := resolveCatalogResource(c, resourceCatalog, filePath, tenant.ID)
		if !ok {
			return
		}

		// A registered resource's tenant is authoritative. Physical provider
		// paths remain an internal locator and are not required to encode access
		// control metadata (some cloud layouts contain other numeric segments).
		if !resourceResolved {
			if err := secutils.ValidateStoragePathTenant(filePath, tenant.ID); err != nil {
				logger.Warnf(c.Request.Context(),
					"[Router] /files denied cross-tenant or invalid path: tenant_id=%d file_path=%q err=%v",
					tenant.ID, filePath, err)
				c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: file path not accessible"})
				return
			}
		}

		backendID, provider := parseStorageTarget(filePath)
		fileSvc, resolvedProvider, ok := resolveTenantFileServiceWithFallback(
			c.Request.Context(), "/files", tenant, backendID, provider, absDir, storageResolver, globalFileService)
		if !ok {
			c.Status(http.StatusBadRequest)
			return
		}

		reader, err := fileSvc.GetFile(c.Request.Context(), filePath)
		if err != nil {
			logger.Warnf(c.Request.Context(), "[Router] /files get file failed: tenant_id=%d provider=%s path=%q err=%v",
				tenant.ID, resolvedProvider, filePath, err)
			c.Status(http.StatusNotFound)
			return
		}

		contentType, inline := secutils.SafeContentTypeByFilename(filePath)
		streamStoredFile(c, reader, contentType, inline, "public, max-age=86400", "/files")
	}
}

func serveFiles(r getRouteRegistrar, globalFileService interfaces.FileService, resolvers ...interfaces.StorageBackendResolver) {
	var storageResolver interfaces.StorageBackendResolver
	if len(resolvers) > 0 {
		storageResolver = resolvers[0]
	}
	serveFilesWithResources(r, globalFileService, storageResolver, nil)
}

// serveFilesWithResources registers the tenant-scoped storage proxy.
// It is registered after auth middleware, so tenant context comes from
// authentication.
//
// Route:
//   - GET /files?file_path=<provider://...>
func serveFilesWithResources(
	r getRouteRegistrar,
	globalFileService interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	resourceCatalog interfaces.ResourceCatalog,
) {
	logger.Infof(context.Background(), "[Router] Serving files from /files")
	// /files sits outside the /api/v1 APIKeyGate, so it carries its own
	// API-key guard. A KB-restricted key is denied (a raw storage path cannot
	// be bounded to its allow-list); full-access keys and tenant-wide retrieve
	// keys pass, since the handler still enforces same-tenant paths
	// (ValidateStoragePathTenant). Embed routes use their own
	// /embed/.../files handler.
	r.GET(
		"/files",
		middleware.AllowFileServeAPIKey(),
		newFileServeHandler(globalFileService, storageResolver, resourceCatalog),
	)
}

// serveResourceGrants exposes short, revocable capability URLs for clients
// such as IM platforms that cannot attach a WeKnora bearer/embed token.
func serveResourceGrants(
	r *gin.Engine,
	resourceCatalog interfaces.ResourceCatalog,
	tenantService interfaces.TenantService,
	globalFileService interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
) {
	if resourceCatalog == nil || tenantService == nil {
		return
	}
	handler := func(c *gin.Context) {
		ctx := c.Request.Context()
		resource, err := resourceCatalog.ResolveAccessGrant(ctx, c.Param("token"))
		if err != nil || resource == nil {
			c.Status(http.StatusNotFound)
			return
		}
		tenant, err := tenantService.GetTenantByID(ctx, resource.TenantID)
		if err != nil || tenant == nil {
			c.Status(http.StatusNotFound)
			return
		}

		// The grant row carries its own backend ID; only the provider scheme
		// comes from the physical path.
		_, provider := parseStorageTarget(resource.PhysicalPath)
		var fileSvc interfaces.FileService
		if storageResolver != nil {
			fileSvc, _, err = storageResolver.ResolveFileService(
				ctx, tenant, resource.StorageBackendID, provider, localStorageBaseDir())
		} else {
			fileSvc = globalFileService
		}
		if err != nil || fileSvc == nil {
			logger.Warnf(ctx, "[Router] resource grant storage resolution failed: resource_id=%s err=%v", resource.ID, err)
			c.Status(http.StatusNotFound)
			return
		}
		reader, err := fileSvc.GetFile(ctx, resource.PhysicalPath)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}

		fileName := resource.OriginalName
		if fileName == "" {
			fileName = resource.PhysicalPath
		}
		contentType, inline := secutils.SafeContentTypeByFilename(fileName)
		if resource.MimeType != "" && inline {
			contentType = resource.MimeType
		}
		streamStoredFile(c, reader, contentType, inline, "private, max-age=300",
			"resource grant (resource_id="+resource.ID+")")
	}
	r.GET("/r/:token", handler)
	r.HEAD("/r/:token", handler)
}

// serveKBScopedFiles registers the KB-scoped file proxy used to render images
// embedded in a knowledge base's content (chunks / wiki pages). Unlike the
// tenant-scoped /files route — which enforces file_path.tenant == caller.tenant
// and therefore cannot serve objects owned by another tenant — this route is
// gated by RequireKBAccess. That guard resolves org-shared / agent-visible KBs
// and rewrites the request context's tenant ID to the KB's *owner* (source)
// tenant, so images stored under the owner tenant (local://<owner>/exports/...)
// become reachable by tenants that legitimately share the KB, while still
// enforcing that the requested path belongs to that owner tenant.
//
// Route:
//   - GET /api/v1/knowledge-bases/:id/files?file_path=<provider://...>
func serveKBScopedFiles(
	r *gin.RouterGroup,
	g *rbacGuards,
	tenantService interfaces.TenantService,
	globalFileService interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	resourceCatalogs ...interfaces.ResourceCatalog,
) {
	logger.Infof(context.Background(), "[Router] Serving KB-scoped files from /knowledge-bases/:id/files")
	// API-key access mirrors /files: KB-restricted keys are denied (an
	// arbitrary file_path under the KB owner tenant cannot be bounded to a
	// key's allow-list), while full-access and tenant-wide retrieve keys pass
	// — KBAccessRead still confines them to KBs they may read (own /
	// org-shared / agent-visible), exactly as it does for a JWT Viewer. The
	// route is declared to the gate with the retrieve policy so it is reachable
	// at all; AllowFileServeAPIKey then applies the stricter not-KB-restricted
	// constraint.
	g.apiKeyRoute(r, http.MethodGet, "/knowledge-bases/:id/files",
		apiKeyRetrieve(apiKeyFullAccess()),
		middleware.AllowFileServeAPIKey(),
		g.Viewer(),
		g.KBAccessRead("id"),
		newKBScopedFileServeHandlerWithResources(
			tenantService,
			globalFileService,
			storageResolver,
			firstResourceCatalog(resourceCatalogs),
		),
	)
}

// newKBScopedFileServeHandler builds the handler backing serveKBScopedFiles.
// The effective (owner) tenant is taken from the request context, which
// RequireKBAccess has already rewritten to the KB's source tenant. The owner
// tenant's storage config is loaded via TenantService so the file is fetched
// from the backend that actually holds it — the caller's own storage config is
// irrelevant here.
func newKBScopedFileServeHandler(
	tenantService interfaces.TenantService,
	globalFileService interfaces.FileService,
	resolvers ...interfaces.StorageBackendResolver,
) gin.HandlerFunc {
	var storageResolver interfaces.StorageBackendResolver
	if len(resolvers) > 0 {
		storageResolver = resolvers[0]
	}
	return newKBScopedFileServeHandlerWithResources(tenantService, globalFileService, storageResolver, nil)
}

func firstResourceCatalog(catalogs []interfaces.ResourceCatalog) interfaces.ResourceCatalog {
	if len(catalogs) == 0 {
		return nil
	}
	return catalogs[0]
}

func newKBScopedFileServeHandlerWithResources(
	tenantService interfaces.TenantService,
	globalFileService interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	resourceCatalog interfaces.ResourceCatalog,
) gin.HandlerFunc {
	absDir := localStorageAbsDir()

	return func(c *gin.Context) {
		ctx := c.Request.Context()

		filePath, ok := requireFilePathQuery(c)
		if !ok {
			return
		}

		// RequireKBAccess rewrote the request context tenant ID to the KB's
		// owner (source) tenant for shared KBs; for own KBs it equals the
		// caller's tenant. Either way it is the tenant that owns this KB's
		// storage objects, so the requested path must belong to it.
		ownerTenantID, ok := types.TenantIDFromContext(ctx)
		if !ok || ownerTenantID == 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: workspace context missing"})
			return
		}
		filePath, _, ok = resolveCatalogResource(c, resourceCatalog, filePath, ownerTenantID)
		if !ok {
			return
		}

		if err := secutils.ValidateKBScopedStoragePath(filePath, ownerTenantID); err != nil {
			logger.Warnf(ctx, "[Router] /knowledge-bases/:id/files denied path not allowed for KB proxy: owner_tenant_id=%d file_path=%q err=%v",
				ownerTenantID, filePath, err)
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: file path not accessible"})
			return
		}

		tenant, err := tenantService.GetTenantByID(ctx, ownerTenantID)
		if err != nil || tenant == nil {
			logger.Warnf(ctx, "[Router] /knowledge-bases/:id/files owner tenant lookup failed: owner_tenant_id=%d err=%v",
				ownerTenantID, err)
			c.Status(http.StatusNotFound)
			return
		}

		backendID, provider := parseStorageTarget(filePath)
		fileSvc, resolvedProvider, ok := resolveTenantFileServiceWithFallback(
			ctx, "/knowledge-bases/:id/files", tenant, backendID, provider, absDir, storageResolver, globalFileService)
		if !ok {
			c.Status(http.StatusBadRequest)
			return
		}

		reader, err := fileSvc.GetFile(ctx, filePath)
		if err != nil {
			logger.Warnf(ctx, "[Router] /knowledge-bases/:id/files get file failed: owner_tenant_id=%d provider=%s path=%q err=%v",
				ownerTenantID, resolvedProvider, filePath, err)
			c.Status(http.StatusNotFound)
			return
		}

		contentType, inline := secutils.SafeContentTypeByFilename(filePath)
		// Cross-tenant shared content — keep it private so shared proxies /
		// CDNs do not cache one tenant's view for another.
		streamStoredFile(c, reader, contentType, inline, "private, max-age=86400", "/knowledge-bases/:id/files")
	}
}

// newMessageScopedFileServeHandler serves resources rendered inside one
// assistant message. The message service first proves that the caller owns the
// containing session. For cross-workspace resources we then require the
// message's agent to still be shared from the resource-owning workspace.
//
// The owner tenant comes from the resource registry whenever possible. This
// also keeps old messages (written before agent_tenant_id was populated)
// readable without accepting a client-provided source workspace ID.
func newMessageScopedFileServeHandler(
	messageService messageFileLookup,
	agentShareService sharedAgentFileLookup,
	tenantService interfaces.TenantService,
	globalFileService interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	resourceCatalog interfaces.ResourceCatalog,
) gin.HandlerFunc {
	absDir := localStorageAbsDir()

	return func(c *gin.Context) {
		ctx := c.Request.Context()
		filePath, ok := requireFilePathQuery(c)
		if !ok {
			return
		}

		callerTenantID, ok := types.TenantIDFromContext(ctx)
		if !ok || callerTenantID == 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: workspace context missing"})
			return
		}
		if messageService == nil {
			c.Status(http.StatusNotFound)
			return
		}

		message, err := messageService.GetMessage(ctx, c.Param("id"), c.Param("message_id"))
		if err != nil || message == nil {
			// Do not reveal whether a message exists outside the caller's session.
			c.Status(http.StatusNotFound)
			return
		}

		resolvedPath := filePath
		var resource *types.StoredResource
		if resourceCatalog != nil {
			resolvedPath, resource, err = resourceCatalog.ResolvePath(ctx, filePath)
			if err != nil {
				c.Status(http.StatusNotFound)
				return
			}
		}

		ownerTenantID := message.AgentTenantID
		if resource != nil {
			ownerTenantID = resource.TenantID
			// A modern message records its source tenant. A resource from any
			// other tenant cannot be smuggled through that message even if the
			// caller happens to have another shared agent with the same ID.
			if message.AgentTenantID != 0 && message.AgentTenantID != ownerTenantID {
				c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: resource not accessible from this message"})
				return
			}
		}
		if ownerTenantID == 0 {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: message resource workspace missing"})
			return
		}

		if ownerTenantID != callerTenantID {
			if message.AgentID == "" || agentShareService == nil {
				c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: shared agent access required"})
				return
			}
			agent, shareErr := agentShareService.GetSharedAgentForTenant(
				ctx,
				callerTenantID,
				types.TenantRoleFromContext(ctx),
				message.AgentID,
				ownerTenantID,
			)
			if shareErr != nil || agent == nil || agent.TenantID != ownerTenantID {
				c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: shared agent access revoked"})
				return
			}
		}

		// Registered resources carry authoritative tenant ownership. Legacy
		// provider paths must still encode the authorized owner tenant.
		if resource == nil {
			if err := secutils.ValidateStoragePathTenant(resolvedPath, ownerTenantID); err != nil {
				logger.Warnf(ctx,
					"[Router] message files denied cross-tenant or invalid path: owner_tenant_id=%d file_path=%q err=%v",
					ownerTenantID, resolvedPath, err)
				c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: file path not accessible"})
				return
			}
		}

		ownerTenant, err := tenantService.GetTenantByID(ctx, ownerTenantID)
		if err != nil || ownerTenant == nil {
			c.Status(http.StatusNotFound)
			return
		}

		backendID, provider := parseStorageTarget(resolvedPath)
		fileSvc, resolvedProvider, ok := resolveTenantFileServiceWithFallback(
			ctx,
			"/sessions/:id/messages/:message_id/files",
			ownerTenant,
			backendID,
			provider,
			absDir,
			storageResolver,
			globalFileService,
		)
		if !ok {
			c.Status(http.StatusBadRequest)
			return
		}

		reader, err := fileSvc.GetFile(ctx, resolvedPath)
		if err != nil {
			logger.Warnf(ctx,
				"[Router] message files get file failed: owner_tenant_id=%d provider=%s path=%q err=%v",
				ownerTenantID, resolvedProvider, resolvedPath, err)
			c.Status(http.StatusNotFound)
			return
		}

		contentType, inline := secutils.SafeContentTypeByFilename(resolvedPath)
		if resource != nil && resource.MimeType != "" && inline {
			contentType = resource.MimeType
		}
		streamStoredFile(c, reader, contentType, inline, "private, max-age=86400", "message files")
	}
}

// serveMessageScopedFiles registers the authenticated proxy used by the chat
// renderer for assistant-message resources. The chat API-key capability is
// sufficient because GetMessage enforces ownership of the API key's session.
func serveMessageScopedFiles(
	r *gin.RouterGroup,
	g *rbacGuards,
	messageService interfaces.MessageService,
	agentShareService interfaces.AgentShareService,
	tenantService interfaces.TenantService,
	globalFileService interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	resourceCatalog interfaces.ResourceCatalog,
) {
	g.apiKeyRoute(
		r,
		http.MethodGet,
		"/sessions/:id/messages/:message_id/files",
		apiKeyChat(apiKeyFullAccess()),
		g.Viewer(),
		newMessageScopedFileServeHandler(
			messageService,
			agentShareService,
			tenantService,
			globalFileService,
			storageResolver,
			resourceCatalog,
		),
	)
}

// servePresignedFiles serves files via HMAC-signed URLs without requiring authentication.
// This is used by IM channels to serve images that are embedded in bot replies.
//
// Routes:
//   - GET  /api/v1/files/presigned?file_path=<provider://...>&tenant_id=<id>&expires=<unix>&sig=<hmac>
//   - HEAD /api/v1/files/presigned?...  (IM platforms issue HEAD first to validate
//     Content-Type / Content-Length before rendering image previews; HEAD must
//     succeed or the inline image renders as broken)
//
// Failure paths log client IP + User-Agent + (truncated) file_path so operators
// can correlate an IM platform's fetch against the upstream signing log line.
// Without this it is otherwise impossible to tell whether a "broken image" is
// caused by an expired signature, a stale URL cached by the platform, the
// platform's IP being blocked, or the URL simply never reaching us.
func servePresignedFiles(r *gin.Engine, tenantService interfaces.TenantService, storageResolver interfaces.StorageBackendResolver) {
	handler := presignedFileHandler(tenantService, localStorageAbsDir(), storageResolver)
	r.GET("/api/v1/files/presigned", handler)
	r.HEAD("/api/v1/files/presigned", handler)
}

// presignedFileHandler returns the shared Gin handler used by both GET and HEAD.
// For HEAD requests it returns the same status + headers but does not stream
// the body — this is enough for IM platforms to validate the URL while saving
// us a full read of the backing object.
func presignedFileHandler(tenantService interfaces.TenantService, absDir string, resolvers ...interfaces.StorageBackendResolver) gin.HandlerFunc {
	var storageResolver interfaces.StorageBackendResolver
	if len(resolvers) > 0 {
		storageResolver = resolvers[0]
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		clientIP := c.ClientIP()
		userAgent := c.Request.UserAgent()

		filePath := strings.TrimSpace(c.Query("file_path"))
		tenantIDStr := strings.TrimSpace(c.Query("tenant_id"))
		expiresStr := strings.TrimSpace(c.Query("expires"))
		sig := strings.TrimSpace(c.Query("sig"))

		if filePath == "" || tenantIDStr == "" || expiresStr == "" || sig == "" {
			logger.Warnf(ctx, "[Router] /files/presigned missing params: client_ip=%s ua=%q file_path=%q tenant_id=%q expires=%q has_sig=%v",
				clientIP, userAgent, filePath, tenantIDStr, expiresStr, sig != "")
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing required parameters"})
			return
		}
		if strings.Contains(filePath, "..") {
			logger.Warnf(ctx, "[Router] /files/presigned rejected path traversal: client_ip=%s file_path=%q", clientIP, filePath)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path"})
			return
		}

		tenantID, err := strconv.ParseUint(tenantIDStr, 10, 64)
		if err != nil {
			logger.Warnf(ctx, "[Router] /files/presigned invalid tenant_id: client_ip=%s tenant_id=%q err=%v", clientIP, tenantIDStr, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant_id"})
			return
		}

		// Verify HMAC signature and expiry. Logged at Warn because every 403
		// here is a signal worth investigating: either the URL was tampered
		// with, the IM platform cached an expired URL, or SYSTEM_AES_KEY was
		// rotated without invalidating in-flight links.
		if !secutils.VerifyFileURLSig(filePath, tenantID, expiresStr, sig) {
			logger.Warnf(ctx, "[Router] /files/presigned sig invalid or expired: client_ip=%s ua=%q tenant_id=%d file_path=%q expires=%s",
				clientIP, userAgent, tenantID, filePath, expiresStr)
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired signature"})
			return
		}

		tenant, err := tenantService.GetTenantByID(ctx, tenantID)
		if err != nil {
			logger.Warnf(ctx, "[Router] /files/presigned tenant lookup failed: client_ip=%s tenant_id=%d err=%v", clientIP, tenantID, err)
			c.Status(http.StatusNotFound)
			return
		}

		backendID, provider := parseStorageTarget(filePath)
		fileSvc, resolvedProvider, err := resolveFileService(ctx, tenant, backendID, provider, absDir, storageResolver)
		if err != nil {
			logger.Warnf(ctx, "[Router] /files/presigned resolve file service failed: client_ip=%s tenant_id=%d provider=%s err=%v",
				clientIP, tenantID, provider, err)
			c.Status(http.StatusBadRequest)
			return
		}

		// HEAD short-circuits the body read inside streamStoredFile. We still
		// need to confirm the object exists via GetFile: skipping it entirely
		// for HEAD would risk reporting 200 for a signed URL that no longer
		// points at a real object, making subsequent GETs from the same client
		// mysteriously fail.
		reader, err := fileSvc.GetFile(ctx, filePath)
		if err != nil {
			logger.Warnf(ctx, "[Router] /files/presigned get file failed: client_ip=%s tenant_id=%d provider=%s path=%q err=%v",
				clientIP, tenantID, resolvedProvider, filePath, err)
			c.Status(http.StatusNotFound)
			return
		}

		contentType, inline := secutils.SafeContentTypeByFilename(filePath)
		streamStoredFile(c, reader, contentType, inline, "public, max-age=86400", "/files/presigned")
	}
}

// servePresignedPreview registers an Admin-only diagnostic endpoint that
// returns the presigned HTTP URL that *would be* generated for a given
// storage path by the calling tenant's current storage config — exactly the
// URL an IM channel would embed in a reply. Operators can paste the result
// into a 4G/mobile browser to verify public reachability without having to
// send a real message through an IM bot.
//
// Route:
//   - GET /api/v1/files/presigned-preview?file_path=<provider://...>
func servePresignedPreview(r *gin.Engine, cfg *config.Config, storageResolver interfaces.StorageBackendResolver) {
	absDir := localStorageAbsDir()

	// This route is registered on the engine root, NOT the /api/v1 group,
	// so the APIKeyGate never runs for it. RequireRole short-circuits
	// API-key principals (deferring to that absent gate), which would let
	// any valid key past the Admin check. Deny API keys explicitly first.
	r.GET("/api/v1/files/presigned-preview",
		middleware.DenyAPIKeyPrincipal(),
		middleware.RequireRole(types.TenantRoleAdmin, cfg),
		func(c *gin.Context) {
			ctx := c.Request.Context()
			filePath, ok := requireFilePathQuery(c)
			if !ok {
				return
			}

			tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
			if tenant == nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: workspace context missing"})
				return
			}

			backendID, provider := parseStorageTarget(filePath)
			fileSvc, resolvedProvider, err := resolveFileService(ctx, tenant, backendID, provider, absDir, storageResolver)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":    err.Error(),
					"provider": provider,
					"hint":     "workspace storage config is missing or incomplete for this provider",
				})
				return
			}

			httpURL, err := fileSvc.GetFileURL(ctx, filePath)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":    err.Error(),
					"provider": resolvedProvider,
					"hint":     "GetFileURL failed; for local storage this usually means APP_EXTERNAL_URL is unset",
				})
				return
			}

			// Detect the "no-op" case where local storage falls back to the
			// provider:// path because APP_EXTERNAL_URL is missing. Surfacing
			// this explicitly is the whole point of the endpoint.
			rewritten := httpURL != filePath
			hint := ""
			if !rewritten {
				hint = "URL unchanged; for local storage set APP_EXTERNAL_URL to enable presigned HTTP URLs"
			}

			c.JSON(http.StatusOK, gin.H{
				"file_path": filePath,
				"provider":  resolvedProvider,
				"url":       httpURL,
				"rewritten": rewritten,
				"hint":      hint,
			})
		})
}
