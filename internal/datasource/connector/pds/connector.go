package pds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Compile-time proof that *Connector satisfies the Connector and
// StreamingConnector interfaces. Catches signature drift at build time.
var (
	_ datasource.Connector          = (*Connector)(nil)
	_ datasource.StreamingConnector = (*Connector)(nil)
)

// checkpointEveryFiles is how many files the streaming bootstrap emits
// between cursor checkpoints, so a timed-out full sync resumes from the
// last checkpoint instead of restarting from scratch
// (Tencent/WeKnora#2136).
const checkpointEveryFiles = 50

// pdsAPI is the contract our connector uses against either the hand-
// rolled *client (OAuth access_token / refresh_token) or the official
// SDK-backed *sdkClient (AK/SK). Both implementations satisfy it, so the
// connector stays agnostic about which auth path is configured.
type pdsAPI interface {
	ListDrive(ctx context.Context) ([]pdsDrive, error)
	ListFile(ctx context.Context, driveID, parentID, marker string) ([]pdsFile, string, error)
	GetFile(ctx context.Context, driveID, fileID string) (pdsFile, error)
	GetDownloadURL(ctx context.Context, driveID, fileID string) (string, error)
	GetLastCursor(ctx context.Context, driveID string) (string, error)
	ListDelta(ctx context.Context, driveID, cursor string) ([]pdsDeltaItem, string, bool, error)
	Download(ctx context.Context, url string) ([]byte, string, error)
}

// newPDSClient returns the right implementation for the configured auth.
// AK/SK credentials route through the official alibabacloud-go SDK
// (preferred — the gateway enforces ACS3-HMAC-SHA256 with strict signed-
// header semantics; the SDK is the tested implementation). Anything else
// (access_token, refresh_token) uses the hand-rolled client.
func newPDSClient(cfg *Config) (pdsAPI, error) {
	if cfg.AccessKeyID != "" && cfg.AccessKeySecret != "" {
		return newSDKClient(cfg)
	}
	return newClient(cfg), nil
}

// Connector implements datasource.Connector + StreamingConnector for
// Alibaba Cloud PDS. It is stateless: every method rebuilds a client from
// the per-data-source Config, so a single Connector instance is safe to
// share across data sources and goroutines.
type Connector struct{}

// NewConnector creates a stateless PDS connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypePDS }

// Validate verifies the credentials by attempting to list the drives the
// credentials can read. A successful Validate also means the resource
// picker will be able to render the drive root.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	cli, err := newPDSClient(cfg)
	if err != nil {
		return fmt.Errorf("pds client init: %w", err)
	}
	drives, err := cli.ListDrive(ctx)
	if err != nil {
		return fmt.Errorf("pds connection failed: %w", err)
	}
	if len(drives) == 0 {
		logger.Warnf(ctx, "[PDS] Validate: credentials have zero drives for domain %s", cfg.DomainID)
	}
	return nil
}

// resolveDriveID determines which drive to sync and returns the parsed
// settings plus the drive ID. Resolution order: Settings.drive_id, the
// PDS_DRIVE_ID env var, then the picker selection in ResourceIDs.
func resolveDriveID(ctx context.Context, config *types.DataSourceConfig) (Settings, string, error) {
	if config == nil {
		return Settings{}, "", fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	settings := parseSettings(config)
	driveID := settings.DriveID
	if driveID == "" {
		driveID = DriveIDFromConfigOrEnv(config)
	}
	if driveID == "" {
		logger.Warnf(ctx,
			"[PDS] drive_id resolution failed: settings.drive_id=%q env=%s=%q resource_ids=%v credentials_keys=%v",
			settings.DriveID, EnvDriveID, os.Getenv(EnvDriveID),
			config.ResourceIDs, credentialKeys(config))
		return settings, "", fmt.Errorf(
			"pds: drive_id is required (pick a drive in the resources step or set %s)", EnvDriveID)
	}
	return settings, driveID, nil
}

// ListResources implements the 3-level lazy picker:
//
//   - parentID == ""                     -> list all drives the credentials can read
//   - parentID == "<driveID>:" (or bare "<driveID>") -> top-level entries of that drive
//   - parentID == "<driveID>:<folderID>" -> sub-folders + files of that folder
//
// Drive nodes are keyed "<driveID>:" (pdsResourceID with an empty file
// part) so children — whose ParentID is normalized to the same token —
// attach to them in the frontend tree. Bare drive IDs are accepted
// defensively for selections saved before that encoding existed.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli, err := newPDSClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("pds client init: %w", err)
	}

	if parentID == "" {
		drives, err := cli.ListDrive(ctx)
		if err != nil {
			return nil, fmt.Errorf("pds list drives: %w", err)
		}
		out := make([]types.Resource, 0, len(drives))
		for _, d := range drives {
			out = append(out, types.Resource{
				ExternalID:  pdsResourceID(d.DriveID, ""),
				Name:        d.Name,
				Type:        "drive",
				URL:         d.Description,
				ModifiedAt:  d.CreatedAt,
				HasChildren: true,
				Metadata: map[string]interface{}{
					"domain_id": cfg.DomainID,
					"drive_id":  d.DriveID,
				},
			})
		}
		return out, nil
	}

	driveID, folderID := splitPDSResourceID(parentID)
	if driveID == "" {
		// Bare token without the "<driveID>:" prefix — treat it as a
		// drive ID (legacy selections) rather than failing the picker.
		driveID, folderID = parentID, ""
	}

	// Follow the marker pagination so folders with more than one page of
	// children surface completely in the picker.
	var out []types.Resource
	marker := ""
	for page := 0; page < 100; page++ {
		files, nextMarker, err := cli.ListFile(ctx, driveID, folderID, marker)
		if err != nil {
			return nil, fmt.Errorf("pds list folder %s: %w", parentID, err)
		}
		for _, f := range files {
			out = append(out, pdsFileToResource(f, driveID))
		}
		if nextMarker == "" {
			break
		}
		marker = nextMarker
	}
	return out, nil
}

// ResolveResourceAncestors walks every selected resource back to its
// drive root, returning the resourceIDs of every folder that needs to be
// expanded so a lazily-loaded picker can reveal the existing selection.
//
// The PDS API does not expose a single-file "give me the parent" query,
// so we walk top-down from each drive root and match selections as we
// go. Best-effort: a broken path stays collapsed; the picker falls back
// to having the user re-expand by hand.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli, err := newPDSClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("pds client init: %w", err)
	}

	seen := make(map[string]bool)
	ancestors := make([]string, 0)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ancestors = append(ancestors, id)
		}
	}

	// Group selections by drive. A selection that is the drive itself
	// ("<driveID>:" or bare "<driveID>") has nothing to reveal.
	driveSelections := make(map[string][]string) // driveID -> fileIDs
	for _, rid := range resourceIDs {
		driveID, fileID := splitPDSResourceID(rid)
		if driveID == "" {
			driveID, fileID = rid, "" // bare drive ID
		}
		if driveID == "" {
			continue
		}
		// Always surface the drive's own resourceID ("<driveID>:") so the
		// picker can match it against its stored tree node.
		add(pdsResourceID(driveID, ""))
		if fileID == "" {
			continue // top-level drive selection
		}
		driveSelections[driveID] = append(driveSelections[driveID], fileID)
	}

	for driveID, fileIDs := range driveSelections {
		remaining := make(map[string]bool, len(fileIDs))
		for _, id := range fileIDs {
			remaining[id] = true
		}
		// parentChain[fileID] = resourceID of that file's containing folder
		parentChain := make(map[string]string)
		// Walk top-down with an explicit queue, starting at the drive root.
		queue := []string{""}
		for len(queue) > 0 && len(remaining) > 0 {
			cur := queue[0]
			queue = queue[1:]
			children, _, err := cli.ListFile(ctx, driveID, cur, "")
			if err != nil {
				logger.Warnf(ctx, "[PDS] resolve ancestors: list drive=%s folder=%s: %v",
					driveID, cur, err)
				break // best-effort
			}
			// cur is the folder we're enumerating; its resourceID is the
			// parent of every child found below.
			curRID := pdsResourceID(driveID, cur)
			for _, child := range children {
				if remaining[child.FileID] {
					delete(remaining, child.FileID)
					// Reveal the chain from the drive root down to this
					// file's parent, walking up via the parent chain we
					// built while discovering folders.
					add(curRID)
					node := cur
					for node != "" {
						parent, ok := parentChain[node]
						if !ok {
							break
						}
						add(parent)
						_, parentFolder := splitPDSResourceID(parent)
						node = parentFolder
					}
				}
				if strings.EqualFold(child.Type, "folder") {
					parentChain[child.FileID] = curRID
					queue = append(queue, child.FileID)
				}
			}
		}
	}

	return ancestors, nil
}

// pdsCursor is the wire format stored in SyncCursor.ConnectorCursor.
//
// ListDeltaCursor is PDS's opaque server cursor, round-tripped verbatim.
// DriveFiles maps every fileID we have synced from this drive to its
// last-seen updatedAt. It exists so a re-bootstrap (full walk) can emit
// tombstones for files that disappeared since the previous sync; it is
// NOT used to detect deletions during delta syncs — the delta feed's op
// field is the only correct deletion signal there.
type pdsCursor struct {
	LastSyncTime    time.Time         `json:"last_sync_time"`
	ListDeltaCursor string            `json:"list_delta_cursor,omitempty"`
	DriveFiles      map[string]string `json:"drive_files,omitempty"`
}

// parsePrevCursor decodes the persisted connector cursor, tolerating a
// nil or malformed payload (returns nil, which means "first run").
func parsePrevCursor(old *types.SyncCursor) *pdsCursor {
	if old == nil || old.ConnectorCursor == nil {
		return nil
	}
	b, err := json.Marshal(old.ConnectorCursor)
	if err != nil {
		return nil
	}
	var p pdsCursor
	if err := json.Unmarshal(b, &p); err != nil {
		return nil
	}
	return &p
}

// toSyncCursor serializes a pdsCursor into the service's SyncCursor shape.
func toSyncCursor(p *pdsCursor) *types.SyncCursor {
	cursorMap := make(map[string]interface{})
	b, _ := json.Marshal(p)
	_ = json.Unmarshal(b, &cursorMap)
	return &types.SyncCursor{
		LastSyncTime:    p.LastSyncTime,
		ConnectorCursor: cursorMap,
	}
}

// needsBootstrap reports whether the sync must start with a full drive
// walk instead of the delta feed. Three states qualify: no cursor at all
// (true first run), a cursor without a server delta position, and the
// "stale handshake" state where an earlier run captured the delta cursor
// but never observed any files (the delta feed alone would then return
// nothing forever).
func needsBootstrap(prev *pdsCursor) bool {
	return prev == nil || prev.ListDeltaCursor == "" || len(prev.DriveFiles) == 0
}

// sink abstracts where fetched items and cursor checkpoints go. The
// streaming production path wires it to the service's StreamHandler; the
// buffered paths (FetchAll/FetchIncremental) collect into a slice.
type sink struct {
	emit       func(ctx context.Context, item types.FetchedItem) error
	checkpoint func(ctx context.Context, cursor *pdsCursor) error // optional
}

// FetchAll performs a full sync of the configured drive. The resourceIDs
// parameter is ignored by design: a PDS data source syncs the whole
// selected drive (the picker's drive selection determines scope; folder
// selections narrow only what the picker revealed, not the sync itself).
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, _ []string,
) ([]types.FetchedItem, error) {
	settings, driveID, err := resolveDriveID(ctx, config)
	if err != nil {
		return nil, err
	}
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli, err := newPDSClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("pds client init: %w", err)
	}

	var items []types.FetchedItem
	s := sink{emit: func(_ context.Context, item types.FetchedItem) error {
		items = append(items, item)
		return nil
	}}
	if _, err := c.syncFullDrive(ctx, cli, driveID, settings, nil, s); err != nil {
		return nil, err
	}
	return items, nil
}

// FetchIncremental syncs changes since the previous cursor. First run (or
// a stale handshake) falls back to a full drive walk; later runs page
// through the PDS change feed (file/list_delta) and emit deletions based
// on each delta item's op — never on absence from a delta page, since the
// feed only returns changed items.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, old *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	settings, driveID, err := resolveDriveID(ctx, config)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli, err := newPDSClient(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("pds client init: %w", err)
	}

	prev := parsePrevCursor(old)
	var items []types.FetchedItem
	s := sink{emit: func(_ context.Context, item types.FetchedItem) error {
		items = append(items, item)
		return nil
	}}
	var cursor *pdsCursor
	if needsBootstrap(prev) {
		cursor, err = c.syncFullDrive(ctx, cli, driveID, settings, prev, s)
	} else {
		cursor, err = c.syncDeltaDrive(ctx, cli, driveID, settings, prev, s)
	}
	if err != nil {
		return nil, nil, err
	}
	return items, toSyncCursor(cursor), nil
}

// FetchStream is the production sync path. It emits each file as it is
// fetched and checkpoints the cursor at page boundaries (and every
// checkpointEveryFiles files during the bootstrap walk), so a timed-out
// sync resumes from the last checkpoint instead of starting over.
func (c *Connector) FetchStream(
	ctx context.Context, config *types.DataSourceConfig,
	old *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	settings, driveID, err := resolveDriveID(ctx, config)
	if err != nil {
		return nil, err
	}
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli, err := newPDSClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("pds client init: %w", err)
	}

	prev := parsePrevCursor(old)
	s := sink{
		emit: h.Emit,
		checkpoint: func(ctx context.Context, p *pdsCursor) error {
			return h.Checkpoint(ctx, toSyncCursor(p))
		},
	}
	var cursor *pdsCursor
	if needsBootstrap(prev) {
		cursor, err = c.syncFullDrive(ctx, cli, driveID, settings, prev, s)
	} else {
		cursor, err = c.syncDeltaDrive(ctx, cli, driveID, settings, prev, s)
	}
	if err != nil {
		return nil, err
	}
	return toSyncCursor(cursor), nil
}

// syncFullDrive walks the whole drive folder tree, emits every supported
// file, and returns a fresh cursor bootstrapped with the latest server
// delta position. Because the walk is a COMPLETE listing, absence from it
// is a reliable deletion signal: files recorded in prev but missing now
// are tombstoned. (Contrast syncDeltaDrive, where absence from a delta
// page means nothing.)
func (c *Connector) syncFullDrive(
	ctx context.Context, cli pdsAPI, driveID string, settings Settings,
	prev *pdsCursor, s sink,
) (*pdsCursor, error) {
	if prev != nil && prev.ListDeltaCursor != "" {
		logger.Infof(ctx,
			"[PDS] cursor exists but drive_files is empty (stale handshake); re-bootstrapping via full walk")
	} else {
		logger.Infof(ctx, "[PDS] bootstrap: capturing delta cursor then full drive walk")
	}

	// Capture the delta cursor BEFORE walking, not after. Order matters:
	// a file uploaded between "the walk listed its folder" and "the
	// cursor is captured" would otherwise be missed by the walk AND fall
	// before the persisted cursor, so no future list_delta would ever
	// return it — the file would stay invisible to incremental syncs.
	// Capturing first closes the gap: such a file surfaces in the next
	// delta page, and the duplicate emit is a harmless upsert (ingest
	// dedupes by external_id).
	deltaCursor, cerr := cli.GetLastCursor(ctx, driveID)
	if cerr != nil {
		// No usable cursor yet: we still full-walk now, but leave
		// ListDeltaCursor empty so the NEXT sync re-bootstraps instead of
		// trusting a half-initialized cursor. No file can be lost this way.
		logger.Warnf(ctx, "[PDS] delta cursor capture before bootstrap failed (next sync will re-bootstrap): %v", cerr)
	}

	files, err := c.listAllFiles(ctx, cli, driveID)
	if err != nil {
		return nil, fmt.Errorf("pds walk drive %s: %w", driveID, err)
	}
	folderPath := buildFolderPathMap(files)

	cursor := &pdsCursor{
		LastSyncTime: time.Now().UTC(),
		DriveFiles:   make(map[string]string),
	}
	present := make(map[string]string) // fileID -> updatedAt, for deletion diff
	emitted := 0
	for _, f := range files {
		if !settings.IsSupportedFile(f.Name) {
			continue
		}
		ts := f.UpdatedAt.Format(time.RFC3339)
		if strings.EqualFold(f.Type, "folder") {
			// Folders are not ingested as documents; record them so a
			// later re-bootstrap can detect deleted folders too.
			present[f.FileID] = ts
			cursor.DriveFiles[f.FileID] = ts
			continue
		}
		item, ferr := c.fetchOneFile(ctx, cli, driveID, f, folderPath)
		if ferr != nil {
			logger.Warnf(ctx, "[PDS] fetch file %s (drive=%s) failed: %v", f.FileID, driveID, ferr)
			continue
		}
		if err := s.emit(ctx, item); err != nil {
			return nil, err
		}
		present[f.FileID] = ts
		cursor.DriveFiles[f.FileID] = ts
		emitted++
		// Checkpoint at regular intervals so a timed-out bootstrap resumes
		// from the last batch instead of the drive root.
		if s.checkpoint != nil && emitted%checkpointEveryFiles == 0 {
			if err := s.checkpoint(ctx, cursor); err != nil {
				return nil, err
			}
		}
	}

	// Deletion diff — valid here because the walk is a complete listing.
	if prev != nil {
		for fileID := range prev.DriveFiles {
			if _, still := present[fileID]; still {
				continue
			}
			if err := s.emit(ctx, types.FetchedItem{
				ExternalID:       pdsFileExternalID(driveID, fileID),
				IsDeleted:        true,
				SourceResourceID: driveID,
				Metadata:         baseMetadata(driveID, fileID, ""),
			}); err != nil {
				return nil, err
			}
		}
	}

	// Record the cursor captured before the walk (see the top of this
	// function for why capture-before-walk is the safe order).
	if cerr == nil {
		cursor.ListDeltaCursor = deltaCursor
	}
	return cursor, nil
}

// syncDeltaDrive pages through the PDS change feed (file/list_delta)
// since the persisted cursor. Each delta item carries the op that changed
// it; deletions are detected via op == "delete" and nothing else — the
// feed only returns CHANGED items, so a file missing from a page is
// almost always just unchanged. Files observed with any other op are
// re-fetched and upserted (create/overwrite/update/move all converge on
// "emit the current content").
func (c *Connector) syncDeltaDrive(
	ctx context.Context, cli pdsAPI, driveID string, settings Settings,
	prev *pdsCursor, s sink,
) (*pdsCursor, error) {
	cursor := &pdsCursor{
		LastSyncTime:    time.Now().UTC(),
		ListDeltaCursor: prev.ListDeltaCursor,
		DriveFiles:      make(map[string]string, len(prev.DriveFiles)),
	}
	// Carry over the baseline so files the delta feed doesn't mention
	// stay tracked.
	for k, v := range prev.DriveFiles {
		cursor.DriveFiles[k] = v
	}

	listCursor := prev.ListDeltaCursor
	recovered := false
	// Per-sync cache of folder metadata gathered while resolving paths.
	folderCache := make(map[string]pdsFile)
	for {
		items, nextCursor, hasMore, err := cli.ListDelta(ctx, driveID, listCursor)
		if err != nil {
			var ce *cursorExpiredError
			if errors.As(err, &ce) && !recovered {
				logger.Warnf(ctx, "[PDS] cursor expired, re-handshaking via file/get_last_cursor")
				fresh, herr := cli.GetLastCursor(ctx, driveID)
				if herr != nil {
					return nil, fmt.Errorf("pds delta re-handshake: %w", herr)
				}
				listCursor = fresh
				cursor.ListDeltaCursor = fresh
				recovered = true
				continue
			}
			return nil, err
		}
		recovered = false

		// Per-page folder map for path metadata. Delta pages can be sparse,
		// so a parent folder referenced by an item may not be in the page;
		// the item then falls back to its name_path or loses folder
		// metadata for this event — acceptable for incremental.
		pageFiles := make([]pdsFile, 0, len(items))
		for _, it := range items {
			pageFiles = append(pageFiles, it.resolvedFile())
		}
		pageFolderPath := buildFolderPathMap(pageFiles)

		for _, it := range items {
			f := it.resolvedFile()
			if f.FileID == "" {
				continue
			}
			if it.isDelete() {
				// Deletion: tombstone files (folders were never ingested),
				// and drop the entry from the baseline either way.
				if !strings.EqualFold(f.Type, "folder") {
					if err := s.emit(ctx, types.FetchedItem{
						ExternalID:       pdsFileExternalID(driveID, f.FileID),
						IsDeleted:        true,
						SourceResourceID: driveID,
						Metadata:         baseMetadata(driveID, f.FileID, ""),
					}); err != nil {
						return nil, err
					}
				}
				delete(cursor.DriveFiles, f.FileID)
				continue
			}
			if !settings.IsSupportedFile(f.Name) {
				continue
			}
			ts := f.UpdatedAt.Format(time.RFC3339)
			cursor.DriveFiles[f.FileID] = ts
			if strings.EqualFold(f.Type, "folder") {
				// Cache the folder's own metadata so files inside it can
				// resolve their path without an extra file/get round-trip.
				folderCache[f.FileID] = f
				continue // folders are tracked but not ingested
			}
			// Reconstruct the readable path (delta items carry none) so
			// the file lands in its KB folder, not the KB root.
			if f.FilePath == "" {
				f.FilePath = c.resolveDeltaFilePath(ctx, cli, driveID, f, folderCache)
			}
			item, ferr := c.fetchOneFile(ctx, cli, driveID, f, pageFolderPath)
			if ferr != nil {
				logger.Warnf(ctx, "[PDS] fetch file %s (drive=%s) failed: %v", f.FileID, driveID, ferr)
				continue
			}
			if err := s.emit(ctx, item); err != nil {
				return nil, err
			}
		}

		if !hasMore || nextCursor == "" {
			break
		}
		listCursor = nextCursor
		cursor.ListDeltaCursor = nextCursor
		// Checkpoint at every page boundary so a timed-out sync resumes
		// from the last page rather than from the start.
		if s.checkpoint != nil {
			if err := s.checkpoint(ctx, cursor); err != nil {
				return nil, err
			}
		}
	}
	return cursor, nil
}

// listAllFiles recursively walks a drive's folder tree via repeated
// ListFile calls.
//
// Why not the flat scan API: PDS's scan returns every file flattened
// WITHOUT folder entries — so each file's parent_file_id references a
// folder ID we never get the name for. Walk the tree explicitly so we can
// stamp each file with its full readable path (e.g. "/02/pgsql.md") on
// the way back up.
//
// Cost: one ListFile round-trip per folder page in the drive. For the
// typical PDS drive (tens of folders) this is well under a second total.
//
// The returned slice carries both files and folders; callers filter by
// type. Each file has its FilePath set to "<parentPath>/<name>" (no
// leading slash on drive-root files). Folders get FilePath =
// "<parentPath>/<name>/".
func (c *Connector) listAllFiles(
	ctx context.Context, cli pdsAPI, driveID string,
) ([]pdsFile, error) {
	var out []pdsFile
	var walk func(parentID, parentPath string, depth int) error
	walk = func(parentID, parentPath string, depth int) error {
		if depth > 32 {
			return fmt.Errorf("pds: folder tree depth exceeds 32 (possible cycle)")
		}
		marker := ""
		for {
			files, nextMarker, err := cli.ListFile(ctx, driveID, parentID, marker)
			if err != nil {
				return fmt.Errorf("pds list file (parent=%q): %w", parentID, err)
			}
			for _, f := range files {
				if strings.EqualFold(f.Type, "folder") {
					f.FilePath = parentPath + f.Name + "/"
					out = append(out, f)
					if err := walk(f.FileID, f.FilePath, depth+1); err != nil {
						// Don't fail the whole sync on a single subtree
						// error — log and continue. The remaining folders
						// may still be reachable.
						logger.Warnf(ctx,
							"[PDS] recursive walk into folder %s (id=%s) failed: %v",
							f.Name, f.FileID, err)
					}
				} else {
					// Drive-root files get a bare name (no leading slash).
					// Nested files get the accumulated parentPath prefix
					// which already starts with "/<folder>/...".
					if parentPath == "/" {
						f.FilePath = f.Name
					} else {
						f.FilePath = parentPath + f.Name
					}
					out = append(out, f)
				}
			}
			if nextMarker == "" {
				return nil
			}
			marker = nextMarker
		}
	}
	if err := walk("", "/", 0); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveDeltaFilePath reconstructs the readable path of a delta item by
// walking UP its parent chain via file/get. Delta pages are sparse — a
// file's ancestor folders are almost never in the page itself — so the
// bootstrap walker's FilePath stamping isn't available on the incremental
// path. Without this reconstruction an item synced via delta carries only
// its basename and lands in the KB root instead of its folder.
//
// Lookups are cached in folderCache (shared per sync) so siblings and
// files in the same subtree pay one file/get per folder, not per file.
//
// Best-effort: if any lookup fails BEFORE reaching the drive root, the
// partial chain is untrustworthy and "" is returned — the item then falls
// back to basename placement (the pre-fix behavior) rather than being
// stamped with a wrong path. A name_path reported by PDS short-circuits
// the walk entirely.
func (c *Connector) resolveDeltaFilePath(
	ctx context.Context, cli pdsAPI, driveID string, f pdsFile,
	folderCache map[string]pdsFile,
) string {
	if f.NamePath != "" {
		return f.NamePath
	}
	names := make([]string, 0, 4)
	parentID := f.ParentID
	reachedRoot := false
	for depth := 0; depth < 32; depth++ {
		if parentID == "" || strings.EqualFold(parentID, "root") {
			reachedRoot = true
			break
		}
		info, ok := folderCache[parentID]
		if !ok {
			got, err := cli.GetFile(ctx, driveID, parentID)
			if err != nil {
				logger.Warnf(ctx, "[PDS] resolve path: get folder %s (drive=%s): %v",
					parentID, driveID, err)
				break
			}
			info = got
			folderCache[parentID] = info
		}
		names = append([]string{info.Name}, names...)
		parentID = info.ParentID
	}
	if !reachedRoot {
		return "" // partial chain — don't stamp a wrong path
	}
	if len(names) == 0 {
		return f.Name // drive-root file
	}
	return "/" + strings.Join(names, "/") + "/" + f.Name
}

// fetchOneFile downloads a single PDS file's bytes and packages them into
// a FetchedItem. The download URL is fetched fresh each call because PDS
// signed URLs expire. folderPath feeds the pds_path / pds_folder metadata
// for callers that only have a flat listing (delta pages).
func (c *Connector) fetchOneFile(
	ctx context.Context, cli pdsAPI, driveID string, f pdsFile,
	folderPath map[string]string,
) (types.FetchedItem, error) {
	url, err := cli.GetDownloadURL(ctx, driveID, f.FileID)
	if err != nil {
		return types.FetchedItem{}, fmt.Errorf("get_download_url: %w", err)
	}
	body, contentType, err := cli.Download(ctx, url)
	if err != nil {
		return types.FetchedItem{}, fmt.Errorf("download: %w", err)
	}
	if contentType == "" {
		contentType = f.ContentType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	fileName := f.Name
	if fileName == "" {
		fileName = f.FileID
	}

	// Prefer the readable path stamped by the recursive folder walker
	// (listAllFiles), then the name_path PDS itself reports (delta
	// events), then the folder map built from a flat listing.
	switch {
	case f.FilePath != "":
		fileName = f.FilePath
	case f.NamePath != "":
		fileName = f.NamePath
	case fileFolderPath(f, folderPath) != "":
		fileName = fileFolderPath(f, folderPath) + "/" + fileName
	}

	return types.FetchedItem{
		ExternalID:       pdsFileExternalID(driveID, f.FileID),
		Title:            f.Name,
		Content:          body,
		ContentType:      contentType,
		FileName:         fileName,
		URL:              url,
		UpdatedAt:        f.UpdatedAt,
		SourceResourceID: driveID,
		Metadata:         baseMetadataWithFolder(driveID, f, folderPath, url),
	}, nil
}

// pdsFileExternalID is the stable external_id we emit. Format:
// "pds:<driveID>:<fileID>". Drive IDs are globally unique within a PDS
// deployment; cross-deployment collisions (two PDS data sources in one
// KB with overlapping drive IDs) are the user's responsibility to avoid —
// one drive per data source is the recommended config.
func pdsFileExternalID(driveID, fileID string) string {
	return "pds:" + driveID + ":" + fileID
}

// credentialKeys returns the non-secret credential keys present on the
// config. Used by the drive_id resolution log to confirm whether the
// credentials map reached the connector at all (e.g. to distinguish "the
// row was never saved" from "the row saved but the credentials didn't").
func credentialKeys(config *types.DataSourceConfig) []string {
	if config == nil {
		return nil
	}
	keys := make([]string, 0, len(config.Credentials))
	for k := range config.Credentials {
		keys = append(keys, k)
	}
	return keys
}
