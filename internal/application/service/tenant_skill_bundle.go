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
	"sort"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/agent/skills"
)

// ErrSkillBundleInvalid marks every rejection of an uploaded archive, so the
// handler can map the whole class to 400 without matching on message text.
var ErrSkillBundleInvalid = errors.New("skill bundle is invalid")

// The skill bundle limits bound decompression so a zip bomb cannot exhaust
// memory before we ever reach the sandbox.
const (
	// maxSkillBundleFiles is the cap on files kept as the skill itself
	// (after dropping GitHub zipball extras and directory entries).
	// Real skills vendor assets: hugohe3/ppt-master's skills/ppt-master
	// tree is ~13k files, almost all under templates/.
	maxSkillBundleFiles = 20_000
	// maxSkillBundleZipEntries bounds central-directory iteration on the
	// raw archive. GitHub zipballs include the whole repository, so this
	// is higher than maxSkillBundleFiles.
	maxSkillBundleZipEntries = 100_000
	maxSkillBundleFileBytes  = 32 << 20  // 32 MiB per entry
	maxSkillBundleTotalBytes = 512 << 20 // 512 MiB across the kept skill files
)

// SkillBundle is a validated, in-memory skill archive.
type SkillBundle struct {
	Name         string
	Version      string
	Description  string
	Instructions string
	// SHA256 is over the uploaded bytes, so re-uploading the same archive is
	// recognisable in the UI and in the ledger, and a ready skill with this
	// digest can skip a billed snapshot rebuild.
	SHA256 string
	// Files maps skill-root-relative paths to contents, SKILL.md included.
	Files map[string][]byte
	// FrontmatterRepaired is true when SKILL.md YAML had to be repaired
	// before it would parse. The original file is unchanged; the install
	// prompt tells the agent to mention this so the user can fix it.
	FrontmatterRepaired bool
}

// SkillBundleParseOptions relaxes the uploaded-zip rules for archives pulled
// from a git host or registry, which wrap the skill in a repo directory and
// ship README/LICENSE next to it.
type SkillBundleParseOptions struct {
	// Subdir is a skill-root path inside the archive (after any single wrap
	// directory). Empty means "find SKILL.md by the usual rules".
	Subdir string
	// AllowExtraFiles keeps files under the skill root and drops the rest,
	// instead of rejecting a GitHub zip that also contains the repo README.
	AllowExtraFiles bool
	// AllowNestedSkill accepts a unique SKILL.md nested deeper than one
	// directory, which is how monorepos and GitHub zipballs arrive.
	AllowNestedSkill bool
}

// ParseSkillBundle validates an uploaded zip and extracts everything the
// install flow needs. It accepts both a flat archive and one wrapped in a
// single top-level directory, because both are what people actually upload.
func ParseSkillBundle(archive []byte) (*SkillBundle, error) {
	return ParseSkillBundleWithOptions(archive, SkillBundleParseOptions{})
}

// ParseSkillBundleWithOptions is ParseSkillBundle with the extra knobs remote
// installs need. SHA256 is still over the input bytes, not the re-rooted view.
func ParseSkillBundleWithOptions(archive []byte, opts SkillBundleParseOptions) (*SkillBundle, error) {
	raw, err := unzipSkillArchive(archive, opts)
	if err != nil {
		return nil, err
	}
	files, err := stripSkillRootPrefix(raw, opts)
	if err != nil {
		return nil, err
	}
	return skillBundleFromFiles(archive, files)
}

func unzipSkillArchive(archive []byte, opts SkillBundleParseOptions) (map[string][]byte, error) {
	entries, err := skillZipEntries(archive, opts)
	if err != nil {
		return nil, err
	}

	raw := make(map[string][]byte, len(entries))
	var totalBytes int64
	for _, item := range entries {
		entryBytes := item.size
		if totalBytes+entryBytes > maxSkillBundleTotalBytes {
			return nil, fmt.Errorf("%w: archive is too large", ErrSkillBundleInvalid)
		}
		totalBytes += entryBytes
		content, err := readLimitedZipEntry(item.file)
		if err != nil {
			return nil, err
		}
		if int64(len(content)) > entryBytes {
			actualExcess := int64(len(content)) - entryBytes
			if totalBytes+actualExcess > maxSkillBundleTotalBytes {
				return nil, fmt.Errorf("%w: archive is too large", ErrSkillBundleInvalid)
			}
			totalBytes += actualExcess
		}
		raw[item.name] = content
	}
	return raw, nil
}

// errSkillFileMissing means the zip is a valid skill archive but the requested
// path is not in it. The browser maps this to 404; every other zip problem is
// still ErrSkillBundleInvalid.
var errSkillFileMissing = errors.New("skill file not found")

type skillZipEntry struct {
	file *zip.File
	name string
	size int64
}

func skillZipEntries(archive []byte, opts SkillBundleParseOptions) ([]skillZipEntry, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("%w: not a readable zip archive: %v", ErrSkillBundleInvalid, err)
	}
	if len(reader.File) > maxSkillBundleZipEntries {
		return nil, fmt.Errorf("%w: archive has more than %d zip entries",
			ErrSkillBundleInvalid, maxSkillBundleZipEntries)
	}

	pending := make([]skillZipEntry, 0, len(reader.File))
	names := make(map[string][]byte, len(reader.File))
	for _, entry := range reader.File {
		name, skip, err := inspectSkillZipPath(entry)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		pending = append(pending, skillZipEntry{
			file: entry,
			name: name,
			size: entry.FileInfo().Size(),
		})
		names[name] = nil
	}

	prefix, err := skillRootPrefix(names, opts)
	if err != nil {
		return nil, err
	}

	out := make([]skillZipEntry, 0, len(pending))
	var totalBytes int64
	for _, item := range pending {
		if prefix != "" && !strings.HasPrefix(item.name, prefix+"/") {
			if opts.AllowExtraFiles {
				continue
			}
			return nil, fmt.Errorf("%w: archive holds files outside the skill directory %q",
				ErrSkillBundleInvalid, prefix)
		}
		if err := inspectKeptSkillZipEntry(item); err != nil {
			return nil, err
		}
		if totalBytes+item.size > maxSkillBundleTotalBytes {
			return nil, fmt.Errorf("%w: archive is too large", ErrSkillBundleInvalid)
		}
		totalBytes += item.size
		out = append(out, item)
		if len(out) > maxSkillBundleFiles {
			return nil, fmt.Errorf("%w: skill directory holds more than %d files",
				ErrSkillBundleInvalid, maxSkillBundleFiles)
		}
	}
	return out, nil
}

func inspectSkillZipPath(entry *zip.File) (name string, skip bool, err error) {
	if entry.FileInfo().IsDir() {
		return "", true, nil
	}
	name = path.Clean(entry.Name)
	if name == "." || strings.HasPrefix(name, "..") ||
		path.IsAbs(name) || strings.Contains(name, "../") {
		return "", false, fmt.Errorf("%w: entry %q escapes the archive root",
			ErrSkillBundleInvalid, entry.Name)
	}
	return name, false, nil
}

func inspectKeptSkillZipEntry(item skillZipEntry) error {
	if item.file.FileInfo().Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: entry %q is a symlink", ErrSkillBundleInvalid, item.file.Name)
	}
	if err := validateSkillEntryName(item.name); err != nil {
		return err
	}
	if item.size > maxSkillBundleFileBytes {
		return fmt.Errorf("%w: entry %q is too large", ErrSkillBundleInvalid, item.file.Name)
	}
	return nil
}

func readLimitedZipEntry(entry *zip.File) ([]byte, error) {
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
	return content, nil
}

// listSkillZipFiles returns skill-root-relative paths and declared uncompressed
// sizes from the zip central directory. It does not inflate file bodies.
func listSkillZipFiles(archive []byte) ([]SkillFileEntry, error) {
	index, err := skillZipFileIndex(archive)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]SkillFileEntry, 0, len(names))
	for _, name := range names {
		out = append(out, SkillFileEntry{Path: name, Size: index[name].size})
	}
	return out, nil
}

// readSkillZipFile inflates one skill-root-relative path and leaves the rest
// of the archive compressed.
func readSkillZipFile(archive []byte, rel string) ([]byte, error) {
	index, err := skillZipFileIndex(archive)
	if err != nil {
		return nil, err
	}
	item, ok := index[rel]
	if !ok {
		return nil, errSkillFileMissing
	}
	return readLimitedZipEntry(item.file)
}

func skillZipFileIndex(archive []byte) (map[string]skillZipEntry, error) {
	entries, err := skillZipEntries(archive, SkillBundleParseOptions{})
	if err != nil {
		return nil, err
	}
	raw := make(map[string][]byte, len(entries))
	byArchiveName := make(map[string]skillZipEntry, len(entries))
	for _, item := range entries {
		raw[item.name] = nil
		byArchiveName[item.name] = item
	}
	opts := SkillBundleParseOptions{}
	stripped, err := stripSkillRootPrefix(raw, opts)
	if err != nil {
		return nil, err
	}
	prefix, err := skillRootPrefix(raw, opts)
	if err != nil {
		return nil, err
	}
	out := make(map[string]skillZipEntry, len(stripped))
	for rel := range stripped {
		archiveName := rel
		if prefix != "" {
			archiveName = prefix + "/" + rel
		}
		item, ok := byArchiveName[archiveName]
		if !ok {
			return nil, fmt.Errorf("%w: SKILL.md is missing", ErrSkillBundleInvalid)
		}
		out[rel] = item
	}
	return out, nil
}

func skillBundleFromFiles(archive []byte, files map[string][]byte) (*SkillBundle, error) {
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

	return &SkillBundle{
		Name:                skill.Name,
		Version:             version,
		Description:         skill.Description,
		Instructions:        skill.Instructions,
		SHA256:              skillArchiveSHA256(archive),
		Files:               files,
		FrontmatterRepaired: skill.FrontmatterRepaired,
	}, nil
}

func skillArchiveSHA256(archive []byte) string {
	sum := sha256.Sum256(archive)
	return hex.EncodeToString(sum[:])
}

// archiveMatchesSHA reports whether archive is the digest the row claims.
// An empty expected digest is treated as unknown, not as a match against
// whatever happens to be on disk — callers that want a fallback must opt in.
func archiveMatchesSHA(archive []byte, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" || len(archive) == 0 {
		return false
	}
	return skillArchiveSHA256(archive) == want
}

// zipSkillFiles writes a deterministic skill-root zip so a remote archive that
// had to be re-rooted can go through the same InstallSkill path as an upload.
func zipSkillFiles(files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			return nil, fmt.Errorf("write skill zip entry %q: %w", name, err)
		}
		if _, err := entry.Write(files[name]); err != nil {
			return nil, fmt.Errorf("write skill zip entry %q: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close skill zip: %w", err)
	}
	return buf.Bytes(), nil
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
	frontmatter := strings.Join(lines[frontmatterStart+1:frontmatterEnd], "\n")
	if _, err := skills.UnmarshalSkillFrontmatter(frontmatter, &metadata); err != nil {
		return "", err
	}
	return metadata.Version, nil
}

// stripSkillRootPrefix re-roots the archive at the directory holding SKILL.md.
func stripSkillRootPrefix(raw map[string][]byte, opts SkillBundleParseOptions) (map[string][]byte, error) {
	prefix, err := skillRootPrefix(raw, opts)
	if err != nil {
		return nil, err
	}
	if prefix == "" {
		return raw, nil
	}
	out := make(map[string][]byte, len(raw))
	for name, content := range raw {
		if !strings.HasPrefix(name, prefix+"/") {
			if opts.AllowExtraFiles {
				continue
			}
			return nil, fmt.Errorf("%w: archive holds files outside the skill directory %q",
				ErrSkillBundleInvalid, prefix)
		}
		out[strings.TrimPrefix(name, prefix+"/")] = content
	}
	if _, ok := out["SKILL.md"]; !ok {
		return nil, fmt.Errorf("%w: SKILL.md is missing", ErrSkillBundleInvalid)
	}
	return out, nil
}

func skillRootPrefix(raw map[string][]byte, opts SkillBundleParseOptions) (string, error) {
	subdir := path.Clean(strings.Trim(opts.Subdir, "/"))
	if subdir == "." {
		subdir = ""
	}
	// A SKILL.md at the zip root is the skill. Nested SKILL.md files are then
	// just extra files, the same way an uploaded bundle vendors them.
	if subdir == "" {
		if _, ok := raw["SKILL.md"]; ok {
			return "", nil
		}
	}

	var matches []string
	for name := range raw {
		if path.Base(name) != "SKILL.md" {
			continue
		}
		dir := path.Dir(name)
		if dir == "." {
			dir = ""
		}
		if subdir != "" {
			if dir == subdir || strings.HasSuffix(dir, "/"+subdir) {
				matches = append(matches, dir)
			}
			continue
		}
		if dir == "" || !strings.Contains(dir, "/") || opts.AllowNestedSkill {
			matches = append(matches, dir)
		}
	}
	if len(matches) == 0 {
		if subdir != "" {
			return "", fmt.Errorf("%w: SKILL.md is missing under %q", ErrSkillBundleInvalid, subdir)
		}
		return "", fmt.Errorf("%w: SKILL.md is missing", ErrSkillBundleInvalid)
	}
	uniq := uniqueStrings(matches)
	if len(uniq) > 1 {
		return "", fmt.Errorf("%w: archive holds more than one skill", ErrSkillBundleInvalid)
	}
	return uniq[0], nil
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
