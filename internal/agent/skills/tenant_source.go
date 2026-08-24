package skills

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// Bundle limits bound decompression. They mirror the install-time limits: the
// archive read here is the same one that was accepted then, and a corrupted or
// hostile object in storage must not be able to exhaust this process.
const (
	maxBundleEntries    = 2000
	maxBundleEntryBytes = 32 << 20  // 32 MiB per entry
	maxBundleBytes      = 256 << 20 // 256 MiB across the archive
)

// cachedBundleCount bounds the unpacked archives one source keeps. A source
// lives for a single agent run, and within that run read_skill is typically
// called several times against the same skill, so a handful of entries removes
// the repeated download without holding archives past the turn.
const cachedBundleCount = 4

// TenantSkillSource exposes the skills an administrator installed into one
// sandbox config's snapshot image.
//
// The three disclosure levels come from different places on purpose: name,
// description and the SKILL.md body are columns on the row, so the request
// path never waits on object storage to tell the model what a skill is, while
// individual resource files come from the uploaded archive. Execution uses
// neither - the files are already in the image.
type TenantSkillSource struct {
	byName map[string]*types.TenantSkillEntity
	// order preserves the repository's ordering so the system prompt is stable
	// between turns.
	order []string

	// loadBundle fetches an uploaded archive by its stored reference. It may
	// be nil when the deployment cannot serve bundles, in which case only the
	// levels backed by the row are available.
	loadBundle func(ref string) ([]byte, error)

	mu sync.Mutex
	// cache holds unpacked archives, most recently used first, keyed by
	// bundle_sha256: the archive is immutable under that key, so a hit can
	// never serve a stale tree.
	cache []cachedBundle
}

type cachedBundle struct {
	key   string
	files map[string][]byte
}

// NewTenantSkillSource builds a source over the rows of one sandbox config.
// Callers pass every row; the source itself decides which are usable.
func NewTenantSkillSource(
	rows []*types.TenantSkillEntity, loadBundle func(ref string) ([]byte, error),
) *TenantSkillSource {
	src := &TenantSkillSource{
		byName:     make(map[string]*types.TenantSkillEntity, len(rows)),
		loadBundle: loadBundle,
	}
	for _, row := range rows {
		if !usableSkillRow(row) {
			continue
		}
		if _, exists := src.byName[row.Name]; exists {
			continue
		}
		src.byName[row.Name] = row
		src.order = append(src.order, row.Name)
	}
	return src
}

// usableSkillRow is the one place "the agent can actually run this" is decided
// for a single row. A row that is still installing, failed, or was disabled by
// an administrator is invisible: telling the model about a skill it cannot
// invoke costs it turns and gains nothing.
//
// The name guard is not defensive: every path this source hands out is
// SkillDirFor(row.Name), which joins the name under the skills root. A name
// that is not a single path segment yields a path outside the skill, or outside
// the root entirely, and those paths reach the model in discovery metadata and
// in SkillFile.Path even though execution would refuse them. Filtering the row
// out here is what keeps them from ever being spoken. sandbox.IsValidSkillName
// is the same rule SkillDirFor enforces, so this cannot disagree with it.
func usableSkillRow(row *types.TenantSkillEntity) bool {
	return row != nil &&
		row.Enabled &&
		row.Status == types.SkillStatusReady &&
		sandbox.IsValidSkillName(row.Name)
}


// DiscoverSkills returns Level 1 metadata for every usable skill.
func (s *TenantSkillSource) DiscoverSkills() ([]*SkillMetadata, error) {
	metadata := make([]*SkillMetadata, 0, len(s.order))
	for _, name := range s.order {
		row := s.byName[name]
		basePath, err := sandbox.SkillDirFor(row.Name)
		if err != nil {
			return nil, err
		}
		metadata = append(metadata, &SkillMetadata{
			Name:        row.Name,
			Description: row.Description,
			BasePath:    basePath,
		})
	}
	return metadata, nil
}

// LoadSkillInstructions returns Level 2 from the row.
func (s *TenantSkillSource) LoadSkillInstructions(name string) (*Skill, error) {
	row, err := s.row(name)
	if err != nil {
		return nil, err
	}
	basePath, err := sandbox.SkillDirFor(row.Name)
	if err != nil {
		return nil, err
	}
	return &Skill{
		Name:         row.Name,
		Description:  row.Description,
		BasePath:     basePath,
		FilePath:     path.Join(basePath, SkillFileName),
		Instructions: row.Instructions,
		Loaded:       true,
	}, nil
}

// LoadSkillFile returns one Level 3 resource out of the uploaded archive.
//
// The archive is read rather than the image: reading a file out of the image
// would need a sandbox, and read_skill must work whether or not this turn has
// already booted one.
func (s *TenantSkillSource) LoadSkillFile(name, relativePath string) (*SkillFile, error) {
	row, err := s.row(name)
	if err != nil {
		return nil, err
	}
	clean, err := safeSkillRelPath(relativePath)
	if err != nil {
		return nil, err
	}
	files, err := s.bundleFiles(row)
	if err != nil {
		return nil, err
	}
	content, ok := files[clean]
	if !ok {
		return nil, fmt.Errorf("file not found in skill %s: %s", name, relativePath)
	}
	basePath, err := sandbox.SkillDirFor(row.Name)
	if err != nil {
		return nil, err
	}
	return &SkillFile{
		Name: relativePath,
		// The path the model sees is the in-image one, because that is where
		// it can act on the file.
		Path:     path.Join(basePath, clean),
		Content:  string(content),
		IsScript: IsScript(clean),
	}, nil
}

// ListSkillFiles lists the archive contents of one skill, sorted so repeated
// calls within a turn do not look like different answers.
func (s *TenantSkillSource) ListSkillFiles(name string) ([]string, error) {
	row, err := s.row(name)
	if err != nil {
		return nil, err
	}
	files, err := s.bundleFiles(row)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for rel := range files {
		names = append(names, rel)
	}
	sort.Strings(names)
	return names, nil
}

// GetSkillBasePath returns the skill's directory inside the image.
func (s *TenantSkillSource) GetSkillBasePath(name string) (string, error) {
	row, err := s.row(name)
	if err != nil {
		return "", err
	}
	return sandbox.SkillDirFor(row.Name)
}

// RemoteScriptPath returns the absolute in-image path of one script. It is
// keyed on the skill name, which is also the directory the installer wrote.
//
// It deliberately does not consult the archive. The image is what executes,
// and a skill whose archive failed to store - the install logs a warning and
// carries on - is still installed and runnable.
func (s *TenantSkillSource) RemoteScriptPath(name, relativePath string) (string, error) {
	row, err := s.row(name)
	if err != nil {
		return "", err
	}
	clean, err := safeSkillRelPath(relativePath)
	if err != nil {
		return "", err
	}
	basePath, err := sandbox.SkillDirFor(row.Name)
	if err != nil {
		return "", err
	}
	return path.Join(basePath, clean), nil
}

func (s *TenantSkillSource) row(name string) (*types.TenantSkillEntity, error) {
	if row, ok := s.byName[name]; ok {
		return row, nil
	}
	return nil, fmt.Errorf("skill not found: %s", name)
}

// safeSkillRelPath normalises a caller-supplied relative path and refuses
// anything that leaves the skill directory.
func safeSkillRelPath(relativePath string) (string, error) {
	trimmed := strings.TrimSpace(relativePath)
	if trimmed == "" {
		return "", fmt.Errorf("skill file path is required")
	}
	if path.IsAbs(trimmed) {
		return "", fmt.Errorf("invalid skill file path: %s", relativePath)
	}
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid skill file path: %s", relativePath)
	}
	return clean, nil
}

// bundleFiles returns the unpacked archive of one skill, downloading it at
// most once per cache lifetime.
func (s *TenantSkillSource) bundleFiles(
	row *types.TenantSkillEntity,
) (map[string][]byte, error) {
	ref := strings.TrimSpace(row.BundleRef)
	if ref == "" {
		return nil, fmt.Errorf(
			"skill %s has no stored bundle; its files cannot be read", row.Name)
	}
	if s.loadBundle == nil {
		return nil, fmt.Errorf("skill bundles are not available in this deployment")
	}

	key := strings.TrimSpace(row.BundleSHA256)
	if key == "" {
		// Without a checksum the reference is the only stable key we have.
		key = ref
	}
	if files := s.cached(key); files != nil {
		return files, nil
	}

	archive, err := s.loadBundle(ref)
	if err != nil {
		return nil, fmt.Errorf("download bundle of skill %s: %w", row.Name, err)
	}
	files, err := unpackSkillBundle(archive)
	if err != nil {
		return nil, fmt.Errorf("read bundle of skill %s: %w", row.Name, err)
	}
	s.store(key, files)
	return files, nil
}

func (s *TenantSkillSource) cached(key string) map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, entry := range s.cache {
		if entry.key != key {
			continue
		}
		s.cache = append(s.cache[:i], s.cache[i+1:]...)
		s.cache = append([]cachedBundle{entry}, s.cache...)
		return entry.files
	}
	return nil
}

func (s *TenantSkillSource) store(key string, files map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = append([]cachedBundle{{key: key, files: files}}, s.cache...)
	if len(s.cache) > cachedBundleCount {
		s.cache = s.cache[:cachedBundleCount]
	}
}

// unpackSkillBundle reads a skill archive into skill-root-relative paths. It
// re-roots the archive at the directory holding SKILL.md, because an archive
// wrapped in a single top-level directory is accepted at upload time and the
// relative paths the model is given must match either shape.
func unpackSkillBundle(archive []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("not a readable zip archive: %w", err)
	}
	if len(reader.File) > maxBundleEntries {
		return nil, fmt.Errorf("archive holds more than %d files", maxBundleEntries)
	}

	raw := make(map[string][]byte, len(reader.File))
	var total int64
	for _, entry := range reader.File {
		info := entry.FileInfo()
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		name := path.Clean(entry.Name)
		if name == "." || path.IsAbs(name) ||
			name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("entry %q escapes the archive root", entry.Name)
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("cannot read %q: %w", entry.Name, err)
		}
		content, err := io.ReadAll(io.LimitReader(rc, maxBundleEntryBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("cannot read %q: %w", entry.Name, err)
		}
		if len(content) > maxBundleEntryBytes {
			return nil, fmt.Errorf("entry %q is too large", entry.Name)
		}
		total += int64(len(content))
		if total > maxBundleBytes {
			return nil, fmt.Errorf("archive is too large")
		}
		raw[name] = content
	}
	return reRootSkillBundle(raw)
}

// reRootSkillBundle strips a single wrapping directory when SKILL.md is not at
// the archive root.
func reRootSkillBundle(raw map[string][]byte) (map[string][]byte, error) {
	if _, ok := raw[SkillFileName]; ok {
		return raw, nil
	}
	prefix := ""
	for name := range raw {
		if path.Base(name) != SkillFileName {
			continue
		}
		dir := path.Dir(name)
		if dir == "." || strings.Contains(dir, "/") {
			continue
		}
		if prefix != "" && prefix != dir {
			return nil, fmt.Errorf("archive holds more than one skill")
		}
		prefix = dir
	}
	if prefix == "" {
		return nil, fmt.Errorf("%s is missing from the archive", SkillFileName)
	}
	out := make(map[string][]byte, len(raw))
	for name, content := range raw {
		if !strings.HasPrefix(name, prefix+"/") {
			continue
		}
		out[strings.TrimPrefix(name, prefix+"/")] = content
	}
	return out, nil
}
