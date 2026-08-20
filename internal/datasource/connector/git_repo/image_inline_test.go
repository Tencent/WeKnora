package git_repo

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// makeImage writes a >512B fake PNG blob so it passes the icon-size filter.
func makeImage(t *testing.T, root, rel string) string {
	t.Helper()
	// Minimal valid-ish PNG header plus padding to exceed minInlinedImageSize.
	blob := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	for len(blob) < 600 {
		blob = append(blob, byte(len(blob)))
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestInlineRelativeImagesMarkdown(t *testing.T) {
	root := t.TempDir()
	makeImage(t, root, "docs/images/foo.png")
	md := "# title\n\n![alt](./images/foo.png)\n"
	out, n := InlineRelativeImages(root, "docs/a.md", []byte(md))
	if n != 1 {
		t.Fatalf("inlined = %d, want 1", n)
	}
	if !strings.Contains(string(out), "data:image/png;base64,") {
		t.Fatalf("output missing data URI: %s", out)
	}
	// Extract the base64 payload (between "base64," and the closing ")").
	b64Re := regexp.MustCompile(`base64,([A-Za-z0-9+/=]+)`)
	m := b64Re.FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("no base64 payload found in: %s", out)
	}
	decoded, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != 600 {
		t.Fatalf("decoded length = %d, want 600", len(decoded))
	}
}

func TestInlineRelativeImagesNestedParentDir(t *testing.T) {
	root := t.TempDir()
	makeImage(t, root, "images/x.png")
	// docs/a.md referencing ../images/x.png resolves up one level to images/x.png,
	// still inside the worktree — a legitimate traversal that must inline.
	md := "![alt](../images/x.png)\n"
	out, n := InlineRelativeImages(root, "docs/a.md", []byte(md))
	if n != 1 {
		t.Fatalf("inlined = %d, want 1 (out=%s)", n, out)
	}
	if !strings.Contains(string(out), "data:image/png;base64,") {
		t.Fatalf("output missing data URI: %s", out)
	}
}

func TestInlineRelativeImagesHTML(t *testing.T) {
	root := t.TempDir()
	makeImage(t, root, "docs/images/y.jpg")
	md := `<p><img src="images/y.jpg" alt="y"/></p>`
	out, n := InlineRelativeImages(root, "docs/a.md", []byte(md))
	if n != 1 {
		t.Fatalf("inlined = %d, want 1", n)
	}
	if !strings.Contains(string(out), "data:image/jpeg;base64,") {
		t.Fatalf("output missing jpeg data URI: %s", out)
	}
}

func TestInlineRelativeImagesSkips(t *testing.T) {
	root := t.TempDir()

	// Missing referenced file → untouched.
	out, n := InlineRelativeImages(root, "docs/a.md", []byte("![missing](./images/absent.png)\n"))
	if n != 0 || !strings.Contains(string(out), "./images/absent.png") {
		t.Fatalf("missing image must be left as-is, got n=%d out=%s", n, out)
	}

	// Absolute path and scheme URLs → untouched.
	out, _ = InlineRelativeImages(root, "docs/a.md", []byte(
		"![a](/files/x.png) ![b](https://example.com/x.png) ![c](data:image/png;base64,AAAA)\n"))
	if !strings.Contains(string(out), "/files/x.png") ||
		!strings.Contains(string(out), "https://example.com/x.png") ||
		!strings.Contains(string(out), "data:image/png;base64,AAAA") {
		t.Fatalf("scheme/absolute refs must be preserved: %s", out)
	}

	// Traversal escaping the worktree → untouched.
	md := "![evil](../../etc/passwd)\n"
	_, n = InlineRelativeImages(root, "docs/a.md", []byte(md))
	if n != 0 {
		t.Fatalf("traversal must not inline, got n=%d", n)
	}

	// Icon-sized image (<512B) → skipped.
	small := filepath.Join(root, "images", "icon.png")
	_ = os.MkdirAll(filepath.Dir(small), 0o755)
	_ = os.WriteFile(small, []byte{0x89, 'P', 'N', 'G'}, 0o644) // 4 bytes
	_, n = InlineRelativeImages(root, "docs/a.md", []byte("![icon](./images/icon.png)\n"))
	if n != 0 {
		t.Fatalf("icon image must be skipped, got n=%d", n)
	}

	// Non-image extension → skipped.
	_ = os.WriteFile(filepath.Join(root, "images", "x.txt"), []byte(strings.Repeat("x", 600)), 0o644)
	_, n = InlineRelativeImages(root, "docs/a.md", []byte("![txt](./images/x.txt)\n"))
	if n != 0 {
		t.Fatalf("non-image extension must be skipped, got n=%d", n)
	}
}

func TestInlineRelativeImagesOverCount(t *testing.T) {
	root := t.TempDir()
	// Exceed the per-doc image count limit.
	var refs []string
	for i := 0; i < maxInlinedImagesPerDoc+5; i++ {
		makeImage(t, root, "docs/images/pic.png")
		refs = append(refs, "![p](./images/pic.png)")
	}
	md := strings.Join(refs, "\n")
	_, n := InlineRelativeImages(root, "docs/a.md", []byte(md))
	if n != maxInlinedImagesPerDoc {
		t.Fatalf("inlined = %d, want cap %d", n, maxInlinedImagesPerDoc)
	}
}

func TestInlineRelativeImagesOversized(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "images", "big.png")
	_ = os.MkdirAll(filepath.Dir(big), 0o755)
	_ = os.WriteFile(big, make([]byte, maxInlinedImageSize+1), 0o644)
	out, n := InlineRelativeImages(root, "docs/a.md", []byte("![big](./images/big.png)\n"))
	if n != 0 {
		t.Fatalf("oversized image must be skipped, got n=%d", n)
	}
	if !strings.Contains(string(out), "./images/big.png") {
		t.Fatalf("oversized ref must be preserved: %s", out)
	}
}

func TestInlineRelativeImagesSameWorktree(t *testing.T) {
	root := t.TempDir()
	makeImage(t, root, "images/x.png")
	// The data URI must appear in the output.
	out, _ := InlineRelativeImages(root, "a.md", []byte("![x](./images/x.png)"))
	if !strings.Contains(string(out), "data:image/png;base64,") {
		t.Fatalf("missing data URI: %s", out)
	}
}
