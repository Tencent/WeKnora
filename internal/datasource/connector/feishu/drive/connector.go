package drive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// DriveConnector implements the datasource.Connector (and StreamingConnector)
// interface for Feishu/Lark Drive (云盘) mode. It shares core.Client/core.Config/core.Region
// and the export/download logic with the wiki Connector; only resource
// enumeration and Fetch dispatch differ. See 飞书云盘数据源设计.md and
// ADR-0001..0004.
type DriveConnector struct {
	region core.Region
}

// NewDriveConnector creates a Drive connector for the given region
// (RegionFeishuDrive or RegionLarkDrive).
func NewDriveConnector(region core.Region) *DriveConnector {
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
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return err
	}

	client := core.NewClient(feishuConfig)
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
// Each core.DriveFile becomes a Resource: folder HasChildren=true, others false.
// resourceID encoding: root = folderToken; child = folderToken + ":" + fileToken
// (reuses core.FeishuWikiNodeResourceSeparator). See ADR-0001 §3.4.
func (c *DriveConnector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}

	client := core.NewClient(feishuConfig)

	if parentID == "" {
		// Root load: read the user-supplied folder_token from config.ResourceIDs.
		rootFolderToken := driveRootFolderToken(config)
		if rootFolderToken == "" {
			return nil, fmt.Errorf("folder_token is required; specify a Drive folder token")
		}
		// Validate access by listing the root's direct children (also lazy-loads
		// the first level for the picker). Reuse the list call rather than a
		// separate ping.
		files, err := client.ListDriveFilesAllPages(ctx, rootFolderToken)
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
	files, err := client.ListDriveFilesAllPages(ctx, folderToken)
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
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := core.NewClient(feishuConfig)

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

			files, err := client.ListDriveFilesAllPages(ctx, cur)
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
// connector implements StreamingConnector.
func (c *DriveConnector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := core.NewClient(feishuConfig)
	return core.FetchAllEngine(ctx, client, config, resourceIDs, driveOps{region: c.region})
}

// FetchIncremental performs an incremental sync by comparing file modified_time
// against the previously recorded state. Defensive fallback path - the service
// prefers FetchStream. Routed through the same engine as FetchStream, so the
// #2136 failure-doesn't-advance-cursor semantics apply here too.
func (c *DriveConnector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, nil, err
	}
	client := core.NewClient(feishuConfig)
	ops := driveOps{region: c.region}
	if len(config.ResourceIDs) == 0 {
		return nil, nil, errors.New(ops.EmptyResourceIDsError())
	}
	return core.FetchIncrementalEngine(ctx, client, config, cursor, ops)
}

// FetchStream performs a resumable, memory-bounded sync. It unifies the full
// and incremental paths: with cursor == nil it fetches everything, and with a
// cursor it skips files whose recorded modified_time is unchanged - the same
// mechanism that lets a sync which timed out mid-traversal resume from the last
// checkpoint instead of restarting (Tencent/WeKnora#2136).
//
// The per-node loop lives in the shared engine (engine.go); this shell only
// wires the Drive NodeOps adapter.
func (c *DriveConnector) FetchStream(
	ctx context.Context, config *types.DataSourceConfig,
	cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := core.NewClient(feishuConfig)
	ops := driveOps{region: c.region}
	if len(config.ResourceIDs) == 0 {
		return nil, errors.New(ops.EmptyResourceIDsError())
	}
	return core.FetchStreamEngine(ctx, client, config, cursor, h, ops)
}

// driveOps adapts the Drive DriveConnector to the generic sync engine. It
// carries the region (for channel + URL) and encodes/decodes the Drive cursor
// wire format (core.FeishuDriveCursor / file_times) so the engine stays format-agnostic.
type driveOps struct {
	region core.Region
}

func (o driveOps) List(ctx context.Context, client *core.Client, resourceID string) ([]core.DriveFile, error, error) {
	files, err := listDriveFilesForResource(ctx, client, resourceID)
	if err == nil {
		return files, nil, nil
	}
	var partial *core.PartialDriveFileListError
	if errors.As(err, &partial) {
		return files, err, nil
	}
	return files, nil, err
}

func (o driveOps) Token(n core.DriveFile) string    { return n.Token }
func (o driveOps) Title(n core.DriveFile) string    { return n.Name }
func (o driveOps) ObjType(n core.DriveFile) string  { return n.Type }
func (o driveOps) EditTime(n core.DriveFile) string { return n.ModifiedTime }

func (o driveOps) Fetch(ctx context.Context, client *core.Client, n core.DriveFile, resourceID string, multimodal bool) ([]*types.FetchedItem, error) {
	return fetchDriveFileContent(ctx, client, n, resourceID, multimodal, o.region)
}

func (o driveOps) ListFailureItems(resourceID string, partial error) []types.FetchedItem {
	var pe *core.PartialDriveFileListError
	if errors.As(partial, &pe) {
		return appendDriveFileListFailureItems(nil, resourceID, o.channel(), pe.Failures)
	}
	return nil
}

func (o driveOps) channel() string {
	if o.region.ConnectorType == types.ConnectorTypeLarkDrive {
		return types.ChannelLarkDrive
	}
	return types.ChannelFeishuDrive
}

func (o driveOps) ResourceNoun() string { return "files" }
func (o driveOps) EmptyResourceIDsError() string {
	return "no resource IDs (Drive folder tokens) configured"
}
func (o driveOps) LogTag() string { return "[FeishuDrive]" }

func (o driveOps) DecodeCursorTimes(m map[string]interface{}) map[string]map[string]string {
	var prev core.FeishuDriveCursor
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &prev)
	return prev.FileTimes
}

func (o driveOps) EncodeCursor(times map[string]map[string]string, lastSync time.Time) *types.SyncCursor {
	fc := core.FeishuDriveCursor{LastSyncTime: lastSync, FileTimes: times}
	m := make(map[string]interface{})
	b, _ := json.Marshal(fc)
	_ = json.Unmarshal(b, &m)
	return &types.SyncCursor{LastSyncTime: lastSync, ConnectorCursor: m}
}

// fetchDriveFileContent fetches the content of a single Drive file and converts
// it to FetchedItems. Dispatches by file.Type, mirroring the wiki
// fetchNodeContent. Shortcuts have already been expanded to their target by
// ListDriveFilesRecursiveFrom, so this only sees the target type.
//
//   - docx                   -> blocks API (Markdown) with export fallback; may return attachments/images
//   - doc/sheet/bitable      -> ExportAndDownload -> docx/xlsx
//   - file                   -> DownloadDriveFile -> original file
//   - mindnote/slides/board  -> Skip (no API), returns (nil, nil)
func fetchDriveFileContent(
	ctx context.Context, client *core.Client, file core.DriveFile, resourceID string, multimodalEnabled bool, region core.Region,
) ([]*types.FetchedItem, error) {
	if !core.IsSupportedDocType(file.Type) {
		return nil, nil
	}

	editTime := core.ParseFeishuTimestamp(file.ModifiedTime)
	// Channel marks the knowledge "source" label. Drive uses its own channel
	// (feishu_drive / lark_drive) so Drive docs show "飞书云盘" / "Lark 云盘"
	// distinct from the wiki connector's "飞书".
	channel := types.ChannelFeishuDrive
	if region.ConnectorType == types.ConnectorTypeLarkDrive {
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
		return core.FetchDocxWithBlocks(ctx, client, core.DocxFetchInput{
			DocToken:          file.Token,
			ObjToken:          file.Token,
			Title:             file.Name,
			URL:               file.URL,
			ResourceID:        resourceID,
			EditTime:          editTime,
			BaseMeta:          baseMeta,
			MultimodalEnabled: multimodalEnabled,
		})

	case "doc", "sheet", "bitable":
		data, fileName, err := client.ExportAndDownload(ctx, file.Token, file.Type)
		if err != nil {
			return nil, fmt.Errorf("export %s (%s): %w", file.Name, file.Type, err)
		}

		ext := core.ExportFileExtToSuffix[core.ObjTypeToExportFileExtension[file.Type]]
		if fileName == "" {
			fileName = core.SanitizeFileName(file.Name) + ext
		} else if !strings.HasSuffix(strings.ToLower(fileName), ext) {
			fileName = core.SanitizeFileName(fileName) + ext
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

// makeDriveResourceID encodes a Drive ResourceID: "folderToken" (root) or
// "folderToken:fileToken" (child). Reuses core.FeishuWikiNodeResourceSeparator.
func makeDriveResourceID(rootFolderToken, fileToken string) string {
	if fileToken == "" {
		return rootFolderToken
	}
	return rootFolderToken + core.FeishuWikiNodeResourceSeparator + fileToken
}

// parseDriveResourceID splits a Drive resourceID into (rootFolderToken, fileToken).
// Mirrors parseWikiResourceID.
func parseDriveResourceID(resourceID string) (rootFolderToken, fileToken string) {
	rootFolderToken, fileToken, _ = strings.Cut(resourceID, core.FeishuWikiNodeResourceSeparator)
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
func listDriveFilesForResource(
	ctx context.Context, client *core.Client, resourceID string,
) ([]core.DriveFile, error) {
	rootFolderToken, fileToken := parseDriveResourceID(resourceID)
	if fileToken == "" {
		return client.ListDriveFilesRecursiveFrom(ctx, rootFolderToken)
	}
	files, err := client.ListDriveFilesRecursiveFrom(ctx, fileToken)
	if err == nil {
		return files, nil
	}
	if !isDriveNotFolderError(err) {
		return nil, err
	}
	all, walkErr := client.ListDriveFilesRecursiveFrom(ctx, rootFolderToken)
	if walkErr != nil {
		var partialErr *core.PartialDriveFileListError
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
func filterDriveFileByToken(files []core.DriveFile, token string) []core.DriveFile {
	var out []core.DriveFile
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
		URL:         c.region.DriveFolderURL(folderToken),
		HasChildren: true,
		Metadata: map[string]interface{}{
			"folder_token": folderToken,
		},
	}
}

// driveFileToResource converts a core.DriveFile (list result) into a picker Resource.
// The ParentID must match the parent folder's ExternalID: the root folder's
// ExternalID is the bare rootFolderToken (see driveFolderToResource), while any
// sub-folder's ExternalID is "rootFolderToken:folderToken". Direct children of
// the root have file.ParentToken == rootFolderToken, so their ParentID is the
// bare rootFolderToken; deeper descendants use the encoded form.
func (c *DriveConnector) driveFileToResource(rootFolderToken string, file core.DriveFile) types.Resource {
	name := file.Name
	if name == "" {
		name = file.Token
	}

	modifiedAt := core.ParseFeishuTimestamp(file.ModifiedTime)

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

// appendDriveFileListFailureItems converts Drive listing failures into error
// FetchedItems so the sync log surfaces which sub-folders could not be listed.
// Mirrors appendWikiNodeListFailureItems.
func appendDriveFileListFailureItems(items []types.FetchedItem, resourceID, channel string, failures []core.DriveFileListFailure) []types.FetchedItem {
	for _, failure := range failures {
		items = append(items, types.FetchedItem{
			ExternalID:       failure.FolderToken,
			Title:            failure.FolderToken,
			SourceResourceID: resourceID,
			Metadata: core.FeishuErrorItemMeta(failure.Err, map[string]string{
				"channel":       channel,
				"folder_token":  failure.FolderToken,
				"failure_stage": "list_children",
			}),
		})
	}
	return items
}
