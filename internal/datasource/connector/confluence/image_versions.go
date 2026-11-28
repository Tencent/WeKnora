package confluence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

func refreshPageImagesEnabled(ds *types.DataSourceConfig) bool {
	v := ds.Settings["refresh_on_image_attachment_change"]
	return v == true || v == "true"
}

// pageImageVersion fingerprints a complete attachment listing, independently of
// the page-body version. Empty listings have the empty token so existing pages
// without images do not need a one-time refresh when the option is enabled.
func pageImageVersion(files []attachment) (string, bool) {
	type imageVersion struct {
		ID, Title, MediaType, Version string
	}
	var images []imageVersion
	known := true
	for _, a := range files {
		mediaType := a.MediaType
		if mediaType == "" {
			mediaType = a.Metadata.MediaType
		}
		if !strings.HasPrefix(strings.ToLower(mediaType), "image/") && !imageAttachmentName(a.Title) {
			continue
		}
		version := ""
		switch {
		case a.Version.Number > 0:
			version = fmt.Sprintf("v:%d", a.Version.Number)
		case a.Version.When != "":
			version = "t:" + a.Version.When
		case a.Version.CreatedAt != "":
			version = "t:" + a.Version.CreatedAt
		default:
			// An unknown revision must never prove that an image is unchanged.
			known = false
		}
		images = append(images, imageVersion{a.ID, a.Title, mediaType, version})
	}
	if len(images) == 0 {
		return "", true
	}
	sort.Slice(images, func(i, j int) bool { return images[i].ID < images[j].ID })
	raw, _ := json.Marshal(images)
	return fmt.Sprintf("%x", sha256.Sum256(raw)), known
}

func imageAttachmentName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".tif", ".tiff", ".avif", ".heic":
		return true
	default:
		return false
	}
}
