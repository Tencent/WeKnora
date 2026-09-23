package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
)

var testMCPImagePNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
	0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
	0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
	0x00, 0x00, 0x02, 0x00, 0x01, 0xE2, 0x21, 0xBC,
	0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
	0x44, 0xAE, 0x42, 0x60, 0x82,
}

type imageResourceCatalogStub struct {
	interfaces.ResourceCatalog
	resource *types.StoredResource
	bound    bool
}

func (s *imageResourceCatalogStub) Resolve(_ context.Context, reference string) (*types.StoredResource, error) {
	if s.resource == nil || reference != types.BuildResourcePath(s.resource.Handle) {
		return nil, errors.New("not found")
	}
	return s.resource, nil
}

func (s *imageResourceCatalogStub) ResolvePath(_ context.Context, reference string) (string, *types.StoredResource, error) {
	resource, err := s.Resolve(context.Background(), reference)
	if err != nil {
		return "", nil, err
	}
	return resource.PhysicalPath, resource, nil
}

func (s *imageResourceCatalogStub) IsReferencedByKnowledgeBase(
	_ context.Context, tenantID uint64, kbID, reference string,
) (bool, error) {
	return s.bound && tenantID == s.resource.TenantID && kbID == "kb-1" &&
		reference == types.BuildResourcePath(s.resource.Handle), nil
}

type imageFileServiceStub struct {
	interfaces.FileService
	data []byte
}

func (s *imageFileServiceStub) GetFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

type imageStorageResolverStub struct {
	interfaces.StorageBackendResolver
	fileService interfaces.FileService
	calls       int
}

func (s *imageStorageResolverStub) ResolveFileService(
	context.Context, *types.Tenant, string, string, string,
) (interfaces.FileService, string, error) {
	s.calls++
	return s.fileService, "local", nil
}

func newImageReadTestServer(data []byte, bound bool) (*Server, context.Context, *imageStorageResolverStub) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, Name: "Docs"}
	srv := newScopeTestServer(kb)
	ep := &types.MCPEndpoint{
		ID: "ep-1", TenantID: 1, KnowledgeBaseIDs: types.StringArray{"kb-1"},
		Tools: types.StringArray{types.MCPEndpointToolGetImage},
	}
	resource := &types.StoredResource{
		ID: "resource-1", Handle: "AbCdEfGhIjKlMnOpQrStUv", TenantID: 1,
		Provider: "local", StorageBackendID: "backend-1",
		PhysicalPath: "local://1/exports/figure.png", MimeType: "image/png",
	}
	catalog := &imageResourceCatalogStub{resource: resource, bound: bound}
	fileService := &imageFileServiceStub{data: data}
	storage := &imageStorageResolverStub{fileService: fileService}
	srv.resourceCatalog = catalog
	srv.storageResolver = storage
	srv.tenantService = &stubTenantService{tenants: map[uint64]*types.Tenant{
		1: {ID: 1, Name: "Workspace"},
	}}
	return srv, mcpCallContext(1, ep), storage
}

func callGetImage(srv *Server, ctx context.Context, resourceID string) (*mcp.CallToolResult, error) {
	return srv.handleGetImage(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"resource_id": resourceID}},
	})
}

func TestHandleGetImageReturnsAuthorizedImageContent(t *testing.T) {
	srv, ctx, storage := newImageReadTestServer(testMCPImagePNG, true)
	resourceID := types.BuildResourcePath("AbCdEfGhIjKlMnOpQrStUv")

	result, err := callGetImage(srv, ctx, resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 2 {
		t.Fatalf("result = %#v, want text and image content", result)
	}
	image, ok := result.Content[1].(mcp.ImageContent)
	if !ok {
		t.Fatalf("content[1] = %T, want mcp.ImageContent", result.Content[1])
	}
	if image.MIMEType != "image/png" || image.Data != base64.StdEncoding.EncodeToString(testMCPImagePNG) {
		t.Fatalf("image content = (%q, %d base64 chars)", image.MIMEType, len(image.Data))
	}
	if storage.calls != 1 {
		t.Fatalf("storage calls = %d, want 1", storage.calls)
	}
}

func TestHandleGetImageRejectsUnboundAndOutOfScopeResources(t *testing.T) {
	tests := []struct {
		name     string
		resource *types.StoredResource
		bound    bool
	}{
		{
			name:  "unbound resource",
			bound: false,
		},
		{
			name: "foreign tenant resource",
			resource: &types.StoredResource{
				ID: "foreign", Handle: "AbCdEfGhIjKlMnOpQrStUv", TenantID: 2,
				Provider: "local", PhysicalPath: "local://2/exports/figure.png",
			},
			bound: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, ctx, storage := newImageReadTestServer(testMCPImagePNG, tc.bound)
			if tc.resource != nil {
				srv.resourceCatalog = &imageResourceCatalogStub{resource: tc.resource, bound: tc.bound}
			}
			result, err := callGetImage(srv, ctx, types.BuildResourcePath("AbCdEfGhIjKlMnOpQrStUv"))
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError {
				t.Fatal("expected an out-of-scope resource error")
			}
			if storage.calls != 0 {
				t.Fatalf("storage calls = %d, want 0", storage.calls)
			}
		})
	}
}

func TestHandleGetImageEnforcesDecodedSizeLimit(t *testing.T) {
	data := append(append([]byte(nil), testMCPImagePNG...), bytes.Repeat([]byte{0}, maxMCPImageSize)...)
	srv, ctx, storage := newImageReadTestServer(data, true)
	result, err := callGetImage(srv, ctx, types.BuildResourcePath("AbCdEfGhIjKlMnOpQrStUv"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || storage.calls != 1 {
		t.Fatalf("oversized result = %#v, storage calls = %d", result, storage.calls)
	}
}

func TestSupportedMCPImageMIME(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n"), "image/png"},
		{"jpeg", []byte{0xff, 0xd8, 0xff}, "image/jpeg"},
		{"gif", []byte("GIF89a"), "image/gif"},
		{"webp", []byte("RIFF----WEBP"), "image/webp"},
		{"unsupported", []byte("not an image"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := supportedMCPImageMIME(tc.data); got != tc.want {
				t.Fatalf("supportedMCPImageMIME() = %q, want %q", got, tc.want)
			}
		})
	}
}
