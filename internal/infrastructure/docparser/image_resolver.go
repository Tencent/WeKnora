package docparser

import (
	"bytes"
	"context"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"path/filepath"
	"regexp"
	"strings"

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
	markdown := result.MarkdownContent
	if len(result.ImageRefs) == 0 {
		return markdown, nil, nil
	}

	// Build a map of original_ref -> image ref for fast lookup
	refMap := make(map[string]types.ImageRef)
	for _, ref := range result.ImageRefs {
		refMap[ref.OriginalRef] = ref
	}

	// Process each image reference found in the markdown
	imgPattern := regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
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
	default:
		return ""
	}
}

// isProviderScheme checks if the path uses a provider:// scheme (local://, minio://, cos://, tos://).
func isProviderScheme(path string) bool {
	for _, prefix := range []string{"local://", "minio://", "cos://", "tos://"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
