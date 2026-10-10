package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
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

func (c *captureSaveBytes) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", nil
}

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

// assertLeadingImagesWinBudget verifies the inline resolver contract: images
// come back in document order, every URL is substituted into the markdown,
// and the leading maxInlineImagesForMultimodal join AI understanding while
// the rest are stored but skipped.
func assertLeadingImagesWinBudget(t *testing.T, out string, images []StoredImage) {
	t.Helper()
	prevPos := -1
	for i, stored := range images {
		pos := strings.Index(out, stored.ServingURL)
		if pos < 0 {
			t.Fatalf("stored URL %q missing from markdown", stored.ServingURL)
		}
		if pos <= prevPos {
			t.Fatalf(
				"stored URL %q is out of document order (pos %d after %d)",
				stored.ServingURL, pos, prevPos,
			)
		}
		prevPos = pos
		if wantSkip := i >= maxInlineImagesForMultimodal; stored.SkipMultimodal != wantSkip {
			t.Fatalf("image %d SkipMultimodal=%v, want %v", i, stored.SkipMultimodal, wantSkip)
		}
	}
}

func TestInlineImageStorageAppliesMultimodalBudgetInDocumentOrder(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	image := `<img src="data:image/png;base64,` + b64 + `">`
	for _, count := range []int{30, 31, 81} {
		t.Run(fmt.Sprintf("%d images", count), func(t *testing.T) {
			svc := &captureSaveBytes{}
			out, images, err := NewImageResolver().ResolveHTMLDataURIImages(
				context.Background(), strings.Repeat(image+"\n", count), svc, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(images) != count || len(svc.saved) != count {
				t.Fatalf("images=%d saved=%d, want %d", len(images), len(svc.saved), count)
			}
			if HasUnresolvedInlineImagePayload(out) {
				t.Fatal("inline image payload remained after storage")
			}
			if got := strings.Count(out, "local://test/"); got != count {
				t.Fatalf("stored URL count=%d, want %d", got, count)
			}
			if len(uniqueStrings(imgServingURLs(images))) != count {
				t.Fatalf(
					"stored URLs are not unique: got %d, want %d",
					len(uniqueStrings(imgServingURLs(images))), count,
				)
			}
			for _, stored := range images {
				if !stored.Inline {
					t.Fatal("HTML data URI image was not marked inline")
				}
			}
			assertLeadingImagesWinBudget(t, out, images)
		})
	}
}

func TestResolveAndStoreKeepsMarkdownAndHTMLImageBudgetsIndependent(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	markdownImage := `![](data:image/png;base64,` + b64 + `)`
	htmlImage := `<img src="data:image/png;base64,` + b64 + `">`
	content := strings.Repeat(markdownImage+"\n", 31) + strings.Repeat(htmlImage+"\n", 31)
	svc := &captureSaveBytes{}

	updated, images, err := NewImageResolver().ResolveAndStore(
		context.Background(),
		&types.ReadResult{MarkdownContent: content},
		svc,
		1,
	)
	if err != nil {
		t.Fatalf("ResolveAndStore: %v", err)
	}
	if len(images) != 62 || len(svc.saved) != 62 {
		t.Fatalf("images=%d saved=%d, want 62 each", len(images), len(svc.saved))
	}
	if HasUnresolvedInlineImagePayload(updated) {
		t.Fatal("inline image payload remained after ResolveAndStore")
	}
	markdownEligible, htmlEligible := 0, 0
	var markdownImages, htmlImages []StoredImage
	for _, stored := range images {
		if stored.OriginalRef == "html-img-data-uri" {
			htmlImages = append(htmlImages, stored)
		} else {
			markdownImages = append(markdownImages, stored)
		}
		if stored.SkipMultimodal {
			continue
		}
		if stored.OriginalRef == "html-img-data-uri" {
			htmlEligible++
		} else {
			markdownEligible++
		}
	}
	if markdownEligible != 30 || htmlEligible != 30 {
		t.Fatalf("multimodal eligible: markdown=%d html=%d, want 30 each", markdownEligible, htmlEligible)
	}
	// Each syntax's images come back in document order and hand their own
	// budget to their own leading images.
	assertLeadingImagesWinBudget(t, updated, markdownImages)
	assertLeadingImagesWinBudget(t, updated, htmlImages)
}

func TestResolveBareBase64ContentBudgetGoesToLeadingImages(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	svc := &captureSaveBytes{}
	out, images, err := NewImageResolver().ResolveBareBase64Content(
		context.Background(), strings.Repeat("data:image/png;base64,"+b64+"\n", 81), svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 81 || len(svc.saved) != 81 {
		t.Fatalf("images=%d saved=%d, want 81 each", len(images), len(svc.saved))
	}
	assertLeadingImagesWinBudget(t, out, images)
}

func TestResolveBareBase64PrefixBudgetGoesToLeadingImages(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	svc := &captureSaveBytes{}
	out, images, err := NewImageResolver().ResolveBareBase64Content(
		context.Background(), strings.Repeat("base64,"+b64+"\n", 81), svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 81 || len(svc.saved) != 81 {
		t.Fatalf("images=%d saved=%d, want 81 each", len(images), len(svc.saved))
	}
	assertLeadingImagesWinBudget(t, out, images)
}

type failAfterInlineSave struct {
	captureSaveBytes
	failAt             int
	calls              int
	deleted            []string
	deleteContextError error
	deleteHasDeadline  bool
	cancelOnFailure    context.CancelFunc
}

func (f *failAfterInlineSave) SaveBytes(
	ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool,
) (string, error) {
	f.calls++
	if f.calls == f.failAt {
		if f.cancelOnFailure != nil {
			f.cancelOnFailure()
		}
		return "", errors.New("temporary storage failure")
	}
	return f.captureSaveBytes.SaveBytes(ctx, data, tenantID, fileName, temp)
}

func (f *failAfterInlineSave) DeleteFile(ctx context.Context, path string) error {
	f.deleted = append(f.deleted, path)
	f.deleteContextError = ctx.Err()
	_, f.deleteHasDeadline = ctx.Deadline()
	return nil
}

func TestResolveAndStoreRollsBackPartialInlineImageSaves(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	image := `<img src="data:image/png;base64,` + b64 + `">`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := &failAfterInlineSave{failAt: 3, cancelOnFailure: cancel}

	_, images, err := NewImageResolver().ResolveAndStore(
		ctx,
		&types.ReadResult{MarkdownContent: strings.Repeat(image+"\n", 4)},
		svc,
		1,
	)

	if err == nil || !strings.Contains(err.Error(), "temporary storage failure") {
		t.Fatalf("err=%v, want temporary storage failure", err)
	}
	if len(images) != 2 || len(svc.saved) != 2 {
		t.Fatalf("returned images=%d saved=%d, want two successful partial saves", len(images), len(svc.saved))
	}
	if len(svc.deleted) != 2 {
		t.Fatalf("deleted=%v, want exactly the two objects saved during this attempt", svc.deleted)
	}
	if svc.deleteContextError != nil {
		t.Errorf("rollback context error = %v, want nil after request cancellation", svc.deleteContextError)
	}
	if !svc.deleteHasDeadline {
		t.Error("rollback context has no timeout deadline")
	}
	for _, savedURL := range svc.urls {
		if !containsString(svc.deleted, savedURL) {
			t.Errorf("newly saved object %q was not rolled back", savedURL)
		}
	}
}

func imgServingURLs(images []StoredImage) []string {
	urls := make([]string, 0, len(images))
	for _, img := range images {
		urls = append(urls, img.ServingURL)
	}
	return urls
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type failingInlineImageSave struct{ captureSaveBytes }

func (f *failingInlineImageSave) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", errors.New("temporary storage failure")
}

func TestResolveHTMLDataURIImagesReturnsStorageFailure(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	markdown := `<img src="data:image/png;base64,` + b64 + `">`
	out, _, err := NewImageResolver().ResolveHTMLDataURIImages(
		context.Background(), markdown, &failingInlineImageSave{}, 1)
	if err == nil || !strings.Contains(err.Error(), "temporary storage failure") {
		t.Fatalf("err=%v, want storage failure", err)
	}
	if !HasUnresolvedInlineImagePayload(out) {
		t.Fatal("storage failure removed the payload before retry")
	}
}

func TestHasUnresolvedInlineImagePayloadIgnoresOrdinaryBase64(t *testing.T) {
	if HasUnresolvedInlineImagePayload("How does base64 work? Try base64,SGVsbG8= as an example.") {
		t.Fatal("ordinary base64 text was treated as an image")
	}
}

func TestHasUnresolvedInlineImagePayloadIgnoresFencedCodeExamples(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	markdown := "```html\n<img src=\"data:image/png;base64," + b64 + "\">\n```"
	if HasUnresolvedInlineImagePayload(markdown) {
		t.Fatal("fenced code example was treated as an unresolved image")
	}
}

func TestInlineImageCodeExamplesAreNeitherResolvedNorDetected(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	for name, markdown := range map[string]string{
		"single-backtick inline code": "Use `data:image/png;base64," + b64 + "` as an example.",
		"multi-backtick inline code":  "Use ``data:image/png;base64," + b64 + "`` as an example.",
		"fenced code":                 "```text\ndata:image/png;base64," + b64 + "\n```",
	} {
		t.Run(name, func(t *testing.T) {
			svc := &captureSaveBytes{}
			out, images, err := NewImageResolver().ResolveBareBase64Content(
				context.Background(), markdown, svc, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(images) != 0 || len(svc.saved) != 0 {
				t.Fatalf("code example saved images=%d, saves=%d", len(images), len(svc.saved))
			}
			if out != markdown {
				t.Fatalf("code example was rewritten: %q", out)
			}
			if HasUnresolvedInlineImagePayload(out) {
				t.Fatal("code example was treated as an unresolved image")
			}
		})
	}
}

func TestResolveAndStoreSkipsImageSyntaxInsideCodeExamples(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	markdown := strings.Join([]string{
		"`![](data:image/png;base64," + b64 + ")`",
		"```html\n<img src=\"data:image/png;base64," + b64 + "\">\n```",
	}, "\n\n")
	svc := &captureSaveBytes{}
	out, images, err := NewImageResolver().ResolveAndStore(
		context.Background(), &types.ReadResult{MarkdownContent: markdown}, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 || len(svc.saved) != 0 {
		t.Fatalf("code examples saved images=%d, saves=%d", len(images), len(svc.saved))
	}
	if out != markdown {
		t.Fatalf("code examples were rewritten: %q", out)
	}
	if HasUnresolvedInlineImagePayload(out) {
		t.Fatal("code example was treated as an unresolved image")
	}
}

func TestHasUnresolvedInlineImagePayloadDetectsInvalidImageDataURI(t *testing.T) {
	markdown := "Actual image: data:image/png;base64,not-valid-image-payload"
	if !HasUnresolvedInlineImagePayload(markdown) {
		t.Fatal("unresolved image data URI was not detected")
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

func TestResolveAndStoreMarkdownImageWithTitle(t *testing.T) {
	png := createTestPNG(200, 150)
	result := &types.ReadResult{
		MarkdownContent: `![图片](images/test.png "图片")`,
		ImageRefs: []types.ImageRef{
			{
				Filename:    "test.png",
				OriginalRef: "images/test.png",
				MimeType:    "image/png",
				ImageData:   png,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image but got %d", len(imgs))
	}
	if len(svc.saved) != 1 {
		t.Fatalf("expected SaveBytes to be called once but got %d", len(svc.saved))
	}
	if !strings.Contains(out, `![图片](local://test/`) || !strings.Contains(out, ` "图片")`) {
		t.Fatalf("markdown image title was not preserved around stored URL: %s", out)
	}
	if strings.Contains(out, "images/test.png") {
		t.Fatalf("original image path was not replaced: %s", out)
	}
}

func TestResolveAndStoreMarkdownImageWithSingleQuotedTitle(t *testing.T) {
	png := createTestPNG(200, 150)
	result := &types.ReadResult{
		MarkdownContent: `![图片](images/test.png '图片')`,
		ImageRefs: []types.ImageRef{
			{
				Filename:    "test.png",
				OriginalRef: "images/test.png",
				MimeType:    "image/png",
				ImageData:   png,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 || len(svc.saved) != 1 {
		t.Fatalf("expected one stored image, got imgs=%d saved=%d", len(imgs), len(svc.saved))
	}
	if !strings.Contains(out, `![图片](local://test/`) || !strings.Contains(out, ` '图片')`) {
		t.Fatalf("markdown image title was not preserved around stored URL: %s", out)
	}
}

func TestResolveAndStoreMarkdownImageWithSpacedFilename(t *testing.T) {
	png := createTestPNG(200, 150)
	result := &types.ReadResult{
		MarkdownContent: `![](images/第 1 页.png)`,
		ImageRefs: []types.ImageRef{
			{
				Filename:    "第 1 页.png",
				OriginalRef: "images/第 1 页.png",
				MimeType:    "image/png",
				ImageData:   png,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 || len(svc.saved) != 1 {
		t.Fatalf("expected one stored image, got imgs=%d saved=%d", len(imgs), len(svc.saved))
	}
	if !strings.Contains(out, `![](local://test/`) {
		t.Fatalf("spaced filename path was not replaced: %s", out)
	}
}

func TestResolveAndStoreMarkdownImageTitleContainingRightParen(t *testing.T) {
	png := createTestPNG(200, 150)
	result := &types.ReadResult{
		MarkdownContent: `![图片](images/test.png "阶段 1) 图片")`,
		ImageRefs: []types.ImageRef{
			{
				Filename:    "test.png",
				OriginalRef: "images/test.png",
				MimeType:    "image/png",
				ImageData:   png,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 || len(svc.saved) != 1 {
		t.Fatalf("expected one stored image, got imgs=%d saved=%d", len(imgs), len(svc.saved))
	}
	if !strings.Contains(out, `![图片](local://test/`) || !strings.Contains(out, ` "阶段 1) 图片")`) {
		t.Fatalf("right-paren title was not preserved around stored URL: %s", out)
	}
}

func TestResolveAndStoreMarkdownImageWithMultilineTitle(t *testing.T) {
	png := createTestPNG(200, 150)
	result := &types.ReadResult{
		MarkdownContent: "![图片](images/test.png\n  \"图片说明\")",
		ImageRefs: []types.ImageRef{
			{
				Filename:    "test.png",
				OriginalRef: "images/test.png",
				MimeType:    "image/png",
				ImageData:   png,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 || len(svc.saved) != 1 {
		t.Fatalf("expected one stored image, got imgs=%d saved=%d", len(imgs), len(svc.saved))
	}
	if !strings.Contains(out, "![图片](local://test/") || !strings.Contains(out, "\n  \"图片说明\")") {
		t.Fatalf("multiline title was not preserved around stored URL: %s", out)
	}
}

func TestResolveAndStoreMarkdownImageWithAngleDestination(t *testing.T) {
	png := createTestPNG(200, 150)
	result := &types.ReadResult{
		MarkdownContent: `![图片](<images/第 1 页 (测试).png> "阶段 1) 图片")`,
		ImageRefs: []types.ImageRef{
			{
				Filename:    "第 1 页 (测试).png",
				OriginalRef: "images/第 1 页 (测试).png",
				MimeType:    "image/png",
				ImageData:   png,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 || len(svc.saved) != 1 {
		t.Fatalf("expected one stored image, got imgs=%d saved=%d", len(imgs), len(svc.saved))
	}
	if !strings.Contains(out, `![图片](<local://test/`) || !strings.Contains(out, `> "阶段 1) 图片")`) {
		t.Fatalf("angle destination wrapper or title was not preserved: %s", out)
	}
}

func TestResolveAndStoreDuplicateReferencesStoreOnce(t *testing.T) {
	png := createTestPNG(200, 150)
	result := &types.ReadResult{
		MarkdownContent: strings.Join([]string{
			`![图片](images/test.png "第一处")`,
			`![图片](images/test.png "第二处")`,
		}, "\n\n"),
		ImageRefs: []types.ImageRef{
			{
				Filename:    "test.png",
				OriginalRef: "images/test.png",
				MimeType:    "image/png",
				ImageData:   png,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(svc.saved) != 1 {
		t.Fatalf("expected duplicate references to save once but got %d", len(svc.saved))
	}
	if len(imgs) != 1 {
		t.Fatalf("expected one stored image record but got %d", len(imgs))
	}
	if strings.Count(out, "local://test/") != 2 {
		t.Fatalf("expected both markdown references to be replaced: %s", out)
	}
	if !strings.Contains(out, `"第一处"`) || !strings.Contains(out, `"第二处"`) {
		t.Fatalf("duplicate reference titles were not preserved: %s", out)
	}
}

func TestResolveAndStoreUnknownReferenceUnchanged(t *testing.T) {
	png := createTestPNG(200, 150)
	result := &types.ReadResult{
		MarkdownContent: `![图片](images/missing.png "图片")`,
		ImageRefs: []types.ImageRef{
			{
				Filename:    "test.png",
				OriginalRef: "images/test.png",
				MimeType:    "image/png",
				ImageData:   png,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(svc.saved) != 0 || len(imgs) != 0 {
		t.Fatalf("unknown reference should not be saved, got imgs=%d saved=%d", len(imgs), len(svc.saved))
	}
	if out != result.MarkdownContent {
		t.Fatalf("unknown reference changed: %s", out)
	}
}

func TestResolveAndStoreSharedMHTMLContract(t *testing.T) {
	type contractImage struct {
		Filename        string `json:"filename"`
		OriginalRef     string `json:"original_ref"`
		MimeType        string `json:"mime_type"`
		ImageDataBase64 string `json:"image_data_base64"`
	}
	var contract struct {
		MarkdownContent string          `json:"markdown_content"`
		Images          []contractImage `json:"images"`
	}

	contractPath := filepath.Join("..", "..", "..", "testdata", "mhtml", "titled-image-contract.json")
	raw, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Images) != 1 {
		t.Fatalf("expected one contract image but got %d", len(contract.Images))
	}

	imageContract := contract.Images[0]
	imageData, err := base64.StdEncoding.DecodeString(imageContract.ImageDataBase64)
	if err != nil {
		t.Fatal(err)
	}
	result := &types.ReadResult{
		MarkdownContent: contract.MarkdownContent,
		ImageRefs: []types.ImageRef{
			{
				Filename:    imageContract.Filename,
				OriginalRef: imageContract.OriginalRef,
				MimeType:    imageContract.MimeType,
				ImageData:   imageData,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(svc.saved) != 1 {
		t.Fatalf("expected SaveBytes once but got %d", len(svc.saved))
	}
	if !bytes.Equal(svc.saved[0], imageData) {
		t.Fatal("stored image bytes do not match contract fixture")
	}
	if len(imgs) != 1 {
		t.Fatalf("expected one stored image record but got %d", len(imgs))
	}
	if imgs[0].OriginalRef != imageContract.OriginalRef || imgs[0].MimeType != imageContract.MimeType {
		t.Fatalf("unexpected stored image metadata: %#v", imgs[0])
	}
	if strings.Contains(out, imageContract.OriginalRef) {
		t.Fatalf("original image path remained in output: %s", out)
	}
	if !strings.Contains(out, `![图片](local://test/`) {
		t.Fatalf("stored URL missing from markdown: %s", out)
	}
	if !strings.Contains(out, `"阶段 1) 图片"`) {
		t.Fatalf("image title was not preserved: %s", out)
	}
	if !strings.Contains(out, "| 赛季制建立 | BP、Rank |\n\n![图片](local://test/") {
		t.Fatalf("table-to-image block boundary changed: %s", out)
	}
	if !strings.Contains(out, `"阶段 1) 图片")`+"\n\n高机动性身法与独特枪械反馈") {
		t.Fatalf("image-to-body block boundary changed: %s", out)
	}
	if !strings.Contains(out, "alpha  \nbeta") {
		t.Fatalf("markdown hard break changed: %s", out)
	}
	if !strings.Contains(out, "```\nline1\n\n\nline2\n```") {
		t.Fatalf("fenced code blank lines changed: %s", out)
	}
}
