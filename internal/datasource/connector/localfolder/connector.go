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
	_ datasource.Connector              = (*Connector)(nil)
	_ datasource.StreamingConnector     = (*Connector)(nil)
	_ datasource.FullStreamingConnector = (*Connector)(nil)
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

// FetchStream is the one sync engine: it walks the folder and reads, emits and
// checkpoints one file at a time, so peak memory is a single file rather than
// every changed file in the folder (Tencent/WeKnora#2136). A file enters the
// cursor only after Emit has accepted it, which is what makes a checkpoint a
// safe restart point.
func (c *Connector) FetchStream(
	ctx context.Context, ds *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.fetchStream(ctx, ds, cursor, h, false)
}

// FetchFullStream re-reads every in-scope file, so a file whose previous ingest
// failed is retried (unchanged content is still deduplicated at ingest without
// re-parsing), while cursor is retained purely as the baseline for deletion
// reconciliation. An interrupted full sync resumes from its last checkpoint
// instead of starting over.
func (c *Connector) FetchFullStream(
	ctx context.Context, ds *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.fetchStream(ctx, ds, cursor, h, true)
}

func (c *Connector) fetchStream(
	ctx context.Context, ds *types.DataSourceConfig,
	cursor *types.SyncCursor, h datasource.StreamHandler, forceFull bool,
) (*types.SyncCursor, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	return cfg.stream(ctx, cursor, h, forceFull)
}

// collector adapts the stream to the batch Connector methods, which the service
// only uses for connectors that cannot stream. It buffers, so the streaming path
// above is what keeps a real sync memory-bounded.
type collector struct{ items []types.FetchedItem }

func (h *collector) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}

func (*collector) Checkpoint(context.Context, *types.SyncCursor) error { return nil }

// FetchAll emits every in-scope file and never reports deletions: with no
// cursor there is no baseline to reconcile against.
func (c *Connector) FetchAll(ctx context.Context, ds *types.DataSourceConfig, _ []string) ([]types.FetchedItem, error) {
	h := &collector{}
	_, err := c.FetchStream(ctx, ds, nil, h)
	return h.items, err
}

// FetchIncremental diffs the folder against the file states recorded in cursor,
// emitting new and changed files plus deletions. mtime and size decide whether a
// file is read at all; a SHA-256 of its content confirms a real change.
func (c *Connector) FetchIncremental(
	ctx context.Context, ds *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	h := &collector{}
	next, err := c.FetchStream(ctx, ds, cursor, h)
	return h.items, next, err
}

// fileState is the per-file sync state kept in the cursor, keyed by the file's
// slash-separated path relative to the root.
type fileState struct {
	ModTime time.Time `json:"mtime"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
}

// cursorState is what this connector stores in the data source cursor. Files is
// the state of every file synced so far. A full sync moves that snapshot into
// FullBaseline and rebuilds Files from scratch, so a run interrupted halfway
// still knows both which files it has already re-read and which files existed
// before it started — the latter being the only safe basis for deletions.
type cursorState struct {
	Files        map[string]fileState `json:"files"`
	FullSync     bool                 `json:"full_sync,omitempty"`
	FullBaseline map[string]fileState `json:"full_baseline,omitempty"`
}

func decodeCursor(cursor *types.SyncCursor) cursorState {
	var state cursorState
	if cursor != nil && cursor.ConnectorCursor != nil {
		raw, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(raw, &state)
	}
	if state.Files == nil {
		state.Files = map[string]fileState{}
	}
	return state
}

// syncCursor snapshots the state for a checkpoint. StreamHandler.Checkpoint only
// borrows the cursor for the duration of the call, so the maps are copied rather
// than shared with the walk that keeps mutating them.
func (c cursorState) syncCursor() *types.SyncCursor {
	snapshot := cursorState{
		Files:    maps.Clone(c.Files),
		FullSync: c.FullSync,
	}
	if c.FullBaseline != nil {
		snapshot.FullBaseline = maps.Clone(c.FullBaseline)
	}
	raw, _ := json.Marshal(snapshot)
	fields := map[string]interface{}{}
	_ = json.Unmarshal(raw, &fields)
	return &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: fields}
}

// prepareCursors splits the stored cursor into the deletion baseline and the
// cursor being built. An incremental run carries its files forward. A full run
// parks them in FullBaseline once and then rebuilds Files, so resuming a partly
// finished full sync keeps both halves instead of re-reading everything.
func prepareCursors(old *types.SyncCursor, forceFull bool) (baseline map[string]fileState, next cursorState) {
	previous := decodeCursor(old)
	next = cursorState{Files: maps.Clone(previous.Files)}
	if !forceFull {
		return previous.Files, next
	}
	if previous.FullSync {
		// Resuming a full sync already in progress: Files is this run's progress.
		next.FullBaseline = previous.FullBaseline
	} else {
		next.FullBaseline = maps.Clone(previous.Files)
		next.Files = map[string]fileState{}
	}
	next.FullSync = true
	if next.FullBaseline == nil {
		next.FullBaseline = map[string]fileState{}
	}
	return next.FullBaseline, next
}

type config struct {
	root    string // absolute and symlink-free
	include []string
	exclude []string
	quiet   time.Duration
	manual  bool // this run was requested by a user, not the scheduler
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
	cfg := &config{root: root, include: include, exclude: exclude, quiet: quietPeriod, manual: ds.ManualTrigger}
	if cfg.manual {
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

// stream walks the folder once, handling a single file at a time: read it, emit
// it, record it, checkpoint. Only one file's bytes are held at any moment, so
// peak memory does not grow with the size of the folder.
//
// baseline is what the folder looked like when the last sync finished (for a
// full run, before it started) and is the only basis for deletions; next is the
// cursor being built. A file is recorded in next only after Emit accepted it, so
// an interrupted run resumes at the first file it had not finished.
func (cfg *config) stream(
	ctx context.Context, cursor *types.SyncCursor, h datasource.StreamHandler, forceFull bool,
) (*types.SyncCursor, error) {
	root, err := os.OpenRoot(cfg.root)
	if err != nil {
		return nil, fmt.Errorf("open root_path: %w", err)
	}
	defer func() { _ = root.Close() }() // read-only handle; a close error changes nothing

	files, err := cfg.scan(root)
	if err != nil {
		// A partial listing would make every unlisted file look deleted.
		return nil, fmt.Errorf("scan root_path: %w", err)
	}
	baseline, next := prepareCursors(cursor, forceFull)
	if !cfg.manual && len(files) == 0 && len(baseline) > 0 {
		// A scheduled sync cannot tell an emptied folder from a mount that broke
		// or was replaced by an empty directory, so it fails instead of deleting
		// everything. A manual sync is the user pointing at this folder and asking
		// for it to be reconciled now, so it goes through and an intentionally
		// emptied folder is applied (deletions still honour sync_deletions).
		return nil, fmt.Errorf(
			"root_path has no matching files but %d were synced before; refusing to delete them all. "+
				"Use \"sync now\" to confirm an intentionally emptied folder", len(baseline))
	}

	settledBefore := time.Now().Add(-cfg.quiet)
	maxSize := utils.GetMaxFileSize()
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info := files[rel]
		prior, known := baseline[rel]
		// keepKnown stops a full sync from forgetting a file it did not get to.
		// A full run rebuilds Files from scratch, so a file skipped here would
		// drop out of the cursor even though it is still on disk, and a later
		// deletion of it could no longer be reconciled. Carrying the previous
		// state over keeps it known without claiming it was re-read.
		keepKnown := func() {
			if forceFull && known {
				next.Files[rel] = prior
			}
		}
		if cur, seen := next.Files[rel]; forceFull && seen &&
			cur.Size == info.Size() && cur.ModTime.Equal(info.ModTime()) {
			continue // already re-read at this exact version in this run; resume past it
		}
		switch {
		case info.ModTime().After(settledBefore):
			keepKnown()
			continue // still being written; a later sync picks it up
		case !forceFull && known && prior.Size == info.Size() && prior.ModTime.Equal(info.ModTime()):
			continue
		case info.Size() > maxSize:
			// The current state is not recorded, so a file that shrinks back under
			// the limit is picked up again rather than silently staying skipped.
			keepKnown()
			if emitErr := h.Emit(ctx, failedItem(rel,
				fmt.Sprintf("file exceeds %d MB", utils.GetMaxFileSizeMB()))); emitErr != nil {
				return nil, emitErr
			}
			continue
		}
		content, err := root.ReadFile(filepath.FromSlash(rel))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // removed between the scan and the read
			}
			keepKnown()
			if emitErr := h.Emit(ctx, failedItem(rel, err.Error())); emitErr != nil {
				return nil, emitErr
			}
			continue
		}
		sum := sha256.Sum256(content)
		state := fileState{ModTime: info.ModTime(), Size: info.Size(), SHA256: hex.EncodeToString(sum[:])}
		switch {
		case !forceFull && known && prior.SHA256 == state.SHA256:
			// Touched without a content change: record the new mtime so the next
			// sync stops re-reading it, but there is nothing to ingest.
		case len(content) == 0 && (!known || prior.Size == 0):
			// Nothing to index; only a file that became empty needs its knowledge
			// updated, and that case has a prior non-empty state.
		default:
			if emitErr := h.Emit(ctx, types.FetchedItem{
				ExternalID:    externalIDPrefix + rel,
				Title:         path.Base(rel),
				Content:       content,
				ContentType:   mime.TypeByExtension(path.Ext(rel)),
				FileName:      rel, // path-qualified, so knowledge folders mirror the directory tree
				UpdatedAt:     info.ModTime(),
				UpdateInPlace: true,
				Metadata:      itemMetadata(rel),
			}); emitErr != nil {
				return nil, emitErr
			}
		}
		next.Files[rel] = state
		if err := h.Checkpoint(ctx, next.syncCursor()); err != nil {
			return nil, err
		}
	}

	for _, rel := range slices.Sorted(maps.Keys(baseline)) {
		if _, stillOnDisk := files[rel]; stillOnDisk {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if emitErr := h.Emit(ctx, types.FetchedItem{
			ExternalID: externalIDPrefix + rel,
			Title:      path.Base(rel),
			IsDeleted:  true,
			Metadata:   itemMetadata(rel),
		}); emitErr != nil {
			return nil, emitErr
		}
		delete(next.Files, rel)
		if err := h.Checkpoint(ctx, next.syncCursor()); err != nil {
			return nil, err
		}
	}

	// The walk finished, so the full-sync baseline has served its purpose and the
	// next run is an ordinary incremental sync again.
	next.FullSync = false
	next.FullBaseline = nil
	return next.syncCursor(), nil
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
