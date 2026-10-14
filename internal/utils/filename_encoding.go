package utils

import (
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
