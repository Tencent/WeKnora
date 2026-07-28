package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// DriveConnector implements the datasource.Connector (and StreamingConnector)
// interface for Feishu/Lark Drive (云盘) mode. It shares Client/Config/Region
// and the export/download logic with the wiki Connector; only resource
// enumeration and fetch dispatch differ. See 飞书云盘数据源设计.md and
// ADR-0001..0004.
type DriveConnector struct {
	region Region
}

// NewDriveConnector creates a Drive connector for the given region
// (RegionFeishuDrive or RegionLarkDrive).
func NewDriveConnector(region Region) *DriveConnector {
	return &DriveConnector{region: region}
}

// Drive supports resumable streaming sync; the service prefers FetchStream over
// FetchAll/FetchIncremental when a connector implements StreamingConnector.
var _ datasource.StreamingConnector = (*DriveConnector)(nil)

// Type returns the connector type identifier.
func (c *DriveConnector) Type() string {
	return c.region.ConnectorType
}

// Validate verifies that the Drive configuration is valid by testing
// connectivity. It does not validate folder_token here - that is done in
// ListResources when the user loads the tree root. Mirrors the wiki Connector.
func (c *DriveConnector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return err
	}

	client := NewClient(feishuConfig)
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("%s connection failed: %w", c.region.Label, err)
	}
	return nil
}

// ListResources lists Feishu Drive resources for selection, loading the tree
// lazily one level at a time. Mirrors the wiki Connector.ListResources shape
// but with the Drive difference that the root is user-supplied (config.
// ResourceIDs[0]) rather than enumerated via ListWikiSpaces.
//
//   - parentID == ""                          -> return the user-supplied root
//     folder (from config.ResourceIDs[0]) as the single root resource
//     (HasChildren=true). Drive has no "space list" API, so the root is
//     user-supplied. folder_token == "" is rejected (ADR-0004).
//   - parentID == folderToken                 -> ListDriveFiles(folderToken)
//     returns the direct children.
//   - parentID == "folderToken:subFolderToken" -> ListDriveFiles(subFolderToken)
//     returns that sub-folder's direct children.
//
// Each driveFile becomes a Resource: folder HasChildren=true, others false.
// resourceID encoding: root = folderToken; child = folderToken + ":" + fileToken
// (reuses feishuWikiNodeResourceSeparator). See ADR-0001 §3.4.
func (c *DriveConnector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}

	client := NewClient(feishuConfig)

	if parentID == "" {
		// Root load: read the user-supplied folder_token from config.ResourceIDs.
		rootFolderToken := driveRootFolderToken(config)
		if rootFolderToken == "" {
			return nil, fmt.Errorf("folder_token is required; specify a Drive folder token")
		}
		// Validate access by listing the root's direct children (also lazy-loads
		// the first level for the picker). Reuse the list call rather than a
		// separate ping.
		files, err := client.listDriveFilesAllPages(ctx, rootFolderToken)
		if err != nil {
			return nil, fmt.Errorf("list feishu drive folder %s: %w", rootFolderToken, err)
		}
		_ = files // children returned via the parentID == rootFolderToken branch below
		// Resolve the root folder's human-readable name via the folder meta API.
		// Best-effort: on failure (no permission / not found) fall back to the
		// folder_token so the picker still renders something usable.
		folderName := rootFolderToken
		if meta, mErr := client.GetDriveFolderMeta(ctx, rootFolderToken); mErr != nil {
			logger.Warnf(ctx, "[FeishuDrive] resolve root folder name failed: %v (falling back to token)", mErr)
		} else if meta.Data.Name != "" {
			folderName = meta.Data.Name
		}
		return []types.Resource{c.driveFolderToResource(rootFolderToken, "", rootFolderToken, folderName)}, nil
	}

	// Lazy load: list only the direct children of the given folder.
	rootFolderToken, folderToken := parseDriveResourceID(parentID)
	if folderToken == "" {
		// parentID is a bare root folder token -> list its children.
		folderToken = rootFolderToken
	}
	files, err := client.listDriveFilesAllPages(ctx, folderToken)
	if err != nil {
		return nil, fmt.Errorf("list feishu drive files under %s: %w", parentID, err)
	}

	resources := make([]types.Resource, 0, len(files))
	for _, f := range files {
		resources = append(resources, c.driveFileToResource(rootFolderToken, f))
	}
	return resources, nil
}

// ResolveResourceAncestors returns the resource IDs of every parent folder that
// has to be expanded so the lazily-loaded picker can reveal each selection.
//
// The wiki connector walks up via GetWikiNode (parent_node_token) in O(depth)
// single-node queries. Drive has no single-file parent query API (verified -
// metas/batch_query does not return parent), so we walk top-down from the root
// folder with ListDriveFiles and share the traversal across all selections in
// the same root. Best-effort: a broken path just stays collapsed. See ADR-0003.
func (c *DriveConnector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := NewClient(feishuConfig)

	seen := make(map[string]bool)
	ancestors := make([]string, 0)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ancestors = append(ancestors, id)
		}
	}

	// Group selections by root folder so one shared traversal covers them all.
	type selection struct {
		fileToken string
	}
	rootSelections := make(map[string][]selection)
	for _, rid := range resourceIDs {
		rootFolderToken, fileToken := parseDriveResourceID(rid)
		if fileToken == "" {
			// A root-level selection is already a top-level node; nothing to reveal.
			continue
		}
		add(rootFolderToken)
		rootSelections[rootFolderToken] = append(rootSelections[rootFolderToken], selection{fileToken})
	}

	// For each root, BFS from the root: at each folder, ListDriveFiles and check
	// which selections are direct children (record their parent chain) and which
	// sub-folders may still contain selections (enqueue). Shared traversal means
	// selections in the same subtree reuse list calls.
	for rootFolderToken, sels := range rootSelections {
		remaining := make(map[string]bool, len(sels))
		for _, s := range sels {
			remaining[s.fileToken] = true
		}
		// parentChain[fileToken] = resourceID of its parent folder
		parentChain := make(map[string]string)

		queue := []string{rootFolderToken}
		for len(queue) > 0 && len(remaining) > 0 {
			cur := queue[0]
			queue = queue[1:]

			files, err := client.listDriveFilesAllPages(ctx, cur)
			if err != nil {
				logger.Warnf(ctx, "[FeishuDrive] resolve ancestors: list %s: %v", cur, err)
				break // best-effort: stop this root's traversal
			}
			for _, f := range files {
				if remaining[f.Token] {
					delete(remaining, f.Token)
					// Record the parent chain from root down to this file's parent.
					chain := buildDriveAncestorChain(rootFolderToken, cur, parentChain)
					for _, a := range chain {
						add(a)
					}
				}
				if f.Type == "folder" {
					parentChain[f.Token] = makeDriveResourceID(rootFolderToken, cur)
					queue = append(queue, f.Token)
				}
			}
		}
	}

	return ancestors, nil
}

// buildDriveAncestorChain walks the parentChain map from cur up to root,
// returning the resourceIDs (root, ... , cur's parent) in root-first order.
func buildDriveAncestorChain(rootFolderToken, cur string, parentChain map[string]string) []string {
	var chain []string
	node := cur
	for node != "" && node != rootFolderToken {
		parent, ok := parentChain[node]
		if !ok {
			break
		}
		chain = append([]string{parent}, chain...)
		_, parentFolderToken := parseDriveResourceID(parent)
		node = parentFolderToken
	}
	return chain
}

// FetchAll performs a full sync of all documents from the selected Drive
// folders. Defensive fallback path - the service prefers FetchStream when the
// connector implements StreamingConnector. Mirrors wiki Connector.FetchAll.
func (c *DriveConnector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := NewClient(feishuConfig)

	var allItems []types.FetchedItem

	for _, resourceID := range resourceIDs {
		files, err := c.listDriveFilesForResource(ctx, client, resourceID)
		if err != nil {
			var partialErr *partialDriveFileListError
			if !errors.As(err, &partialErr) {
				return nil, fmt.Errorf("list files for resource %s: %w", resourceID, err)
			}
			allItems = appendDriveFileListFailureItems(allItems, resourceID, c.driveChannel(), partialErr.Failures)
		}

		tally := newFetchTally(len(files))
		for i, file := range files {
			items, ferr := c.fetchDriveFileContent(ctx, client, file, resourceID, config.MultimodalEnabled)
			if ferr != nil {
				tally.fail()
				allItems = append(allItems, types.FetchedItem{
					ExternalID:       file.Token,
					Title:            file.Name,
					SourceResourceID: resourceID,
					Metadata:         feishuErrorItemMeta(ferr, nil),
				})
				continue
			}
			if len(items) > 0 {
				tally.fetch()
				for _, it := range items {
					allItems = append(allItems, *it)
				}
			} else {
				tally.skip(file.Type)
			}
			if n := i + 1; n%100 == 0 {
				logger.Infof(ctx, "[FeishuDrive] sync progress resource=%s %d/%d (%s)",
					resourceID, n, len(files), tally.summary())
			}
		}
		logger.Infof(ctx, "[FeishuDrive] sync summary resource=%s %s", resourceID, tally.summary())
	}

	return allItems, nil
}

// FetchIncremental performs an incremental sync by comparing file modified_time
// against the previously recorded state. Defensive fallback path. Mirrors wiki
// Connector.FetchIncremental. See ADR-0002 (modified_time comes from the list
// API directly, no batch_query).
func (c *DriveConnector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return nil, nil, err
	}
	client := NewClient(feishuConfig)

	var prevCursor feishuDriveCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		cursorBytes, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(cursorBytes, &prevCursor)
	}

	newCursor := feishuDriveCursor{
		LastSyncTime: time.Now(),
		FileTimes:    make(map[string]map[string]string),
	}

	var changedItems []types.FetchedItem

	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (Drive folder tokens) configured")
	}

	for _, resourceID := range resourceIDs {
		files, err := c.listDriveFilesForResource(ctx, client, resourceID)
		var partialErr *partialDriveFileListError
		if err != nil {
			if !errors.As(err, &partialErr) {
				return nil, nil, fmt.Errorf("list files for resource %s: %w", resourceID, err)
			}
			changedItems = appendDriveFileListFailureItems(changedItems, resourceID, c.driveChannel(), partialErr.Failures)
		}

		newCursor.FileTimes[resourceID] = make(map[string]string)
		if partialErr != nil && prevCursor.FileTimes != nil {
			if prevTimes, ok := prevCursor.FileTimes[resourceID]; ok {
				for ft, mt := range prevTimes {
					newCursor.FileTimes[resourceID][ft] = mt
				}
			}
		}

		currentFiles := make(map[string]bool)
		for _, file := range files {
			currentFiles[file.Token] = true
			// Use ModifiedTime (document content edit time) for change detection.
			// The list API returns this directly (verified) - equivalent to the
			// wiki node's obj_edit_time. See ADR-0002.
			modifyTimeStr := file.ModifiedTime
			newCursor.FileTimes[resourceID][file.Token] = modifyTimeStr

			if prevCursor.FileTimes != nil {
				if prevTimes, ok := prevCursor.FileTimes[resourceID]; ok {
					if prevModify, exists := prevTimes[file.Token]; exists {
						if prevModify == modifyTimeStr {
							continue
						}
					}
				}
			}

			items, ferr := c.fetchDriveFileContent(ctx, client, file, resourceID, config.MultimodalEnabled)
			if ferr != nil {
				changedItems = append(changedItems, types.FetchedItem{
					ExternalID:       file.Token,
					Title:            file.Name,
					SourceResourceID: resourceID,
					Metadata:         feishuErrorItemMeta(ferr, nil),
				})
				continue
			}
			for _, it := range items {
				changedItems = append(changedItems, *it)
			}
		}

		// Detect deleted files (only when the full tree was listed successfully).
		if partialErr == nil && prevCursor.FileTimes != nil {
			if prevTimes, ok := prevCursor.FileTimes[resourceID]; ok {
				for ft := range prevTimes {
					if !currentFiles[ft] {
						changedItems = append(changedItems, types.FetchedItem{
							ExternalID:       ft,
							IsDeleted:        true,
							SourceResourceID: resourceID,
						})
					}
				}
			}
		}
	}

	return changedItems, newCursor.toSyncCursor(), nil
}

// FetchStream performs a resumable, memory-bounded sync. It unifies the full
// and incremental paths: with cursor == nil it fetches everything, and with a
// cursor it skips files whose recorded modified_time is unchanged - the same
// mechanism that lets a sync which timed out mid-traversal resume from the last
// checkpoint instead of restarting (Tencent/WeKnora#2136).
//
// Mirrors the wiki Connector.FetchStream. The three cursor semantics that MUST
// be preserved (ADR-0001):
//  1. Resume fast-path: prevModify == curModify -> skip, keep cursor.
//  2. Failure does NOT advance the cursor: retain prevModify so the file is
//     retried next run instead of being permanently skipped.
//  3. toSyncCursor() JSON-marshals for snapshot isolation.
func (c *DriveConnector) FetchStream(
	ctx context.Context, config *types.DataSourceConfig,
	cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	feishuConfig, err := parseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := NewClient(feishuConfig)

	var prevCursor feishuDriveCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		cursorBytes, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(cursorBytes, &prevCursor)
	}

	newCursor := feishuDriveCursor{
		LastSyncTime: time.Now(),
		FileTimes:    make(map[string]map[string]string),
	}

	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, fmt.Errorf("no resource IDs (Drive folder tokens) configured")
	}

	processed := 0
	lastCheckpoint := time.Now()
	for _, resourceID := range resourceIDs {
		files, err := c.listDriveFilesForResource(ctx, client, resourceID)
		var partialErr *partialDriveFileListError
		if err != nil {
			if !errors.As(err, &partialErr) {
				return nil, fmt.Errorf("list files for resource %s: %w", resourceID, err)
			}
			for _, item := range appendDriveFileListFailureItems(nil, resourceID, c.driveChannel(), partialErr.Failures) {
				if eerr := h.Emit(ctx, item); eerr != nil {
					return nil, eerr
				}
			}
		}

		newCursor.FileTimes[resourceID] = make(map[string]string)
		// On a partial listing, carry prior modify times forward so a later full
		// listing can still detect changes and deletions.
		if partialErr != nil && prevCursor.FileTimes != nil {
			if prevTimes, ok := prevCursor.FileTimes[resourceID]; ok {
				for ft, mt := range prevTimes {
					newCursor.FileTimes[resourceID][ft] = mt
				}
			}
		}

		currentFiles := make(map[string]bool)
		tally := newFetchTally(len(files))
		for i, file := range files {
			currentFiles[file.Token] = true
			modifyTimeStr := file.ModifiedTime

			var prevModify string
			var hadPrev bool
			if prevCursor.FileTimes != nil {
				if prevTimes, ok := prevCursor.FileTimes[resourceID]; ok {
					prevModify, hadPrev = prevTimes[file.Token]
				}
			}

			// Resume/incremental fast-path: a file recorded at its current modify
			// time is unchanged (or already synced this run) - keep the record
			// and skip re-fetching.
			if hadPrev && prevModify == modifyTimeStr {
				newCursor.FileTimes[resourceID][file.Token] = modifyTimeStr
				continue
			}

			items, ferr := c.fetchDriveFileContent(ctx, client, file, resourceID, config.MultimodalEnabled)
			if ferr != nil {
				tally.fail()
				// Do NOT advance the cursor: the content was never fetched.
				// Retain the prior modify time (if any) so prev != current next
				// run and the file is retried, instead of being permanently
				// skipped on a transient export failure (Tencent/WeKnora#2136).
				if hadPrev {
					newCursor.FileTimes[resourceID][file.Token] = prevModify
				}
				if eerr := h.Emit(ctx, types.FetchedItem{
					ExternalID:       file.Token,
					Title:            file.Name,
					SourceResourceID: resourceID,
					Metadata:         feishuErrorItemMeta(ferr, nil),
				}); eerr != nil {
					return nil, eerr
				}
			} else {
				// Fetched, or an unsupported type (nothing to fetch): record the
				// current modify time so the file is not re-processed next run.
				newCursor.FileTimes[resourceID][file.Token] = modifyTimeStr
				if len(items) > 0 {
					tally.fetch()
					for _, it := range items {
						if eerr := h.Emit(ctx, *it); eerr != nil {
							return nil, eerr
						}
					}
				} else {
					// Unsupported type (mindnote/slides/…): no item.
					tally.skip(file.Type)
				}
			}

			processed++
			if processed%feishuStreamCheckpointInterval == 0 || time.Since(lastCheckpoint) >= feishuStreamCheckpointMaxInterval {
				if cerr := h.Checkpoint(ctx, newCursor.toSyncCursor()); cerr != nil {
					logger.Warnf(ctx, "[FeishuDrive] stream checkpoint failed: %v", cerr)
				}
				lastCheckpoint = time.Now()
			}
			if n := i + 1; n%100 == 0 {
				logger.Infof(ctx, "[FeishuDrive] stream progress resource=%s %d/%d (%s)",
					resourceID, n, len(files), tally.summary())
			}
		}

		// Detect deleted files (only when the full tree was listed successfully).
		// Partial == nil guard: a partial listing did not enumerate the whole
		// subtree, so deletion detection would false-positive.
		if partialErr == nil && prevCursor.FileTimes != nil {
			if prevTimes, ok := prevCursor.FileTimes[resourceID]; ok {
				for ft := range prevTimes {
					if !currentFiles[ft] {
						if eerr := h.Emit(ctx, types.FetchedItem{
							ExternalID:       ft,
							IsDeleted:        true,
							SourceResourceID: resourceID,
						}); eerr != nil {
							return nil, eerr
						}
					}
				}
			}
		}
		logger.Infof(ctx, "[FeishuDrive] stream summary resource=%s %s", resourceID, tally.summary())
	}

	return newCursor.toSyncCursor(), nil
}

// toSyncCursor converts the connector-specific feishuDriveCursor into the
// generic SyncCursor persisted by the service. JSON marshal for snapshot
// isolation, decoupled from later mutation of the connector's maps. Mirrors
// feishuCursor.toSyncCursor (ADR-0001 §3).
func (fc feishuDriveCursor) toSyncCursor() *types.SyncCursor {
	m := make(map[string]interface{})
	cursorBytes, _ := json.Marshal(fc)
	_ = json.Unmarshal(cursorBytes, &m)
	return &types.SyncCursor{
		LastSyncTime:    fc.LastSyncTime,
		ConnectorCursor: m,
	}
}

// fetchDriveFileContent fetches the content of a single Drive file and converts
// it to FetchedItems. Dispatches by file.Type, mirroring the wiki
// fetchNodeContent. Shortcuts have already been expanded to their target by
// ListDriveFilesRecursiveFrom, so this only sees the target type.
//
//   - docx                   -> blocks API (Markdown) with export fallback; may return attachments/images
//   - doc/sheet/bitable      -> ExportAndDownload -> docx/xlsx
//   - file                   -> DownloadDriveFile -> original file
//   - mindnote/slides/board  -> skip (no API), returns (nil, nil)
func (c *DriveConnector) fetchDriveFileContent(
	ctx context.Context, client *Client, file driveFile, resourceID string, multimodalEnabled bool,
) ([]*types.FetchedItem, error) {
	if !isSupportedDocType(file.Type) {
		return nil, nil
	}

	editTime := parseFeishuTimestamp(file.ModifiedTime)
	// Channel marks the knowledge "source" label. Drive uses its own channel
	// (feishu_drive / lark_drive) so Drive docs show "飞书云盘" / "Lark 云盘"
	// distinct from the wiki connector's "飞书".
	channel := types.ChannelFeishuDrive
	if c.region.ConnectorType == types.ConnectorTypeLarkDrive {
		channel = types.ChannelLarkDrive
	}
	baseMeta := map[string]string{
		"obj_token":    file.Token,
		"obj_type":     file.Type,
		"file_token":   file.Token,
		"folder_token": file.ParentToken,
		"channel":      channel,
	}

	switch file.Type {
	case "docx":
		return fetchDocxWithBlocks(ctx, client, docxFetchInput{
			docToken:          file.Token,
			objToken:          file.Token,
			title:             file.Name,
			url:               file.URL,
			resourceID:        resourceID,
			editTime:          editTime,
			baseMeta:          baseMeta,
			multimodalEnabled: multimodalEnabled,
		})

	case "doc", "sheet", "bitable":
		data, fileName, err := client.ExportAndDownload(ctx, file.Token, file.Type)
		if err != nil {
			return nil, fmt.Errorf("export %s (%s): %w", file.Name, file.Type, err)
		}

		ext := exportFileExtToSuffix[objTypeToExportFileExtension[file.Type]]
		if fileName == "" {
			fileName = sanitizeFileName(file.Name) + ext
		} else if !strings.HasSuffix(strings.ToLower(fileName), ext) {
			fileName = sanitizeFileName(fileName) + ext
		}

		return []*types.FetchedItem{{
			// Drive uses file token as external_id
			ExternalID:  file.Token,
			Title:       file.Name,
			Content:     data,
			ContentType: "application/octet-stream",
			FileName:    fileName,
			// list API returns the absolute url
			URL:              file.URL,
			UpdatedAt:        editTime,
			SourceResourceID: resourceID,
			Metadata:         baseMeta,
		}}, nil

	case "file":
		data, err := client.DownloadDriveFile(ctx, file.Token)
		if err != nil {
			return nil, fmt.Errorf("download file %s (%s): %w", file.Name, file.Token, err)
		}

		fileName := file.Name
		if fileName == "" {
			fileName = file.Token
		}

		return []*types.FetchedItem{{
			ExternalID:       file.Token,
			Title:            file.Name,
			Content:          data,
			ContentType:      "application/octet-stream",
			FileName:         fileName,
			URL:              file.URL,
			UpdatedAt:        editTime,
			SourceResourceID: resourceID,
			Metadata:         baseMeta,
		}}, nil

	default:
		return nil, nil
	}
}

// --- Helpers ---

// makeDriveResourceID encodes a Drive resourceID: "folderToken" (root) or
// "folderToken:fileToken" (child). Reuses feishuWikiNodeResourceSeparator.
func makeDriveResourceID(rootFolderToken, fileToken string) string {
	if fileToken == "" {
		return rootFolderToken
	}
	return rootFolderToken + feishuWikiNodeResourceSeparator + fileToken
}

// parseDriveResourceID splits a Drive resourceID into (rootFolderToken, fileToken).
// Mirrors parseWikiResourceID.
func parseDriveResourceID(resourceID string) (rootFolderToken, fileToken string) {
	rootFolderToken, fileToken, _ = strings.Cut(resourceID, feishuWikiNodeResourceSeparator)
	return rootFolderToken, fileToken
}

// listDriveFilesForResource lists the files to sync for a given resourceID.
// A resourceID is either a bare root folderToken (sync the whole subtree) or
// "rootFolderToken:fileToken" (sync a single selected file or sub-folder).
//
// For a single-file selection we cannot pass the fileToken to
// ListDriveFilesRecursiveFrom - that API expects a folder and returns 1061002
// (params error) for a file token. Instead we walk the root folder subtree (the
// file's parent) and filter to just the selected fileToken. This mirrors the
// wiki connector, which resolves a single selected node via GetWikiNode; Drive
// has no single-file meta API, so filtering the subtree walk is the equivalent.
//
// A sub-folder selection (fileToken is itself a folder) is handled by walking
// that sub-folder's subtree directly - ListDriveFilesRecursiveFrom accepts a
// folder token, so no filtering is needed there.
func (c *DriveConnector) listDriveFilesForResource(
	ctx context.Context, client *Client, resourceID string,
) ([]driveFile, error) {
	rootFolderToken, fileToken := parseDriveResourceID(resourceID)
	if fileToken == "" {
		// Whole root subtree.
		return client.ListDriveFilesRecursiveFrom(ctx, rootFolderToken)
	}

	// fileToken may be a file or a folder. Try walking it as a folder first; if
	// that succeeds it was a folder (sync its subtree). If it fails with a
	// params error it is a file - fall back to walking the root and filtering.
	files, err := client.ListDriveFilesRecursiveFrom(ctx, fileToken)
	if err == nil {
		return files, nil
	}
	// Heuristic: a 1061002 (params error) means fileToken is not a folder. Any
	// other error (auth, not found, ...) propagates as a real failure.
	if !isDriveNotFolderError(err) {
		return nil, err
	}

	// fileToken is a file: walk the root subtree and keep only the match.
	all, walkErr := client.ListDriveFilesRecursiveFrom(ctx, rootFolderToken)
	if walkErr != nil {
		// If the recursive walk itself partially failed, still search the
		// successfully-listed subset; the selected file may be among them.
		var partialErr *partialDriveFileListError
		if !errors.As(walkErr, &partialErr) {
			return nil, walkErr
		}
		all = filterDriveFileByToken(all, fileToken)
		if len(all) == 0 {
			return nil, walkErr
		}
		return all, walkErr
	}
	return filterDriveFileByToken(all, fileToken), nil
}

// isDriveNotFolderError reports whether err indicates the token was not a
// folder (1061002 params error from the list API when a file token is passed).
func isDriveNotFolderError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "1061002") || strings.Contains(s, "params error")
}

// filterDriveFileByToken returns only the entries whose Token matches token.
func filterDriveFileByToken(files []driveFile, token string) []driveFile {
	var out []driveFile
	for _, f := range files {
		if f.Token == token {
			out = append(out, f)
		}
	}
	return out
}

// driveRootFolderToken extracts the user-supplied root folder_token from the
// data source config (ResourceIDs[0]).
func driveRootFolderToken(config *types.DataSourceConfig) string {
	if config == nil || len(config.ResourceIDs) == 0 {
		return ""
	}
	root, _ := parseDriveResourceID(config.ResourceIDs[0])
	return root
}

// driveFolderToResource builds the root Resource for a Drive folder. The root
// folder's name is resolved via GetDriveFolderMeta by the caller (best-effort,
// falling back to the token). For sub-folders, use driveFileToResource instead -
// the list API returns each child folder's Name.
//
// The root folder's ExternalID is the bare rootFolderToken (no ":fileToken"
// suffix) so it matches the resource_id the user saved in
// form.config.resource_ids = [folderToken]. A "token:token" encoding would
// break selection matching on edit.
func (c *DriveConnector) driveFolderToResource(rootFolderToken, parentToken, folderToken, name string) types.Resource {
	if name == "" {
		name = folderToken
	}
	return types.Resource{
		ExternalID:  rootFolderToken,
		Name:        name,
		Type:        "drive_folder",
		URL:         c.region.driveFolderURL(folderToken),
		HasChildren: true,
		Metadata: map[string]interface{}{
			"folder_token": folderToken,
		},
	}
}

// driveFileToResource converts a driveFile (list result) into a picker Resource.
// The ParentID must match the parent folder's ExternalID: the root folder's
// ExternalID is the bare rootFolderToken (see driveFolderToResource), while any
// sub-folder's ExternalID is "rootFolderToken:folderToken". Direct children of
// the root have file.ParentToken == rootFolderToken, so their ParentID is the
// bare rootFolderToken; deeper descendants use the encoded form.
func (c *DriveConnector) driveFileToResource(rootFolderToken string, file driveFile) types.Resource {
	name := file.Name
	if name == "" {
		name = file.Token
	}

	modifiedAt := parseFeishuTimestamp(file.ModifiedTime)

	parentID := makeDriveResourceID(rootFolderToken, file.ParentToken)
	if file.ParentToken == rootFolderToken || file.ParentToken == "" {
		// Direct child of the root folder: parent is the root, whose
		// ExternalID is the bare rootFolderToken (no ":token" suffix).
		parentID = rootFolderToken
	}

	return types.Resource{
		ExternalID:  makeDriveResourceID(rootFolderToken, file.Token),
		Name:        name,
		Type:        file.Type,
		URL:         file.URL,
		ParentID:    parentID,
		HasChildren: file.Type == "folder",
		ModifiedAt:  modifiedAt,
		Metadata: map[string]interface{}{
			"file_token":   file.Token,
			"obj_type":     file.Type,
			"folder_token": file.ParentToken,
		},
	}
}

// driveChannel returns the knowledge channel for this connector's region.
func (c *DriveConnector) driveChannel() string {
	if c.region.ConnectorType == types.ConnectorTypeLarkDrive {
		return types.ChannelLarkDrive
	}
	return types.ChannelFeishuDrive
}

// appendDriveFileListFailureItems converts Drive listing failures into error
// FetchedItems so the sync log surfaces which sub-folders could not be listed.
// Mirrors appendWikiNodeListFailureItems.
func appendDriveFileListFailureItems(items []types.FetchedItem, resourceID, channel string, failures []driveFileListFailure) []types.FetchedItem {
	for _, failure := range failures {
		items = append(items, types.FetchedItem{
			ExternalID:       failure.FolderToken,
			Title:            failure.FolderToken,
			SourceResourceID: resourceID,
			Metadata: feishuErrorItemMeta(failure.Err, map[string]string{
				"channel":       channel,
				"folder_token":  failure.FolderToken,
				"failure_stage": "list_children",
			}),
		})
	}
	return items
}
