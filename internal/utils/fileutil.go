package utils

import "strings"

// IsActiveBrowserContentExt reports whether a file extension can execute script
// or otherwise participate as active same-origin browser content when served
// inline. User-uploaded files with these extensions must be downloaded instead
// of previewed inline.
func IsActiveBrowserContentExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".svg", ".svgz", ".html", ".htm", ".xhtml", ".xml", ".js", ".mjs", ".css":
		return true
	default:
		return false
	}
}

// GetContentTypeByExt returns the content type based on file extension
func GetContentTypeByExt(ext string) string {
	if IsActiveBrowserContentExt(ext) {
		return "application/octet-stream"
	}
	switch strings.ToLower(ext) {
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".json":
		return "application/json"
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	case ".md":
		return "text/markdown; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// SafeContentTypeByFilename returns a browser-safe content type for serving
// user-controlled files and whether that file may be displayed inline.
func SafeContentTypeByFilename(filename string) (string, bool) {
	ext := filename
	if idx := strings.LastIndex(ext, "."); idx >= 0 {
		ext = ext[idx:]
	} else {
		ext = ""
	}
	if IsActiveBrowserContentExt(ext) {
		return "application/octet-stream", false
	}
	return GetContentTypeByExt(ext), true
}
