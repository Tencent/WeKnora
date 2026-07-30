package searchutil

import (
	"regexp"
	"strings"
)

const modelImagePlaceholder = "[image]"

var (
	markdownAngleImageURLRE = regexp.MustCompile(`(?s)(!\[[^\]]*\]\(\s*)<[^>\r\n]*>([^)]*\))`)
	markdownImageURLRE      = regexp.MustCompile(`(?s)(!\[[^\]]*\]\(\s*)[^\s)]+([^)]*\))`)
	htmlImageSrcDoubleRE    = regexp.MustCompile(`(?is)(<img\b[^>]*\bsrc\s*=\s*")[^"]*(")`)
	htmlImageSrcSingleRE    = regexp.MustCompile(`(?is)(<img\b[^>]*\bsrc\s*=\s*')[^']*(')`)
	imageURLDoubleRE        = regexp.MustCompile(`(?is)(<image\b[^>]*\burl\s*=\s*")[^"]*(")`)
	imageURLSingleRE        = regexp.MustCompile(`(?is)(<image\b[^>]*\burl\s*=\s*')[^']*(')`)
	rawImageDataURIRe       = regexp.MustCompile(`(?i)data:image/[a-z0-9.+-]+;base64,[a-z0-9+/=]{200,}`)
	rawDataURIRe            = regexp.MustCompile(`(?i)data:[a-z0-9.+/-]+;base64,[a-z0-9+/=]{200,}`)
)

// CanonicalizeImageURLsForModel removes storage-location entropy from model
// inputs while retaining Markdown alt text, titles, HTML attributes, OCR and
// captions. Export URLs are transport metadata: embedding or extracting them
// adds no semantics and makes an identical reparse produce a different cache
// key whenever the storage layer generates a new UUID or signed URL.
func CanonicalizeImageURLsForModel(content string) string {
	if content == "" {
		return content
	}

	canonical := content
	if strings.Contains(canonical, "![") {
		canonical = markdownAngleImageURLRE.ReplaceAllString(canonical, `${1}`+modelImagePlaceholder+`${2}`)
		canonical = markdownImageURLRE.ReplaceAllString(canonical, `${1}`+modelImagePlaceholder+`${2}`)
	}
	if strings.Contains(strings.ToLower(canonical), "<img") {
		canonical = htmlImageSrcDoubleRE.ReplaceAllString(canonical, `${1}`+modelImagePlaceholder+`${2}`)
		canonical = htmlImageSrcSingleRE.ReplaceAllString(canonical, `${1}`+modelImagePlaceholder+`${2}`)
	}
	if strings.Contains(strings.ToLower(canonical), "<image") {
		canonical = imageURLDoubleRE.ReplaceAllString(canonical, `${1}`+modelImagePlaceholder+`${2}`)
		canonical = imageURLSingleRE.ReplaceAllString(canonical, `${1}`+modelImagePlaceholder+`${2}`)
	}
	if strings.Contains(canonical, "base64,") {
		canonical = rawImageDataURIRe.ReplaceAllString(canonical, modelImagePlaceholder)
		canonical = rawDataURIRe.ReplaceAllString(canonical, modelImagePlaceholder)
	}
	return canonical
}
