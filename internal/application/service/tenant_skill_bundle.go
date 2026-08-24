package service

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"gopkg.in/yaml.v3"
)

// ErrSkillBundleInvalid marks every rejection of an uploaded archive, so the
// handler can map the whole class to 400 without matching on message text.
var ErrSkillBundleInvalid = errors.New("skill bundle is invalid")

// The skill bundle limits bound decompression so a zip bomb cannot exhaust
// memory before we ever reach the sandbox.
const (
	maxSkillBundleFiles      = 2000
	maxSkillBundleFileBytes  = 32 << 20  // 32 MiB per entry
	maxSkillBundleTotalBytes = 256 << 20 // 256 MiB across the archive
)

// SkillBundle is a validated, in-memory skill archive.
type SkillBundle struct {
	Name         string
	Version      string
	Description  string
	Instructions string
	// SHA256 is over the uploaded bytes, so re-uploading the same archive is
	// recognisable in the UI and in the ledger.
	SHA256 string
	// Files maps skill-root-relative paths to contents, SKILL.md included.
	Files map[string][]byte
}

// ParseSkillBundle validates an uploaded zip and extracts everything the
// install flow needs. It accepts both a flat archive and one wrapped in a
// single top-level directory, because both are what people actually upload.
func ParseSkillBundle(archive []byte) (*SkillBundle, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("%w: not a readable zip archive: %v", ErrSkillBundleInvalid, err)
	}
	if len(reader.File) > maxSkillBundleFiles {
		return nil, fmt.Errorf("%w: archive holds more than %d files",
			ErrSkillBundleInvalid, maxSkillBundleFiles)
	}

	raw := make(map[string][]byte, len(reader.File))
	var totalBytes int64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: entry %q is a symlink", ErrSkillBundleInvalid, entry.Name)
		}
		name := path.Clean(entry.Name)
		if name == "." || strings.HasPrefix(name, "..") ||
			path.IsAbs(name) || strings.Contains(name, "../") {
			return nil, fmt.Errorf("%w: entry %q escapes the archive root",
				ErrSkillBundleInvalid, entry.Name)
		}
		if err := validateSkillEntryName(name); err != nil {
			return nil, err
		}
		if entry.FileInfo().Size() > maxSkillBundleFileBytes {
			return nil, fmt.Errorf("%w: entry %q is too large", ErrSkillBundleInvalid, entry.Name)
		}
		entryBytes := entry.FileInfo().Size()
		if totalBytes+entryBytes > maxSkillBundleTotalBytes {
			return nil, fmt.Errorf("%w: archive is too large", ErrSkillBundleInvalid)
		}
		totalBytes += entryBytes
		rc, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: cannot read %q: %v", ErrSkillBundleInvalid, entry.Name, err)
		}
		content, err := io.ReadAll(io.LimitReader(rc, maxSkillBundleFileBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: cannot read %q: %v", ErrSkillBundleInvalid, entry.Name, err)
		}
		if len(content) > maxSkillBundleFileBytes {
			return nil, fmt.Errorf("%w: entry %q is too large", ErrSkillBundleInvalid, entry.Name)
		}
		if int64(len(content)) > entryBytes {
			actualExcess := int64(len(content)) - entryBytes
			if totalBytes+actualExcess > maxSkillBundleTotalBytes {
				return nil, fmt.Errorf("%w: archive is too large", ErrSkillBundleInvalid)
			}
			totalBytes += actualExcess
		}
		raw[name] = content
	}

	files, err := stripSkillRootPrefix(raw)
	if err != nil {
		return nil, err
	}

	manifest, ok := files["SKILL.md"]
	if !ok {
		return nil, fmt.Errorf("%w: SKILL.md is missing", ErrSkillBundleInvalid)
	}
	skill, err := skills.ParseSkillFile(string(manifest))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSkillBundleInvalid, err)
	}
	version, err := parseSkillBundleVersion(string(manifest))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSkillBundleInvalid, err)
	}

	sum := sha256.Sum256(archive)
	return &SkillBundle{
		Name:         skill.Name,
		Version:      version,
		Description:  skill.Description,
		Instructions: skill.Instructions,
		SHA256:       hex.EncodeToString(sum[:]),
		Files:        files,
	}, nil
}

// validateSkillEntryName bans control characters and nothing else.
//
// Shell safety is not this function's job: every interpolation of a bundle
// path goes through sandbox.ShellQuote, which makes any name inert. What
// quoting cannot fix is a name that rewrites the lines it is printed into —
// the entry names also reach log lines, error messages and the image
// manifest, and a newline or an escape sequence there forges a second record.
// So control characters (NUL included) are refused and ordinary punctuation is
// not: real bundles vendor node_modules/@scope/pkg and wheels named
// numpy-1.26.4+cpu.whl, and a charset that rejected '@' or '+' broke them for
// no security gain. Traversal and symlinks are checked separately by the
// caller.
func validateSkillEntryName(name string) error {
	for _, r := range name {
		if unicode.IsControl(r) || r == 0 {
			return fmt.Errorf("%w: entry %q holds unsupported character %q",
				ErrSkillBundleInvalid, name, r)
		}
	}
	return nil
}

func parseSkillBundleVersion(manifest string) (string, error) {
	lines := strings.Split(manifest, "\n")
	frontmatterStart := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			frontmatterStart = i
			break
		}
		if strings.TrimSpace(line) != "" {
			break
		}
	}
	if frontmatterStart < 0 {
		return "", nil
	}

	frontmatterEnd := -1
	for i := frontmatterStart + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			frontmatterEnd = i
			break
		}
	}
	if frontmatterEnd < 0 {
		return "", nil
	}

	var metadata struct {
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[frontmatterStart+1:frontmatterEnd], "\n")), &metadata); err != nil {
		return "", err
	}
	return metadata.Version, nil
}

// stripSkillRootPrefix re-roots the archive at the directory holding SKILL.md.
func stripSkillRootPrefix(raw map[string][]byte) (map[string][]byte, error) {
	if _, ok := raw["SKILL.md"]; ok {
		return raw, nil
	}
	var prefix string
	for name := range raw {
		if path.Base(name) != "SKILL.md" {
			continue
		}
		dir := path.Dir(name)
		if dir == "." || strings.Contains(dir, "/") {
			// Either already handled above, or nested deeper than one level:
			// we refuse to guess which of several directories is the skill.
			continue
		}
		if prefix != "" && prefix != dir {
			return nil, fmt.Errorf("%w: archive holds more than one skill", ErrSkillBundleInvalid)
		}
		prefix = dir
	}
	if prefix == "" {
		return nil, fmt.Errorf("%w: SKILL.md is missing", ErrSkillBundleInvalid)
	}
	out := make(map[string][]byte, len(raw))
	for name, content := range raw {
		if !strings.HasPrefix(name, prefix+"/") {
			return nil, fmt.Errorf("%w: archive holds files outside the skill directory %q",
				ErrSkillBundleInvalid, prefix)
		}
		out[strings.TrimPrefix(name, prefix+"/")] = content
	}
	return out, nil
}
