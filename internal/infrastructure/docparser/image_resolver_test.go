package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
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

func TestHasUnresolvedInlineImagePayloadIgnoresShortProseExample(t *testing.T) {
	markdown := "Actual image: data:image/png;base64,not-valid-image-payload"
	if HasUnresolvedInlineImagePayload(markdown) {
		t.Fatal("ordinary prose must not fail document ingestion")
	}
}

func TestHasUnresolvedInlineImagePayloadDetectsRealImageBytes(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	if !HasUnresolvedInlineImagePayload("data:image/png;base64," + payload) {
		t.Fatal("unprocessed valid image bytes must be detected")
	}
}

func TestSkippedMalformedImageSyntaxIsOmitted(t *testing.T) {
	for _, markdown := range []string{
		"![](data:image/png;base64,not-valid-payload)",
		`<img src="data:image/png;base64,not-valid-payload">`,
	} {
		t.Run(markdown[:min(len(markdown), 20)], func(t *testing.T) {
			out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
				&types.ReadResult{MarkdownContent: markdown}, &captureSaveBytes{}, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(images) != 0 {
				t.Fatalf("stored %d malformed images", len(images))
			}
			if strings.Contains(out, "base64,") {
				t.Fatalf("raw payload leaked: %q", out)
			}
			if !strings.Contains(out, "[image omitted]") {
				t.Fatalf("missing placeholder: %q", out)
			}
		})
	}
}

func TestOversizedInlineImagesAreOmitted(t *testing.T) {
	data := make([]byte, maxRemoteImageSize+1)
	copy(data, []byte("\x89PNG"))
	payload := base64.StdEncoding.EncodeToString(data)

	for _, input := range []string{
		"![](data:image/png;base64," + payload + ")",
		`<img src="data:image/png;base64,` + payload + `">`,
	} {
		out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
			&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(images) != 0 {
			t.Fatalf("oversized image unexpectedly saved: %d", len(images))
		}
		if out != inlineImageOmitted {
			t.Fatalf("expected omitted image placeholder, got %q", out[:min(120, len(out))])
		}
	}
}

func TestWrappedBareDataURIIsResolvedWithoutLeakingPayload(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	var wrapped strings.Builder
	for i := 0; i < len(payload); i += 76 {
		if i > 0 {
			wrapped.WriteByte('\n')
		}
		wrapped.WriteString(payload[i:min(i+76, len(payload))])
	}
	input := "Before\ndata:image/png;base64," + wrapped.String() + "\nAfter"
	out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
		&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || !strings.Contains(out, images[0].ServingURL) {
		t.Fatalf("valid wrapped Base64 should be stored; images=%d", len(images))
	}
	if strings.Contains(out, "base64,") || !strings.Contains(out, "Before") || !strings.Contains(out, "After") {
		t.Fatal("wrapped data URI leaked or surrounding text was lost")
	}
}

func TestBareDataURIKeepsFollowingEnglishParagraphs(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	input := "before data:image/png;base64," + payload +
		"\nDocumentation\nAnotherParagraph\nAfter"

	out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
		&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("expected the valid PNG to be stored, got %d images", len(images))
	}
	for _, text := range []string{"before", "Documentation", "AnotherParagraph", "After", images[0].ServingURL} {
		if !strings.Contains(out, text) {
			t.Errorf("resolved document lost %q: %q", text, out)
		}
	}
	if strings.Contains(out, inlineImageOmitted) || strings.Contains(out, "base64,") {
		t.Fatalf("image should be replaced without dropping adjacent prose: %q", out)
	}
}

func TestTruncatedWrappedImageReturnsErrorWithoutLosingProse(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	for _, prefix := range []string{"data:image/png;base64,", "base64,"} {
		for _, length := range []int{76, 152} {
			encoded := payload[:76]
			if length > 76 {
				encoded += "\n" + payload[76:length]
			}
			input := prefix + encoded + "\nDocumentation\nAnotherParagraph"
			out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
				&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
			if !errors.Is(err, ErrAmbiguousInlineImage) || len(images) != 0 || out != input {
				t.Fatalf("ambiguous image must return an error with original text: images=%d err=%v", len(images), err)
			}
		}
	}
}

func TestMalformedMultilineInlineImagePreservesOriginal(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	for _, prefix := range []string{"data:image/png;base64,", "base64,"} {
		for _, firstLine := range []string{
			encoded[:76], encoded[:76] + "\r", encoded[:76] + " \t", encoded[:64] + "!" + encoded[64:76],
		} {
			input := "[![](data:image/png;base64," + encoded + ")](https://example.com)\n\n" +
				prefix + firstLine + "\n" + encoded[76:152] + "\n" + encoded[152:228] + "\n" + encoded[228:304] +
				"\nPayment terms are net 30 days."
			svc := &failAfterInlineSave{}
			result := &types.ReadResult{MarkdownContent: input}
			out, _, err := NewImageResolver().ResolveAndStore(context.Background(), result, svc, 1)
			if !errors.Is(err, ErrAmbiguousInlineImage) {
				t.Fatalf("expected explicit parse error, got %v", err)
			}
			if out != input || result.MarkdownContent != input {
				t.Fatal("parse failure changed the original content")
			}
			if len(svc.saved) != 1 || len(svc.deleted) != 1 || svc.deleted[0] != svc.urls[0] {
				t.Fatalf("partial upload was not rolled back: saved=%d deleted=%d", len(svc.saved), len(svc.deleted))
			}
		}
	}
}

func TestTruncatedInlineImageBeforeProse(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	for _, prefix := range []string{"data:image/png;base64,", "base64,"} {
		for _, nextLine := range []string{
			"这里是商品管理的重要业务说明。", "# Product management", encoded[76:152],
			encoded[76:152] + " 这里是业务正文", encoded[76:120] + "!" + encoded[120:152],
			"!" + encoded[76:152], "Documentation",
		} {
			input := prefix + encoded[:76] + "\n" + nextLine + "\n这是下一段正常正文。"
			svc := &captureSaveBytes{}
			out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
				&types.ReadResult{MarkdownContent: input}, svc, 1)
			if strings.HasPrefix(nextLine, "这里") || strings.HasPrefix(nextLine, "#") {
				if err != nil || out != inlineImageOmitted+"\n"+nextLine+"\n这是下一段正常正文。" ||
					HasUnresolvedInlineImagePayload(out) {
					t.Fatalf("clear prose must survive image omission: out=%q err=%v", out, err)
				}
			} else if !errors.Is(err, ErrAmbiguousInlineImage) || out != input {
				t.Fatalf("possible encoding must stop ingestion with original text: err=%v", err)
			}
			if len(images) != 0 || len(svc.saved) != 0 {
				t.Fatal("truncated image uploaded")
			}
		}
	}
}

func TestUnknownInlineImageFormat(t *testing.T) {
	for _, syntax := range []string{"bare", "markdown", "html"} {
		for _, valid := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", syntax, valid), func(t *testing.T) {
				data := bytes.Repeat([]byte{0, 1, 2, 3}, 256)
				if valid {
					data = createTestPNG(200, 150)
				}
				input := "data:image/fake;base64," + base64.StdEncoding.EncodeToString(data)
				switch syntax {
				case "markdown":
					input = "![](" + input + ")"
				case "html":
					input = `<img src="` + input + `">`
				}
				svc := &captureSaveBytes{}
				out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
					&types.ReadResult{MarkdownContent: input}, svc, 1)
				if err != nil {
					t.Fatal(err)
				}
				if valid {
					if len(images) != 1 || len(svc.saved) != 1 || !bytes.Equal(svc.saved[0], data) {
						t.Fatal("recognized valid image must remain supported")
					}
				} else if out != inlineImageOmitted || len(images) != 0 || len(svc.saved) != 0 {
					t.Fatal("unrecognized binary content was accepted as an image")
				}
			})
		}
	}
}

func TestWrappedSVGIsStoredWithoutLeakingPayload(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="150"><!--` +
		strings.Repeat("illustration ", 80) + `--><rect width="200" height="150"/></svg>`
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		payload := encoding.EncodeToString([]byte(svg))
		var wrapped strings.Builder
		for i := 0; i < len(payload); i += 76 {
			if i > 0 {
				wrapped.WriteByte('\n')
			}
			wrapped.WriteString(payload[i:min(i+76, len(payload))])
		}
		for _, prefix := range []string{"data:image/svg+xml;base64,", "base64,"} {
			input := prefix + wrapped.String() + "\nDocumentation\nAnotherParagraph"
			out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
				&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(images) != 1 || images[0].MimeType != "image/svg+xml" {
				t.Fatalf("wrapped SVG must be stored, got %v", images)
			}
			if out != "![image]("+images[0].ServingURL+")\nDocumentation\nAnotherParagraph" {
				t.Fatalf("SVG payload leaked or prose changed: %q", out)
			}
			if !HasUnresolvedInlineImagePayload(input) {
				t.Fatal("the guard must detect an unresolved SVG image")
			}
		}
	}
}

func TestOversizedTruncatedWrappedImagePreservesOriginal(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))[:76]
	rows := maxRemoteImageSize/57 + 2 // Each full 76-character row decodes to 57 bytes.
	input := "data:image/png;base64," + payload +
		strings.Repeat("\n"+strings.Repeat("A", 76), rows) + "\nDocumentation"
	out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
		&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
	if !errors.Is(err, ErrAmbiguousInlineImage) || len(images) != 0 || out != input {
		t.Fatalf("oversized ambiguous image must preserve original text: images=%d err=%v", len(images), err)
	}
}

func BenchmarkWrappedPayloadBeyondSizeLimit(b *testing.B) {
	prefix := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))[:76]
	for _, decodedMiB := range []int{11, 22} {
		input := prefix + strings.Repeat("\n"+strings.Repeat("A", 76), decodedMiB*1024*1024/57+1)
		b.Run(fmt.Sprintf("%d_MiB", decodedMiB), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				end, complete, _ := wrappedBase64PayloadEnd(input, 0, 76)
				if complete || end != 76 {
					b.Fatal("oversized truncated payload must not consume uncertain continuation lines")
				}
			}
		})
	}
}

func TestInlineStorageLimitStillRetainsLeadingImageBudget(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	input := strings.Repeat(`<img src="data:image/png;base64,`+payload+`">`+"\n", maxInlineImagesPerDocument+1)
	svc := &captureSaveBytes{}
	out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
		&types.ReadResult{MarkdownContent: input}, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != maxInlineImagesPerDocument || len(svc.saved) != maxInlineImagesPerDocument {
		t.Fatalf("stored=%d saved=%d, want %d", len(images), len(svc.saved), maxInlineImagesPerDocument)
	}
	if strings.Count(out, inlineImageOmitted) != 1 {
		t.Fatalf("expected one omitted image; got %d", strings.Count(out, inlineImageOmitted))
	}
	assertLeadingImagesWinBudget(t, out, images)
}

func TestBareInlineSyntaxesKeepSeparateAIBudgets(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	input := strings.Repeat("data:image/png;base64,"+payload+"\n", 31) +
		strings.Repeat("base64,"+payload+"\n", 31)
	svc := &captureSaveBytes{}
	out, images, err := NewImageResolver().ResolveBareBase64Content(context.Background(), input, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 62 || len(svc.saved) != 62 {
		t.Fatalf("stored=%d saved=%d, want 62", len(images), len(svc.saved))
	}
	var dataURI, prefix []StoredImage
	for _, img := range images {
		switch img.OriginalRef {
		case "bare-data-uri":
			dataURI = append(dataURI, img)
		case "bare-base64":
			prefix = append(prefix, img)
		default:
			t.Fatalf("unexpected image source %q", img.OriginalRef)
		}
	}
	if len(dataURI) != 31 || len(prefix) != 31 {
		t.Fatalf("bare syntax counts=%d/%d, want 31 each", len(dataURI), len(prefix))
	}
	assertLeadingImagesWinBudget(t, out, dataURI)
	assertLeadingImagesWinBudget(t, out, prefix)
}

func TestShortWrappedBareImageDoesNotLeakRemainder(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	var wrapped strings.Builder
	for i := 0; i < len(payload); i += wrappedBase64LineLength {
		if i > 0 {
			wrapped.WriteByte('\n')
		}
		wrapped.WriteString(payload[i:min(i+wrappedBase64LineLength, len(payload))])
	}
	input := "Before\ndata:image/png;base64," + wrapped.String() + "\nAfter"
	out, _, err := NewImageResolver().ResolveBareBase64Content(context.Background(),
		input, &captureSaveBytes{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "base64,") || strings.Contains(out, payload[wrappedBase64LineLength:]) {
		t.Fatalf("short wrapped image leaked into body: %q", out)
	}
	if !strings.Contains(out, "Before") || !strings.Contains(out, "After") {
		t.Fatalf("surrounding text was dropped: %q", out)
	}
}

func TestMalformedBareImageIsRemovedAsOneToken(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	broken := payload[:128] + "!" + payload[128:] + strings.Repeat("A", 4096)
	input := "before data:image/png;base64," + broken + " after"
	out, images, err := NewImageResolver().ResolveBareBase64Content(context.Background(),
		input, &captureSaveBytes{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("malformed data unexpectedly saved: %d", len(images))
	}
	if !strings.Contains(out, inlineImageOmitted) || strings.Contains(out, "base64,") ||
		strings.Contains(out, strings.Repeat("A", 128)) {
		t.Fatalf("malformed encoded tail leaked: %q", out[:min(200, len(out))])
	}
}

func TestHTMLAndIndentedCodeExamplesArePreserved(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	token := "data:image/png;base64," + payload
	content := "    " + token + "\n<pre><code>" + token + "</code></pre>\n" +
		"<code>" + token + "</code>\n![](" + token + ")"
	svc := &captureSaveBytes{}
	out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
		&types.ReadResult{MarkdownContent: content}, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("literal examples must not be uploaded, got %d images", len(images))
	}
	if strings.Count(out, token) != 3 {
		t.Fatalf("literal code image examples changed: %q", out[:min(200, len(out))])
	}
	if HasUnresolvedInlineImagePayload(out) {
		t.Fatal("literal HTML or indented code triggered unresolved-image guard")
	}
}

func TestIndentedListItemImageIsNotMistakenForCode(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	input := "- Product example\n    ![](data:image/png;base64," + payload + ")"
	out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
		&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("list image must be stored, found %d stored images", len(images))
	}
	if strings.Contains(out, "base64,") || !strings.Contains(out, images[0].ServingURL) {
		t.Fatalf("list image was not resolved: %q", out)
	}
}

func TestTabIndentedCodeExampleIsPreserved(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	input := "\tdata:image/png;base64," + payload
	out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
		&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 || out != input {
		t.Fatalf("tab-indented code example changed: images=%d output=%q", len(images), out)
	}
	if HasUnresolvedInlineImagePayload(out) {
		t.Fatal("tab-indented code example triggered unresolved-image guard")
	}
}

func TestIndentedCodeInsideListIsNotResolved(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	input := "- Product example\n\n      ![](data:image/png;base64," + payload + ")"
	out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
		&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("indented list code example was uploaded, got %d", len(images))
	}
	if out != input || HasUnresolvedInlineImagePayload(out) {
		t.Fatal("indented list code example must stay literal and not trigger guard")
	}
}

func BenchmarkHasUnresolvedInlineImagePayloadWithManyFencedCodeBlocks(b *testing.B) {
	const codeBlock = "```text\ndata:image/png;base64," +
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" +
		"\n```\n"
	markdown := strings.Repeat(codeBlock, 1000)
	b.SetBytes(int64(len(markdown)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if HasUnresolvedInlineImagePayload(markdown) {
			b.Fatal("code examples must not be treated as unresolved image payloads")
		}
	}
}

func BenchmarkHasUnresolvedInlineImagePayloadWithManyIndentedCodeLines(b *testing.B) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	for _, lineCount := range []int{1000, 3000, 6000} {
		markdown := strings.Repeat("    data:image/png;base64,"+payload+"\n", lineCount)
		b.Run(fmt.Sprintf("%d_lines", lineCount), func(b *testing.B) {
			b.SetBytes(int64(len(markdown)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if HasUnresolvedInlineImagePayload(markdown) {
					b.Fatal("indented code examples must not be treated as unresolved images")
				}
			}
		})
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

func TestResolveAndStoreReturnsImagesInDocumentOrder(t *testing.T) {
	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	var refs []types.ImageRef
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		refs = append(refs, types.ImageRef{
			Filename: name, OriginalRef: "images/" + name, MimeType: "image/png", ImageData: png,
		})
	}
	result := &types.ReadResult{
		MarkdownContent: strings.Join([]string{
			`![](images/a.png)`, `![](data:image/png;base64,` + b64 + `)`, `![](images/b.png)`, `![](images/c.png)`,
		}, "\n\n"),
		ImageRefs: refs,
	}
	out, imgs, err := NewImageResolver().ResolveAndStore(context.Background(), result, &captureSaveBytes{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 4 {
		t.Fatalf("expected 4 images but got %d", len(imgs))
	}
	if imgs[0].OriginalRef != "images/a.png" || imgs[2].OriginalRef != "images/b.png" ||
		imgs[3].OriginalRef != "images/c.png" {
		t.Fatalf("images are not in document order: %+v", imgs)
	}
	last := -1
	for _, img := range imgs {
		pos := strings.Index(out, img.ServingURL)
		if pos <= last {
			t.Fatalf("image %s at %d does not follow %d", img.ServingURL, pos, last)
		}
		last = pos
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

// Exercise the ingestion boundary, including the guard used before chunking.
func TestInlineImageIntegrityAndProse(t *testing.T) {
	originalPNG := createTestPNG(200, 150)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><!--` + strings.Repeat("drawing ", 90) + `--><rect/></svg>`)
	prefacedSVG := append([]byte(`<?xml version="1.0"?><!--`+strings.Repeat("preface ", 90)+`-->`), svg...)
	prose := "\nDocumentation\nAnotherParagraph\nThis business rule is essential.\n" +
		"Payment terms are net 30 (days).\n中文正文\n" + strings.Repeat("BusinessIdentifier", 8)
	cases := []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"png", originalPNG, true},
		{"png-first-line", originalPNG[:48], false},
		// Even a padded truncated tail cannot establish the boundary of subsequent prose.
		{"png-second-line", originalPNG[:95], false},
		{"png-near-end", originalPNG[:len(originalPNG)-12], false},
		{"svg", svg, true},
		{"svg-preface", prefacedSVG, true},
		{"svg-truncated", prefacedSVG[:len(prefacedSVG)-6], false},
	}
	for _, tc := range cases {
		for _, width := range []int{0, 64, 76, 80, 120} {
			mime := "png"
			if strings.HasPrefix(tc.name, "svg") {
				mime = "svg+xml"
			}
			for _, prefix := range []string{"data:image/" + mime + ";base64,", "base64,"} {
				t.Run(fmt.Sprintf("%s/%d/%s", tc.name, width, prefix), func(t *testing.T) {
					encoded := base64.StdEncoding.EncodeToString(tc.data)
					var lines []string
					if width == 0 {
						lines = []string{encoded}
					} else {
						for i := 0; i < len(encoded); i += width {
							lines = append(lines, encoded[i:min(i+width, len(encoded))])
						}
					}
					svc := &captureSaveBytes{}
					input := prefix + strings.Join(lines, "\n") + prose
					out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
						&types.ReadResult{MarkdownContent: input}, svc, 1)
					if !tc.valid {
						// An XML preamble without a visible SVG root cannot identify
						// a bare Base64 fragment as an image; leave it wholly untouched.
						unknownImage := tc.name == "svg-truncated" && prefix == "base64," && width > 0
						if (unknownImage && err != nil) || (!unknownImage && !errors.Is(err, ErrAmbiguousInlineImage)) {
							t.Fatalf("unexpected parse result: %v", err)
						}
						if out != input || len(images) != 0 || len(svc.saved) != 0 {
							t.Fatal("ambiguous image was modified or uploaded")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if len(images) != 1 || len(svc.saved) != 1 {
						t.Fatalf("images=%d saves=%d", len(images), len(svc.saved))
					}
					if !bytes.Equal(svc.saved[0], tc.data) {
						t.Fatal("stored bytes differ from original")
					}
					replacement := "![image](" + images[0].ServingURL + ")"
					if out != replacement+prose {
						t.Fatalf("unexpected payload boundary or prose changed: %q", out)
					}
					if HasUnresolvedInlineImagePayload(out) {
						t.Fatal("resolved content must not trigger the guard")
					}
				})
			}
		}
	}
}

func TestBrokenMarkdownImagePreservesBusinessProse(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	for _, length := range []int{0, 64, len(payload)} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			prose := "\nThis business rule is essential.\n\nPayment terms are net 30 (days)."
			svc := &captureSaveBytes{}
			input := "![](data:image/png;base64," + payload[:length] + prose
			out, _, err := NewImageResolver().ResolveAndStore(context.Background(),
				&types.ReadResult{MarkdownContent: input}, svc, 1)
			if length == 64 {
				if !errors.Is(err, ErrAmbiguousInlineImage) || out != input || len(svc.saved) != 0 {
					t.Fatalf("ambiguous image must fail without changing prose: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(out, prose) {
				t.Fatalf("business prose changed: %q", out)
			}
			if HasUnresolvedInlineImagePayload(out) {
				t.Fatal("guard rejected degraded image")
			}
		})
	}
}

func TestInlineImageRejectsInvalidStructure(t *testing.T) {
	corruptPNG := append([]byte(nil), createTestPNG(200, 150)...)
	// Keep the header and IEND intact while invalidating the IDAT checksum.
	corruptPNG[len(corruptPNG)-13] ^= 1
	corruptBMP := make([]byte, 1024)
	copy(corruptBMP, "BM")
	binary.LittleEndian.PutUint32(corruptBMP[2:6], uint32(len(corruptBMP)))
	corruptWebP := make([]byte, 1024)
	copy(corruptWebP, "RIFF")
	copy(corruptWebP[8:], "WEBP")
	binary.LittleEndian.PutUint32(corruptWebP[4:8], uint32(len(corruptWebP)-8))
	cases := []struct {
		name, mime string
		data       []byte
	}{
		{"png-checksum", "png", corruptPNG},
		{"bmp-header-only", "bmp", corruptBMP},
		{"webp-header-only", "webp", corruptWebP},
		{"svg-mismatched", "svg+xml", []byte(`<svg><!--` + strings.Repeat("drawing ", 90) + `--><g></svg>`)},
		{
			"svg-wrong-root", "svg+xml",
			[]byte(`<document><!--` + strings.Repeat("drawing ", 90) + `--><svg/></document>`),
		},
		{"svg-multiple-roots", "svg+xml", []byte(`<svg><!--` + strings.Repeat("drawing ", 90) + `--></svg><svg/>`)},
	}
	for _, tc := range cases {
		for _, syntax := range []string{"bare", "markdown", "html"} {
			t.Run(tc.name+"/"+syntax, func(t *testing.T) {
				source := "data:image/" + tc.mime + ";base64," + base64.StdEncoding.EncodeToString(tc.data)
				if syntax == "markdown" {
					source = "![](" + source + ")"
				}
				if syntax == "html" {
					source = `<img src="` + source + `">`
				}
				svc := &captureSaveBytes{}
				out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
					&types.ReadResult{MarkdownContent: source}, svc, 1)
				if err != nil {
					t.Fatal(err)
				}
				if out != inlineImageOmitted || len(images) != 0 || len(svc.saved) != 0 {
					t.Fatalf("invalid image was not safely omitted: %q, images=%d saves=%d",
						out, len(images), len(svc.saved))
				}
			})
		}
	}
}

func TestBareBase64XMLTextIsPreserved(t *testing.T) {
	for _, text := range []string{
		`<?xml version="1.0"?><document><!--` + strings.Repeat("notes ", 100) + `--></document>`,
		`<!--` + strings.Repeat("notes ", 100) + `--><document><svg/></document>`,
	} {
		input := "base64," + base64.StdEncoding.EncodeToString([]byte(text))
		svc := &captureSaveBytes{}
		out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
			&types.ReadResult{MarkdownContent: input}, svc, 1)
		if err != nil {
			t.Fatal(err)
		}
		if out != input || len(images) != 0 || len(svc.saved) != 0 ||
			HasUnresolvedInlineImagePayload(out) {
			t.Fatal("ordinary XML was treated as an image")
		}
	}
}

func TestTruncatedImagePreservesAmbiguousFullWidthProse(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(createTestPNG(200, 150))
	for _, width := range []int{64, 76, 80, 120} {
		for _, prefix := range []string{"data:image/png;base64,", "base64,"} {
			t.Run(fmt.Sprintf("%d/%s", width, prefix), func(t *testing.T) {
				prose := "\n" + strings.Repeat("Business", width/8) + strings.Repeat("X", width%8) +
					"\nDocumentation\nAnotherParagraph"
				svc := &captureSaveBytes{}
				input := prefix + payload[:width] + prose
				out, _, err := NewImageResolver().ResolveAndStore(context.Background(),
					&types.ReadResult{MarkdownContent: input}, svc, 1)
				if !errors.Is(err, ErrAmbiguousInlineImage) || out != input || len(svc.saved) != 0 {
					t.Fatalf("ambiguous prose must be preserved with a parse error: %v", err)
				}
			})
		}
	}
}

func TestWrappedSelfClosingSVG(t *testing.T) {
	for _, width := range []int{64, 76} {
		// End a self-closing SVG on a full Base64 line, without padding.
		size := width * 9
		opening, closing := `<svg xmlns="http://www.w3.org/2000/svg" data-note="`, `"/>`
		svg := []byte(opening + strings.Repeat("a", size-len(opening)-len(closing)) + closing)
		encoded := base64.StdEncoding.EncodeToString(svg)
		var lines []string
		for i := 0; i < len(encoded); i += width {
			lines = append(lines, encoded[i:i+width])
		}
		svc := &captureSaveBytes{}
		out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
			&types.ReadResult{MarkdownContent: "base64," + strings.Join(lines, "\n") + "\nDocumentation"}, svc, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(images) != 1 || len(svc.saved) != 1 || !bytes.Equal(svc.saved[0], svg) {
			t.Fatalf("%d-column SVG was not stored intact", width)
		}
		if out != "![image]("+images[0].ServingURL+")\nDocumentation" {
			t.Fatal("SVG boundary was lost")
		}
	}
}

func TestBareImageTrailingPunctuation(t *testing.T) {
	original := createTestPNG(200, 150)
	payload := base64.StdEncoding.EncodeToString(original)
	damaged := payload[:128] + "!" + payload[128:] + strings.Repeat("A", 4096)
	prose := "\nDocumentation\nAnotherParagraph\nPayment terms are net 30 (days).\n中文正文"
	for _, prefix := range []string{"data:image/png;base64,", "base64,"} {
		for _, punctuation := range []string{".", ",", ";", ""} {
			for _, broken := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", prefix, punctuation, broken), func(t *testing.T) {
					encoded := payload
					if broken {
						encoded = damaged
					}
					svc := &captureSaveBytes{}
					input := prefix + encoded + punctuation + prose
					out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
						&types.ReadResult{MarkdownContent: input}, svc, 1)
					if broken && punctuation == "" {
						if !errors.Is(err, ErrAmbiguousInlineImage) || out != input ||
							len(svc.saved) != 0 || len(images) != 0 {
							t.Fatalf("unbounded damaged image must preserve original text: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					replacement := inlineImageOmitted
					if broken {
						if len(svc.saved) != 0 || len(images) != 0 {
							t.Fatal("damaged image uploaded")
						}
					} else {
						if len(svc.saved) != 1 || len(images) != 1 || !bytes.Equal(svc.saved[0], original) {
							t.Fatal("image was not saved byte-for-byte")
						}
						replacement = "![image](" + images[0].ServingURL + ")"
					}
					if out != replacement+punctuation+prose {
						t.Fatalf("encoding residue or prose changed: %q", out)
					}
					if HasUnresolvedInlineImagePayload(out) {
						t.Fatal("resolved image triggered guard")
					}
				})
			}
		}
	}
}

func TestInlineImageDecodeResourceLimits(t *testing.T) {
	var largePNG bytes.Buffer
	// A valid, compact 24 MP PNG is outside the fixed inline decoding support limit.
	if err := png.Encode(&largePNG, image.NewGray(image.Rect(0, 0, 6000, 4000))); err != nil {
		t.Fatal(err)
	}
	frame := image.NewPaletted(image.Rect(0, 0, 512, 512), color.Palette{color.Black, color.White})
	var animation bytes.Buffer
	frames := make([]*image.Paletted, 140)
	for i := range frames {
		frames[i] = frame
	}
	if err := gif.EncodeAll(&animation, &gif.GIF{Image: frames, Delay: make([]int, len(frames))}); err != nil {
		t.Fatal(err)
	}
	brokenAnimation := append([]byte(nil), animation.Bytes()[:animation.Len()-2]...)
	brokenAnimation = append(brokenAnimation, ';') // Missing the final sub-block terminator.
	var smallAnimation, tooManyFrames bytes.Buffer
	if err := gif.EncodeAll(&smallAnimation, &gif.GIF{Image: frames[:2], Delay: make([]int, 2)}); err != nil {
		t.Fatal(err)
	}
	tinyFrame := image.NewPaletted(image.Rect(0, 0, 64, 64), color.Palette{color.Black, color.White})
	tinyFrames := make([]*image.Paletted, 257)
	for i := range tinyFrames {
		tinyFrames[i] = tinyFrame
	}
	if err := gif.EncodeAll(&tooManyFrames, &gif.GIF{
		Image: tinyFrames, Delay: make([]int, len(tinyFrames)),
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mime string
		data       []byte
		supported  bool
	}{
		{"pixel-budget", "png", largePNG.Bytes(), false},
		{"broken-pixel-budget", "png", largePNG.Bytes()[:largePNG.Len()-12], false},
		{"animation-budget", "gif", animation.Bytes(), false},
		{"frame-budget", "gif", tooManyFrames.Bytes(), false},
		{"valid-animation", "gif", smallAnimation.Bytes(), true},
		{"broken-animation", "gif", brokenAnimation, false},
	} {
		for _, syntax := range []string{
			"bare", "bare-base64", "bare-wrapped", "bare-base64-wrapped", "markdown", "html",
		} {
			t.Run(tc.name+"/"+syntax, func(t *testing.T) {
				encoded := base64.StdEncoding.EncodeToString(tc.data)
				if strings.HasSuffix(syntax, "wrapped") {
					var lines []string
					for i := 0; i < len(encoded); i += 76 {
						lines = append(lines, encoded[i:min(i+76, len(encoded))])
					}
					encoded = strings.Join(lines, "\n")
				}
				source := "data:image/" + tc.mime + ";base64," + encoded
				if strings.HasPrefix(syntax, "bare-base64") {
					source = "base64," + encoded
				}
				if syntax == "markdown" {
					source = "![](" + source + ")"
				}
				if syntax == "html" {
					source = `<img src="` + source + `">`
				}
				prose := "\nDocumentation\nAnotherParagraph"
				svc := &captureSaveBytes{}
				out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
					&types.ReadResult{MarkdownContent: source + prose}, svc, 1)
				if strings.HasPrefix(tc.name, "broken-") && strings.HasPrefix(syntax, "bare") {
					if !errors.Is(err, ErrAmbiguousInlineImage) || out != source+prose || len(svc.saved) != 0 {
						t.Fatal("a decode limit must not excuse an incomplete image boundary")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				replacement := inlineImageOmitted
				if tc.supported {
					if len(images) != 1 || len(svc.saved) != 1 || !bytes.Equal(svc.saved[0], tc.data) {
						t.Fatal("image within the validation budget was not preserved")
					}
					replacement = "![image](" + images[0].ServingURL + ")"
					if syntax == "markdown" {
						replacement = "![](" + images[0].ServingURL + ")"
					}
				} else if len(images) != 0 || len(svc.saved) != 0 {
					t.Fatal("invalid or unverifiable image was stored")
				}
				if out != replacement+prose {
					t.Fatal("resource filtering left payload or changed prose")
				}
				if HasUnresolvedInlineImagePayload(out) {
					t.Fatal("omitted image triggered guard")
				}
			})
		}
	}
}

func TestWrappedImageFragmentBoundaries(t *testing.T) {
	original := createTestPNG(200, 150)
	encoded := base64.StdEncoding.EncodeToString(original)
	for _, width := range []int{80, 120} {
		for _, newline := range []string{"\n", "\r\n"} {
			for _, prefix := range []string{"data:image/png;base64,", "base64,"} {
				t.Run(fmt.Sprintf("%d/%q/%s", width, newline, prefix), func(t *testing.T) {
					var lines []string
					for i := 0; i < len(encoded); i += width {
						lines = append(lines, encoded[i:min(i+width, len(encoded))])
					}
					prose := ". Payment terms are net 30 (days)." + newline + "Documentation" + newline + "中文正文"
					svc := &captureSaveBytes{}
					out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
						&types.ReadResult{MarkdownContent: prefix + strings.Join(lines, newline) + prose}, svc, 1)
					if err != nil {
						t.Fatal(err)
					}
					if len(images) != 1 || len(svc.saved) != 1 || !bytes.Equal(svc.saved[0], original) {
						t.Fatal("wrapped image changed")
					}
					if out != "![image]("+images[0].ServingURL+")"+prose {
						t.Fatalf("encoding residue or prose changed: %q", out)
					}
					if HasUnresolvedInlineImagePayload(out) {
						t.Fatal("resolved wrapped image triggered guard")
					}
				})
			}
		}
	}
}

func TestTruncatedWrappedSVGPreservesAlphabeticProse(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><!--` + strings.Repeat("drawing ", 90) + `--></svg>`
	payload := base64.StdEncoding.EncodeToString([]byte(svg))[:76]
	prose := "\nAABB\nDocumentation\nAnotherParagraph"
	for _, prefix := range []string{"data:image/svg+xml;base64,", "base64,"} {
		svc := &captureSaveBytes{}
		input := prefix + payload + prose
		out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
			&types.ReadResult{MarkdownContent: input}, svc, 1)
		if !errors.Is(err, ErrAmbiguousInlineImage) || out != input || len(images) != 0 || len(svc.saved) != 0 {
			t.Fatalf("ambiguous SVG must preserve original text: %v", err)
		}
	}
}

func TestDamagedInlineImageBoundaries(t *testing.T) {
	truncated := base64.StdEncoding.EncodeToString(createTestPNG(200, 150)[:48])
	for _, suffix := range []string{"\nINV202600001\n正文", "\norder/item-2026\n正文", "\nAAAA\n正文", "\nTOTAL=100\n正文"} {
		for _, prefix := range []string{"data:image/png;base64,", "base64,"} {
			input := prefix + truncated + suffix
			out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
				&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
			if !errors.Is(err, ErrAmbiguousInlineImage) || len(images) != 0 || out != input {
				t.Fatalf("damaged image consumed prose: out=%q images=%d err=%v", out, len(images), err)
			}
		}
	}
	for _, quote := range []string{"\"", "'"} {
		input := "<img src=" + quote + "data:image/png;base64," + truncated + ">\n正文"
		out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
			&types.ReadResult{MarkdownContent: input}, &captureSaveBytes{}, 1)
		if err != nil || len(images) != 0 || out != "<img src="+quote+inlineImageOmitted+">\n正文" ||
			HasUnresolvedInlineImagePayload(out) {
			t.Fatalf("unclosed image not omitted locally: out=%q images=%d err=%v", out, len(images), err)
		}
	}
}

func TestInlinePNGWithoutPixelDataIsOmitted(t *testing.T) {
	original := createTestPNG(200, 150)
	for _, width := range []uint32{200, 4097} {
		data := append([]byte(nil), original[:33]...)
		binary.BigEndian.PutUint32(data[16:20], width)
		binary.BigEndian.PutUint32(data[20:24], 4097)
		binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
		data = append(data, original[len(original)-12:]...) // No IDAT: deliberately no pixel data.
		input := "![](data:image/png;base64," + base64.StdEncoding.EncodeToString(data) + ")\n正文"
		svc := &captureSaveBytes{}
		out, images, err := NewImageResolver().ResolveAndStore(context.Background(),
			&types.ReadResult{MarkdownContent: input}, svc, 1)
		if err != nil ||
			HasUnresolvedInlineImagePayload(out) {
			t.Fatalf("unexpected failure: %v", err)
		}
		want := inlineImageOmitted + "\n正文"
		if len(images) != 0 || len(svc.saved) != 0 {
			t.Fatalf("PNG without pixel data was stored at width %d", width)
		}
		if out != want {
			t.Fatalf("out=%q want=%q", out, want)
		}
	}
}

func TestResolveAndStore_ExportImageRefsBeyondInlineBudget(t *testing.T) {
	data := createTestPNG(200, 150)
	result := &types.ReadResult{}
	for i := 0; i < 61; i++ {
		ref := fmt.Sprintf("image-%d.png", i)
		result.MarkdownContent += "![image](" + ref + ")\n"
		result.ImageRefs = append(result.ImageRefs, types.ImageRef{
			OriginalRef: ref, Filename: ref, ImageData: data, MimeType: "image/png",
		})
	}
	out, images, err := NewImageResolver().ResolveAndStore(context.Background(), result, &captureSaveBytes{}, 1)
	require.NoError(t, err)
	require.Len(t, images, 61, "native export ImageRefs must retain all images beyond the inline URI budget")
	require.NotContains(t, out, "data:image/")
	for i := 0; i < 61; i++ {
		require.NotContains(t, out, fmt.Sprintf("](image-%d.png)", i))
	}
}
