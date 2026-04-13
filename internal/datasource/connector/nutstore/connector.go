package nutstore

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connector implements the datasource.Connector interface for Nutstore WebDAV.
type Connector struct{}

// NewConnector creates a new Nutstore connector.
func NewConnector() *Connector {
	return &Connector{}
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
