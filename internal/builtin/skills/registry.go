// Package skills contains reviewed skill resources shipped with WeKnora.
// External recommendations deliberately contain metadata only.
package skills

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed catalog.json
var catalogJSON []byte

//go:embed all:packages
var packages embed.FS

// Version identifies the currently published built-in bundle.
const Version = "2026.09.2"

// ImageRoot is the installation directory for preloaded skill packages.
const ImageRoot = "/opt/weknora/builtin/skills"

// Entry describes an embedded skill or an external recommendation.
type Entry struct {
	Runtime      string            `json:"runtime"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Title        map[string]string `json:"title"`
	Description  map[string]string `json:"description"`
	Category     string            `json:"category"`
	Distribution string            `json:"distribution"`
	Publisher    string            `json:"publisher"`
	License      string            `json:"license"`
	SourceURL    string            `json:"source_url"`
	LicenseURL   string            `json:"license_url"`
	DocsURL      string            `json:"docs_url,omitempty"`
	Version      string            `json:"version,omitempty"`
	Digest       string            `json:"digest,omitempty"`
}

// List returns catalog entries with digests for embedded resources.
func List() []Entry {
	var entries []Entry
	if err := json.Unmarshal(catalogJSON, &entries); err != nil {
		panic(err)
	}
	for i := range entries {
		if entries[i].Distribution == "builtin" {
			if entries[i].Version == "" {
				entries[i].Version = Version
			}
			files, err := Files(entries[i].ID)
			if err != nil {
				panic(err)
			}
			entries[i].Digest = DigestFiles(files)
		}
	}
	return entries
}

// Files checks membership before touching the embedded filesystem. An external
// entry can never become installable by submitting its id to an install API.
func Files(id string) (map[string][]byte, error) {
	var entries []Entry
	if err := json.Unmarshal(catalogJSON, &entries); err != nil {
		return nil, err
	}
	name := ""
	for _, entry := range entries {
		if entry.ID == id && entry.Distribution == "builtin" {
			name = entry.Name
			break
		}
	}
	if name == "" {
		return nil, fmt.Errorf("skill %q is not a bundled skill", id)
	}
	root := "packages/" + name
	files := make(map[string][]byte)
	err := fs.WalkDir(packages, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := packages.ReadFile(p)
		if err == nil {
			files[p[len(root)+1:]] = data
		}
		return err
	})
	return files, err
}

// DigestFiles is shared with the image build: sorted path, NUL, contents, NUL.
// It includes instructions, licenses and dependency declarations, not just code.
func DigestFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(files[name])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Archive creates a reproducible archive for a built-in catalog entry.
func Archive(id string) ([]byte, error) {
	files, err := Files(id)
	if err != nil {
		return nil, err
	}
	return archiveFiles(files)
}

func archiveFiles(files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, name := range names {
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		if _, err = f.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Matches reports whether files match the current embedded package.
func Matches(name string, files map[string][]byte) bool {
	pack, err := Files(name)
	return err == nil && DigestFiles(pack) == DigestFiles(files)
}

// MatchesArchiveDigest identifies our exact published archive, never a custom
// package that merely reuses a builtin's name or version.
func MatchesArchiveDigest(name, digest string) bool {
	archive, err := Archive(name)
	if err != nil || digest == "" {
		return false
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) == digest {
		return true
	}
	if name == "browser" {
		oldArchive, err := archiveFiles(legacyBrowserFiles())
		if err == nil {
			oldSum := sha256.Sum256(oldArchive)
			return hex.EncodeToString(oldSum[:]) == digest
		}
	}
	return false
}
