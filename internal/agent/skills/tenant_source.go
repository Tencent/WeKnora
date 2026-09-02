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
	// maxBundleZipEntries bounds central-directory iteration on the raw
	// archive, mirroring maxSkillBundleZipEntries. A GitHub zipball carries
	// the whole repository, so it sits well above the count of files kept as
	// the skill.
	maxBundleZipEntries = 100_000
	// maxBundleEntries caps the files kept as the skill, mirroring
	// maxSkillBundleFiles. It is counted after directory entries and the
	// zipball's extra trees are dropped, exactly as the install counts them:
	// applying it to the raw entry count instead would reject archives the
	// install accepted, taking read_skill down for a working install.
	maxBundleEntries    = 20_000
	maxBundleEntryBytes = 32 << 20  // 32 MiB per entry
	maxBundleBytes      = 512 << 20 // 512 MiB across one archive (install cap)
)

// A source lives for a single agent run. read_skill is typically called
// several times against the same skill, so a handful of compressed zips
// removes the repeated download. 64 MiB is the keep-around budget; a zip
// larger than that can stay as the sole occupant so a large skill does not
// re-download on every file, without pinning several 100+ MiB archives.
const (
	cachedBundleCount = 4
	cachedBundleBytes = 64 << 20
)

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

	// loadBundle fetches the skill zip. It may be nil when the deployment
	// cannot serve bundles, in which case only the levels backed by the row
	// are available. Callers that leave install.BundleRef empty should resolve
	// the catalog object here.
	loadBundle func(row *types.TenantSkillEntity) ([]byte, error)

	mu sync.Mutex
	// cache holds downloaded archives, most recently used first, keyed by
	// bundle_sha256 (or the storage ref). The zip stays compressed: list and
	// read_skill inflate one entry at a time so a 13k-file skill cannot pin
	// hundreds of megabytes unpacked for the rest of the turn.
	cache []cachedBundle
}

type cachedBundle struct {
	key     string
	archive []byte
}

// NewTenantSkillSource builds a source over the rows of one sandbox config.
// Callers pass every row; the source itself decides which are usable.
func NewTenantSkillSource(
	rows []*types.TenantSkillEntity, loadBundle func(row *types.TenantSkillEntity) ([]byte, error),
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
	archive, err := s.bundleArchive(row)
	if err != nil {
		return nil, err
	}
	index, err := skillBundleFileIndex(archive)
	if err != nil {
		return nil, err
	}
	item, ok := index[clean]
	if !ok {
		return nil, fmt.Errorf("file not found in skill %s: %s", name, relativePath)
	}
	content, err := readLimitedSkillZipFile(item)
	if err != nil {
		return nil, err
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
	archive, err := s.bundleArchive(row)
	if err != nil {
		return nil, err
	}
	index, err := skillBundleFileIndex(archive)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(index))
	for rel := range index {
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

// bundleArchive returns the compressed zip of one skill, downloading it at
// most once per cache lifetime.
func (s *TenantSkillSource) bundleArchive(
	row *types.TenantSkillEntity,
) ([]byte, error) {
	if s.loadBundle == nil {
		return nil, fmt.Errorf("skill bundles are not available in this deployment")
	}

	key := strings.TrimSpace(row.BundleSHA256)
	if key == "" {
		key = strings.TrimSpace(row.BundleRef)
	}
	if key == "" {
		key = strings.TrimSpace(row.CatalogID)
	}
	if key == "" {
		key = strings.TrimSpace(row.ID)
	}
	if archive := s.cached(key); archive != nil {
		return archive, nil
	}

	archive, err := s.loadBundle(row)
	if err != nil {
		return nil, fmt.Errorf("download bundle of skill %s: %w", row.Name, err)
	}
	if len(archive) == 0 {
		return nil, fmt.Errorf(
			"skill %s has no stored bundle; its files cannot be read", row.Name)
	}
	s.store(key, archive)
	return archive, nil
}

func (s *TenantSkillSource) cached(key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, entry := range s.cache {
		if entry.key != key {
			continue
		}
		s.cache = append(s.cache[:i], s.cache[i+1:]...)
		s.cache = append([]cachedBundle{entry}, s.cache...)
		return entry.archive
	}
	return nil
}

func (s *TenantSkillSource) store(key string, archive []byte) {
	if key == "" || len(archive) == 0 || len(archive) > maxBundleBytes {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, entry := range s.cache {
		if entry.key != key {
			continue
		}
		s.cache = append(s.cache[:i], s.cache[i+1:]...)
		break
	}
	s.cache = append([]cachedBundle{{key: key, archive: archive}}, s.cache...)
	for len(s.cache) > 1 &&
		(len(s.cache) > cachedBundleCount || s.cachedBytes() > cachedBundleBytes) {
		s.cache = s.cache[:len(s.cache)-1]
	}
}

func (s *TenantSkillSource) cachedBytes() int {
	total := 0
	for _, entry := range s.cache {
		total += len(entry.archive)
	}
	return total
}

// skillBundleFileIndex maps skill-root-relative paths to zip entries without
// inflating file bodies.
func skillBundleFileIndex(archive []byte) (map[string]*zip.File, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("not a readable zip archive: %w", err)
	}
	if len(reader.File) > maxBundleZipEntries {
		return nil, fmt.Errorf("archive holds more than %d entries", maxBundleZipEntries)
	}

	raw := make(map[string]*zip.File, len(reader.File))
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
		raw[name] = entry
	}
	prefix, err := skillZipRootPrefix(raw)
	if err != nil {
		return nil, err
	}
	out := raw
	if prefix != "" {
		out = make(map[string]*zip.File, len(raw))
		for name, entry := range raw {
			if !strings.HasPrefix(name, prefix+"/") {
				continue
			}
			out[strings.TrimPrefix(name, prefix+"/")] = entry
		}
		if _, ok := out[SkillFileName]; !ok {
			return nil, fmt.Errorf("%s is missing from the archive", SkillFileName)
		}
	}
	if len(out) > maxBundleEntries {
		return nil, fmt.Errorf("archive holds more than %d files", maxBundleEntries)
	}
	return out, nil
}

func skillZipRootPrefix(raw map[string]*zip.File) (string, error) {
	if _, ok := raw[SkillFileName]; ok {
		return "", nil
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
			return "", fmt.Errorf("archive holds more than one skill")
		}
		prefix = dir
	}
	if prefix == "" {
		return "", fmt.Errorf("%s is missing from the archive", SkillFileName)
	}
	return prefix, nil
}

func readLimitedSkillZipFile(entry *zip.File) ([]byte, error) {
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
	return content, nil
}
