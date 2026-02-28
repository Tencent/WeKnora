package docparser

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
)

// StoredImage describes an image that has been saved to local storage.
type StoredImage struct {
	OriginalRef string // reference in the original markdown
	LocalPath   string // absolute path on disk (/data/files/images/xxx.png)
	ServingURL  string // URL for frontend access (/files/images/xxx.png)
	MimeType    string
}

// ImageResolver reads images from a DocReader ReadResult (temp dir or shared storage)
// and saves them to the local file-serving directory, replacing markdown references.
type ImageResolver struct {
	// LocalStorageDir is where images are persisted (e.g. /data/files)
	LocalStorageDir string
	// URLPrefix is the HTTP path prefix for serving (e.g. /files)
	URLPrefix string
}

// NewImageResolver creates a resolver with defaults from environment.
func NewImageResolver(localStorageDir, urlPrefix string) *ImageResolver {
	if localStorageDir == "" {
		localStorageDir = "/data/files"
	}
	if urlPrefix == "" {
		urlPrefix = "/files"
	}
	return &ImageResolver{
		LocalStorageDir: localStorageDir,
		URLPrefix:       urlPrefix,
	}
}

// ResolveAndStore reads images from the convert result and persists them.
// It returns the updated markdown with replaced image refs and a list of stored images.
func (r *ImageResolver) ResolveAndStore(
	result *types.ReadResult,
) (updatedMarkdown string, images []StoredImage, err error) {
	markdown := result.MarkdownContent
	if len(result.ImageRefs) == 0 && result.ImageDirPath == "" {
		return markdown, nil, nil
	}

	imageDir := filepath.Join(r.LocalStorageDir, "images")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return markdown, nil, fmt.Errorf("create image dir: %w", err)
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

		// Skip already-resolved URLs
		if strings.HasPrefix(refPath, "http://") || strings.HasPrefix(refPath, "https://") || strings.HasPrefix(refPath, "/files/") {
			continue
		}

		// Try to find the image data
		imgBytes, mimeType, err := r.readImageBytes(result, refPath, refMap)
		if err != nil || imgBytes == nil {
			continue
		}

		// Determine extension
		ext := extFromMime(mimeType)
		if ext == "" {
			ext = filepath.Ext(refPath)
		}
		if ext == "" {
			ext = ".png"
		}

		// Save to local storage
		newName := uuid.New().String() + ext
		destPath := filepath.Join(imageDir, newName)
		if writeErr := os.WriteFile(destPath, imgBytes, 0o644); writeErr != nil {
			logger.Printf("WARN: failed to save image %s: %v", refPath, writeErr)
			continue
		}

		servingURL := r.URLPrefix + "/images/" + newName

		images = append(images, StoredImage{
			OriginalRef: refPath,
			LocalPath:   destPath,
			ServingURL:  servingURL,
			MimeType:    mimeType,
		})

		// Replace in markdown
		markdown = markdown[:m[4]] + servingURL + markdown[m[5]:]
	}

	return markdown, images, nil
}

// readImageBytes resolves image data with cascading fallback:
//   - Mode A: shared temp directory (same-machine / Docker volume)
//   - Mode C: inline bytes carried in gRPC response (universal, cross-machine)
//   - Mode B: shared object storage via storage_key (future)
func (r *ImageResolver) readImageBytes(
	result *types.ReadResult,
	refPath string,
	refMap map[string]types.ImageRef,
) ([]byte, string, error) {
	ref, found := refMap[refPath]

	// Mode A: read from shared temp directory
	if result.ImageDirPath != "" {
		candidates := []string{
			filepath.Join(result.ImageDirPath, "images", filepath.Base(refPath)),
			filepath.Join(result.ImageDirPath, refPath),
		}
		for _, candidate := range candidates {
			data, err := os.ReadFile(candidate)
			if err == nil {
				mime := "application/octet-stream"
				if found {
					mime = ref.MimeType
				}
				if mime == "" {
					mime = mimeFromExt(filepath.Ext(candidate))
				}
				return data, mime, nil
			}
		}
	}

	// Mode C: inline image bytes from gRPC/HTTP response
	if found && len(ref.ImageData) > 0 {
		mime := ref.MimeType
		if mime == "" {
			mime = mimeFromExt(filepath.Ext(ref.Filename))
		}
		return ref.ImageData, mime, nil
	}

	// Mode B: shared object storage — storage_key is a download URL returned by storage.upload_bytes()
	if found && ref.StorageKey != "" {
		data, err := downloadImage(ref.StorageKey)
		if err != nil {
			return nil, "", fmt.Errorf("download image from storage %s: %w", ref.StorageKey, err)
		}
		mime := ref.MimeType
		if mime == "" {
			mime = mimeFromExt(filepath.Ext(ref.Filename))
		}
		return data, mime, nil
	}

	return nil, "", fmt.Errorf("image not found: %s", refPath)
}

func downloadImage(url string) ([]byte, error) {
	return utils.DownloadBytes(url)
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

func mimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
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
		return "application/octet-stream"
	}
}
