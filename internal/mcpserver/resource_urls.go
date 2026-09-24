package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
)

// rewriteResourceURLs is the outbound boundary shared by the existing MCP
// tools. Like IM, external MCP hosts cannot fetch private storage references.
// Use the common URL rewriter, with an additional per-resource authorization
// gate: a URL mentioned in editable chunk/wiki/answer text is not a grant.
func (s *Server) rewriteResourceURLs(
	ctx context.Context, ep *types.MCPEndpoint, result *mcp.CallToolResult,
) *mcp.CallToolResult {
	if result == nil || result.IsError || s.resourceCatalog == nil {
		return result
	}
	bindings, ok := s.resourceCatalog.(interfaces.KBResourceLookup)
	if !ok {
		return result
	}
	resolver := &resourceURLResolver{
		server: s, ctx: ctx, endpoint: ep, bindings: bindings,
		tenants: make(map[uint64]*types.Tenant),
		files:   make(map[resourceStorageKey]interfaces.FileService),
	}
	rewriter := storageurl.NewRewriter(resolver, "MCP")
	out := *result
	out.Content = append([]mcp.Content(nil), result.Content...)
	for i, content := range out.Content {
		if text, ok := content.(mcp.TextContent); ok {
			text.Text = rewriter.String(ctx, text.Text)
			out.Content[i] = text
		}
	}
	if result.StructuredContent != nil {
		// Agent tools use typed slices/maps (including []askReference and
		// []map[string]string). Normalize the wire copy so CopyData visits all
		// string leaves without mutating service objects or persisted messages.
		data, err := json.Marshal(result.StructuredContent)
		if err == nil {
			var structured map[string]any
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber() // Preserve integer IDs/counters without float64 rounding.
			if err = decoder.Decode(&structured); err == nil {
				out.StructuredContent = rewriter.CopyData(ctx, structured)
			}
		}
	}
	return &out
}

type resourceStorageKey struct {
	tenantID  uint64
	backendID string
	provider  string
}

type resourceURLResolver struct {
	server   *Server
	ctx      context.Context
	endpoint *types.MCPEndpoint
	bindings interfaces.KBResourceLookup
	loaded   bool
	kbs      []*types.KnowledgeBase
	tenants  map[uint64]*types.Tenant
	files    map[resourceStorageKey]interfaces.FileService
}

// ResolveFileService implements storageurl.Resolver. Failure leaves the
// original reference untouched, just as on REST/IM, without failing the answer.
func (r *resourceURLResolver) ResolveFileService(reference string) interfaces.FileService {
	if !r.loaded {
		r.loaded = true
		var err error
		r.kbs, err = r.server.allowedKnowledgeBases(r.ctx, r.endpoint)
		if err != nil {
			logger.Warnf(r.ctx, "[MCP] failed to resolve resource knowledge-base scope: %v", err)
			return nil
		}
	}
	for _, kb := range r.kbs {
		grant, err := r.server.resolveKB(r.ctx, kb, types.OrgRoleViewer)
		if err != nil {
			continue
		}
		file, err := access.ResolveKBFile(
			r.ctx, grant, kb.ID, reference, r.server.resourceCatalog, r.bindings,
		)
		if err != nil {
			continue
		}
		return r.fileService(grant.Context(r.ctx), file)
	}
	return nil
}

func (r *resourceURLResolver) fileService(ctx context.Context, file access.FileAccess) interfaces.FileService {
	tenant := r.tenants[file.OwnerTenantID]
	if tenant == nil {
		tenant, _ = types.TenantInfoFromContext(ctx)
		if tenant == nil || tenant.ID != file.OwnerTenantID {
			if r.server.tenantService == nil {
				return nil
			}
			var err error
			tenant, err = r.server.tenantService.GetTenantByID(ctx, file.OwnerTenantID)
			if err != nil || tenant == nil || tenant.ID != file.OwnerTenantID {
				return nil
			}
		}
		r.tenants[file.OwnerTenantID] = tenant
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	backendID, _, _ := types.ParseStorageBackendPath(file.Path)
	if file.StorageBackendID != "" {
		backendID = file.StorageBackendID
	}
	key := resourceStorageKey{file.OwnerTenantID, backendID, types.ParseProviderScheme(file.Path)}
	if cached := r.files[key]; cached != nil {
		return &resourceURLFileService{FileService: cached, ctx: ctx}
	}
	global := r.server.fileService
	if backendID != "" {
		// A missing or disabled explicit backend must not select an unrelated
		// global bucket just because its provider name happens to match.
		if r.server.storageResolver == nil {
			return nil
		}
		global = nil
	}
	fileService, _, ok := filesvc.ResolveTenantFileServiceWithFallback(
		ctx, "MCP resource URL", tenant, backendID, key.provider, storageurl.LocalStorageBaseDir(),
		r.server.storageResolver, global,
	)
	if !ok || fileService == nil {
		return nil
	}
	// Reuse the catalog's /r/<token> links when APP_EXTERNAL_URL is configured,
	// including when a legacy storage fallback returns a bare provider service.
	fileService = filesvc.NewResourceCatalogFileService(fileService, r.server.resourceCatalog)
	r.files[key] = fileService
	return &resourceURLFileService{FileService: fileService, ctx: ctx}
}

// Carry the authorized owner's context through the shared rewriter, which
// otherwise passes the original endpoint tenant to GetFileURL.
type resourceURLFileService struct {
	interfaces.FileService
	ctx context.Context
}

func (s *resourceURLFileService) GetFileURL(_ context.Context, reference string) (string, error) {
	return s.FileService.GetFileURL(s.ctx, strings.TrimSpace(reference))
}
