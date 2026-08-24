package git_repo

import (
	"encoding/base64"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Inline limits mirror the docparser image pipeline (image_resolver.go) so that
// everything the connector inlines is guaranteed to be accepted downstream:
//   - maxInlinedImagesPerDoc mirrors maxRemoteImages (30 per document)
//   - maxInlinedImageSize mirrors maxRemoteImageSize (10 MB decoded per image)
//   - minInlinedImageSize mirrors the isIconImage byte heuristic (<512 B)
//   - maxInlinedImageBytesPerDoc keeps the base64-inflated markdown payload
//     within a sane budget (the docparser gRPC read has a 50 MB cap, and base64
//     expands raw bytes by ~33%).
const (
	maxInlinedImagesPerDoc     = 30
	maxInlinedImageSize        = 10 * 1024 * 1024
	minInlinedImageSize        = 512
	maxInlinedImageBytesPerDoc = 40 * 1024 * 1024
)

var (
	// markdownImageRefRe mirrors image_resolver.go's imgMarkdownPattern: it
	// captures the image URL target of ![alt](url) (group 2), with support for
	// one level of balanced parentheses in the URL.
	markdownImageRefRe = regexp.MustCompile(`!\[(.*?)\]\(([^()\s]*(?:\([^)]*\)[^()\s]*)*)\)`)
	// htmlImgSrcRe captures a quoted <img src="..."> value.
	htmlImgSrcRe = regexp.MustCompile(`(?i)<img\s[^>]*?src\s*=\s*["']([^"']+)["']`)
)

// InlineRelativeImages rewrites relative image references in a markdown (or
// HTML) document to inline data:image/...;base64 URIs by reading the referenced
// blobs from the git worktree. The standard WeKnora ingest pipeline then
// extracts those data URIs, stores the bytes, and rewrites the refs to
// previewable provider URLs — no pipeline changes needed.
//
// References that already carry a scheme, an absolute path, or that point at a
// missing/oversized/icon-sized file are left untouched so the document still
// syncs (a broken ref merely does not render). Traversal outside the worktree
// is rejected. Returns the rewritten bytes and the number of inlined images.
func InlineRelativeImages(worktreeRoot, docRel string, data []byte) ([]byte, int) {
	markdown := string(data)

	type replacement struct {
		start, end int
		value      string
	}
	var reps []replacement
	var st inlineState

	for _, m := range markdownImageRefRe.FindAllStringSubmatchIndex(markdown, -1) {
		if len(m) < 6 {
			continue
		}
		target := markdown[m[4]:m[5]]
		if !shouldInline(target) {
			continue
		}
		if v, ok := st.inlineTarget(worktreeRoot, docRel, target); ok {
			reps = append(reps, replacement{start: m[4], end: m[5], value: v})
		}
	}
	for _, m := range htmlImgSrcRe.FindAllStringSubmatchIndex(markdown, -1) {
		if len(m) < 3 {
			continue
		}
		src := markdown[m[2]:m[3]]
		if !shouldInline(src) {
			continue
		}
		if v, ok := st.inlineTarget(worktreeRoot, docRel, src); ok {
			reps = append(reps, replacement{start: m[2], end: m[3], value: v})
		}
	}

	// Apply replacements in reverse order so earlier spans stay valid.
	for i := len(reps) - 1; i >= 0; i-- {
		r := reps[i]
		markdown = markdown[:r.start] + r.value + markdown[r.end:]
	}
	return []byte(markdown), st.inlined
}

// inlineState tracks the per-document inline budget shared by both syntaxes.
type inlineState struct {
	inlined    int
	totalBytes int64
}

// inlineTarget resolves and base64-encodes a single relative target, enforcing
// the shared limits. Returns the replacement value and whether it was inlined.
func (s *inlineState) inlineTarget(worktreeRoot, docRel, target string) (string, bool) {
	if s.inlined >= maxInlinedImagesPerDoc {
		return "", false
	}
	abs, ok := resolveImagePath(worktreeRoot, docRel, target)
	if !ok {
		return "", false
	}
	blob, mime, ok := readImageFile(abs)
	if !ok {
		return "", false
	}
	if len(blob) < minInlinedImageSize || len(blob) > maxInlinedImageSize {
		return "", false
	}
	inflated := int64(len(blob)*4/3 + 1)
	if s.totalBytes+inflated > maxInlinedImageBytesPerDoc {
		return "", false
	}
	s.totalBytes += inflated
	s.inlined++
	return dataURI(mime, blob), true
}

// resolveImagePath maps a relative image target inside a document to an
// absolute path under the worktree, rejecting traversal that escapes it.
func resolveImagePath(worktreeRoot, docRel, target string) (string, bool) {
	dir := path.Dir(filepath.ToSlash(docRel))
	if dir == "." {
		dir = ""
	}
	abs := path.Clean(path.Join(dir, filepath.ToSlash(target)))
	if abs == ".." || strings.HasPrefix(abs, "../") {
		return "", false
	}
	full, err := resolveUnderRoot(worktreeRoot, abs)
	if err != nil {
		return "", false
	}
	return full, true
}

// shouldInline reports whether a target is a relative path worth inlining.
// Absolute paths (/...), protocol-relative (//), scheme URLs (http/https/data:/
// provider://) and empty targets are left as-is.
func shouldInline(target string) bool {
	t := strings.TrimSpace(target)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "data:") || strings.HasPrefix(t, "/") || strings.HasPrefix(t, "//") {
		return false
	}
	return !strings.Contains(t, "://")
}

// readImageFile reads a worktree image blob and derives its MIME type from the
// file extension. Unknown extensions are skipped.
func readImageFile(abs string) ([]byte, string, bool) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, "", false
	}
	mime := mimeFromExt(abs)
	if mime == "" {
		return nil, "", false
	}
	return data, mime, true
}

func mimeFromExt(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	default:
		return ""
	}
}

func dataURI(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}
