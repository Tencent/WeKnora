package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
)

// skillFileTextLimit is how much of a text file the admin browser is given.
// The archive itself may hold up to maxSkillBundleFileBytes; dumping that
// into a JSON response would freeze the settings drawer.
const skillFileTextLimit = 1 << 20 // 1 MiB

// skillFileImageLimit is the decoded size cap for an inline image preview.
const skillFileImageLimit = 2 << 20 // 2 MiB

const (
	skillFileEncodingUTF8   = "utf-8"
	skillFileEncodingBase64 = "base64"
	skillFileEncodingBinary = "binary"
)

// SkillFileEntry is one path in an installed skill's stored archive.
type SkillFileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// SkillFileContent is one file the admin browser asked to open.
type SkillFileContent struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Encoding  string `json:"encoding"`
	Content   string `json:"content,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
}

// ListSkillFiles lists the stored archive of one installed skill. The files
// come from the uploaded bundle rather than the live image: browsing must
// work while the skill is still installing, and without booting a sandbox.
func (s *TenantSkillService) ListSkillFiles(
	ctx context.Context, tenantID uint64, configID, skillID string,
) ([]SkillFileEntry, error) {
	files, err := s.skillBundleFiles(ctx, tenantID, configID, skillID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]SkillFileEntry, 0, len(names))
	for _, name := range names {
		out = append(out, SkillFileEntry{Path: name, Size: int64(len(files[name]))})
	}
	return out, nil
}

// ReadSkillFile returns one file from the stored archive. Binary files are
// either inlined as base64 (images small enough to preview) or reported
// without a body so the UI can say they cannot be opened.
func (s *TenantSkillService) ReadSkillFile(
	ctx context.Context, tenantID uint64, configID, skillID, relativePath string,
) (*SkillFileContent, error) {
	clean, err := safeSkillFilePath(relativePath)
	if err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	files, err := s.skillBundleFiles(ctx, tenantID, configID, skillID)
	if err != nil {
		return nil, err
	}
	body, ok := files[clean]
	if !ok {
		return nil, apperrors.NewNotFoundError("skill file not found")
	}
	return projectSkillFileContent(clean, body), nil
}

func (s *TenantSkillService) skillBundleFiles(
	ctx context.Context, tenantID uint64, configID, skillID string,
) (map[string][]byte, error) {
	skill, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return nil, err
	}
	if skill == nil {
		return nil, apperrors.NewNotFoundError("skill not found")
	}
	ref := strings.TrimSpace(skill.BundleRef)
	if ref == "" {
		return nil, apperrors.NewNotFoundError("skill files are not available")
	}
	fs, err := s.fileServiceForTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	reader, err := fs.GetFile(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("download bundle of skill %s: %w", skill.Name, err)
	}
	defer func() { _ = reader.Close() }()
	archive, err := io.ReadAll(io.LimitReader(reader, maxSkillBundleTotalBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bundle of skill %s: %w", skill.Name, err)
	}
	if len(archive) > maxSkillBundleTotalBytes {
		return nil, fmt.Errorf("skill bundle %s is larger than the upload limit", ref)
	}
	bundle, err := ParseSkillBundle(archive)
	if err != nil {
		return nil, err
	}
	return bundle.Files, nil
}

// safeSkillFilePath normalises a caller-supplied relative path and refuses
// anything that leaves the skill directory.
func safeSkillFilePath(relativePath string) (string, error) {
	trimmed := strings.TrimSpace(relativePath)
	if trimmed == "" {
		return "", fmt.Errorf("skill file path is required")
	}
	if strings.Contains(trimmed, "\\") {
		return "", fmt.Errorf("invalid skill file path: %s", relativePath)
	}
	if path.IsAbs(trimmed) {
		return "", fmt.Errorf("invalid skill file path: %s", relativePath)
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == ".." {
			return "", fmt.Errorf("invalid skill file path: %s", relativePath)
		}
	}
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid skill file path: %s", relativePath)
	}
	return clean, nil
}

func projectSkillFileContent(rel string, body []byte) *SkillFileContent {
	out := &SkillFileContent{
		Path: rel,
		Size: int64(len(body)),
	}
	if mediaType, ok := skillImageMediaType(rel); ok {
		out.MediaType = mediaType
		if len(body) > skillFileImageLimit {
			out.Encoding = skillFileEncodingBinary
			out.Binary = true
			return out
		}
		out.Encoding = skillFileEncodingBase64
		out.Content = base64.StdEncoding.EncodeToString(body)
		return out
	}
	if skillFileLooksBinary(body) {
		out.Encoding = skillFileEncodingBinary
		out.Binary = true
		return out
	}
	out.Encoding = skillFileEncodingUTF8
	if ext := strings.ToLower(path.Ext(rel)); ext != "" {
		out.MediaType = "text/plain"
		if ext == ".md" || ext == ".markdown" {
			out.MediaType = "text/markdown"
		}
	}
	if len(body) > skillFileTextLimit {
		out.Content = string(body[:skillFileTextLimit])
		out.Truncated = true
		return out
	}
	out.Content = string(body)
	return out
}

func skillFileLooksBinary(body []byte) bool {
	if bytes.IndexByte(body, 0) >= 0 {
		return true
	}
	return !utf8.Valid(body)
}

func skillImageMediaType(rel string) (string, bool) {
	switch strings.ToLower(path.Ext(rel)) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".bmp":
		return "image/bmp", true
	case ".ico":
		return "image/x-icon", true
	case ".svg":
		return "image/svg+xml", true
	default:
		return "", false
	}
}
