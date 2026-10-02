package types

import "strings"

// UnknownFileType is returned when a name carries no extension.
const UnknownFileType = "unknown"

// SupportedImportFileExtensions is the single source of truth for extensions
// accepted by every knowledge import path: direct upload, file-URL download,
// the worker's post-download re-check, and data-source connectors (RSS, GitLab,
// OPDS, …). Keeping one set avoids the drift that let direct upload accept xlsx
// while URL import rejected it (#2447).
//
// It lives in this package because both the ingestion service and the connector
// packages need it, and neither may import the other.
var SupportedImportFileExtensions = map[string]struct{}{
	"pdf": {}, "txt": {}, "docx": {}, "doc": {}, "epub": {},
	"html": {}, "htm": {}, "mhtml": {}, "md": {}, "markdown": {},
	"xmind": {},
	"png":   {}, "jpg": {}, "jpeg": {}, "gif": {},
	"csv": {}, "xlsx": {}, "xls": {}, "pptx": {}, "ppt": {}, "json": {},
	"mp3": {}, "wav": {}, "m4a": {}, "flac": {}, "ogg": {},
}

// NormalizeFileExtension lowercases an extension and strips a leading dot so
// callers can pass either "xlsx", ".XLSX", or a raw user-supplied file_type.
func NormalizeFileExtension(ext string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
}

// IsSupportedImportExtension reports whether a bare extension can be imported.
func IsSupportedImportExtension(ext string) bool {
	ext = NormalizeFileExtension(ext)
	if ext == "" || ext == UnknownFileType {
		return false
	}
	_, ok := SupportedImportFileExtensions[ext]
	return ok
}
