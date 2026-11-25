package handler

import (
	"mime"
	"mime/multipart"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func recoverWindowsMultipartFilename(file *multipart.FileHeader) string {
	// Go takes the basename before decoding the filename. On Windows this
	// can split a GBK character at its 0x5c trail byte.
	_, params, err := mime.ParseMediaType(file.Header.Get("Content-Disposition"))
	rawName := params["filename"]
	backslashes := strings.Count(rawName, "\\")
	if err != nil || rawName == file.Filename || backslashes == 0 || utf8.ValidString(rawName) {
		return file.Filename
	}

	name, err := simplifiedchinese.GBK.NewDecoder().String(rawName)
	if err != nil || strings.ContainsRune(name, unicode.ReplacementChar) || strings.Count(name, "\\") == backslashes {
		return file.Filename
	}
	return filepath.Base(name)
}
