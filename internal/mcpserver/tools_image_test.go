package mcpserver

import (
	"context"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
)

const testImageReference = "resource://AbCdEfGhIjKlMnOpQrStUv"

type stubImageCatalog struct {
	interfaces.ResourceCatalog
	resource *types.StoredResource
	bound    bool
}

func (s *stubImageCatalog) ResolvePath(_ context.Context, value string) (string, *types.StoredResource, error) {
	if value != testImageReference || s.resource == nil {
		return "", nil, nil
	}
	return s.resource.PhysicalPath, s.resource, nil
}

func (s *stubImageCatalog) IsReferencedByKnowledgeBase(
	_ context.Context, tenantID uint64, kbID, reference string,
) (bool, error) {
	return s.bound && s.resource != nil && tenantID == s.resource.TenantID &&
		kbID == "kb-1" && reference == testImageReference, nil
}

type stubImageFileService struct {
	interfaces.FileService
	data  string
	opens int
}

func (s *stubImageFileService) GetFile(_ context.Context, _ string) (io.ReadCloser, error) {
	s.opens++
	return io.NopCloser(strings.NewReader(s.data)), nil
}

func TestGetImageReturnsBoundKnowledgeBaseImage(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	imageBytes := "\x89PNG\r\n\x1a\npayload"
	catalog := &stubImageCatalog{
		bound: true,
		resource: &types.StoredResource{
			TenantID:     1,
			State:        types.ResourceStateActive,
			PhysicalPath: "local://1/images/chart.png",
			OriginalName: "chart.png",
			MimeType:     "image/png",
			Kind:         "image",
		},
	}
	srv := &Server{
		kbService:       &stubKBService{kbs: map[string]*types.KnowledgeBase{"kb-1": {ID: "kb-1", TenantID: 1}}},
		tenantService:   &stubTenantService{tenants: map[uint64]*types.Tenant{1: {ID: 1}}},
		resourceCatalog: catalog,
		fileService:     &stubImageFileService{data: imageBytes},
	}
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, KnowledgeBaseIDs: types.StringArray{"kb-1"}}
	result, err := srv.handleGetImage(mcpCallContext(1, ep), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Arguments: map[string]any{"resource_id": testImageReference},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %#v", result.Content)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content length = %d, want text plus image", len(result.Content))
	}
	image, ok := result.Content[1].(mcp.ImageContent)
	if !ok {
		t.Fatalf("content[1] = %T, want mcp.ImageContent", result.Content[1])
	}
	if image.MIMEType != "image/png" || image.Data != base64.StdEncoding.EncodeToString([]byte(imageBytes)) {
		t.Fatalf("image = %#v", image)
	}
}

func TestGetImageRejectsContentThatDoesNotMatchImageMetadata(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	catalog := &stubImageCatalog{
		bound: true,
		resource: &types.StoredResource{
			TenantID:     1,
			State:        types.ResourceStateActive,
			PhysicalPath: "local://1/images/chart.png",
			OriginalName: "chart.png",
			MimeType:     "image/png",
			Kind:         "image",
		},
	}
	srv := &Server{
		kbService:       &stubKBService{kbs: map[string]*types.KnowledgeBase{"kb-1": {ID: "kb-1", TenantID: 1}}},
		tenantService:   &stubTenantService{tenants: map[uint64]*types.Tenant{1: {ID: 1}}},
		resourceCatalog: catalog,
		fileService:     &stubImageFileService{data: "not-an-image"},
	}
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, KnowledgeBaseIDs: types.StringArray{"kb-1"}}
	result, err := srv.handleGetImage(mcpCallContext(1, ep), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Arguments: map[string]any{"resource_id": testImageReference},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("non-image bytes must be rejected, got %#v", result.Content)
	}
}

func TestGetImageRejectsKnownOversizeImageBeforeOpeningStorage(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	catalog := &stubImageCatalog{
		bound: true,
		resource: &types.StoredResource{
			TenantID:     1,
			State:        types.ResourceStateActive,
			PhysicalPath: "local://1/images/chart.png",
			OriginalName: "chart.png",
			MimeType:     "image/png",
			Kind:         "image",
			Size:         maxMCPImageBytes + 1,
		},
	}
	files := &stubImageFileService{data: "should-not-be-read"}
	srv := &Server{
		kbService:       &stubKBService{kbs: map[string]*types.KnowledgeBase{"kb-1": {ID: "kb-1", TenantID: 1}}},
		tenantService:   &stubTenantService{tenants: map[uint64]*types.Tenant{1: {ID: 1}}},
		resourceCatalog: catalog,
		fileService:     files,
	}
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, KnowledgeBaseIDs: types.StringArray{"kb-1"}}
	result, err := srv.handleGetImage(mcpCallContext(1, ep), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Arguments: map[string]any{"resource_id": testImageReference},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("oversize image must be rejected, got %#v", result.Content)
	}
	if files.opens != 0 {
		t.Fatalf("storage opened %d time(s), want 0", files.opens)
	}
}

func TestGetImageRejectsResourceOutsideEndpointKnowledgeBases(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	catalog := &stubImageCatalog{
		bound: false,
		resource: &types.StoredResource{
			TenantID:     1,
			State:        types.ResourceStateActive,
			PhysicalPath: "local://1/images/chart.png",
			OriginalName: "chart.png",
			MimeType:     "image/png",
			Kind:         "image",
		},
	}
	files := &stubImageFileService{data: "\x89PNG\r\n\x1a\npayload"}
	srv := &Server{
		kbService:       &stubKBService{kbs: map[string]*types.KnowledgeBase{"kb-1": {ID: "kb-1", TenantID: 1}}},
		tenantService:   &stubTenantService{tenants: map[uint64]*types.Tenant{1: {ID: 1}}},
		resourceCatalog: catalog,
		fileService:     files,
	}
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, KnowledgeBaseIDs: types.StringArray{"kb-1"}}
	result, err := srv.handleGetImage(mcpCallContext(1, ep), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Arguments: map[string]any{"resource_id": testImageReference},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("unbound resource must be rejected, got %#v", result.Content)
	}
	if files.opens != 0 {
		t.Fatalf("storage opened %d time(s), want 0", files.opens)
	}
}
