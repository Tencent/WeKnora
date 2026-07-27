package skillpkg

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

type Limits struct {
	MaxArchiveBytes  int64
	MaxExpandedBytes int64
	MaxFileBytes     int64
	MaxFiles         int
	MaxPathDepth     int
}

func DefaultLimits() Limits {
	return Limits{
		MaxArchiveBytes: 20 << 20, MaxExpandedBytes: 100 << 20,
		MaxFileBytes: 20 << 20, MaxFiles: 500, MaxPathDepth: 12,
	}
}

type PackageError struct {
	Code string
	Path string
}

func (e *PackageError) Error() string {
	if e.Path == "" {
		return e.Code
	}
	return e.Code + ": " + e.Path
}

type Manifest struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description"`
	Category    string   `yaml:"category" json:"category"`
	Scripts     []string `yaml:"scripts" json:"scripts"`
}

type ValidatedFile struct {
	Path string
	Data []byte
	Mode uint32
}

type ValidatedPackage struct {
	Manifest   Manifest
	Files      []ValidatedFile
	HasScripts bool
	tenantID   uint64
	uploadID   string
}

type Validator struct {
	limits Limits
}

func NewValidator(limits Limits) *Validator {
	return &Validator{limits: limits}
}

func (v *Validator) Validate(reader io.Reader, declaredSize int64) (*ValidatedPackage, error) {
	if declaredSize < 0 || declaredSize > v.limits.MaxArchiveBytes {
		return nil, &PackageError{Code: "archive_size_exceeded"}
	}
	archive, err := io.ReadAll(io.LimitReader(reader, v.limits.MaxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	if int64(len(archive)) > v.limits.MaxArchiveBytes {
		return nil, &PackageError{Code: "archive_size_exceeded"}
	}
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, &PackageError{Code: "invalid_zip"}
	}
	if len(zr.File) > v.limits.MaxFiles {
		return nil, &PackageError{Code: "file_count_exceeded"}
	}
	prefix, err := archivePrefix(zr.File)
	if err != nil {
		return nil, err
	}
	return v.readEntries(zr.File, prefix)
}

func archivePrefix(files []*zip.File) (string, error) {
	for _, file := range files {
		clean, err := cleanArchivePath(file.Name)
		if err != nil {
			return "", err
		}
		if clean == "SKILL.md" {
			return "", nil
		}
	}
	var prefix string
	for _, file := range files {
		clean, err := cleanArchivePath(file.Name)
		if err != nil {
			return "", err
		}
		parts := strings.Split(clean, "/")
		if len(parts) < 2 {
			return "", &PackageError{Code: "missing_skill_manifest"}
		}
		if prefix == "" {
			prefix = parts[0]
		} else if prefix != parts[0] {
			return "", &PackageError{Code: "missing_skill_manifest"}
		}
	}
	return prefix + "/", nil
}

func (v *Validator) readEntries(files []*zip.File, prefix string) (*ValidatedPackage, error) {
	result := &ValidatedPackage{}
	seen := make(map[string]struct{}, len(files))
	var expanded int64
	for _, entry := range files {
		clean, err := cleanArchivePath(entry.Name)
		if err != nil {
			return nil, err
		}
		if prefix != "" {
			clean = strings.TrimPrefix(clean, prefix)
			if clean == "" {
				continue
			}
		}
		if err := v.validateEntry(entry, clean, seen); err != nil {
			return nil, err
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		data, err := readZipFile(entry, v.limits.MaxFileBytes)
		if err != nil {
			return nil, err
		}
		expanded += int64(len(data))
		if expanded > v.limits.MaxExpandedBytes {
			return nil, &PackageError{Code: "expanded_size_exceeded", Path: clean}
		}
		isScript := isScriptPath(clean)
		if strings.HasPrefix(clean, "scripts/") && !isScript {
			return nil, &PackageError{Code: "unsupported_script", Path: clean}
		}
		result.HasScripts = result.HasScripts || isScript
		result.Files = append(result.Files, ValidatedFile{Path: clean, Data: data, Mode: uint32(entry.Mode().Perm())})
	}
	manifestFile := findValidatedFile(result.Files, "SKILL.md")
	if manifestFile == nil {
		return nil, &PackageError{Code: "missing_skill_manifest"}
	}
	manifest, err := parseManifest(manifestFile.Data)
	if err != nil {
		return nil, err
	}
	result.Manifest = manifest
	registered := make(map[string]struct{}, len(manifest.Scripts))
	for _, script := range manifest.Scripts {
		clean, cleanErr := cleanArchivePath(script)
		if cleanErr != nil || clean != script || !isScriptPath(clean) {
			return nil, &PackageError{Code: "invalid_registered_script", Path: script}
		}
		registered[clean] = struct{}{}
	}
	for _, file := range result.Files {
		if isScriptPath(file.Path) {
			if _, ok := registered[file.Path]; !ok {
				return nil, &PackageError{Code: "unregistered_script", Path: file.Path}
			}
		}
	}
	return result, nil
}

func (v *Validator) validateEntry(entry *zip.File, clean string, seen map[string]struct{}) error {
	if entry.Flags&0x1 != 0 {
		return &PackageError{Code: "encrypted_zip", Path: clean}
	}
	mode := entry.Mode()
	if !mode.IsRegular() && !mode.IsDir() {
		return &PackageError{Code: "unsupported_entry_type", Path: clean}
	}
	if len(strings.Split(clean, "/")) > v.limits.MaxPathDepth {
		return &PackageError{Code: "path_depth_exceeded", Path: clean}
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".git" || part == ".env" || strings.HasPrefix(part, ".env.") {
			return &PackageError{Code: "reserved_path", Path: clean}
		}
	}
	key := strings.ToLower(norm.NFC.String(clean))
	if _, exists := seen[key]; exists {
		return &PackageError{Code: "duplicate_path", Path: clean}
	}
	seen[key] = struct{}{}
	return nil
}

func cleanArchivePath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", &PackageError{Code: "absolute_path", Path: name}
	}
	clean := path.Clean(name)
	if clean == "." || clean == "" {
		return "", &PackageError{Code: "empty_path", Path: name}
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", &PackageError{Code: "path_traversal", Path: name}
	}
	return norm.NFC.String(clean), nil
}

func readZipFile(file *zip.File, max int64) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, &PackageError{Code: "invalid_zip_entry", Path: file.Name}
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, max+1))
	if err != nil {
		return nil, &PackageError{Code: "invalid_zip_entry", Path: file.Name}
	}
	if int64(len(data)) > max {
		return nil, &PackageError{Code: "file_size_exceeded", Path: file.Name}
	}
	return data, nil
}

func findValidatedFile(files []ValidatedFile, name string) *ValidatedFile {
	for i := range files {
		if files[i].Path == name {
			return &files[i]
		}
	}
	return nil
}

var skillNamePattern = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}-]{0,49}$`)

func parseManifest(data []byte) (Manifest, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return Manifest{}, &PackageError{Code: "invalid_skill_manifest"}
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return Manifest{}, &PackageError{Code: "invalid_skill_manifest"}
	}
	var manifest Manifest
	if err := yaml.Unmarshal([]byte(text[4:4+end]), &manifest); err != nil {
		return Manifest{}, &PackageError{Code: "invalid_skill_manifest"}
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Description = strings.TrimSpace(manifest.Description)
	if !skillNamePattern.MatchString(manifest.Name) || manifest.Description == "" || len([]rune(manifest.Description)) > 500 {
		return Manifest{}, &PackageError{Code: "invalid_skill_manifest"}
	}
	switch manifest.Category {
	case "content", "data", "development", "workflow", "other":
	default:
		manifest.Category = "other"
	}
	return manifest, nil
}

func isScriptPath(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".py", ".sh", ".js":
		return true
	default:
		return false
	}
}
