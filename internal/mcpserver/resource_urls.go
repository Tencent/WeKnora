package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/mark3labs/mcp-go/mcp"
)

// resourceBindingLookup is the catalog surface the resolver needs: the
// authoritative per-KB check plus the reverse lookup that names candidate KBs.
type resourceBindingLookup interface {
	interfaces.KBResourceLookup
	interfaces.MessageFileBindingLookup
}

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
	bindings, ok := s.resourceCatalog.(resourceBindingLookup)
	if !ok {
		return result
	}
	resolver := &resourceURLResolver{
		server: s, ctx: ctx, endpoint: ep, bindings: bindings,
		kbs:   make(map[string]*resourceKBScope),
		files: make(map[resourceStorageKey]interfaces.FileService),
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
		out.StructuredContent = rewriteStructuredContent(ctx, rewriter, result.StructuredContent)
	}
	return &out
}

// rewriteStructuredContent rewrites a JSON copy of a tool's structured result.
// Agent tools use typed slices/maps (including []askReference and
// []map[string]string), so the wire form is normalized for the rewriter
// without mutating service objects or persisted messages.
func rewriteStructuredContent(ctx context.Context, rewriter *storageurl.Rewriter, value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		logger.Warnf(ctx, "[MCP] structured result left unrewritten: marshal failed: %v", err)
		return value
	}
	if !storageurl.Pattern.Match(data) {
		return value
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber() // Preserve integer IDs/counters without float64 rounding.
	if err := decoder.Decode(&decoded); err != nil {
		logger.Warnf(ctx, "[MCP] structured result left unrewritten: decode failed: %v", err)
		return value
	}
	return rewriter.CopyValue(ctx, decoded)
}

type resourceStorageKey struct {
	tenantID  uint64
	backendID string
	provider  string
}

// resourceKBScope is one knowledge base's authorization, resolved at most once
// per tool call. A nil grant records a denial.
type resourceKBScope struct {
	grant *access.KBAccess
	ctx   context.Context
}

type resourceURLResolver struct {
	server   *Server
	ctx      context.Context
	endpoint *types.MCPEndpoint
	bindings resourceBindingLookup
	kbs      map[string]*resourceKBScope
	files    map[resourceStorageKey]interfaces.FileService
}

// ResolveFileService implements storageurl.Resolver. Failure leaves the
// original reference untouched, just as on REST/IM, without failing the answer.
//
// The catalog's reverse lookup names the KBs that bind the reference, so the
// cost is independent of how many KBs the endpoint can reach. Each candidate
// must still pass the endpoint scope, the caller's grant and ResolveKBFile.
func (r *resourceURLResolver) ResolveFileService(reference string) interfaces.FileService {
	for _, kbID := range r.boundKnowledgeBases(reference) {
		scope := r.knowledgeBaseScope(kbID)
		if scope.grant == nil {
			continue
		}
		file, err := access.ResolveKBFile(
			r.ctx, scope.grant, kbID, reference, r.server.resourceCatalog, r.bindings,
		)
		if err != nil {
			logResourceAccessError(r.ctx, "authorize", kbID, err)
			continue
		}
		return r.fileService(scope.ctx, file)
	}
	return nil
}

// boundKnowledgeBases returns the live KBs that explicitly bind reference under
// its owner tenant. Legacy provider paths carry their owner in the path.
func (r *resourceURLResolver) boundKnowledgeBases(reference string) []string {
	physical, resource, err := r.server.resourceCatalog.ResolvePath(r.ctx, reference)
	if err != nil {
		return nil
	}
	owner := secutils.ParseTenantIDFromStoragePath(physical)
	if resource != nil {
		owner = resource.TenantID
	}
	if owner == 0 {
		return nil
	}
	origins, err := r.bindings.GetMessageFileBindings(r.ctx, owner, reference, "")
	if err != nil {
		logger.Warnf(r.ctx, "[MCP] failed to look up resource bindings: %v", err)
		return nil
	}
	if origins == nil {
		return nil
	}
	return origins.KnowledgeBaseIDs
}

// knowledgeBaseScope authorizes one KB for this endpoint. A restricted endpoint
// accepts only its allowlist; an unrestricted one accepts any KB the caller can
// view, which covers the shared KBs `ask` reaches through the agent's own KB
// selection. Owned and shared KBs alike go through the same viewer grant.
func (r *resourceURLResolver) knowledgeBaseScope(kbID string) *resourceKBScope {
	if scope, ok := r.kbs[kbID]; ok {
		return scope
	}
	scope := &resourceKBScope{}
	r.kbs[kbID] = scope
	if r.endpoint.RestrictsKnowledgeBases() && !slices.Contains(r.endpoint.KnowledgeBaseIDs, kbID) {
		return scope
	}
	kb, err := r.server.kbService.GetKnowledgeBaseByIDOnly(r.ctx, kbID)
	if err != nil || kb == nil {
		return scope
	}
	grant, ctx, err := r.server.authorizeKB(r.ctx, kb, types.OrgRoleViewer)
	if err != nil {
		logResourceAccessError(r.ctx, "resolve grant", kbID, err)
		return scope
	}
	scope.grant, scope.ctx = grant, ctx
	return scope
}

// logResourceAccessError surfaces infrastructure failures; plain denials are
// expected for references the caller may not see and stay quiet.
func logResourceAccessError(ctx context.Context, step, kbID string, err error) {
	if errors.Is(err, access.ErrForbidden) || errors.Is(err, access.ErrNotFound) ||
		errors.Is(err, access.ErrUnauthorized) {
		return
	}
	logger.Warnf(ctx, "[MCP] resource URL %s failed for knowledge base %s: %v", step, kbID, err)
}

// fileService opens the owner's storage. ctx comes from authorizeKB, so it
// already carries the owner's TenantInfo for shared knowledge bases.
func (r *resourceURLResolver) fileService(ctx context.Context, file access.FileAccess) interfaces.FileService {
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil || tenant.ID != file.OwnerTenantID {
		return nil
	}
	backendID, _, _ := types.ParseStorageBackendPath(file.Path)
	if file.StorageBackendID != "" {
		backendID = file.StorageBackendID
	}
	key := resourceStorageKey{file.OwnerTenantID, backendID, types.ParseProviderScheme(file.Path)}
	fileService := r.files[key]
	if fileService == nil {
		global := r.server.fileService
		if backendID != "" {
			// A missing or disabled explicit backend must not select an unrelated
			// global bucket just because its provider name happens to match.
			if r.server.storageResolver == nil {
				return nil
			}
			global = nil
		}
		// Both the backend resolver and the global service are already wrapped
		// by the resource catalog, which mints /r/<token> links for
		// resource:// handles when APP_EXTERNAL_URL is configured.
		var ok bool
		fileService, _, ok = filesvc.ResolveTenantFileServiceWithFallback(
			ctx, "MCP resource URL", tenant, backendID, key.provider, storageurl.LocalStorageBaseDir(),
			r.server.storageResolver, global,
		)
		if !ok || fileService == nil {
			return nil
		}
		r.files[key] = fileService
	}
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
