package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
)

const maxMCPImageSize = 10 << 20

func getImageTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolGetImage,
		mcp.WithDescription("Read an image from an authorized knowledge base and return standard MCP ImageContent. "+
			"Pass the resource:// handle from search_knowledge or read_document."),
		mcp.WithString("resource_id", mcp.Required(),
			mcp.Description("The resource:// image handle returned in image metadata")),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

func (s *Server) handleGetImage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	resourceID, err := req.RequireString("resource_id")
	if err != nil {
		return mcp.NewToolResultError("resource_id is required"), nil
	}
	if _, ok := types.ParseResourcePath(resourceID); !ok {
		return mcp.NewToolResultError("resource_id must be a canonical resource:// handle"), nil
	}
	if s.resourceCatalog == nil || s.storageResolver == nil {
		return mcp.NewToolResultError("image retrieval is unavailable"), nil
	}

	resource, err := s.resourceCatalog.Resolve(ctx, resourceID)
	if err != nil || resource == nil {
		return mcp.NewToolResultError("image was not found or is outside this endpoint's scope"), nil
	}
	kbs, err := s.allowedKnowledgeBases(ctx, ep)
	if err != nil {
		return mcp.NewToolResultError("failed to resolve endpoint knowledge-base scope"), nil
	}
	bindings, ok := s.resourceCatalog.(interfaces.KBResourceLookup)
	if !ok {
		return mcp.NewToolResultError("image was not found or is outside this endpoint's scope"), nil
	}

	// A resource handle is not an authorization token. Require a live image
	// binding to a KB this endpoint can read, including shared KBs.
	var authorizedKB *types.KnowledgeBase
	for _, kb := range kbs {
		if kb == nil || kb.TenantID != resource.TenantID {
			continue
		}
		grant, grantErr := s.resolveKB(ctx, kb, types.OrgRoleViewer)
		if grantErr != nil {
			continue
		}
		if _, fileErr := access.ResolveKBFile(ctx, grant, kb.ID, resourceID, s.resourceCatalog, bindings); fileErr == nil {
			authorizedKB = kb
			break
		}
	}
	if authorizedKB == nil {
		return mcp.NewToolResultError("image was not found or is outside this endpoint's scope"), nil
	}

	fileCtx, err := s.scopedKBContext(ctx, authorizedKB, types.OrgRoleViewer)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tenant, _ := fileCtx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil && s.tenantService != nil {
		tenant, err = s.tenantService.GetTenantByID(fileCtx, resource.TenantID)
		if err != nil {
			tenant = nil
		}
	}
	if tenant == nil {
		return mcp.NewToolResultError("image storage is unavailable"), nil
	}

	fileSvc, _, err := s.storageResolver.ResolveFileService(
		fileCtx,
		tenant,
		resource.StorageBackendID,
		resource.Provider,
		strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR")),
	)
	if err != nil || fileSvc == nil {
		return mcp.NewToolResultError("image storage is unavailable"), nil
	}
	reader, err := fileSvc.GetFile(fileCtx, resourceID)
	if err != nil || reader == nil {
		return mcp.NewToolResultError("image could not be read"), nil
	}
	defer reader.Close()

	data, err := io.ReadAll(io.LimitReader(reader, maxMCPImageSize+1))
	if err != nil {
		return mcp.NewToolResultError("image could not be read"), nil
	}
	if len(data) > maxMCPImageSize {
		return mcp.NewToolResultError("image exceeds the 10 MiB limit"), nil
	}
	mimeType := supportedMCPImageMIME(data)
	if mimeType == "" {
		return mcp.NewToolResultError("image must be PNG, JPEG, GIF, or WebP"), nil
	}

	return mcp.NewToolResultImage(
		"Image content for "+resourceID,
		base64.StdEncoding.EncodeToString(data),
		mimeType,
	), nil
}

func supportedMCPImageMIME(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case len(data) >= 3 && bytes.Equal(data[:3], []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}
