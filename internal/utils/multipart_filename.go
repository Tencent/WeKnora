package utils

import (
	"mime"
	"mime/multipart"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// RestoreMultipartFilename recovers a GBK filename truncated by filepath.Base
// inside mime/multipart on Windows. The original parameter remains in Header.
// Other filenames keep the standard library's path handling unchanged.
func RestoreMultipartFilename(file *multipart.FileHeader) {
	if file == nil || file.Header == nil {
		return
	}
	_, params, err := mime.ParseMediaType(file.Header.Get("Content-Disposition"))
	if err != nil {
		return
	}
	raw := params["filename"]
	if raw == "" || raw == file.Filename || utf8.ValidString(raw) || !strings.Contains(raw, `\`) {
		return
	}
	decoded, err := simplifiedchinese.GBK.NewDecoder().String(raw)
	if err != nil || strings.ContainsRune(decoded, utf8.RuneError) ||
		strings.ContainsAny(decoded, `/\`) || strings.TrimSpace(decoded) == "" {
		return
	}
	for _, r := range decoded {
		if r < 32 || r == 127 {
			return
		}
	}
	file.Filename = decoded
}
