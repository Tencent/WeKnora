package utils

import (
	"sort"
	"strings"
)

// supportedImportFileExtensions is the single source of truth for extensions
// accepted by every knowledge import path: direct upload, file-URL download,
// the worker's post-download re-check and data source connectors. Keeping one
// set avoids the drift that let direct upload accept xlsx while URL import
// rejected it (#2447).
var supportedImportFileExtensions = map[string]struct{}{
	"pdf": {}, "txt": {}, "docx": {}, "doc": {}, "epub": {},
	"html": {}, "htm": {}, "mhtml": {}, "md": {}, "markdown": {},
	"xmind": {},
	"png":   {}, "jpg": {}, "jpeg": {}, "gif": {},
	"csv": {}, "xlsx": {}, "xls": {}, "pptx": {}, "ppt": {}, "json": {},
	"mp3": {}, "wav": {}, "m4a": {}, "flac": {}, "ogg": {},
}

// NormalizeImportExtension lowercases an extension and strips surrounding
// whitespace and one leading dot, so callers can pass "xlsx", ".XLSX" or a
// raw user-supplied file_type.
func NormalizeImportExtension(ext string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
}

// IsSupportedImportExtension reports whether a bare extension can be imported.
func IsSupportedImportExtension(ext string) bool {
	ext = NormalizeImportExtension(ext)
	if ext == "" {
		return false
	}
	_, ok := supportedImportFileExtensions[ext]
	return ok
}

// SupportedImportExtensions returns a sorted copy of the supported extensions.
func SupportedImportExtensions() []string {
	out := make([]string, 0, len(supportedImportFileExtensions))
	for ext := range supportedImportFileExtensions {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out
}
