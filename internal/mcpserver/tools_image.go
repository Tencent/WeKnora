package mcpserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
)

const maxMCPImageBytes = 10 << 20

var mcpImageMIMETypes = map[string]bool{
	"image/gif":  true,
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

func getImageTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolGetImage,
		mcp.WithDescription(
			"Read one image referenced by a knowledge-base result and return standard MCP image content.",
		),
		mcp.WithString(
			"resource_id",
			mcp.Required(),
			mcp.Description("A resource:// image identifier returned by search_knowledge or read_document"),
		),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

func (s *Server) handleGetImage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	reference, err := req.RequireString("resource_id")
	if err != nil {
		return mcp.NewToolResultError("resource_id is required"), nil
	}
	reference = strings.TrimSpace(reference)
	if _, ok := types.ParseResourcePath(reference); !ok || s.resourceCatalog == nil {
		return mcp.NewToolResultError("image was not found or is outside this endpoint's scope"), nil
	}
	physical, resource, err := s.resourceCatalog.ResolvePath(ctx, reference)
	if err != nil || resource == nil || resource.State != types.ResourceStateActive {
		return mcp.NewToolResultError("image was not found or is outside this endpoint's scope"), nil
	}
	bindings, ok := s.resourceCatalog.(interfaces.KBResourceLookup)
	if !ok {
		return mcp.NewToolResultError("image was not found or is outside this endpoint's scope"), nil
	}
	kbs, err := s.allowedKnowledgeBases(ctx, ep)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to authorize image", err), nil
	}
	authorized := false
	for _, kb := range kbs {
		if kb.TenantID != resource.TenantID {
			continue
		}
		bound, bindErr := bindings.IsReferencedByKnowledgeBase(ctx, resource.TenantID, kb.ID, reference)
		if bindErr != nil {
			return mcp.NewToolResultErrorFromErr("failed to authorize image", bindErr), nil
		}
		if bound {
			authorized = true
			break
		}
	}
	if !authorized {
		return mcp.NewToolResultError("image was not found or is outside this endpoint's scope"), nil
	}
	mimeType := strings.ToLower(strings.TrimSpace(resource.MimeType))
	if !mcpImageMIMETypes[mimeType] {
		return mcp.NewToolResultError("resource is not a supported image (PNG, JPEG, GIF, or WebP)"), nil
	}
	if resource.Size > maxMCPImageBytes {
		return mcp.NewToolResultErrorf("image exceeds the %d MiB MCP limit", maxMCPImageBytes>>20), nil
	}
	if s.tenantService == nil {
		return mcp.NewToolResultError("image storage is unavailable"), nil
	}
	tenant, err := s.tenantService.GetTenantByID(ctx, resource.TenantID)
	if err != nil || tenant == nil {
		return mcp.NewToolResultError("image storage is unavailable"), nil
	}
	backendID, providerPath, scoped := types.ParseStorageBackendPath(physical)
	if !scoped {
		providerPath = physical
	}
	if resource.StorageBackendID != "" {
		backendID = resource.StorageBackendID
	}
	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	if baseDir == "" {
		baseDir = "/data/files"
	}
	absDir, _ := filepath.Abs(baseDir)
	fileService, _, ok := filesvc.ResolveTenantFileServiceWithFallback(
		ctx, "MCP get_image", tenant, backendID, types.ParseProviderScheme(providerPath), absDir,
		s.storageResolver, s.fileService,
	)
	if !ok || fileService == nil {
		return mcp.NewToolResultError("image storage is unavailable"), nil
	}
	reader, err := fileService.GetFile(ctx, physical)
	if err != nil {
		return mcp.NewToolResultError("image could not be read"), nil
	}
	defer func() {
		_ = reader.Close()
	}()
	data, err := io.ReadAll(io.LimitReader(reader, maxMCPImageBytes+1))
	if err != nil {
		return mcp.NewToolResultError("image could not be read"), nil
	}
	if len(data) > maxMCPImageBytes {
		return mcp.NewToolResultErrorf("image exceeds the %d MiB MCP limit", maxMCPImageBytes>>20), nil
	}
	if detected := strings.ToLower(strings.TrimSpace(http.DetectContentType(data))); detected != mimeType {
		return mcp.NewToolResultError("resource content does not match its image MIME type"), nil
	}
	name := strings.TrimSpace(resource.OriginalName)
	if name == "" {
		name = "knowledge-base image"
	}
	return mcp.NewToolResultImage(
		fmt.Sprintf("Image: %s", name), base64.StdEncoding.EncodeToString(data), mimeType,
	), nil
}
