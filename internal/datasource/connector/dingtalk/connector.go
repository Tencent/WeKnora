package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.Connector = (*Connector)(nil)

type Connector struct{}

func NewConnector() *Connector { return &Connector{} }

func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	cli := newClient(cfg)
	token, err := cli.accessToken(ctx)
	if err != nil {
		return fmt.Errorf("dingtalk access token failed: %w", err)
	}
	if _, err := cli.listSpaces(ctx, token); err != nil {
		return fmt.Errorf("dingtalk list spaces failed: %w", err)
	}
	return nil
}

func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)
	token, err := cli.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	if parentID == "" {
		spaces, err := cli.listSpaces(ctx, token)
		if err != nil {
			return nil, err
		}
		out := make([]types.Resource, 0, len(spaces))
		for _, sp := range spaces {
			out = append(out, types.Resource{
				ExternalID:  resourceID(resourceKindSpace, sp.ID, ""),
				Name:        sp.Name,
				Type:        "space",
				Description: sp.ID,
				URL:         "https://alidocs.dingtalk.com/i/drive/" + sp.ID + "/0",
				HasChildren: true,
				Metadata:    map[string]interface{}{"space_id": sp.ID},
			})
		}
		return out, nil
	}

	kind, spaceID, fileID := parseResourceID(parentID)
	if kind == "" || spaceID == "" {
		return nil, fmt.Errorf("%w: invalid DingTalk resource id %q", datasource.ErrResourceNotFound, parentID)
	}
	entries, err := cli.listAllEntries(ctx, token, spaceID, maxDriveEntries)
	if err != nil {
		return nil, err
	}
	parentFileID := "0"
	if kind == resourceKindFile {
		parentFileID = fileID
	}
	out := make([]types.Resource, 0)
	for _, entry := range entries {
		if entry.ParentID != parentFileID {
			continue
		}
		out = append(out, entryToResource(spaceID, parentID, entry))
	}
	return out, nil
}

func (c *Connector) ResolveResourceAncestors(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]string, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)
	token, err := cli.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	needed := map[string]bool{}
	for _, id := range resourceIDs {
		kind, spaceID, fileID := parseResourceID(id)
		if kind == resourceKindSpace {
			continue
		}
		if kind != resourceKindFile || spaceID == "" || fileID == "" {
			continue
		}
		needed[resourceID(resourceKindSpace, spaceID, "")] = true
		entries, err := cli.listAllEntries(ctx, token, spaceID, maxDriveEntries)
		if err != nil {
			return nil, err
		}
		byID := make(map[string]driveEntry, len(entries))
		for _, entry := range entries {
			byID[entry.ID] = entry
		}
		for cur := byID[fileID].ParentID; cur != "" && cur != "0"; cur = byID[cur].ParentID {
			needed[resourceID(resourceKindFile, spaceID, cur)] = true
		}
	}
	out := make([]string, 0, len(needed))
	for id := range needed {
		out = append(out, id)
	}
	slices.Sort(out)
	return out, nil
}

func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	var prev *dingtalkCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		b, err := json.Marshal(cursor.ConnectorCursor)
		if err == nil {
			var parsed dingtalkCursor
			if json.Unmarshal(b, &parsed) == nil {
				prev = &parsed
			}
		}
	}
	items, next, err := c.walk(ctx, config, config.ResourceIDs, prev, true)
	if next == nil {
		return items, nil, err
	}
	cursorMap := make(map[string]interface{})
	if b, marshalErr := json.Marshal(next); marshalErr == nil {
		_ = json.Unmarshal(b, &cursorMap)
	}
	return items, &types.SyncCursor{
		LastSyncTime:    next.LastSyncTime,
		ConnectorCursor: cursorMap,
	}, err
}

func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *dingtalkCursor,
	incremental bool,
) ([]types.FetchedItem, *dingtalkCursor, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := newClient(cfg)
	token, err := cli.accessToken(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(resourceIDs) == 0 {
		spaces, err := cli.listSpaces(ctx, token)
		if err != nil {
			return nil, nil, err
		}
		for _, sp := range spaces {
			resourceIDs = append(resourceIDs, resourceID(resourceKindSpace, sp.ID, ""))
		}
	}

	next := &dingtalkCursor{LastSyncTime: time.Now().UTC(), FileSignals: map[string]string{}}
	var out []types.FetchedItem
	var details []string
	seen := map[string]bool{}

	for _, rid := range resourceIDs {
		items, signals, err := c.fetchResource(ctx, cli, token, rid, prev, incremental, config.MultimodalEnabled)
		for k, v := range signals {
			next.FileSignals[k] = v
			seen[k] = true
		}
		out = append(out, items...)
		if err != nil {
			details = append(details, err.Error())
		}
	}

	if incremental && prev != nil && len(prev.FileSignals) > 0 {
		for id := range prev.FileSignals {
			if !seen[id] {
				out = append(out, types.FetchedItem{
					ExternalID: id,
					IsDeleted:  true,
					Metadata:   map[string]string{"channel": types.ChannelDingtalk},
				})
			}
		}
	}

	if len(details) > 0 {
		if len(out) == 0 {
			return nil, next, fmt.Errorf("all DingTalk resources failed: %s", strings.Join(details, "; "))
		}
		return out, next, &datasource.PartialFetchError{Details: details}
	}
	return out, next, nil
}

func (c *Connector) fetchResource(
	ctx context.Context,
	cli *client,
	token, rid string,
	prev *dingtalkCursor,
	incremental bool,
	multimodalEnabled bool,
) ([]types.FetchedItem, map[string]string, error) {
	kind, spaceID, fileID := parseResourceID(rid)
	if kind == "" || spaceID == "" {
		return nil, nil, fmt.Errorf("invalid DingTalk resource id %q", rid)
	}
	entries, err := cli.listAllEntries(ctx, token, spaceID, maxDriveEntries)
	if err != nil {
		return nil, nil, fmt.Errorf("list entries for %s: %w", rid, err)
	}
	selected := selectedEntries(entries, kind, fileID)
	signals := make(map[string]string, len(selected))
	var out []types.FetchedItem
	for _, entry := range selected {
		externalID := resourceID(resourceKindFile, spaceID, entry.ID)
		signal := entry.signal()
		signals[externalID] = signal
		if incremental && prev != nil && prev.FileSignals[externalID] == signal {
			continue
		}
		item, err := c.fetchEntry(ctx, cli, token, spaceID, rid, entry, multimodalEnabled)
		if err != nil {
			out = append(out, types.FetchedItem{
				ExternalID:       externalID,
				Title:            entry.Name,
				SourceResourceID: rid,
				Metadata: map[string]string{
					"channel":  types.ChannelDingtalk,
					"error":    err.Error(),
					"space_id": spaceID,
					"file_id":  entry.ID,
				},
			})
			continue
		}
		out = append(out, *item)
	}
	return out, signals, nil
}

func (c *Connector) fetchEntry(
	ctx context.Context,
	cli *client,
	token, spaceID, sourceResourceID string,
	entry driveEntry,
	multimodalEnabled bool,
) (*types.FetchedItem, error) {
	if entry.Size > maxFileBytes {
		return nil, fmt.Errorf("file exceeds 50MB")
	}
	meta := map[string]string{
		"channel":       types.ChannelDingtalk,
		"space_id":      spaceID,
		"file_id":       entry.ID,
		"file_path":     entry.displayPath(),
		"modified_time": entry.ModifiedAt.Format(time.RFC3339),
	}
	externalID := resourceID(resourceKindFile, spaceID, entry.ID)
	title := entry.Name
	if strings.TrimSpace(title) == "" {
		title = entry.ID
	}

	if entry.isOnlineDocument() {
		docID := entry.DocKey
		if docID == "" {
			docID = entry.ID
		}
		content, err := cli.readDocument(ctx, token, docID)
		if err == nil {
			return &types.FetchedItem{
				ExternalID:       externalID,
				Title:            title,
				Content:          content,
				ContentType:      "text/markdown",
				FileName:         sanitizeFileName(title) + ".md",
				URL:              entry.sourceURL(spaceID),
				UpdatedAt:        entry.ModifiedAt,
				SourceResourceID: sourceResourceID,
				Metadata:         meta,
			}, nil
		}
		logger.Warnf(ctx, "[DingTalk] blocks read failed for %s (%s), falling back to download: %v", title, docID, err)
	}

	if !supportedFile(entry.Name, entry.MediaType) {
		return nil, fmt.Errorf("unsupported file type")
	}
	if !multimodalEnabled && isImageExt(filepath.Ext(entry.Name)) {
		// Match Feishu's behaviour: image OCR requires a multimodal KB.
		return nil, fmt.Errorf("image OCR requires multimodal knowledge base")
	}
	downloadURL, headers, err := cli.downloadInfo(ctx, token, spaceID, entry.ID)
	if err != nil {
		return nil, err
	}
	data, err := cli.download(ctx, downloadURL, headers)
	if err != nil {
		// Download URLs can expire. Re-query once, mirroring the standalone project.
		downloadURL, headers, retryErr := cli.downloadInfo(ctx, token, spaceID, entry.ID)
		if retryErr != nil {
			return nil, err
		}
		data, err = cli.download(ctx, downloadURL, headers)
		if err != nil {
			return nil, err
		}
	}
	return &types.FetchedItem{
		ExternalID:       externalID,
		Title:            title,
		Content:          data,
		ContentType:      contentType(entry.Name, entry.MediaType, data),
		FileName:         fileName(title, entry.Name),
		URL:              entry.sourceURL(spaceID),
		UpdatedAt:        entry.ModifiedAt,
		SourceResourceID: sourceResourceID,
		Metadata:         meta,
	}, nil
}

func selectedEntries(entries []driveEntry, kind, fileID string) []driveEntry {
	if kind == resourceKindFile && fileID != "" {
		root, ok := findEntry(entries, fileID)
		if ok && !root.isFolder() {
			return []driveEntry{root}
		}
	}
	parent := "0"
	if kind == resourceKindFile {
		parent = fileID
	}
	acceptedParents := map[string]bool{parent: true}
	var out []driveEntry
	changed := true
	for changed {
		changed = false
		for _, entry := range entries {
			if !acceptedParents[entry.ParentID] || containsEntry(out, entry.ID) {
				continue
			}
			if entry.isFolder() {
				if !acceptedParents[entry.ID] {
					acceptedParents[entry.ID] = true
					changed = true
				}
				continue
			}
			out = append(out, entry)
		}
	}
	return out
}

func entryToResource(spaceID, parentResourceID string, entry driveEntry) types.Resource {
	typ := "file"
	if entry.isFolder() {
		typ = "folder"
	}
	return types.Resource{
		ExternalID:  resourceID(resourceKindFile, spaceID, entry.ID),
		Name:        firstNonEmpty(entry.Name, entry.ID),
		Type:        typ,
		Description: entry.displayPath(),
		URL:         entry.sourceURL(spaceID),
		ParentID:    parentResourceID,
		HasChildren: entry.isFolder(),
		ModifiedAt:  entry.ModifiedAt,
		Metadata: map[string]interface{}{
			"space_id":   spaceID,
			"file_id":    entry.ID,
			"parent_id":  entry.ParentID,
			"media_type": entry.MediaType,
			"size":       entry.Size,
		},
	}
}

func findEntry(entries []driveEntry, id string) (driveEntry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return driveEntry{}, false
}

func containsEntry(entries []driveEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func contentType(name, mediaType string, data []byte) string {
	if strings.TrimSpace(mediaType) != "" && strings.Contains(mediaType, "/") {
		return mediaType
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp":
		return http.DetectContentType(data)
	default:
		return "application/octet-stream"
	}
}

func fileName(title, original string) string {
	ext := filepath.Ext(original)
	if ext == "" {
		ext = ".txt"
	}
	name := sanitizeFileName(title)
	if strings.HasSuffix(strings.ToLower(name), strings.ToLower(ext)) {
		return name
	}
	return name + ext
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
