// Package localfolder implements a data source connector that syncs files from a
// directory on the server, typically a bind-mounted host folder such as an
// Obsidian vault, into a knowledge base.
package localfolder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"mime"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/bmatcuk/doublestar/v4"
)

// AllowedRootsEnv names the environment variable listing the directories,
// separated by os.PathListSeparator, that local_folder data sources may read.
// While it is unset every configuration is rejected, so the host filesystem is
// never exposed without an explicit deployment opt-in.
const AllowedRootsEnv = "WEKNORA_LOCAL_FOLDER_ROOTS"

const (
	// quietPeriod defers files modified this recently to a later sync, so a note
	// that is still being typed, or written by an agent, is ingested once settled.
	quietPeriod      = 10 * time.Second
	externalIDPrefix = "local_folder:"
)

var (
	// Images and audio are left out: they need VLM/ASR, and vault attachments
	// are mostly images.
	defaultInclude = []string{"**/*.{md,markdown,txt,pdf,docx,doc,pptx,ppt,xlsx,xls,csv,html,htm,epub}"}
	defaultExclude = []string{".obsidian/**", ".git/**", ".trash/**"}

	// supportedExtensions mirrors service.supportedImportFileExtensions, which this
	// package cannot import. Other files are skipped silently instead of failing
	// on every sync.
	supportedExtensions = map[string]struct{}{
		"pdf": {}, "txt": {}, "docx": {}, "doc": {}, "epub": {},
		"html": {}, "htm": {}, "mhtml": {}, "md": {}, "markdown": {}, "xmind": {},
		"png": {}, "jpg": {}, "jpeg": {}, "gif": {},
		"csv": {}, "xlsx": {}, "xls": {}, "pptx": {}, "ppt": {}, "json": {},
		"mp3": {}, "wav": {}, "m4a": {}, "flac": {}, "ogg": {},
	}
)

var (
	_ datasource.Connector          = (*Connector)(nil)
	_ datasource.FullSyncWithCursor = (*Connector)(nil)
)

// Connector syncs a local directory. It is stateless: the per-file sync state
// lives in the data source cursor.
type Connector struct{}

// NewConnector creates a local folder connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeLocalFolder }

// Validate checks that root_path is an accessible directory inside an allowed
// root and that the include/exclude patterns are valid.
func (c *Connector) Validate(_ context.Context, ds *types.DataSourceConfig) error {
	_, err := parseConfig(ds)
	return err
}

// ListResources returns nothing: the synced scope is set by root_path and the
// include/exclude patterns rather than picked from a resource tree.
func (c *Connector) ListResources(context.Context, *types.DataSourceConfig, string) ([]types.Resource, error) {
	return []types.Resource{}, nil
}

// ResolveResourceAncestors has nothing to resolve; see ListResources.
func (c *Connector) ResolveResourceAncestors(
	context.Context, *types.DataSourceConfig, []string,
) ([]string, error) {
	return []string{}, nil
}

// FetchAll emits every in-scope file and never reports deletions. The service
// prefers FetchAllFromCursor for full syncs.
func (c *Connector) FetchAll(ctx context.Context, ds *types.DataSourceConfig, _ []string) ([]types.FetchedItem, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	items, _, err := cfg.sync(ctx, nil, false, true)
	return items, err
}

// FetchAllFromCursor is the full-sync path. It re-emits every in-scope file, so
// a file whose previous ingest failed is retried (unchanged content is skipped
// at ingest without re-parsing), and still reports files deleted since cursor.
func (c *Connector) FetchAllFromCursor(
	ctx context.Context, ds *types.DataSourceConfig, _ []string, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, nil, err
	}
	return cfg.sync(ctx, decodeCursor(cursor), true, true)
}

// FetchIncremental diffs the folder against the file states recorded in cursor,
// emitting new and changed files plus deletions. mtime and size decide whether a
// file is read at all; a SHA-256 of its content confirms a real change.
func (c *Connector) FetchIncremental(
	ctx context.Context, ds *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, nil, err
	}
	return cfg.sync(ctx, decodeCursor(cursor), true, false)
}

// fileState is the per-file sync state kept in the cursor, keyed by the file's
// slash-separated path relative to the root.
type fileState struct {
	ModTime time.Time `json:"mtime"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
}

func decodeCursor(cursor *types.SyncCursor) map[string]fileState {
	var state struct {
		Files map[string]fileState `json:"files"`
	}
	if cursor != nil && cursor.ConnectorCursor != nil {
		raw, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(raw, &state)
	}
	return state.Files
}

type config struct {
	root    string // absolute and symlink-free
	include []string
	exclude []string
	quiet   time.Duration
}

func parseConfig(ds *types.DataSourceConfig) (*config, error) {
	if ds == nil {
		return nil, datasource.ErrInvalidConfig
	}
	rootPath, _ := setting(ds, "root_path").(string)
	root, err := resolveRoot(strings.TrimSpace(rootPath))
	if err != nil {
		return nil, err
	}
	include, err := patterns(setting(ds, "include"), defaultInclude)
	if err != nil {
		return nil, err
	}
	exclude, err := patterns(setting(ds, "exclude"), defaultExclude)
	if err != nil {
		return nil, err
	}
	cfg := &config{root: root, include: include, exclude: exclude, quiet: quietPeriod}
	if ds.ManualTrigger {
		cfg.quiet = 0 // the user asked to sync now, including files saved moments ago
	}
	return cfg, nil
}

// setting reads a non-secret setting. A validate-credentials request only
// carries a credentials map, so credentials are consulted when settings lack
// the key, as for the RSS connector.
func setting(ds *types.DataSourceConfig, key string) interface{} {
	if value, ok := ds.Settings[key]; ok {
		return value
	}
	return ds.Credentials[key]
}

// patterns parses a newline-separated string or a string array of doublestar
// patterns. A missing or empty value yields the defaults.
func patterns(raw interface{}, defaults []string) ([]string, error) {
	var values []string
	switch v := raw.(type) {
	case nil:
	case string:
		values = strings.Split(v, "\n")
	case []interface{}:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%w: include/exclude patterns must be strings", datasource.ErrInvalidConfig)
			}
			values = append(values, s)
		}
	default:
		return nil, fmt.Errorf("%w: include/exclude must be a string or a string array", datasource.ErrInvalidConfig)
	}
	out := make([]string, 0, len(values))
	for _, pattern := range values {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if !doublestar.ValidatePattern(pattern) {
			return nil, fmt.Errorf("%w: invalid pattern %q", datasource.ErrInvalidConfig, pattern)
		}
		out = append(out, pattern)
	}
	if len(out) == 0 {
		return defaults, nil
	}
	return out, nil
}

// resolveRoot returns root_path with symlinks resolved, provided it is an
// existing directory inside one of the (also resolved) AllowedRootsEnv
// directories, so neither "../" segments nor a symlinked root reach outside.
func resolveRoot(rootPath string) (string, error) {
	allowed := filepath.SplitList(os.Getenv(AllowedRootsEnv))
	if len(allowed) == 0 {
		return "", fmt.Errorf("%w: local folder sync is disabled; set %s", datasource.ErrInvalidConfig, AllowedRootsEnv)
	}
	if rootPath == "" || !filepath.IsAbs(rootPath) {
		return "", fmt.Errorf("%w: root_path must be an absolute path", datasource.ErrInvalidConfig)
	}
	root, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", fmt.Errorf("%w: root_path is not accessible: %v", datasource.ErrInvalidConfig, err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: root_path is not a directory", datasource.ErrInvalidConfig)
	}
	for _, base := range allowed {
		resolvedBase, err := filepath.EvalSymlinks(strings.TrimSpace(base))
		if err != nil || !filepath.IsAbs(resolvedBase) {
			continue
		}
		if _, err := utils.SafePathUnderBase(resolvedBase, root); err == nil {
			return root, nil
		}
	}
	return "", fmt.Errorf("%w: root_path is outside the directories allowed by %s",
		datasource.ErrInvalidConfig, AllowedRootsEnv)
}

// sync scans the folder and diffs it against prev, returning the items to
// ingest and the cursor to persist. reportDeletions emits files that are in
// prev but gone from the folder; emitAll emits settled files even when their
// recorded state is unchanged.
func (cfg *config) sync(
	ctx context.Context, prev map[string]fileState, reportDeletions, emitAll bool,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	root, err := os.OpenRoot(cfg.root)
	if err != nil {
		return nil, nil, fmt.Errorf("open root_path: %w", err)
	}
	defer func() { _ = root.Close() }() // read-only handle; a close error changes nothing

	files, err := cfg.scan(root)
	if err != nil {
		// A partial listing would make every unlisted file look deleted.
		return nil, nil, fmt.Errorf("scan root_path: %w", err)
	}
	if reportDeletions && len(files) == 0 && len(prev) > 0 {
		// An unmounted or emptied bind mount looks exactly like "all files deleted".
		return nil, nil, fmt.Errorf(
			"root_path has no matching files but %d were synced before; refusing to delete them all", len(prev))
	}

	settledBefore := time.Now().Add(-cfg.quiet)
	maxSize := utils.GetMaxFileSize()
	next := make(map[string]fileState, len(files))
	var items []types.FetchedItem
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		info := files[rel]
		old, known := prev[rel]
		if known {
			// Until a newer state is confirmed, keep the last synced one so the file
			// is neither reported deleted nor considered up to date.
			next[rel] = old
		}
		switch {
		case info.ModTime().After(settledBefore):
			continue // still being written; a later sync picks it up
		case !emitAll && known && old.Size == info.Size() && old.ModTime.Equal(info.ModTime()):
			continue
		case info.Size() > maxSize:
			items = append(items, failedItem(rel, fmt.Sprintf("file exceeds %d MB", utils.GetMaxFileSizeMB())))
			continue
		}
		content, err := root.ReadFile(filepath.FromSlash(rel))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				items = append(items, failedItem(rel, err.Error()))
			}
			continue
		}
		sum := sha256.Sum256(content)
		state := fileState{ModTime: info.ModTime(), Size: info.Size(), SHA256: hex.EncodeToString(sum[:])}
		next[rel] = state
		if !emitAll && known && old.SHA256 == state.SHA256 {
			continue // touched without a content change
		}
		if len(content) == 0 && (!known || old.Size == 0) {
			continue // nothing to index; only a file that became empty needs its knowledge updated
		}
		items = append(items, types.FetchedItem{
			ExternalID:    externalIDPrefix + rel,
			Title:         path.Base(rel),
			Content:       content,
			ContentType:   mime.TypeByExtension(path.Ext(rel)),
			FileName:      rel, // path-qualified, so knowledge folders mirror the directory tree
			UpdatedAt:     info.ModTime(),
			UpdateInPlace: true,
			Metadata:      itemMetadata(rel),
		})
	}
	if reportDeletions {
		for _, rel := range slices.Sorted(maps.Keys(prev)) {
			if _, ok := files[rel]; !ok {
				items = append(items, types.FetchedItem{
					ExternalID: externalIDPrefix + rel,
					Title:      path.Base(rel),
					IsDeleted:  true,
					Metadata:   itemMetadata(rel),
				})
			}
		}
	}
	return items, &types.SyncCursor{
		LastSyncTime:    time.Now().UTC(),
		ConnectorCursor: map[string]interface{}{"files": next},
	}, nil
}

// scan lists the in-scope regular files under root by slash-separated relative
// path. Symlinks are never followed or synced, and excluded directories are
// pruned. The os.Root also rejects any access that would escape the root.
func (cfg *config) scan(root *os.Root) (map[string]fs.FileInfo, error) {
	files := make(map[string]fs.FileInfo)
	err := fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if matchAny(cfg.exclude, rel) {
				return fs.SkipDir
			}
			return nil
		}
		// Symlinks, devices, sockets and pipes have type bits and are skipped.
		if !d.Type().IsRegular() || !matchFile(cfg.include, rel) || matchFile(cfg.exclude, rel) {
			return nil
		}
		if _, ok := supportedExtensions[strings.ToLower(strings.TrimPrefix(path.Ext(rel), "."))]; !ok {
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil // removed while scanning
		}
		if err != nil {
			return err
		}
		files[rel] = info
		return nil
	})
	return files, err
}

func matchAny(patterns []string, rel string) bool {
	for _, pattern := range patterns {
		if ok, _ := doublestar.Match(pattern, rel); ok {
			return true
		}
	}
	return false
}

// matchFile matches a file path with its extension compared case-insensitively,
// so REPORT.PDF meets **/*.pdf. Lowercase patterns are the norm; the unchanged
// path is tried too, so a pattern written as **/*.PDF keeps working.
func matchFile(patterns []string, rel string) bool {
	ext := path.Ext(rel)
	return matchAny(patterns, rel) || matchAny(patterns, strings.TrimSuffix(rel, ext)+strings.ToLower(ext))
}

func itemMetadata(rel string) map[string]string {
	return map[string]string{"channel": types.ConnectorTypeLocalFolder, "local_path": rel}
}

// failedItem reports a file that could not be read, so the sync log shows it.
func failedItem(rel, reason string) types.FetchedItem {
	metadata := itemMetadata(rel)
	metadata["error"] = reason
	return types.FetchedItem{ExternalID: externalIDPrefix + rel, Title: path.Base(rel), Metadata: metadata}
}
