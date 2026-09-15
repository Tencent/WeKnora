package outline

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// stubDownloader serves canned bytes per attachment id.
type stubDownloader struct {
	data  map[string][]byte
	ctype map[string]string
	calls int
}

func (s *stubDownloader) DownloadAttachment(ctx context.Context, id string) ([]byte, string, error) {
	s.calls++
	b, ok := s.data[id]
	if !ok {
		return nil, "", errors.New("not found")
	}
	ct := s.ctype[id]
	if ct == "" {
		ct = "image/png"
	}
	return b, ct, nil
}

// pngBytes returns a byte slice that sniffs as PNG.
func pngBytes(n int) []byte {
	b := make([]byte, n)
	copy(b, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	return b
}

func TestEmbedAttachmentImages_ReplacesWithDataURI(t *testing.T) {
	md := "Trước ảnh\n\n![](/api/attachments.redirect?id=att-1)\n\nSau ảnh\n"
	d := &stubDownloader{data: map[string][]byte{"att-1": pngBytes(1024)}}

	out, n := embedAttachmentImages(context.Background(), d, md)

	if n != 1 {
		t.Fatalf("inlined = %d, want 1", n)
	}
	if !strings.Contains(out, "](data:image/png;base64,") {
		t.Errorf("output has no data URI:\n%s", out)
	}
	if strings.Contains(out, "attachments.redirect") {
		t.Errorf("original attachment URL still present:\n%s", out)
	}
	if !strings.Contains(out, "Trước ảnh") || !strings.Contains(out, "Sau ảnh") {
		t.Error("surrounding text was lost")
	}
}

// The Markdown title must become the alt text and must NOT survive after the
// data URI: WeKnora's data-URI regex captures everything up to the closing
// paren, so a trailing title lands inside the base64 payload and the image is
// dropped on decode.
func TestEmbedAttachmentImages_TitleBecomesAltAndIsNotTrailing(t *testing.T) {
	md := "![](/api/attachments.redirect?id=att-1 \"Màn hình quét QR\")"
	d := &stubDownloader{data: map[string][]byte{"att-1": pngBytes(512)}}

	out, n := embedAttachmentImages(context.Background(), d, md)

	if n != 1 {
		t.Fatalf("inlined = %d, want 1", n)
	}
	if !strings.HasPrefix(out, "![Màn hình quét QR](data:image/png;base64,") {
		t.Errorf("title was not promoted to alt text:\n%s", out)
	}
	if strings.Contains(out, "\"Màn hình quét QR\")") {
		t.Errorf("title still trails the data URI, which corrupts the base64 payload:\n%s", out)
	}
	if !strings.HasSuffix(out, ")") || strings.Count(out, ")") != 1 {
		t.Errorf("malformed image syntax:\n%s", out)
	}
}

// Guards the same trap from the consumer side: what the connector emits must
// survive the exact regex plus payload extraction that ingestion applies.
func TestEmbedAttachmentImages_OutputSurvivesIngestionRegex(t *testing.T) {
	md := "![](/api/attachments.redirect?id=att-1 \"Chú thích\")"
	d := &stubDownloader{data: map[string][]byte{"att-1": pngBytes(700)}}

	out, _ := embedAttachmentImages(context.Background(), d, md)

	m := ingestionDataURIRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("ingestion regex does not match the emitted image:\n%s", out)
	}
	// Ingestion takes everything after ";base64," up to the closing paren and
	// only strips whitespace before decoding, so any leftover must be base64.
	payload := m[2]
	idx := strings.Index(payload, ";base64,")
	if idx < 0 {
		t.Fatalf("emitted data URI has no ;base64, marker: %q", payload)
	}
	b64 := payload[idx+len(";base64,"):]
	if !isBase64Only(b64) {
		t.Errorf("payload carries non-base64 characters, ingestion would fail to decode: %q",
			firstRunes(b64, 60))
	}
}

func TestEmbedAttachmentImages_KeepsExistingAlt(t *testing.T) {
	md := "![Sơ đồ luồng](/api/attachments.redirect?id=att-1 \"bị bỏ qua\")"
	d := &stubDownloader{data: map[string][]byte{"att-1": pngBytes(512)}}

	out, _ := embedAttachmentImages(context.Background(), d, md)
	if !strings.HasPrefix(out, "![Sơ đồ luồng](data:") {
		t.Errorf("existing alt text was not preserved:\n%s", out)
	}
}

func TestEmbedAttachmentImages_AbsoluteURL(t *testing.T) {
	md := "![](https://docs.example.com/api/attachments.redirect?id=att-1)"
	d := &stubDownloader{data: map[string][]byte{"att-1": pngBytes(512)}}

	_, n := embedAttachmentImages(context.Background(), d, md)
	if n != 1 {
		t.Fatalf("inlined = %d, want 1 (absolute attachment URLs must match too)", n)
	}
}

func TestEmbedAttachmentImages_DownloadFailureKeepsLink(t *testing.T) {
	md := "![](/api/attachments.redirect?id=missing)"
	d := &stubDownloader{data: map[string][]byte{}}

	out, n := embedAttachmentImages(context.Background(), d, md)
	if n != 0 {
		t.Errorf("inlined = %d, want 0", n)
	}
	if out != md {
		t.Errorf("a failed download must leave the document untouched, got:\n%s", out)
	}
}

func TestEmbedAttachmentImages_OversizeKeepsLink(t *testing.T) {
	md := "![](/api/attachments.redirect?id=big)"
	d := &stubDownloader{data: map[string][]byte{"big": pngBytes(maxImageBytes + 1)}}

	out, n := embedAttachmentImages(context.Background(), d, md)
	if n != 0 {
		t.Errorf("inlined = %d, want 0 for an oversize image", n)
	}
	if out != md {
		t.Error("oversize image should keep its original link")
	}
}

func TestEmbedAttachmentImages_CapsAtMaxInlineImages(t *testing.T) {
	var sb strings.Builder
	data := map[string][]byte{}
	total := maxInlineImages + 5
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("att-%02d", i)
		data[id] = pngBytes(256)
		sb.WriteString("![](/api/attachments.redirect?id=" + id + ")\n")
	}
	d := &stubDownloader{data: data}

	out, n := embedAttachmentImages(context.Background(), d, sb.String())
	if n != maxInlineImages {
		t.Fatalf("inlined = %d, want %d", n, maxInlineImages)
	}
	// The uninlined tail keeps its URLs rather than being silently deleted.
	if got := strings.Count(out, "attachments.redirect"); got != total-maxInlineImages {
		t.Errorf("leftover links = %d, want %d", got, total-maxInlineImages)
	}
	if d.calls > maxInlineImages {
		t.Errorf("downloaded %d attachments, want at most %d (no wasted downloads past the cap)",
			d.calls, maxInlineImages)
	}
}

func TestEmbedAttachmentImages_MimeFromContentType(t *testing.T) {
	md := "![](/api/attachments.redirect?id=att-1)"
	d := &stubDownloader{
		data:  map[string][]byte{"att-1": pngBytes(512)},
		ctype: map[string]string{"att-1": "image/jpeg; charset=binary"},
	}

	out, _ := embedAttachmentImages(context.Background(), d, md)
	if !strings.Contains(out, "data:image/jpeg;base64,") {
		t.Errorf("Content-Type parameters were not stripped:\n%s", out)
	}
}

func TestEmbedAttachmentImages_NonImageContentTypeKeepsLink(t *testing.T) {
	md := "![](/api/attachments.redirect?id=pdf-1)"
	d := &stubDownloader{
		data:  map[string][]byte{"pdf-1": []byte("%PDF-1.4 ...")},
		ctype: map[string]string{"pdf-1": "application/pdf"},
	}

	out, n := embedAttachmentImages(context.Background(), d, md)
	if n != 0 {
		t.Errorf("inlined = %d, want 0 for a non-image attachment", n)
	}
	if out != md {
		t.Error("non-image attachment should keep its original link")
	}
}

func TestEmbedAttachmentImages_NoImagesIsIdentity(t *testing.T) {
	md := "## Tiêu đề\n\nKhông có ảnh nào ở đây.\n"
	d := &stubDownloader{data: map[string][]byte{}}

	out, n := embedAttachmentImages(context.Background(), d, md)
	if n != 0 || out != md {
		t.Errorf("input without images was modified:\n%q", out)
	}
	if d.calls != 0 {
		t.Errorf("calls = %d, want 0", d.calls)
	}
}

func TestStripEscapeArtifacts(t *testing.T) {
	bs := "\\"
	in := strings.Join([]string{
		"## Mục lớn",
		"",
		"    " + bs + "n" + bs + "n",
		"* Nội dung",
		"  " + bs,
		"Câu có " + bs + "[ngoặc vuông" + bs + "] hợp lệ",
		"",
	}, "\n")

	out := stripEscapeArtifacts(in)

	if strings.Contains(out, bs+"n") {
		t.Errorf("literal backslash-n artifact survived:\n%q", out)
	}
	if !strings.Contains(out, "## Mục lớn") || !strings.Contains(out, "* Nội dung") {
		t.Errorf("real content was removed:\n%q", out)
	}
	if !strings.Contains(out, bs+"[ngoặc vuông"+bs+"]") {
		t.Errorf("valid inline escapes must be preserved:\n%q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == bs {
			t.Errorf("a bare backslash line survived:\n%q", out)
		}
	}
}

// A fenced code block can legitimately contain a bare backslash line; the
// cleaner must not reach inside one.
func TestStripEscapeArtifacts_LeavesFencedCodeAlone(t *testing.T) {
	bs := "\\"
	in := strings.Join([]string{
		"Trước",
		"```",
		"  " + bs,
		"```",
		"  " + bs,
		"Sau",
	}, "\n")

	out := stripEscapeArtifacts(in)

	if strings.Count(out, bs) != 1 {
		t.Errorf("expected exactly the in-fence backslash to survive, got:\n%q", out)
	}
	if !strings.Contains(out, "```\n  "+bs+"\n```") {
		t.Errorf("fenced content was altered:\n%q", out)
	}
}

func TestStripEscapeArtifacts_NoArtifactsIsIdentity(t *testing.T) {
	in := "## Tiêu đề\n\nMột đoạn bình thường.\n"
	if out := stripEscapeArtifacts(in); out != in {
		t.Errorf("clean input was modified:\n%q", out)
	}
}

// --- helpers ---

// ingestionDataURIRe is a copy of imgMarkdownDataURI from
// internal/infrastructure/docparser/image_resolver.go. Duplicated rather than
// imported because it is unexported there; if ingestion's regex ever changes,
// this test is where the mismatch should surface.
var ingestionDataURIRe = regexp.MustCompile(
	`!\[(.*?)\]\((?i:(data:image/[^;]+;base64,\s*[^)]+))\)`,
)

func isBase64Only(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '+', r == '/', r == '=', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func firstRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
