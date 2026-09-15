package core

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// ImageRelName is the stable basename used in parent Markdown placeholders
// and as the child knowledge FileName stem. Extension is filled in after
// the download is sniffed (png/jpg/gif).
func ImageRelName(token, ext string) string {
	if ext == "" {
		ext = ".png"
	}
	return "image-" + token + ext
}

// BoardRelName is the stable basename for an embedded whiteboard image.
func BoardRelName(whiteboardID, ext string) string {
	if ext == "" {
		ext = ".png"
	}
	return "board-" + whiteboardID + ext
}

// DocSubtreeDir is the knowledge-base folder_path segment for one Feishu
// cloud document and its attachment/image/board children. Display-only;
// physical storage is unchanged.
func DocSubtreeDir(title string) string {
	dir := SanitizeFileName(title)
	if len(dir) > types.MaxKnowledgeFolderSegmentLength {
		dir = truncateUTF8(dir, types.MaxKnowledgeFolderSegmentLength)
	}
	return dir
}

// NestedFileName prefixes a basename with the document folder so
// CreateKnowledgeFromFile splits it into folder_path + file name.
func NestedFileName(title, base string) string {
	return DocSubtreeDir(title) + "/" + base
}
