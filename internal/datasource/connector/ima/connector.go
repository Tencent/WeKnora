package ima

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Compile-time proof that *Connector satisfies the datasource.Connector interface.
var _ datasource.Connector = (*Connector)(nil)

// Connector implements datasource.Connector for Tencent IMA (ima.qq.com).
type Connector struct{}

// NewConnector creates a new IMA connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeIMA }

// Validate verifies the credentials by calling get_addable_knowledge_base_list
// — the endpoint most likely to succeed even when the token has zero KBs, and
// the same one ListResources uses. It returns 110030 (无权限) when the token
// itself is invalid, which client.callAPI already maps to ErrInvalidCredentials.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseIMAConfig(config)
	if err != nil {
		return err
	}
	cli := newClient(cfg)
	if _, err := cli.GetAddableKnowledgeBaseList(ctx, "", defaultPageSize); err != nil {
		return fmt.Errorf("ima connection failed: %w", err)
	}
	return nil
}

// ResolveResourceAncestors: IMA knowledge bases are exposed as a flat list of
// top-level resources, so a lazy-loaded picker has no ancestors to reveal.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

// ListResources returns the flat list of knowledge bases the token can read.
// parentID is honoured only for the "no children" contract — see the note on
// Connector.ListResources in internal/datasource/connector.go.
//
// Primary source is get_addable_knowledge_base_list, which returns the KBs the
// current OpenAPI credential has permission to operate on. When that endpoint
// returns an empty list (e.g. a legacy tenant only exposes read scopes) we
// fall back to search_knowledge_base with an empty query so users still see
// something to pick.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	if parentID != "" {
		return []types.Resource{}, nil
	}

	cfg, err := parseIMAConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	type kbLite struct {
		ID       string
		Name     string
		CoverURL string
	}
	var bases []kbLite

	cursor := ""
	for {
		resp, err := cli.GetAddableKnowledgeBaseList(ctx, cursor, defaultPageSize)
		if err != nil {
			return nil, fmt.Errorf("get_addable_knowledge_base_list: %w", err)
		}
		for _, b := range resp.AddableKnowledgeBaseList {
			bases = append(bases, kbLite{ID: b.ID, Name: b.Name})
		}
		if resp.IsEnd || resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	logger.Infof(ctx, "[IMA] get_addable_knowledge_base_list returned %d knowledge bases", len(bases))

	if len(bases) == 0 {
		cursor = ""
		for {
			resp, err := cli.SearchKnowledgeBase(ctx, "", cursor, searchPageSize)
			if err != nil {
				return nil, fmt.Errorf("search_knowledge_base fallback: %w", err)
			}
			for _, b := range resp.InfoList {
				bases = append(bases, kbLite{ID: b.ID, Name: b.Name, CoverURL: b.CoverURL})
			}
			if resp.IsEnd || resp.NextCursor == "" {
				break
			}
			cursor = resp.NextCursor
		}
		logger.Infof(ctx, "[IMA] search_knowledge_base fallback returned %d knowledge bases", len(bases))
	}

	ids := make([]string, 0, len(bases))
	for _, b := range bases {
		ids = append(ids, b.ID)
	}
	details := map[string]knowledgeBaseInfo{}
	for i := 0; i < len(ids); i += 20 {
		end := i + 20
		if end > len(ids) {
			end = len(ids)
		}
		batch, err := cli.GetKnowledgeBase(ctx, ids[i:end])
		if err != nil {
			logger.Warnf(ctx, "[IMA] get_knowledge_base batch failed (skipping enrichment): %v", err)
			continue
		}
		for k, v := range batch {
			details[k] = v
		}
	}

	out := make([]types.Resource, 0, len(bases))
	for _, b := range bases {
		desc := ""
		coverURL := b.CoverURL
		if d, ok := details[b.ID]; ok {
			desc = d.Description
			if coverURL == "" {
				coverURL = d.CoverURL
			}
		}
		out = append(out, types.Resource{
			ExternalID:  b.ID,
			Name:        b.Name,
			Type:        "knowledge_base",
			Description: desc,
			URL:         cfg.GetBaseURL(),
			Metadata: map[string]interface{}{
				"cover_url": coverURL,
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExternalID < out[j].ExternalID })
	logger.Infof(ctx, "[IMA] ListResources returning %d knowledge bases to UI", len(out))
	return out, nil
}

// FetchAll performs a full sync of every knowledge base in resourceIDs.
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

// FetchIncremental syncs new / replaced / removed items against the cursor.
//
// Each IMA item is identified by a stable logical_key (see logicalKey), NOT by
// media_id — because IMA reassigns media_id whenever a same-named file is
// replaced in place, and we want that replacement to surface as an UPDATE to
// the same knowledge item rather than a delete-plus-insert pair.
//
// Emitted this cycle:
//
//   - logical_key new since last sync                → add    (new content)
//   - logical_key present, media_id changed          → update (re-fetched content;
//     ingest layer's existing
//     external_id match triggers
//     delete-and-recreate)
//   - logical_key unchanged, media_id unchanged      → skip
//   - logical_key present last sync, absent this sync → IsDeleted tombstone
//
// LIMITATION: IMA still exposes no per-item updated_at, so an in-place edit
// that keeps the SAME media_id (same file bytes replaced under the hood by
// IMA, if that ever happens) is invisible to us. Users needing a full content
// refresh should periodically run a Full sync.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (knowledge base IDs) configured")
	}

	var prev *imaCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		var p imaCursor
		b, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(b, &p)
		prev = &p
	}

	items, newCursor, err := c.walk(ctx, config, resourceIDs, prev, true)
	if err != nil {
		return nil, nil, err
	}

	cursorMap := make(map[string]interface{})
	b, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(b, &cursorMap)

	return items, &types.SyncCursor{
		LastSyncTime:    newCursor.LastSyncTime,
		ConnectorCursor: cursorMap,
	}, nil
}

// walk is the shared implementation for FetchAll / FetchIncremental.
// When incremental is false, prev is ignored and returned cursor is nil.
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *imaCursor,
	incremental bool,
) ([]types.FetchedItem, *imaCursor, error) {
	cfg, err := parseIMAConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := newClient(cfg)

	newCursor := &imaCursor{
		LastSyncTime: time.Now(),
		KBLogical:    make(map[string]map[string]string),
		KBMedia:      make(map[string]map[string]string),
	}
	var out []types.FetchedItem

	for _, kbID := range resourceIDs {
		files, folderPath, err := listAllKBFiles(ctx, cli, kbID)
		if err != nil {
			return nil, nil, fmt.Errorf("list KB %s: %w", kbID, err)
		}

		currentLogical := make(map[string]string, len(files))
		currentMedia := make(map[string]string, len(files))
		keys := make([]string, len(files))
		for i, f := range files {
			// f.MediaType is 0 in list responses (IMA omits it there); we rely on
			// (kb_id, parent_folder_id, title) which IMA enforces uniqueness on
			// via check_repeated_names. Encoding media_type as 0 is fine as long
			// as it stays consistent across syncs — which it does, since 0 is a
			// constant here.
			key := logicalKey(kbID, f.ParentFolderID, f.Title, f.MediaType)
			keys[i] = key
			currentLogical[key] = f.MediaID
			currentMedia[f.MediaID] = f.Title
		}
		newCursor.KBLogical[kbID] = currentLogical
		newCursor.KBMedia[kbID] = currentMedia

		replaced := 0
		for i, f := range files {
			key := keys[i]

			// Incremental skip / replacement detection.
			if incremental && prev != nil && prev.KBLogical != nil {
				if prevSet, ok := prev.KBLogical[kbID]; ok {
					if prevMediaID, seen := prevSet[key]; seen {
						if prevMediaID == f.MediaID {
							continue // unchanged
						}
						replaced++ // media_id changed → replacement, fall through to re-fetch
					}
				}
			}

			item, ok := fetchOneMedia(ctx, cli, cfg, kbID, key, f, folderPath)
			if !ok {
				continue
			}
			out = append(out, item)
		}

		// Deletion detection (incremental only) — based on logical_key vanishing,
		// so a same-name replacement (media_id churns but logical_key stays)
		// never fires a spurious delete.
		deleted := 0
		if incremental && prev != nil && prev.KBLogical != nil {
			if prevSet, ok := prev.KBLogical[kbID]; ok {
				for prevKey := range prevSet {
					if _, still := currentLogical[prevKey]; !still {
						out = append(out, types.FetchedItem{
							ExternalID:       prevKey,
							IsDeleted:        true,
							SourceResourceID: kbID,
						})
						deleted++
					}
				}
			}
		}

		logger.Infof(ctx, "[IMA] KB %s: total=%d replaced=%d deleted=%d",
			kbID, len(files), replaced, deleted)
	}

	if !incremental {
		return out, nil, nil
	}
	return out, newCursor, nil
}

// walkedFile is an internal representation of a knowledge item after we've
// resolved its folder location within the KB tree.
type walkedFile struct {
	knowledgeInfo
	// FolderPath is the "/A/B" style path (folder names, no leading slash)
	// of the containing folder — empty for root-level items.
	FolderPath string
}

// listAllKBFiles recursively enumerates a knowledge base into a flat list of
// files, resolving each item's folder path from the on-the-fly folder tree.
// folderPath maps folder_id → readable path (for metadata).
func listAllKBFiles(
	ctx context.Context, cli *client, kbID string,
) ([]walkedFile, map[string]string, error) {
	folderPath := map[string]string{}
	var out []walkedFile

	// BFS/DFS over folders. Start from root (empty folder_id).
	type todo struct {
		folderID string
		path     string
	}
	stack := []todo{{folderID: "", path: ""}}

	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		cursor := ""
		for {
			resp, err := cli.GetKnowledgeList(ctx, kbID, cur.folderID, cursor, defaultPageSize)
			if err != nil {
				return nil, nil, err
			}
			for _, raw := range resp.KnowledgeList {
				// Probe each entry: an entry with a non-empty folder_id is a
				// folder; otherwise it's a knowledge item (file / note / etc.).
				var probe struct {
					FolderID string `json:"folder_id"`
					MediaID  string `json:"media_id"`
				}
				_ = json.Unmarshal(raw, &probe)

				if probe.FolderID != "" && probe.MediaID == "" {
					var fi folderInfo
					if err := json.Unmarshal(raw, &fi); err != nil {
						continue
					}
					child := cur.path
					if child == "" {
						child = fi.Name
					} else {
						child = cur.path + "/" + fi.Name
					}
					folderPath[fi.FolderID] = child
					stack = append(stack, todo{folderID: fi.FolderID, path: child})
					continue
				}
				if probe.MediaID == "" {
					continue // unrecognized shape, skip defensively
				}
				var ki knowledgeInfo
				if err := json.Unmarshal(raw, &ki); err != nil {
					continue
				}
				out = append(out, walkedFile{
					knowledgeInfo: ki,
					FolderPath:    cur.path,
				})
			}
			if resp.IsEnd || resp.NextCursor == "" {
				break
			}
			cursor = resp.NextCursor
		}
	}
	return out, folderPath, nil
}

// fetchOneMedia calls get_media_info for a single item and, when possible,
// downloads its body. Returns false if the item should be skipped (unsupported
// media type, download error, note-type, etc.).
//
// externalID is the stable logical_key computed by the caller — same value
// across syncs even after an IMA same-name replacement mints a new media_id.
// The raw media_id is still preserved in metadata for observability.
func fetchOneMedia(
	ctx context.Context, cli *client, cfg *Config,
	kbID string, externalID string, f walkedFile, folderPath map[string]string,
) (types.FetchedItem, bool) {
	info, err := cli.GetMediaInfo(ctx, f.MediaID)
	if err != nil {
		logger.Warnf(ctx, "[IMA] get_media_info(%s) failed, skipping: %v", f.MediaID, err)
		return types.FetchedItem{}, false
	}

	if isSkippableMediaType(info.MediaType) {
		logger.Infof(ctx, "[IMA] skip media %s (title=%q media_type=%d): unsupported by wiki OpenAPI",
			f.MediaID, f.Title, info.MediaType)
		return types.FetchedItem{}, false
	}

	if info.URLInfo.URL == "" {
		logger.Warnf(ctx, "[IMA] media %s (title=%q) has no url_info.url, skipping", f.MediaID, f.Title)
		return types.FetchedItem{}, false
	}

	ext := extensionForMediaType(info.MediaType)
	if ext == "" {
		return types.FetchedItem{
			ExternalID:       externalID,
			Title:            f.Title,
			URL:              info.URLInfo.URL,
			SourceResourceID: kbID,
			UpdatedAt:        time.Now(),
			Metadata:         baseMetadata(externalID, f, folderPath, info, kbID),
		}, true
	}

	body, ct, err := cli.DownloadURL(ctx, info.URLInfo)
	if err != nil {
		logger.Warnf(ctx, "[IMA] download media %s (title=%q) failed, skipping: %v", f.MediaID, f.Title, err)
		return types.FetchedItem{}, false
	}
	if ct == "" || strings.HasPrefix(ct, "application/octet-stream") {
		ct = mimeForExtension(ext)
	}

	fileName := sanitizeFileName(f.Title)
	if !strings.HasSuffix(strings.ToLower(fileName), "."+ext) {
		fileName = fileName + "." + ext
	}

	return types.FetchedItem{
		ExternalID:       externalID,
		Title:            f.Title,
		Content:          body,
		ContentType:      ct,
		FileName:         fileName,
		URL:              info.URLInfo.URL,
		UpdatedAt:        time.Now(),
		SourceResourceID: kbID,
		Metadata:         baseMetadata(externalID, f, folderPath, info, kbID),
	}, true
}

// baseMetadata builds the metadata map preserved on every ingested item.
// externalID (the caller's logical_key) is stored so operators can join the
// stable identity back to the raw media_id — useful when debugging why a
// same-name replacement showed up as an update instead of an add.
func baseMetadata(externalID string, f walkedFile, folderPath map[string]string, info *getMediaInfoResp, kbID string) map[string]string {
	m := map[string]string{
		"channel":           types.ChannelIMA,
		"media_id":          f.MediaID,
		"ima_logical_key":   externalID,
		"knowledge_base_id": kbID,
		"folder_path":       f.FolderPath,
		"media_type":        fmt.Sprintf("%d", info.MediaType),
	}
	if f.ParentFolderID != "" {
		m["parent_folder_id"] = f.ParentFolderID
	}
	if fp, ok := folderPath[f.ParentFolderID]; ok && fp != "" {
		m["folder_path"] = fp
	}
	if info.NotebookExtInfo.NotebookID != "" {
		m["notebook_id"] = info.NotebookExtInfo.NotebookID
	}
	return m
}

// sanitizeFileName removes filesystem-hostile characters and truncates to a
// safe UTF-8 boundary.
func sanitizeFileName(name string) string {
	if name == "" {
		return "untitled"
	}
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	result := replacer.Replace(name)
	const maxBytes = 200
	if len(result) > maxBytes {
		result = result[:maxBytes]
		for len(result) > 0 {
			r, size := utf8.DecodeLastRuneInString(result)
			if r != utf8.RuneError || size != 1 {
				break
			}
			result = result[:len(result)-1]
		}
	}
	return result
}
