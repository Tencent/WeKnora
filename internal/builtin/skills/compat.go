package skills

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"sync"
)

// Preserve the exact resources of the published controller without pointer
// support. Existing sessions keep their instructions and basic browser tools.
//
//go:embed compat/browser-2026.09.1.zip
var legacyBrowserArchive []byte

var (
	legacyBrowserOnce   sync.Once
	legacyBrowserPack   map[string][]byte
	legacyBrowserDigest string
)

func legacyBrowserFiles() map[string][]byte {
	legacyBrowserOnce.Do(func() {
		archive, err := zip.NewReader(bytes.NewReader(legacyBrowserArchive), int64(len(legacyBrowserArchive)))
		if err != nil {
			panic(err)
		}
		legacyBrowserPack = map[string][]byte{}
		for _, file := range archive.File {
			reader, err := file.Open()
			if err != nil {
				panic(err)
			}
			data, err := io.ReadAll(reader)
			closeErr := reader.Close()
			if err != nil {
				panic(err)
			}
			if closeErr != nil {
				panic(closeErr)
			}
			legacyBrowserPack[file.Name] = data
		}
		legacyBrowserDigest = DigestFiles(legacyBrowserPack)
	})
	return legacyBrowserPack
}

func isLegacyBrowserDigest(name, digest string) bool {
	if name != "browser" {
		return false
	}
	legacyBrowserFiles()
	return digest == legacyBrowserDigest
}

// FilesForEntry never serves a new controller's resources to an older image.
func FilesForEntry(entry Entry) (map[string][]byte, error) {
	if isLegacyBrowserDigest(entry.Name, entry.Digest) {
		result := map[string][]byte{}
		for name, data := range legacyBrowserFiles() {
			result[name] = bytes.Clone(data)
		}
		return result, nil
	}
	files, err := Files(entry.ID)
	if err != nil {
		return nil, err
	}
	if DigestFiles(files) != entry.Digest {
		return nil, fmt.Errorf("unsupported skill digest: %s", entry.Name)
	}
	return files, nil
}
