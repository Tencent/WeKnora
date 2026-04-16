package nutstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connector implements the datasource.Connector interface for Nutstore WebDAV.
type Connector struct {
	syncConfig datasource.SyncConfig
}

// NewConnector creates a new Nutstore connector.
func NewConnector() *Connector {
	return &Connector{
		syncConfig: datasource.LoadSyncConfig(),
	}
}

// Type returns the connector type identifier.
func (c *Connector) Type() string {
	return types.ConnectorTypeNutstore
}

// Validate verifies connectivity by sending an OPTIONS request.
// When settings are available (edit stage), also verifies root_path exists.
// At creation stage, only credentials are available (no settings),
// so we only validate Ping. RootPath is validated fully during ListResources.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config.Credentials, config.Settings)
	if err != nil {
		return err
	}

	client := NewClient(cfg)
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("nutstore connection failed: %w", err)
	}

	// When settings include root_path (edit stage), verify the path exists
	if cfg.RootPath != "" && cfg.RootPath != "/" {
		_, err := client.ListDirectory(ctx, cfg.RootPath, "0")
		if err != nil {
			return fmt.Errorf("root path %q not found or not accessible: %w", cfg.RootPath, err)
		}
	}

	return nil
}

// ListResources lists files and directories under the configured root path.
// Uses Depth:1 (single level) to quickly return top-level items for the UI.
// The full recursive traversal happens later during sync (FetchAll → expandResources).
func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig) ([]types.Resource, error) {
	cfg, err := parseConfig(config.Credentials, config.Settings)
	if err != nil {
		return nil, err
	}

	client := NewClient(cfg)
	files, err := client.ListDirectory(ctx, cfg.RootPath, "1")
	if err != nil {
		return nil, fmt.Errorf("list nutstore resources: %w", err)
	}

	resources := make([]types.Resource, 0, len(files))
	for _, f := range files {
		resType := "file"
		externalID := f.Path
		if f.IsDir {
			resType = "folder"
			// Ensure directory paths end with "/" so expandResources can identify them
			if !strings.HasSuffix(externalID, "/") {
				externalID += "/"
			}
		}

		parentPath := path.Dir(f.Path)
		if parentPath == "." {
			parentPath = "/"
		}

		metadata := map[string]interface{}{
			"size":         strconv.FormatInt(f.Size, 10),
			"content_type": f.ContentType,
		}

		resources = append(resources, types.Resource{
			ExternalID: externalID,
			Name:       f.Name,
			Type:       resType,
			ParentID:   parentPath,
			ModifiedAt: f.LastModified,
			Metadata:   metadata,
		})
	}

	return resources, nil
}

// FetchAll performs a full sync of the specified resources.
// resourceIDs are file/directory paths selected by the user.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	cfg, err := parseConfig(config.Credentials, config.Settings)
	if err != nil {
		return nil, err
	}

	client := NewClient(cfg)

	// Expand directory resourceIDs to individual files
	filePaths, err := c.expandResources(ctx, client, resourceIDs)
	if err != nil {
		return nil, err
	}

	// Filter by file_types if configured (empty = sync all)
	if len(cfg.FileTypes) > 0 {
		filtered := filePaths[:0]
		for _, fi := range filePaths {
			if matchesFileTypes(fi.Name, cfg.FileTypes) {
				filtered = append(filtered, fi)
			}
		}
		filePaths = filtered
	}

	var items []types.FetchedItem
	for _, fi := range filePaths {
		item, err := c.fetchFile(ctx, client, fi)
		if err != nil {
			logger.Warnf(ctx, "failed to fetch %s: %v", fi.Path, err)
			items = append(items, types.FetchedItem{
				ExternalID: fi.Path,
				Title:      nameWithoutExt(fi.Name),
				Metadata: map[string]string{
					"error": err.Error(),
				},
			})
			continue
		}
		items = append(items, *item)
	}

	return items, nil
}

// FetchIncremental performs incremental sync by comparing WebDAV file list
// against existing Knowledge records in the database.
// The cursor only stores LastSyncTime for logging; actual comparison is done
// by the service layer querying the Knowledge table.
func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	// For now, FetchIncremental delegates to FetchAll and lets the service layer
	// handle dedup via external_id matching (delete + re-create pattern).
	// This is the same pattern used by the Feishu connector where the cursor
	// tracks edit times in-memory. A more efficient DB-based comparison
	// can be added later without changing the Connector interface.

	items, err := c.FetchAll(ctx, config, config.ResourceIDs)
	if err != nil {
		return nil, nil, err
	}

	nextCursor := &types.SyncCursor{
		LastSyncTime: time.Now(),
	}

	return items, nextCursor, nil
}

// expandResources expands directory paths to individual file FileInfos.
func (c *Connector) expandResources(ctx context.Context, client *Client, resourceIDs []string) ([]FileInfo, error) {
	var allFiles []FileInfo
	seen := make(map[string]bool)

	for _, resID := range resourceIDs {
		// Check if it's a directory by trying to list it
		if strings.HasSuffix(resID, "/") || resID == "/" {
			files, err := client.ListDirectoryRecursive(ctx, resID)
			if err != nil {
				logger.Warnf(ctx, "failed to list directory %s: %v", resID, err)
				continue
			}
			for _, f := range files {
				if !f.IsDir && !seen[f.Path] {
					seen[f.Path] = true
					allFiles = append(allFiles, f)
				}
			}
		} else {
			// Single file - get its info
			infos, err := client.ListDirectory(ctx, resID, "0")
			if err != nil {
				logger.Warnf(ctx, "failed to get info for %s: %v", resID, err)
				continue
			}
			for _, f := range infos {
				if !f.IsDir && !seen[f.Path] {
					seen[f.Path] = true
					allFiles = append(allFiles, f)
				}
			}
		}
	}

	return allFiles, nil
}

// fetchFile fetches a single file, differentiating between parseable and unparseable files.
func (c *Connector) fetchFile(ctx context.Context, client *Client, fi FileInfo) (*types.FetchedItem, error) {
	fileName := fi.Name
	title := nameWithoutExt(fileName)
	parentDir := path.Dir(fi.Path)
	if parentDir == "." {
		parentDir = "/"
	}

	baseMeta := map[string]string{
		"channel":     "nutstore",
		"source_path": fi.Path,
		"file_size":   strconv.FormatInt(fi.Size, 10),
	}

	if isParseableFile(fileName) {
		// Parseable file: download content (will be uploaded to storage like CStore)
		content, contentType, err := client.DownloadFile(ctx, fi.Path)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", fi.Path, err)
		}

		return &types.FetchedItem{
			ExternalID:       fi.Path,
			Title:            title,
			Content:          content,
			ContentType:      contentType,
			FileName:         fileName,
			UpdatedAt:        fi.LastModified,
			SourceResourceID: parentDir,
			Metadata:         baseMeta,
		}, nil
	}

	// Unparseable file: only get share link, no download
	shareURL, _ := client.GetShareURL(ctx, fi.Path)

	baseMeta["parse_skip"] = "true"

	return &types.FetchedItem{
		ExternalID:       fi.Path,
		Title:            title,
		Content:          nil,
		FileName:         fileName,
		URL:              shareURL,
		UpdatedAt:        fi.LastModified,
		SourceResourceID: parentDir,
		Metadata:         baseMeta,
	}, nil
}

// isParseableFile checks if a file can be parsed by the docreader pipeline.
// Mirrors the logic in knowledge.go:isValidFileType.
func isParseableFile(filename string) bool {
	ext := strings.ToLower(path.Ext(filename))
	ext = strings.TrimPrefix(ext, ".")
	switch ext {
	case "pdf", "txt", "docx", "doc", "md", "markdown",
		"png", "jpg", "jpeg", "gif",
		"csv", "xlsx", "xls", "pptx", "ppt", "json",
		"mp3", "wav", "m4a", "flac", "ogg":
		return true
	default:
		return false
	}
}

// nameWithoutExt returns the file name without extension.
func nameWithoutExt(name string) string {
	ext := path.Ext(name)
	if ext == "" {
		return name
	}
	return strings.TrimSuffix(name, ext)
}

// matchesFileTypes checks if a filename's extension is in the allowed list.
// Extensions in the list should be without dots (e.g., "pdf", "docx").
func matchesFileTypes(filename string, fileTypes []string) bool {
	ext := strings.ToLower(path.Ext(filename))
	ext = strings.TrimPrefix(ext, ".")
	if ext == "" {
		return false
	}
	for _, ft := range fileTypes {
		if strings.ToLower(ft) == ext {
			return true
		}
	}
	return false
}

// FetchStream performs a full streaming sync.
func (c *Connector) FetchStream(ctx context.Context, config *types.DataSourceConfig) (datasource.FetchStream, error) {
	return c.fetchStream(ctx, config, nil)
}

// FetchStreamIncremental performs an incremental streaming sync.
func (c *Connector) FetchStreamIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) (datasource.FetchStream, error) {
	return c.fetchStream(ctx, config, cursor)
}

const defaultFileInfoChCap = 100

func (c *Connector) fetchStream(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) (datasource.FetchStream, error) {
	client, err := newClientFromConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	var prevSnapshot *NutstoreSnapshot
	if cursor != nil && cursor.ConnectorCursor != nil {
		prevSnapshot = loadSnapshot(cursor)
	}

	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		cfg, _ := parseConfig(config.Credentials, config.Settings)
		if cfg != nil && cfg.RootPath != "" {
			resourceIDs = []string{cfg.RootPath}
		} else {
			resourceIDs = []string{"/"}
		}
	}

	dirs, allowedFiles, fullDirs, err := c.expandDirectories(ctx, client, resourceIDs)
	if err != nil {
		return nil, fmt.Errorf("expand directories: %w", err)
	}

	stream := datasource.NewAsyncFetchStream(c.syncConfig.DownloadWorkers)
	go c.runStreamPipeline(ctx, client, stream, dirs, prevSnapshot, allowedFiles, fullDirs)
	return stream, nil
}

func (c *Connector) runStreamPipeline(
	ctx context.Context,
	client *Client,
	stream *datasource.AsyncFetchStream,
	dirs []string,
	prevSnapshot *NutstoreSnapshot,
	allowedFiles map[string]bool,
	fullDirs map[string]bool,
) {
	// Stage 1: List all files via streaming BFS
	fileInfoCh := make(chan FileInfo, defaultFileInfoChCap)
	listErrCh := make(chan error, 1)

	go func() {
		defer close(fileInfoCh)
		defer close(listErrCh)
		for _, dir := range dirs {
			subCh := make(chan FileInfo, defaultFileInfoChCap)
			subErrCh := make(chan error, 1)
			go client.ListDirectoryRecursiveStream(ctx, dir, subCh, subErrCh)
			for fi := range subCh {
				select {
				case fileInfoCh <- fi:
				case <-ctx.Done():
					listErrCh <- ctx.Err()
					return
				}
			}
			if err := <-subErrCh; err != nil {
				listErrCh <- err
				return
			}
		}
		listErrCh <- nil
	}()

	// Stage 2: Metadata compare + download workers
	seen := make(map[string]bool)
	changedCh := make(chan FileInfo, c.syncConfig.DownloadWorkers)
	newSnapshot := &NutstoreSnapshot{Files: make(map[string]FileMetadata)}
	var mu sync.Mutex

	// Metadata filter goroutine
	go func() {
		defer close(changedCh)
		for fi := range fileInfoCh {
			// Filter: allow if file is under any full-directory resource,
			// or explicitly listed as a single-file resource
			if !isFileAllowed(fi.Path, allowedFiles, fullDirs) {
				continue
			}

			mu.Lock()
			seen[fi.Path] = true
			newSnapshot.Files[fi.Path] = FileMetadata{
				ModifiedAt: fi.LastModified,
				Size:       fi.Size,
				ETag:       fi.ETag,
			}
			mu.Unlock()

			isChanged := true
			if prevSnapshot != nil {
				pm, exists := prevSnapshot.Files[fi.Path]
				if exists && fi.Size == pm.Size && fi.LastModified.Equal(pm.ModifiedAt) {
					isChanged = false
				}
			}

			if !isChanged {
				continue
			}

			if !isParseableFile(fi.Name) {
				// Get share URL for unparseable files (best-effort)
				shareURL, _ := client.GetShareURL(ctx, fi.Path)
				stream.Send(datasource.StreamFetchedItem{
					Action:           datasource.ActionMetadata,
					Title:            nameWithoutExt(fi.Name),
					FileName:         fi.Name,
					ExternalID:       fi.Path,
					Size:             fi.Size,
					ModifiedAt:       fi.LastModified,
					SourceURL:        shareURL,
					SourceResourceID: findResourceID(fi.Path, dirs),
					Metadata:         map[string]string{"source_path": fi.Path, "channel": "nutstore"},
				})
				continue
			}

			select {
			case changedCh <- fi:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Download workers
	var wg sync.WaitGroup
	for i := 0; i < c.syncConfig.DownloadWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fi := range changedCh {
				item := c.downloadAndWrap(ctx, client, fi, dirs)
				if !stream.Send(item) {
					return
				}
			}
		}()
	}
	wg.Wait()

	// Wait for listing to finish
	listErr := <-listErrCh

	// Stage 3: Delete detection
	if listErr == nil && prevSnapshot != nil {
		mu.Lock()
		seenCopy := make(map[string]bool, len(seen))
		for k, v := range seen {
			seenCopy[k] = v
		}
		mu.Unlock()

		deleted := detectDeleted(prevSnapshot, seenCopy)
		for _, p := range deleted {
			stream.Send(datasource.StreamFetchedItem{
				Action:     datasource.ActionDelete,
				ExternalID: p,
				FileName:   filepath.Base(p),
				Metadata:   map[string]string{"source_path": p},
			})
		}
	}

	// Build cursor
	var finalCursor *types.SyncCursor
	if listErr == nil {
		finalCursor = &types.SyncCursor{
			LastSyncTime:    time.Now(),
			ConnectorCursor: map[string]interface{}{"nutstore_snapshot": newSnapshot},
		}
	}

	stream.Finish(finalCursor, listErr)
}

// downloadAndWrap downloads a file and wraps it as a StreamFetchedItem.
// Files with known size > threshold are downloaded directly to temp files
// without buffering in memory. Smaller files are read into memory.
func (c *Connector) downloadAndWrap(ctx context.Context, client *Client, fi FileInfo, dirs []string) datasource.StreamFetchedItem {
	baseMeta := map[string]string{"source_path": fi.Path, "channel": "nutstore"}
	baseItem := func(action datasource.SyncAction, err error) datasource.StreamFetchedItem {
		return datasource.StreamFetchedItem{
			Action: action, ExternalID: fi.Path, FileName: fi.Name, Err: err, Metadata: baseMeta,
		}
	}

	// Large file path: stream directly to temp file, never hold full content in memory
	if fi.Size > c.syncConfig.LargeFileThreshold {
		if err := os.MkdirAll(c.syncConfig.TempDir, 0o700); err != nil {
			return baseItem(datasource.ActionError, fmt.Errorf("create temp dir: %w", err))
		}
		tmpFile, err := os.CreateTemp(c.syncConfig.TempDir, "sync-*")
		if err != nil {
			return baseItem(datasource.ActionError, fmt.Errorf("create temp file: %w", err))
		}
		_, err = client.DownloadFileToWriter(ctx, fi.Path, tmpFile)
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return baseItem(datasource.ActionError, fmt.Errorf("download %s: %w", fi.Path, err))
		}
		// Get actual size from file
		stat, _ := tmpFile.Stat()
		actualSize := fi.Size
		if stat != nil {
			actualSize = stat.Size()
		}
		if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return baseItem(datasource.ActionError, fmt.Errorf("seek temp file: %w", err))
		}
		return datasource.StreamFetchedItem{
			Action:           datasource.ActionUpsert,
			Title:            nameWithoutExt(fi.Name),
			FileName:         fi.Name,
			ExternalID:       fi.Path,
			Size:             actualSize,
			ModifiedAt:       fi.LastModified,
			SourceResourceID: findResourceID(fi.Path, dirs),
			Metadata:         baseMeta,
			Body:             &tempFileReadCloser{File: tmpFile},
		}
	}

	// Small file path: download into memory
	data, _, err := client.DownloadFile(ctx, fi.Path)
	if err != nil {
		return baseItem(datasource.ActionError, fmt.Errorf("download %s: %w", fi.Path, err))
	}

	return datasource.StreamFetchedItem{
		Action:           datasource.ActionUpsert,
		Title:            nameWithoutExt(fi.Name),
		FileName:         fi.Name,
		ExternalID:       fi.Path,
		Size:             int64(len(data)),
		ModifiedAt:       fi.LastModified,
		SourceResourceID: findResourceID(fi.Path, dirs),
		Metadata:         baseMeta,
		Body:             io.NopCloser(bytes.NewReader(data)),
	}
}

// tempFileReadCloser wraps an os.File and removes it on Close.
type tempFileReadCloser struct {
	*os.File
}

func (t *tempFileReadCloser) Close() error {
	name := t.File.Name()
	err := t.File.Close()
	os.Remove(name)
	return err
}

// newClientFromConfig creates a Client from DataSourceConfig.
func newClientFromConfig(config *types.DataSourceConfig) (*Client, error) {
	cfg, err := parseConfig(config.Credentials, config.Settings)
	if err != nil {
		return nil, err
	}
	return NewClient(cfg), nil
}

// expandDirectories resolves resource IDs to directory paths for streaming.
// Returns deduplicated directories to scan and an optional set of allowed file paths.
// When allowedFiles is non-nil, only files in this set OR under full-directory resources
// are processed. A directory resource means "sync everything under it" (no filter);
// a single-file resource means "only sync that specific file from its parent dir".
func (c *Connector) expandDirectories(ctx context.Context, client *Client, resourceIDs []string) (dirs []string, allowedFiles map[string]bool, fullDirs map[string]bool, err error) {
	dirSet := make(map[string]bool)    // dedup dirs
	fullDirs = make(map[string]bool)   // dirs selected as full-directory resources
	var singleFiles []string           // individual file paths

	for _, resID := range resourceIDs {
		if strings.HasSuffix(resID, "/") || resID == "/" {
			if !dirSet[resID] {
				dirSet[resID] = true
				dirs = append(dirs, resID)
			}
			fullDirs[resID] = true
		} else {
			infos, err := client.ListDirectory(ctx, resID, "0")
			if err != nil {
				logger.Warnf(ctx, "failed to check %s: %v", resID, err)
				continue
			}
			for _, info := range infos {
				if info.IsDir {
					p := info.Path
					if !strings.HasSuffix(p, "/") {
						p += "/"
					}
					if !dirSet[p] {
						dirSet[p] = true
						dirs = append(dirs, p)
					}
					fullDirs[p] = true
				} else {
					parentDir := path.Dir(info.Path) + "/"
					if !dirSet[parentDir] {
						dirSet[parentDir] = true
						dirs = append(dirs, parentDir)
					}
					singleFiles = append(singleFiles, info.Path)
				}
			}
		}
	}

	// Only build allowedFiles if there are single-file selections
	if len(singleFiles) > 0 {
		allowedFiles = make(map[string]bool, len(singleFiles))
		for _, f := range singleFiles {
			allowedFiles[f] = true
		}
	}

	if len(dirs) == 0 {
		dirs = []string{"/"}
		fullDirs["/"] = true
	}
	return dirs, allowedFiles, fullDirs, nil
}

// isFileAllowed checks whether a file path passes the resource selection filter.
// Returns true if: (a) no filter is active (allowedFiles is nil), or
// (b) the file is explicitly in allowedFiles, or (c) the file is under any fullDir.
func isFileAllowed(filePath string, allowedFiles map[string]bool, fullDirs map[string]bool) bool {
	if allowedFiles == nil {
		return true
	}
	if allowedFiles[filePath] {
		return true
	}
	for fd := range fullDirs {
		if strings.HasPrefix(filePath, fd) {
			return true
		}
	}
	return false
}

// compareMetadata compares current files against a previous snapshot.
func compareMetadata(prev *NutstoreSnapshot, current []FileInfo) (changed []FileInfo, seen map[string]bool) {
	seen = make(map[string]bool, len(current))
	for _, f := range current {
		seen[f.Path] = true
		if prev == nil {
			changed = append(changed, f)
			continue
		}
		pm, exists := prev.Files[f.Path]
		if !exists {
			changed = append(changed, f)
			continue
		}
		if f.Size != pm.Size || !f.LastModified.Equal(pm.ModifiedAt) {
			changed = append(changed, f)
		}
	}
	return changed, seen
}

// detectDeleted returns paths that were in the previous snapshot but not in seen.
func detectDeleted(prev *NutstoreSnapshot, seen map[string]bool) []string {
	if prev == nil {
		return nil
	}
	var deleted []string
	for p := range prev.Files {
		if !seen[p] {
			deleted = append(deleted, p)
		}
	}
	return deleted
}

// findResourceID finds which resource directory a file path belongs to.
func findResourceID(filePath string, dirs []string) string {
	for _, d := range dirs {
		if strings.HasPrefix(filePath, d) {
			return d
		}
	}
	return ""
}

// loadSnapshot extracts NutstoreSnapshot from a SyncCursor.
func loadSnapshot(cursor *types.SyncCursor) *NutstoreSnapshot {
	raw, ok := cursor.ConnectorCursor["nutstore_snapshot"]
	if !ok {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snap NutstoreSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil
	}
	return &snap
}
