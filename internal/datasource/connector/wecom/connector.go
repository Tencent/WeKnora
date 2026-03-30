package wecom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connector implements the datasource.Connector interface for WeCom (企业微信).
type Connector struct{}

// NewConnector creates a new WeCom document connector.
func NewConnector() *Connector {
	return &Connector{}
}

// Type returns the connector type identifier.
func (c *Connector) Type() string {
	return types.ConnectorTypeWecomDoc
}

// Validate verifies that the WeCom configuration is valid by testing connectivity.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	wecomConfig, err := parseWecomConfig(config)
	if err != nil {
		return err
	}

	client := NewClient(wecomConfig)
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("wecom connection failed: %w", err)
	}

	return nil
}

// ListResources lists available smart document categories as selectable resources.
// Each "resource" represents a document type category (all, docs, sheets).
func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig) ([]types.Resource, error) {
	wecomConfig, err := parseWecomConfig(config)
	if err != nil {
		return nil, err
	}

	// Verify connectivity
	client := NewClient(wecomConfig)
	if err := client.Ping(ctx); err != nil {
		return nil, fmt.Errorf("wecom connection failed: %w", err)
	}

	// Fetch actual doc counts per category to show the user
	allDocs, err := client.ListSmartDocuments(ctx, DocFilterAll)
	if err != nil {
		logger.Warnf(ctx, "[WeCom] failed to list documents for resource discovery: %v", err)
	}

	docCount, sheetCount := 0, 0
	for _, d := range allDocs {
		switch d.DocType {
		case DocFilterDoc:
			docCount++
		case DocFilterSheet:
			sheetCount++
		}
	}

	resources := []types.Resource{
		{
			ExternalID:  ResourceAllDocs,
			Name:        "全部文档",
			Type:        "doc_category",
			Description: fmt.Sprintf("所有智能文档 (%d 个)", len(allDocs)),
		},
		{
			ExternalID:  ResourceDocs,
			Name:        "文档",
			Type:        "doc_category",
			Description: fmt.Sprintf("仅智能文档类型 (%d 个)", docCount),
		},
		{
			ExternalID:  ResourceSheets,
			Name:        "表格",
			Type:        "doc_category",
			Description: fmt.Sprintf("仅智能表格类型 (%d 个)", sheetCount),
		},
	}

	// If WeDrive space IDs are configured in settings, list them too
	if spaceIDs := getSettingStringSlice(config, "space_ids"); len(spaceIDs) > 0 {
		for _, sid := range spaceIDs {
			resources = append(resources, types.Resource{
				ExternalID:  "wedrive:" + sid,
				Name:        "微盘空间: " + sid,
				Type:        "wedrive_space",
				Description: "企业微信微盘空间",
			})
		}
	}

	return resources, nil
}

// FetchAll performs a full sync of all documents from the selected resources.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	wecomConfig, err := parseWecomConfig(config)
	if err != nil {
		return nil, err
	}

	client := NewClient(wecomConfig)

	var allItems []types.FetchedItem

	for _, resID := range resourceIDs {
		if strings.HasPrefix(resID, "wedrive:") {
			spaceID := strings.TrimPrefix(resID, "wedrive:")
			items, err := c.fetchDriveFiles(ctx, client, config, spaceID)
			if err != nil {
				return nil, fmt.Errorf("fetch wedrive space %s: %w", spaceID, err)
			}
			allItems = append(allItems, items...)
		} else {
			items, err := c.fetchSmartDocs(ctx, client, resID)
			if err != nil {
				return nil, fmt.Errorf("fetch smart docs (resource=%s): %w", resID, err)
			}
			allItems = append(allItems, items...)
		}
	}

	return allItems, nil
}

// FetchIncremental performs an incremental sync by comparing document modify times.
func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	wecomConfig, err := parseWecomConfig(config)
	if err != nil {
		return nil, nil, err
	}

	client := NewClient(wecomConfig)

	var prevCursor wecomCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		cursorBytes, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(cursorBytes, &prevCursor)
	}

	newCursor := wecomCursor{
		LastSyncTime:   time.Now(),
		DocModifyTimes: make(map[string]int64),
	}

	var changedItems []types.FetchedItem

	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs configured")
	}

	for _, resID := range resourceIDs {
		if strings.HasPrefix(resID, "wedrive:") {
			spaceID := strings.TrimPrefix(resID, "wedrive:")
			items, driveModTimes, err := c.fetchDriveFilesIncremental(ctx, client, config, spaceID, prevCursor.DocModifyTimes)
			if err != nil {
				return nil, nil, fmt.Errorf("incremental fetch wedrive space %s: %w", spaceID, err)
			}
			changedItems = append(changedItems, items...)
			for k, v := range driveModTimes {
				newCursor.DocModifyTimes[k] = v
			}
			continue
		}

		docFilter, ok := docFilterForResource[resID]
		if !ok {
			docFilter = DocFilterAll
		}

		docs, err := client.ListSmartDocuments(ctx, docFilter)
		if err != nil {
			return nil, nil, fmt.Errorf("list smart docs for incremental: %w", err)
		}

		currentDocIDs := make(map[string]bool)

		for _, doc := range docs {
			currentDocIDs[doc.DocID] = true

			info, err := client.GetDocBaseInfo(ctx, doc.DocID)
			if err != nil {
				logger.Warnf(ctx, "[WeCom] failed to get doc info for %s: %v", doc.DocID, err)
				changedItems = append(changedItems, types.FetchedItem{
					ExternalID: doc.DocID,
					Title:      doc.DocName,
					Metadata:   map[string]string{"error": err.Error()},
				})
				newCursor.DocModifyTimes[doc.DocID] = 0
				continue
			}

			newCursor.DocModifyTimes[doc.DocID] = info.ModifyTime

			if prevModTime, exists := prevCursor.DocModifyTimes[doc.DocID]; exists {
				if prevModTime == info.ModifyTime {
					continue // unchanged
				}
			}

			item := c.buildSmartDocItem(doc.DocID, info)
			changedItems = append(changedItems, item)
		}

		// Detect deleted docs
		for docID := range prevCursor.DocModifyTimes {
			if !currentDocIDs[docID] && !strings.HasPrefix(docID, "wedrive:") {
				changedItems = append(changedItems, types.FetchedItem{
					ExternalID: docID,
					IsDeleted:  true,
				})
			}
		}
	}

	nextCursorMap := make(map[string]interface{})
	cursorBytes, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(cursorBytes, &nextCursorMap)

	nextSyncCursor := &types.SyncCursor{
		LastSyncTime:    time.Now(),
		ConnectorCursor: nextCursorMap,
	}

	return changedItems, nextSyncCursor, nil
}

// ──────────────────────────────────────────────────────────────────────
// Internal fetch helpers
// ──────────────────────────────────────────────────────────────────────

// fetchSmartDocs fetches all smart documents for a given resource category.
func (c *Connector) fetchSmartDocs(ctx context.Context, client *Client, resourceID string) ([]types.FetchedItem, error) {
	docFilter, ok := docFilterForResource[resourceID]
	if !ok {
		docFilter = DocFilterAll
	}

	docs, err := client.ListSmartDocuments(ctx, docFilter)
	if err != nil {
		return nil, err
	}

	var items []types.FetchedItem
	for _, doc := range docs {
		info, err := client.GetDocBaseInfo(ctx, doc.DocID)
		if err != nil {
			items = append(items, types.FetchedItem{
				ExternalID: doc.DocID,
				Title:      doc.DocName,
				Metadata:   map[string]string{"error": err.Error()},
			})
			continue
		}

		items = append(items, c.buildSmartDocItem(doc.DocID, info))
	}

	return items, nil
}

// buildSmartDocItem constructs a FetchedItem for a smart document.
func (c *Connector) buildSmartDocItem(docID string, info *docBaseInfo) types.FetchedItem {
	docURL := buildDocURL(docID, info.DocType)
	modTime := time.Unix(info.ModifyTime, 0)

	return types.FetchedItem{
		ExternalID:  docID,
		Title:       info.DocName,
		URL:         docURL,
		ContentType: "text/html",
		UpdatedAt:   modTime,
		Metadata: map[string]string{
			"doc_type":    fmt.Sprintf("%d", info.DocType),
			"create_time": fmt.Sprintf("%d", info.CreateTime),
			"modify_time": fmt.Sprintf("%d", info.ModifyTime),
			"channel":     types.ChannelWecom,
		},
	}
}

// fetchDriveFiles fetches all files from a WeDrive space.
func (c *Connector) fetchDriveFiles(ctx context.Context, client *Client, config *types.DataSourceConfig, spaceID string) ([]types.FetchedItem, error) {
	files, err := client.ListAllDriveFilesRecursive(ctx, spaceID)
	if err != nil {
		return nil, err
	}

	userID := getSettingString(config, "userid")

	var items []types.FetchedItem
	for _, f := range files {
		if f.FileType == FileTypeFolder {
			continue
		}

		item, err := c.fetchDriveFileContent(ctx, client, f, spaceID, userID)
		if err != nil {
			items = append(items, types.FetchedItem{
				ExternalID:       "wedrive:" + f.FileID,
				Title:            f.FileName,
				SourceResourceID: "wedrive:" + spaceID,
				Metadata:         map[string]string{"error": err.Error()},
			})
			continue
		}
		if item != nil {
			items = append(items, *item)
		}
	}

	return items, nil
}

// fetchDriveFilesIncremental performs incremental fetch for a WeDrive space.
func (c *Connector) fetchDriveFilesIncremental(ctx context.Context, client *Client, config *types.DataSourceConfig, spaceID string, prevModTimes map[string]int64) ([]types.FetchedItem, map[string]int64, error) {
	files, err := client.ListAllDriveFilesRecursive(ctx, spaceID)
	if err != nil {
		return nil, nil, err
	}

	userID := getSettingString(config, "userid")
	modTimes := make(map[string]int64)
	currentIDs := make(map[string]bool)
	var changed []types.FetchedItem

	for _, f := range files {
		if f.FileType == FileTypeFolder {
			continue
		}
		key := "wedrive:" + f.FileID
		currentIDs[key] = true
		modTimes[key] = f.ModifyTime

		if prevMod, exists := prevModTimes[key]; exists && prevMod == f.ModifyTime {
			continue // unchanged
		}

		item, err := c.fetchDriveFileContent(ctx, client, f, spaceID, userID)
		if err != nil {
			changed = append(changed, types.FetchedItem{
				ExternalID:       key,
				Title:            f.FileName,
				SourceResourceID: "wedrive:" + spaceID,
				Metadata:         map[string]string{"error": err.Error()},
			})
			continue
		}
		if item != nil {
			changed = append(changed, *item)
		}
	}

	// Detect deletions
	for prevKey := range prevModTimes {
		if strings.HasPrefix(prevKey, "wedrive:") && !currentIDs[prevKey] {
			changed = append(changed, types.FetchedItem{
				ExternalID:       prevKey,
				IsDeleted:        true,
				SourceResourceID: "wedrive:" + spaceID,
			})
		}
	}

	return changed, modTimes, nil
}

// fetchDriveFileContent downloads file content for a WeDrive file.
func (c *Connector) fetchDriveFileContent(ctx context.Context, client *Client, f driveFileItem, spaceID, userID string) (*types.FetchedItem, error) {
	modTime := time.Unix(f.ModifyTime, 0)
	externalID := "wedrive:" + f.FileID
	baseMeta := map[string]string{
		"file_type":     fmt.Sprintf("%d", f.FileType),
		"file_id":       f.FileID,
		"space_id":      spaceID,
		"create_userid": f.CreateUserID,
		"update_userid": f.UpdateUserID,
		"channel":       types.ChannelWecom,
	}

	switch f.FileType {
	case FileTypeFile:
		// Binary uploaded file — download via WeDrive API
		if userID == "" {
			return &types.FetchedItem{
				ExternalID:       externalID,
				Title:            f.FileName,
				URL:              f.URL,
				UpdatedAt:        modTime,
				SourceResourceID: "wedrive:" + spaceID,
				Metadata:         baseMeta,
			}, nil
		}

		dlResp, err := client.GetDriveFileDownloadURL(ctx, f.FileID, userID)
		if err != nil {
			return nil, fmt.Errorf("get download url for %s: %w", f.FileName, err)
		}

		data, err := client.DownloadFromURL(ctx, dlResp.DownloadURL, dlResp.CookieName, dlResp.CookieValue)
		if err != nil {
			return nil, fmt.Errorf("download file %s: %w", f.FileName, err)
		}

		return &types.FetchedItem{
			ExternalID:       externalID,
			Title:            f.FileName,
			Content:          data,
			ContentType:      "application/octet-stream",
			FileName:         f.FileName,
			URL:              f.URL,
			UpdatedAt:        modTime,
			SourceResourceID: "wedrive:" + spaceID,
			Metadata:         baseMeta,
		}, nil

	case FileTypeDoc, FileTypeSheet, FileTypeForm:
		// Smart doc in WeDrive — import via URL
		url := f.URL
		if url == "" {
			url = buildDocURL(f.FileID, f.FileType)
		}

		return &types.FetchedItem{
			ExternalID:       externalID,
			Title:            f.FileName,
			URL:              url,
			ContentType:      "text/html",
			UpdatedAt:        modTime,
			SourceResourceID: "wedrive:" + spaceID,
			Metadata:         baseMeta,
		}, nil

	default:
		return nil, nil
	}
}

// ──────────────────────────────────────────────────────────────────────
// Helper functions
// ──────────────────────────────────────────────────────────────────────

func parseWecomConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}

	var wecomConfig Config
	if err := json.Unmarshal(credBytes, &wecomConfig); err != nil {
		return nil, fmt.Errorf("parse wecom credentials: %w", err)
	}

	if wecomConfig.CorpID == "" || wecomConfig.CorpSecret == "" {
		return nil, fmt.Errorf("wecom corp_id and corp_secret are required")
	}

	return &wecomConfig, nil
}

// buildDocURL constructs the WeCom document URL based on doc type.
// docType can be either DocFilter* or FileType* constants (4 = sheet in both).
func buildDocURL(docID string, docType int) string {
	switch docType {
	case FileTypeSheet: // also matches DocFilterSheet (both are 4)
		return fmt.Sprintf("https://doc.weixin.qq.com/sheet/%s", docID)
	case FileTypeForm, DocFilterForm:
		return fmt.Sprintf("https://doc.weixin.qq.com/forms/%s", docID)
	default:
		return fmt.Sprintf("https://doc.weixin.qq.com/doc/%s", docID)
	}
}

// getSettingString retrieves a string value from config.Settings.
func getSettingString(config *types.DataSourceConfig, key string) string {
	if config == nil || config.Settings == nil {
		return ""
	}
	v, ok := config.Settings[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// getSettingStringSlice retrieves a string slice from config.Settings.
func getSettingStringSlice(config *types.DataSourceConfig, key string) []string {
	if config == nil || config.Settings == nil {
		return nil
	}
	v, ok := config.Settings[key]
	if !ok {
		return nil
	}
	switch val := v.(type) {
	case []string:
		return val
	case []interface{}:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case string:
		if val == "" {
			return nil
		}
		return strings.Split(val, ",")
	default:
		return nil
	}
}
