package utils

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// uploadFilenameLegacyEncodings lists ANSI code pages commonly used by Windows
// clients (notably curl -F) when building multipart Content-Disposition
// filename= parameters without RFC 5987 filename*. GBK/CP936 is first because
// that is the encoding reported for zh-CN Windows uploads (#4268).
var uploadFilenameLegacyEncodings = []encoding.Encoding{
	simplifiedchinese.GBK,
}

// NormalizeUploadFilename reinterprets a multipart upload filename that arrived
// as legacy Windows ANSI bytes (most commonly GBK on zh-CN systems) as UTF-8.
//
// Valid UTF-8 input is returned unchanged. When no legacy decoder yields a clean
// UTF-8 string, the original value is returned so callers can still reject it
// via ValidateInput and surface an encoding-specific error.
//
// This helper does not relax XSS / control-character checks; callers must still
// run ValidateInput on the result before persisting the name.
func NormalizeUploadFilename(name string) string {
	if name == "" || utf8.ValidString(name) {
		return name
	}

	raw := []byte(name)
	for _, enc := range uploadFilenameLegacyEncodings {
		decoded, err := enc.NewDecoder().Bytes(raw)
		if err != nil || len(decoded) == 0 {
			continue
		}
		out := string(decoded)
		// Reject replacement runes from a soft decoder; we only accept a clean
		// round-trip to UTF-8.
		if !utf8.ValidString(out) || strings.ContainsRune(out, '\uFFFD') {
			continue
		}
		return out
	}
	return name
}

// IsValidUTF8String reports whether s is valid UTF-8. Exported for callers that
// want to distinguish encoding failures from other ValidateInput rejects.
func IsValidUTF8String(s string) bool {
	return utf8.ValidString(s)
}

// RawDispositionFilename extracts the raw filename= value bytes from a
// Content-Disposition header *before* quoted-string unescaping.
//
// Go's mime/multipart parser unescapes \" and \\ inside quoted filenames into
// FileHeader.Filename. When a legacy ANSI page (e.g. GBK) uses 0x5C as a trail
// byte, that unescape silently drops a character. Comparing these raw bytes to
// []byte(FileHeader.Filename) detects that case (#4269 case E).
//
// Only the plain filename= parameter is considered (not RFC 5987 filename*).
// ok is false when no filename= parameter is present.
func RawDispositionFilename(disposition string) (raw []byte, ok bool) {
	if disposition == "" {
		return nil, false
	}
	b := []byte(disposition)
	// Case-insensitive search for filename= that is not filename*=
	lower := strings.ToLower(disposition)
	start := 0
	for {
		i := strings.Index(lower[start:], "filename")
		if i < 0 {
			return nil, false
		}
		i += start
		rest := lower[i+len("filename"):]
		if strings.HasPrefix(rest, "*=") {
			// RFC 5987 filename* — skip; callers that set this already sent UTF-8.
			start = i + len("filename*")
			continue
		}
		if !strings.HasPrefix(rest, "=") {
			start = i + len("filename")
			continue
		}
		valStart := i + len("filename=")
		if valStart >= len(b) {
			return nil, false
		}
		if b[valStart] == '"' {
			// Quoted: copy until closing " without unescaping.
			valStart++
			out := make([]byte, 0, 64)
			for j := valStart; j < len(b); j++ {
				if b[j] == '"' {
					return out, true
				}
				out = append(out, b[j])
			}
			return out, true // unterminated quote: best-effort
		}
		// Token: until ; or end
		end := valStart
		for end < len(b) && b[end] != ';' {
			end++
		}
		return bytes.TrimSpace(b[valStart:end]), true
	}
}

// ResolveMultipartUploadFilename chooses which byte view of an upload filename
// to feed into NormalizeUploadFilename.
//
// When the raw Content-Disposition filename= bytes differ from
// []byte(parsedFilename), the multipart parser consumed an escape (often a GBK
// trail 0x5C). Prefer the raw bytes so a subsequent NormalizeUploadFilename
// recovers the full CJK title instead of silently shortening it.
func ResolveMultipartUploadFilename(parsedFilename, contentDisposition string) string {
	raw, ok := RawDispositionFilename(contentDisposition)
	if !ok || len(raw) == 0 {
		return NormalizeUploadFilename(parsedFilename)
	}
	if bytes.Equal(raw, []byte(parsedFilename)) {
		return NormalizeUploadFilename(parsedFilename)
	}
	return NormalizeUploadFilename(string(raw))
}
