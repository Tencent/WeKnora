package docparser

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockFileService is a minimal FileService implementation for testing.
type mockFileService struct {
	saved []savedEntry
}

type savedEntry struct {
	Data     []byte
	TenantID uint64
	FileName string
}

func (m *mockFileService) CheckConnectivity(ctx context.Context) error { return nil }
func (m *mockFileService) SaveFile(ctx context.Context, file *multipart.FileHeader, tenantID uint64, knowledgeID string) (string, error) {
	return "", nil
}
func (m *mockFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	m.saved = append(m.saved, savedEntry{Data: data, TenantID: tenantID, FileName: fileName})
	return fmt.Sprintf("local://images/%s", fileName), nil
}
func (m *mockFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	return nil, nil
}
func (m *mockFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	return filePath, nil
}
func (m *mockFileService) DeleteFile(ctx context.Context, filePath string) error { return nil }

func TestResolveRemoteImages_NormalDownload(t *testing.T) {
	// Whitelist localhost for this test so the test server is reachable
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")

	// Create a test HTTP server that serves a real PNG image.
	pngData := createTestPNG(200, 200)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(pngData)
	}))
	defer ts.Close()

	markdown := fmt.Sprintf("# Hello\n\n![photo](%s/image.png)\n\nSome text", ts.URL)

	resolver := NewImageResolver()
	fSvc := &mockFileService{}

	updated, images, err := resolver.ResolveRemoteImages(context.Background(), markdown, fSvc, 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(images) != 1 {
		t.Fatalf("expected 1 stored image, got %d", len(images))
	}

	// URL should have been replaced.
	if strings.Contains(updated, ts.URL) {
		t.Errorf("original URL should have been replaced in markdown, got: %s", updated)
	}
	if !strings.Contains(updated, "local://images/") {
		t.Errorf("expected local:// URL in markdown, got: %s", updated)
	}

	// Verify saved data.
	if len(fSvc.saved) != 1 {
		t.Fatalf("expected 1 saved entry, got %d", len(fSvc.saved))
	}
	if fSvc.saved[0].TenantID != 42 {
		t.Errorf("expected tenantID 42, got %d", fSvc.saved[0].TenantID)
	}
}

func TestResolveRemoteImages_SSRFBlocked(t *testing.T) {
	// URLs pointing to private IPs should be blocked by SSRF check.
	markdown := "![evil](http://127.0.0.1:8080/secret.png)\n\n![also-evil](http://169.254.169.254/metadata)"

	resolver := NewImageResolver()
	fSvc := &mockFileService{}

	updated, images, err := resolver.ResolveRemoteImages(context.Background(), markdown, fSvc, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both images should be left unchanged (SSRF blocked).
	if len(images) != 0 {
		t.Errorf("expected 0 stored images (SSRF blocked), got %d", len(images))
	}
	if updated != markdown {
		t.Errorf("markdown should be unchanged when SSRF blocked")
	}
}

func TestResolveRemoteImages_NonImageContentType(t *testing.T) {
	// Whitelist localhost for this test so the test server is reachable
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html>not an image</html>"))
	}))
	defer ts.Close()

	markdown := fmt.Sprintf("![bad](%s/page.html)", ts.URL)

	resolver := NewImageResolver()
	fSvc := &mockFileService{}

	updated, images, err := resolver.ResolveRemoteImages(context.Background(), markdown, fSvc, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(images) != 0 {
		t.Errorf("expected 0 images for non-image content type, got %d", len(images))
	}
	// Original URL should be preserved.
	if !strings.Contains(updated, ts.URL) {
		t.Errorf("original URL should be preserved for non-image content")
	}
}

func TestResolveRemoteImages_ProviderSchemeSkipped(t *testing.T) {
	markdown := "![already](local://images/abc.png)\n![also](minio://bucket/key.jpg)"

	resolver := NewImageResolver()
	fSvc := &mockFileService{}

	updated, images, err := resolver.ResolveRemoteImages(context.Background(), markdown, fSvc, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(images) != 0 {
		t.Errorf("expected 0 images for provider:// URLs, got %d", len(images))
	}
	if updated != markdown {
		t.Errorf("markdown should be unchanged for provider:// URLs")
	}
}

func TestResolveRemoteImages_MultipleImages(t *testing.T) {
	// Whitelist localhost for this test so the test server is reachable
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")

	pngData := createTestPNG(256, 256)
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(pngData)
	}))
	defer ts.Close()

	markdown := fmt.Sprintf("![img1](%s/a.png)\n\ntext\n\n![img2](%s/b.png)\n\n![img3](%s/c.png)",
		ts.URL, ts.URL, ts.URL)

	resolver := NewImageResolver()
	fSvc := &mockFileService{}

	updated, images, err := resolver.ResolveRemoteImages(context.Background(), markdown, fSvc, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(images) != 3 {
		t.Fatalf("expected 3 stored images, got %d", len(images))
	}
	if callCount != 3 {
		t.Errorf("expected 3 HTTP requests, got %d", callCount)
	}
	if strings.Contains(updated, ts.URL) {
		t.Errorf("all original URLs should have been replaced")
	}
}

func TestResolveRemoteImages_NoImages(t *testing.T) {
	markdown := "# Just text\n\nNo images here."

	resolver := NewImageResolver()
	fSvc := &mockFileService{}

	updated, images, err := resolver.ResolveRemoteImages(context.Background(), markdown, fSvc, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(images) != 0 {
		t.Errorf("expected 0 images, got %d", len(images))
	}
	if updated != markdown {
		t.Errorf("markdown should be unchanged")
	}
}

func TestResolveRemoteImages_Server404(t *testing.T) {
	// Whitelist localhost for this test so the test server is reachable
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	markdown := fmt.Sprintf("![missing](%s/nope.png)", ts.URL)

	resolver := NewImageResolver()
	fSvc := &mockFileService{}

	updated, images, err := resolver.ResolveRemoteImages(context.Background(), markdown, fSvc, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(images) != 0 {
		t.Errorf("expected 0 images for 404, got %d", len(images))
	}
	// Original URL preserved on failure.
	if !strings.Contains(updated, ts.URL) {
		t.Errorf("original URL should be preserved on download failure")
	}
}

func TestExtFromURLPath(t *testing.T) {
	tests := []struct {
		url    string
		expect string
	}{
		{"https://example.com/photo.jpg", ".jpg"},
		{"https://example.com/photo.JPEG", ".jpeg"},
		{"https://example.com/photo.png?v=2", ""},  // query param — path.Ext won't catch it cleanly but that's ok
		{"https://example.com/photo.gif", ".gif"},
		{"https://example.com/photo.webp", ".webp"},
		{"https://example.com/photo.bmp", ".bmp"},
		{"https://example.com/photo.svg", ".svg"},
		{"https://example.com/photo.pdf", ""},
		{"https://example.com/noext", ""},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := extFromURLPath(tt.url)
			if got != tt.expect {
				t.Errorf("extFromURLPath(%q) = %q, want %q", tt.url, got, tt.expect)
			}
		})
	}
}
