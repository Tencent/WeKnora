package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// createTestPNG generates a minimal PNG image with the given dimensions.
func createTestPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 128, G: 128, B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestIsIconImage(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		expect bool
	}{
		{
			name:   "tiny bytes (< 512B)",
			data:   make([]byte, 256),
			expect: true,
		},
		{
			name:   "small icon 32x32",
			data:   createTestPNG(32, 32),
			expect: true,
		},
		{
			name:   "small icon 48x48",
			data:   createTestPNG(48, 48),
			expect: true,
		},
		{
			name:   "borderline 64x64",
			data:   createTestPNG(64, 64),
			expect: false,
		},
		{
			name:   "normal image 200x150",
			data:   createTestPNG(200, 150),
			expect: false,
		},
		{
			name:   "wide but short 200x30",
			data:   createTestPNG(200, 30),
			expect: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isIconImage(tt.data)
			if got != tt.expect {
				t.Errorf("isIconImage() = %v, want %v (data len=%d)", got, tt.expect, len(tt.data))
			}
		})
	}
}

// captureSaveBytes implements interfaces.FileService for tests; only SaveBytes records data.
type captureSaveBytes struct {
	saved [][]byte
	urls  []string
}

func (c *captureSaveBytes) CheckConnectivity(context.Context) error { return nil }

func (c *captureSaveBytes) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (c *captureSaveBytes) SaveBytes(_ context.Context, data []byte, _ uint64, fileName string, _ bool) (string, error) {
	c.saved = append(c.saved, append([]byte(nil), data...))
	u := "local://test/" + fileName
	c.urls = append(c.urls, u)
	return u, nil
}

func (c *captureSaveBytes) GetFile(context.Context, string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *captureSaveBytes) GetFileURL(context.Context, string) (string, error) { return "", nil }

func (c *captureSaveBytes) DeleteFile(context.Context, string) error { return nil }

var _ interfaces.FileService = (*captureSaveBytes)(nil)

func TestResolveDataURIImages(t *testing.T) {
	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	md := "pre ![](data:image/png;base64," + b64 + ") post"
	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveDataURIImages(context.Background(), md, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d images", len(imgs))
	}
	if len(svc.saved) != 1 || !bytes.Equal(svc.saved[0], png) {
		t.Fatal("SaveBytes payload mismatch")
	}
	if !strings.Contains(out, "local://test/") || strings.Contains(out, "data:image") {
		t.Fatalf("markdown: %s", out)
	}
}

func TestResolveDataURIImages_CaseInsensitive(t *testing.T) {
	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	md := "![](DATA:image/png;BASE64," + b64 + ")"
	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveDataURIImages(context.Background(), md, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 || !strings.Contains(out, "local://test/") {
		t.Fatalf("imgs=%d out=%q", len(imgs), out)
	}
}

func TestResolveDataURIImages_AltTextWithBracket(t *testing.T) {
	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	md := `pre ![C:\Users\Frank\RichOle\YAJ]7D)SW_W6.png](data:image/png;base64,` + b64 + `) post`
	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveDataURIImages(context.Background(), md, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image but got %d; alt text with ] was not handled", len(imgs))
	}
	if strings.Contains(out, "base64") {
		t.Fatalf("base64 content still present in output: %s", out[:min(200, len(out))])
	}
}

func TestResolveDataURIImages_XEmfMimeType(t *testing.T) {
	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	md := `![](data:image/x-emf;base64,` + b64 + `)`
	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveDataURIImages(context.Background(), md, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image for x-emf mime type but got %d", len(imgs))
	}
	if strings.Contains(out, "data:image") {
		t.Fatalf("data URI still present for x-emf: %s", out[:min(200, len(out))])
	}
}

func TestResolveHTMLDataURIImages(t *testing.T) {
	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	md := `text <img src="data:image/png;base64,` + b64 + `" alt="test"> more`
	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveHTMLDataURIImages(context.Background(), md, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image from HTML img tag but got %d", len(imgs))
	}
	if strings.Contains(out, "<img") || strings.Contains(out, "base64") {
		t.Fatalf("HTML img tag or base64 still present: %s", out[:min(200, len(out))])
	}
	if !strings.Contains(out, "![image](local://test/") {
		t.Fatalf("expected markdown image replacement, got: %s", out[:min(200, len(out))])
	}
}

func TestResolveBareBase64Content_BareDataURI(t *testing.T) {
	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	md := `some text data:image/png;base64,` + b64 + ` more text`
	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveBareBase64Content(context.Background(), md, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image from bare data URI but got %d", len(imgs))
	}
	if strings.Contains(out, "base64") {
		t.Fatalf("base64 content still present: %s", out[:min(200, len(out))])
	}
}

func TestResolveBareBase64Content_InsideBrokenMarkdownRef(t *testing.T) {
	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	// Simulate a broken markdown ref that the primary regex already handled
	// If primary regex handles it, this won't reach the bare handler
	// But test the bare handler's behavior when prev char is '('
	md := `text (data:image/png;base64,` + b64 + `) more`
	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveBareBase64Content(context.Background(), md, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image but got %d", len(imgs))
	}
	if strings.Contains(out, "base64") {
		t.Fatalf("base64 still present: %s", out[:min(200, len(out))])
	}
	// When preceded by '(', should replace with just URL (not wrapped in ![image]())
	if strings.Contains(out, "![image]") {
		t.Fatalf("should not wrap in ![image]() when inside parentheses: %s", out[:min(200, len(out))])
	}
}

func TestResolveAndStore_MultipleFormats(t *testing.T) {
	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	// Markdown with: standard data URI, alt text with ], x-emf mime type
	md := strings.Join([]string{
		`# Document`,
		`![](data:image/png;base64,` + b64 + `)`,
		`![C:\path\file]name.png](data:image/png;base64,` + b64 + `)`,
		`![](data:image/x-emf;base64,` + b64 + `)`,
	}, "\n\n")

	result := &types.ReadResult{MarkdownContent: md}
	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 3 {
		t.Fatalf("expected 3 images but got %d", len(imgs))
	}
	if strings.Contains(out, "base64") {
		t.Fatalf("base64 content still in output after ResolveAndStore")
	}
	if strings.Contains(out, "data:image") {
		t.Fatalf("data URI still in output after ResolveAndStore")
	}
}
