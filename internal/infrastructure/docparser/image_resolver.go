package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const (
	// minImageDimension is the minimum width/height in pixels; images smaller
	// than this on either axis are treated as icons and filtered out.
	minImageDimension = 128
	// minImageBytes is the minimum file size in bytes; very small images are
	// almost certainly icons or decorative elements.
	minImageBytes = 512 // 512 bytes
)

// isIconImage returns true if the image data looks like a small icon or
// decorative element that should be filtered out. It checks pixel dimensions
// when decodable, and falls back to raw byte size otherwise.
func isIconImage(data []byte) bool {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		// Cannot decode dimensions — fall back to size-only heuristic.
		return len(data) < minImageBytes
	}
	if cfg.Width < minImageDimension || cfg.Height < minImageDimension {
		return true
	}
	return false
}

// StoredImage describes an image that has been saved to storage.
type StoredImage struct {
	OriginalRef string // reference in the original markdown
	ServingURL  string // provider:// URL (e.g. local://images/xxx.png, minio://bucket/key)
	MimeType    string
}

// ImageResolver reads images from a DocReader ReadResult (inline bytes only)
// and saves them via FileService, replacing markdown references with unified URLs.
type ImageResolver struct {
	// TenantID for storage path namespacing
	TenantID uint64
}

// NewImageResolver creates a resolver.
func NewImageResolver() *ImageResolver {
	return &ImageResolver{}
}

// ResolveAndStore reads images from the convert result, persists them via fileSvc,
// and replaces markdown references with provider:// URLs.
// It returns the updated markdown and a list of stored images.
func (r *ImageResolver) ResolveAndStore(
	ctx context.Context,
	result *types.ReadResult,
	fileSvc interfaces.FileService,
	tenantID uint64,
) (updatedMarkdown string, images []StoredImage, err error) {
	markdown := UnwrapLinkedImages(result.MarkdownContent)
	md2, imgDataURIs, _ := r.ResolveDataURIImages(ctx, markdown, fileSvc, tenantID)
	markdown = md2
	images = append(images, imgDataURIs...)

	md3, imgHTML, _ := r.ResolveHTMLDataURIImages(ctx, markdown, fileSvc, tenantID)
	markdown = md3
	images = append(images, imgHTML...)

	md4, imgBare, _ := r.ResolveBareBase64Content(ctx, markdown, fileSvc, tenantID)
	markdown = md4
	images = append(images, imgBare...)

	if len(result.ImageRefs) == 0 {
		return markdown, images, nil
	}

	// Build a map of original_ref -> image ref for fast lookup
	refMap := make(map[string]types.ImageRef)
	for _, ref := range result.ImageRefs {
		refMap[ref.OriginalRef] = ref
	}

	// Process each image reference found in the markdown.
	// The URL group supports one level of balanced parentheses so that URLs
	// like https://example.com/item_(abc)/123 are captured in full.
	// Allow spaces in URLs (exclude only parens and newlines) to handle
	// filenames with spaces, e.g. "images/第 1 页.jpg".
	imgPattern := regexp.MustCompile(`!\[(.*?)\]\(([^()\n]*(?:\([^)]*\)[^()\n]*)*)\)`)
	matches := imgPattern.FindAllStringSubmatchIndex(markdown, -1)

	// Process in reverse order to preserve positions when replacing
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		refPath := markdown[m[4]:m[5]] // group 2: the URL/path

		// Skip already-resolved URLs (http/https, unified /files/, or provider:// scheme)
		if strings.HasPrefix(refPath, "http://") || strings.HasPrefix(refPath, "https://") ||
			isProviderScheme(refPath) {
			continue
		}

		// Find inline image bytes from the result
		ref, found := refMap[refPath]
		if !found || len(ref.ImageData) == 0 {
			continue
		}

		// Filter out small icons and decorative images
		if isIconImage(ref.ImageData) {
			// Remove the image reference from markdown entirely
			markdown = markdown[:m[0]] + markdown[m[1]:]
			continue
		}

		// Determine extension
		ext := extFromMime(ref.MimeType)
		if ext == "" {
			ext = filepath.Ext(ref.Filename)
		}
		if ext == "" {
			ext = ".png"
		}

		// Save via FileService — returns provider:// path
		fileName := uuid.New().String() + ext
		servingURL, saveErr := fileSvc.SaveBytes(ctx, ref.ImageData, tenantID, fileName, false)
		if saveErr != nil {
			log.Printf("WARN: failed to save image %s: %v", refPath, saveErr)
			continue
		}

		images = append(images, StoredImage{
			OriginalRef: refPath,
			ServingURL:  servingURL,
			MimeType:    ref.MimeType,
		})

		// Replace in markdown
		markdown = markdown[:m[4]] + servingURL + markdown[m[5]:]
	}

	return markdown, images, nil
}

func extFromMime(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/svg+xml":
		return ".svg"
	default:
		return ""
	}
}

// isProviderScheme checks if the path uses a provider:// scheme (local://, minio://, cos://, tos://).
func isProviderScheme(p string) bool {
	for _, prefix := range []string{"local://", "minio://", "cos://", "tos://", "s3://"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Helper functions for base64 image handling
// ---------------------------------------------------------------------------

// cleanBase64Payload removes whitespace characters from a base64 payload string.
func cleanBase64Payload(payload string) string {
	payload = strings.ReplaceAll(payload, "\n", "")
	payload = strings.ReplaceAll(payload, "\r", "")
	payload = strings.ReplaceAll(payload, "\t", "")
	payload = strings.ReplaceAll(payload, " ", "")
	return payload
}

// decodeBase64Flexible tries standard, raw, URL-safe, and raw-URL-safe base64 decodings.
func decodeBase64Flexible(payload string) ([]byte, error) {
	if data, err := base64.StdEncoding.DecodeString(payload); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(payload); err == nil {
		return data, nil
	}
	if data, err := base64.URLEncoding.DecodeString(payload); err == nil {
		return data, nil
	}
	return base64.RawURLEncoding.DecodeString(payload)
}

// sniffImageMime detects the MIME type by examining the magic bytes of image data.
func sniffImageMime(data []byte) string {
	if len(data) < 4 {
		return ""
	}
	if data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if data[0] == 'G' && data[1] == 'I' && data[2] == 'F' {
		return "image/gif"
	}
	if len(data) >= 12 &&
		data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P' {
		return "image/webp"
	}
	if data[0] == 'B' && data[1] == 'M' {
		return "image/bmp"
	}
	return ""
}

// ---------------------------------------------------------------------------
// HTML <img> tag data URI resolution
// ---------------------------------------------------------------------------

// imgHTMLDataURI matches HTML <img> tags with inline data:image/*;base64,... in the src attribute.
var imgHTMLDataURI = regexp.MustCompile(
	`(?i)<img\s[^>]*?src\s*=\s*["'](data:image/[^;]+;base64,[^"']+)["'][^>]*?/?\s*>`,
)

// ResolveHTMLDataURIImages finds <img src="data:image/*;base64,..."> tags in markdown,
// decodes the images, stores them via fileSvc, and replaces each tag with a markdown
// image reference using the storage URL.
func (r *ImageResolver) ResolveHTMLDataURIImages(
	ctx context.Context,
	markdown string,
	fileSvc interfaces.FileService,
	tenantID uint64,
) (updatedMarkdown string, images []StoredImage, err error) {
	matches := imgHTMLDataURI.FindAllStringSubmatchIndex(markdown, -1)
	if len(matches) == 0 {
		return markdown, nil, nil
	}

	processed := 0
	for i := len(matches) - 1; i >= 0; i-- {
		if processed >= maxRemoteImages {
			break
		}
		m := matches[i]
		dataURI := markdown[m[2]:m[3]]
		mimeType, payload, ok := parseImageDataURI(dataURI)
		if !ok {
			continue
		}
		payload = cleanBase64Payload(payload)
		if payload == "" {
			continue
		}
		data, decErr := decodeBase64Flexible(payload)
		if decErr != nil {
			log.Printf("WARN: HTML img data URI base64 decode failed: %v", decErr)
			continue
		}
		if len(data) > maxRemoteImageSize {
			continue
		}
		if isIconImage(data) {
			markdown = markdown[:m[0]] + markdown[m[1]:]
			continue
		}
		ext := extFromMime(mimeType)
		if ext == "" {
			ext = ".png"
		}
		fileName := uuid.New().String() + ext
		servingURL, saveErr := fileSvc.SaveBytes(ctx, data, tenantID, fileName, false)
		if saveErr != nil {
			log.Printf("WARN: failed to save HTML img data URI image: %v", saveErr)
			continue
		}
		images = append(images, StoredImage{
			OriginalRef: "html-img-data-uri",
			ServingURL:  servingURL,
			MimeType:    mimeType,
		})
		markdown = markdown[:m[0]] + fmt.Sprintf("![image](%s)", servingURL) + markdown[m[1]:]
		processed++
	}
	return markdown, images, nil
}

// ---------------------------------------------------------------------------
// Bare base64/data URI resolution (catch-all)
// ---------------------------------------------------------------------------

// bareDataURIPattern matches standalone data:image/*;base64,... strings.
var bareDataURIPattern = regexp.MustCompile(
	`(?i)data:image/([^;\s]+);base64,([A-Za-z0-9+/=]{100,})`,
)

// bareBase64CommaPrefixed matches base64,DATA patterns (partial data URIs missing the mime prefix).
var bareBase64CommaPrefixed = regexp.MustCompile(
	`base64,([A-Za-z0-9+/=]{200,})`,
)

// ResolveBareBase64Content finds remaining bare data URIs and base64 image content
// in the markdown text, decodes and stores them, and replaces with image references.
// This acts as a catch-all after the standard markdown and HTML resolvers.
func (r *ImageResolver) ResolveBareBase64Content(
	ctx context.Context,
	markdown string,
	fileSvc interfaces.FileService,
	tenantID uint64,
) (updatedMarkdown string, images []StoredImage, err error) {
	md, imgs1 := r.resolveBareDataURIs(ctx, markdown, fileSvc, tenantID)
	markdown = md
	images = append(images, imgs1...)

	md2, imgs2 := r.resolveBareBase64Prefix(ctx, markdown, fileSvc, tenantID)
	markdown = md2
	images = append(images, imgs2...)

	return markdown, images, nil
}

func (r *ImageResolver) resolveBareDataURIs(
	ctx context.Context,
	markdown string,
	fileSvc interfaces.FileService,
	tenantID uint64,
) (string, []StoredImage) {
	matches := bareDataURIPattern.FindAllStringSubmatchIndex(markdown, -1)
	if len(matches) == 0 {
		return markdown, nil
	}

	var images []StoredImage
	processed := 0
	for i := len(matches) - 1; i >= 0; i-- {
		if processed >= maxRemoteImages {
			break
		}
		m := matches[i]
		// Check context: skip HTML src attributes, but handle broken markdown refs
		insideWrapper := false
		if m[0] > 0 {
			prev := markdown[m[0]-1]
			if prev == '"' || prev == '\'' {
				continue // inside HTML attribute — already handled by ResolveHTMLDataURIImages
			}
			if prev == '(' {
				insideWrapper = true // likely inside a broken ![...](...) ref
			}
		}
		mimeSubtype := strings.ToLower(markdown[m[2]:m[3]])
		payload := markdown[m[4]:m[5]]
		mimeType := "image/" + mimeSubtype

		payload = cleanBase64Payload(payload)
		if payload == "" {
			continue
		}
		data, decErr := decodeBase64Flexible(payload)
		if decErr != nil {
			log.Printf("WARN: bare data URI base64 decode failed: %v", decErr)
			continue
		}
		if len(data) > maxRemoteImageSize {
			continue
		}
		if isIconImage(data) {
			markdown = markdown[:m[0]] + markdown[m[1]:]
			continue
		}
		ext := extFromMime(mimeType)
		if ext == "" {
			ext = ".png"
		}
		fileName := uuid.New().String() + ext
		servingURL, saveErr := fileSvc.SaveBytes(ctx, data, tenantID, fileName, false)
		if saveErr != nil {
			log.Printf("WARN: failed to save bare data URI image: %v", saveErr)
			continue
		}
		images = append(images, StoredImage{
			OriginalRef: "bare-data-uri",
			ServingURL:  servingURL,
			MimeType:    mimeType,
		})
		if insideWrapper {
			// Inside a broken markdown ref like ![weird]alt](data:...) — replace data URI only
			markdown = markdown[:m[0]] + servingURL + markdown[m[1]:]
		} else {
			markdown = markdown[:m[0]] + fmt.Sprintf("![image](%s)", servingURL) + markdown[m[1]:]
		}
		processed++
	}
	return markdown, images
}

func (r *ImageResolver) resolveBareBase64Prefix(
	ctx context.Context,
	markdown string,
	fileSvc interfaces.FileService,
	tenantID uint64,
) (string, []StoredImage) {
	matches := bareBase64CommaPrefixed.FindAllStringSubmatchIndex(markdown, -1)
	if len(matches) == 0 {
		return markdown, nil
	}

	var images []StoredImage
	processed := 0
	for i := len(matches) - 1; i >= 0; i-- {
		if processed >= maxRemoteImages {
			break
		}
		m := matches[i]
		// Skip if preceded by ';' — this is part of a data URI handled above
		if m[0] > 0 && markdown[m[0]-1] == ';' {
			continue
		}
		payload := markdown[m[2]:m[3]]
		payload = cleanBase64Payload(payload)
		if payload == "" {
			continue
		}
		data, decErr := decodeBase64Flexible(payload)
		if decErr != nil {
			continue
		}
		if len(data) > maxRemoteImageSize {
			continue
		}
		mimeType := sniffImageMime(data)
		if mimeType == "" {
			continue
		}
		if isIconImage(data) {
			markdown = markdown[:m[0]] + markdown[m[1]:]
			continue
		}
		ext := extFromMime(mimeType)
		if ext == "" {
			ext = ".png"
		}
		fileName := uuid.New().String() + ext
		servingURL, saveErr := fileSvc.SaveBytes(ctx, data, tenantID, fileName, false)
		if saveErr != nil {
			log.Printf("WARN: failed to save bare base64 image: %v", saveErr)
			continue
		}
		images = append(images, StoredImage{
			OriginalRef: "bare-base64",
			ServingURL:  servingURL,
			MimeType:    mimeType,
		})
		markdown = markdown[:m[0]] + fmt.Sprintf("![image](%s)", servingURL) + markdown[m[1]:]
		processed++
	}
	return markdown, images
}

// ---------------------------------------------------------------------------
// Remote image resolution (for manual / web-clipped markdown content)
// ---------------------------------------------------------------------------

const (
	// maxRemoteImageSize is the maximum allowed size for a single remote image download.
	maxRemoteImageSize = 10 * 1024 * 1024 // 10 MB
	// maxRemoteImages is the maximum number of remote images to process per document.
	maxRemoteImages = 30
	// remoteImageFetchTimeout is the per-image HTTP request timeout.
	remoteImageFetchTimeout = 15 * time.Second
)

// reLinkedImage matches the nested [![alt](img_url)](link_url) pattern where
// an image is wrapped inside a Markdown link. We unwrap it to just ![alt](img_url)
// so that downstream image-processing regexes only have to handle the flat form.
// The URL groups support one level of balanced parentheses.
var reLinkedImage = regexp.MustCompile(
	`\[!\[([^\]]*)\]\(([^()\s]*(?:\([^)]*\)[^()\s]*)*)\)\]` + // [![alt](img_url)]
		`\([^()\s]*(?:\([^)]*\)[^()\s]*)*\)`, // (link_url) — captured but discarded
)

// UnwrapLinkedImages replaces all [![alt](img_url)](link_url) occurrences in
// the markdown with just ![alt](img_url), stripping the outer link wrapper.
// This should be called before any image-extraction regex so that only the
// flat ![alt](url) form needs to be handled.
func UnwrapLinkedImages(markdown string) string {
	return reLinkedImage.ReplaceAllString(markdown, "![$1]($2)")
}

// imgMarkdownPattern matches Markdown image syntax: ![alt](url).
// The alt-text group uses .*? (non-greedy) to allow literal ] in alt text.
// The URL group supports one level of balanced parentheses so that URLs
// like https://example.com/item_(abc)/123 are captured in full.
var imgMarkdownPattern = regexp.MustCompile(`!\[(.*?)\]\(([^()\s]*(?:\([^)]*\)[^()\s]*)*)\)`)

// imgMarkdownDataURI matches markdown images whose URL is a data:image/*;base64,...
// payload. (?i) applies to the whole parenthesized data URI.
// The alt-text group uses .*? (non-greedy) to allow literal ] inside alt text
// (e.g. file paths like ![C:\img]name.png](data:...)).
var imgMarkdownDataURI = regexp.MustCompile(
	`!\[(.*?)\]\((?i:(data:image/[^;]+;base64,\s*[^)]+))\)`,
)

// parseImageDataURI splits a data URI into image MIME type and base64 payload.
func parseImageDataURI(dataURI string) (mimeType string, b64Payload string, ok bool) {
	const sep = ";base64,"
	idx := strings.Index(strings.ToLower(dataURI), sep)
	if idx < 0 {
		return "", "", false
	}
	meta := strings.TrimSpace(dataURI[:idx])
	const prefix = "data:image/"
	if len(meta) < len(prefix) || !strings.EqualFold(meta[:len(prefix)], prefix) {
		return "", "", false
	}
	sub := strings.TrimSpace(meta[len(prefix):])
	mimeType = "image/" + strings.ToLower(sub)
	b64Payload = strings.TrimSpace(dataURI[idx+len(sep):])
	if b64Payload == "" {
		return "", "", false
	}
	return mimeType, b64Payload, true
}

// ResolveDataURIImages finds embedded data:image/*;base64 images in markdown,
// decodes them, stores via fileSvc, and replaces each reference with the returned
// provider URL (same limits as remote images: count and decoded size).
func (r *ImageResolver) ResolveDataURIImages(
	ctx context.Context,
	markdown string,
	fileSvc interfaces.FileService,
	tenantID uint64,
) (updatedMarkdown string, images []StoredImage, err error) {
	markdown = UnwrapLinkedImages(markdown)
	matches := imgMarkdownDataURI.FindAllStringSubmatchIndex(markdown, -1)
	if len(matches) == 0 {
		return markdown, nil, nil
	}

	processed := 0
	for i := len(matches) - 1; i >= 0; i-- {
		if processed >= maxRemoteImages {
			break
		}
		m := matches[i]
		if len(m) < 6 {
			continue
		}
		dataURI := markdown[m[4]:m[5]]
		mimeType, payload, ok := parseImageDataURI(dataURI)
		if !ok {
			continue
		}
		payload = cleanBase64Payload(payload)
		if payload == "" {
			continue
		}
		data, decErr := decodeBase64Flexible(payload)
		if decErr != nil {
			log.Printf("WARN: data URI base64 decode failed: %v", decErr)
			continue
		}
		if len(data) > maxRemoteImageSize {
			log.Printf("WARN: data URI image exceeds size limit (%d bytes)", maxRemoteImageSize)
			continue
		}
		if isIconImage(data) {
			markdown = markdown[:m[0]] + markdown[m[1]:]
			continue
		}
		ext := extFromMime(mimeType)
		if ext == "" {
			ext = ".png"
		}
		fileName := uuid.New().String() + ext
		servingURL, saveErr := fileSvc.SaveBytes(ctx, data, tenantID, fileName, false)
		if saveErr != nil {
			log.Printf("WARN: failed to save data URI image: %v", saveErr)
			continue
		}
		images = append(images, StoredImage{
			OriginalRef: dataURI,
			ServingURL:  servingURL,
			MimeType:    mimeType,
		})
		markdown = markdown[:m[4]] + servingURL + markdown[m[5]:]
		processed++
	}
	return markdown, images, nil
}

// ResolveRemoteImages scans a Markdown string for image references whose URL
// is http:// or https://, downloads each one through an SSRF-safe HTTP client,
// uploads the bytes via fileSvc, and replaces the original URL with the
// provider:// serving URL.
//
// Images that fail SSRF validation, exceed size limits, or cannot be downloaded
// are left unchanged (the original URL is preserved).
//
// Returns the updated Markdown and a list of successfully stored images.
func (r *ImageResolver) ResolveRemoteImages(
	ctx context.Context,
	markdown string,
	fileSvc interfaces.FileService,
	tenantID uint64,
) (updatedMarkdown string, images []StoredImage, err error) {
	markdown = UnwrapLinkedImages(markdown)

	matches := imgMarkdownPattern.FindAllStringSubmatchIndex(markdown, -1)
	if len(matches) == 0 {
		return markdown, nil, nil
	}

	// Build a shared SSRF-safe HTTP client for all downloads.
	httpClient := secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
		Timeout:      remoteImageFetchTimeout,
		MaxRedirects: 5,
	})

	processed := 0

	// Process in reverse order so that earlier indices stay valid after replacements.
	for i := len(matches) - 1; i >= 0; i-- {
		if processed >= maxRemoteImages {
			break
		}
		m := matches[i]
		imgURL := markdown[m[4]:m[5]] // group 2: the URL

		// Only process remote http(s) URLs.
		if !strings.HasPrefix(imgURL, "http://") && !strings.HasPrefix(imgURL, "https://") {
			continue
		}

		// Already a provider scheme — skip.
		if isProviderScheme(imgURL) {
			continue
		}

		// --- SSRF check (centralised entry-point with whitelist support) ---
		if err := secutils.ValidateURLForSSRF(imgURL); err != nil {
			log.Printf("WARN: remote image blocked by SSRF check (%v): %s", err, imgURL)
			continue
		}

		// --- Download ---
		data, mimeType, dlErr := downloadImage(ctx, httpClient, imgURL)
		if dlErr != nil {
			log.Printf("WARN: failed to download remote image %s: %v", imgURL, dlErr)
			continue
		}

		// Filter out icons / tiny decorative images.
		if isIconImage(data) {
			continue
		}

		// Determine file extension.
		ext := extFromMime(mimeType)
		if ext == "" {
			ext = extFromURLPath(imgURL)
		}
		if ext == "" {
			ext = ".png" // safe default
		}

		// --- Upload to storage ---
		fileName := uuid.New().String() + ext
		servingURL, saveErr := fileSvc.SaveBytes(ctx, data, tenantID, fileName, false)
		if saveErr != nil {
			log.Printf("WARN: failed to save remote image %s: %v", imgURL, saveErr)
			continue
		}

		images = append(images, StoredImage{
			OriginalRef: imgURL,
			ServingURL:  servingURL,
			MimeType:    mimeType,
		})

		// Replace URL in markdown.
		markdown = markdown[:m[4]] + servingURL + markdown[m[5]:]
		processed++
	}

	return markdown, images, nil
}

// downloadImage fetches an image from remoteURL using the provided SSRF-safe
// client. It validates Content-Type and enforces maxRemoteImageSize.
func downloadImage(ctx context.Context, client *http.Client, remoteURL string) (data []byte, mimeType string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	// Some CDNs require a browser-like User-Agent.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; WeKnora/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("HTTP GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	// Determine MIME type from Content-Type header.
	ct := resp.Header.Get("Content-Type")
	mimeType, _, _ = mime.ParseMediaType(ct)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Only allow image content types (or octet-stream which we sniff later).
	if !strings.HasPrefix(mimeType, "image/") && mimeType != "application/octet-stream" {
		return nil, "", fmt.Errorf("non-image content type: %s", mimeType)
	}

	// Read body with size limit.
	limited := io.LimitReader(resp.Body, maxRemoteImageSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}
	if len(body) > maxRemoteImageSize {
		return nil, "", fmt.Errorf("image exceeds %d bytes limit", maxRemoteImageSize)
	}

	// If MIME was octet-stream, sniff the real type from body.
	if mimeType == "application/octet-stream" {
		detected := http.DetectContentType(body)
		if strings.HasPrefix(detected, "image/") {
			mimeType = detected
		} else {
			return nil, "", fmt.Errorf("downloaded data is not an image (sniffed: %s)", detected)
		}
	}

	return body, mimeType, nil
}

// extFromURLPath extracts the image file extension from the URL path segment.
func extFromURLPath(rawURL string) string {
	p := path.Ext(path.Base(rawURL))
	switch strings.ToLower(p) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return strings.ToLower(p)
	default:
		return ""
	}
}
