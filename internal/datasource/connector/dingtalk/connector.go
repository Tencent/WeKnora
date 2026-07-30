package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// Compile-time proof that *Connector satisfies the datasource.Connector interface.
var _ datasource.Connector = (*Connector)(nil)

// Connector implements datasource.Connector for DingTalk.
type Connector struct{}

// NewConnector creates a new DingTalk connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

// Validate verifies the given credentials by attempting to get an access token.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return err
	}
	cli := NewClient(cfg)
	if err := cli.Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	return nil
}

// ResolveResourceAncestors has nothing to do for DingTalk: workspaces are a flat
// list with no nesting, so a selection has no ancestors to reveal.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

// ListResources returns all workspaces accessible to the app.
// DingTalk workspaces are a flat list (no nesting), so lazy-load for a specific
// parent returns empty.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	if parentID != "" {
		return []types.Resource{}, nil
	}

	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := NewClient(cfg)

	workspaces, err := cli.ListWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dingtalk workspaces: %w", err)
	}

	out := make([]types.Resource, 0, len(workspaces))
	for _, ws := range workspaces {
		out = append(out, types.Resource{
			ExternalID: ws.WorkspaceID,
			Name:       ws.WorkspaceName,
			Type:       "workspace",
			URL:        "",
			ModifiedAt: parseDingtalkTimestamp(ws.UpdateTime),
			Metadata: map[string]interface{}{
				"workspace_type": ws.WorkspaceType,
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExternalID < out[j].ExternalID })
	return out, nil
}

// FetchAll performs a full sync of all documents in the specified workspaces.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

// FetchIncremental returns items changed (or deleted) since the prior cursor.
func (c *Connector) FetchIncremental(
	ctx context.Context,
	config *types.DataSourceConfig,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (workspace IDs) configured")
	}

	var prev *dingtalkCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		var p dingtalkCursor
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
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *dingtalkCursor,
	incremental bool,
) ([]types.FetchedItem, *dingtalkCursor, error) {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := NewClient(cfg)

	newCursor := &dingtalkCursor{
		LastSyncTime:      time.Now(),
		WorkspaceDocTimes: make(map[string]map[string]string),
	}
	var out []types.FetchedItem

	for _, workspaceID := range resourceIDs {
		docs, err := cli.ListDocuments(ctx, workspaceID)
		if err != nil {
			return nil, nil, fmt.Errorf("list docs for workspace %s: %w", workspaceID, err)
		}

		currentDocs := make(map[string]bool)
		newCursor.WorkspaceDocTimes[workspaceID] = make(map[string]string)

		for _, d := range docs {
			docIDStr := d.DocumentID
			currentDocs[docIDStr] = true
			updateTimeStr := strconv.FormatInt(d.UpdateTime, 10)
			newCursor.WorkspaceDocTimes[workspaceID][docIDStr] = updateTimeStr

			// Incremental: skip if content hasn't changed.
			if incremental && prev != nil && prev.WorkspaceDocTimes != nil {
				if prevTimes, ok := prev.WorkspaceDocTimes[workspaceID]; ok {
					if prevTimes[docIDStr] == updateTimeStr {
						continue
					}
				}
			}

			detail, err := cli.GetDocumentContent(ctx, d.DocumentID)
			if err != nil {
				out = append(out, types.FetchedItem{
					ExternalID:       docIDStr,
					Title:            d.DocumentName,
					SourceResourceID: workspaceID,
					Metadata: map[string]string{
						"error":        err.Error(),
						"channel":      types.ChannelDingtalk,
						"document_id":  docIDStr,
						"workspace_id": workspaceID,
					},
				})
				continue
			}

			// Determine content type and content
			contentType := "text/markdown"
			content := []byte(detail.Content)
			if detail.ContentType == "html" {
				contentType = "text/html"
			}

			fileName := sanitizeFileName(detail.DocumentName)
			switch detail.DocumentType {
			case "sheet":
				fileName += ".xlsx"
			case "slide":
				fileName += ".pptx"
			default:
				fileName += ".md"
			}

			out = append(out, types.FetchedItem{
				ExternalID:       docIDStr,
				Title:            detail.DocumentName,
				Content:          content,
				ContentType:      contentType,
				FileName:         fileName,
				URL:              detail.URL,
				UpdatedAt:        parseDingtalkTimestamp(detail.UpdateTime),
				SourceResourceID: workspaceID,
				Metadata: map[string]string{
					"document_id":   docIDStr,
					"workspace_id":  workspaceID,
					"document_type": detail.DocumentType,
					"creator":       detail.Creator,
					"creator_name":  detail.CreatorName,
					"channel":       types.ChannelDingtalk,
				},
			})
		}

		// Deletion detection (incremental only)
		if incremental && prev != nil && prev.WorkspaceDocTimes != nil {
			if prevTimes, ok := prev.WorkspaceDocTimes[workspaceID]; ok {
				for prevDocID := range prevTimes {
					if !currentDocs[prevDocID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevDocID,
							IsDeleted:        true,
							SourceResourceID: workspaceID,
						})
					}
				}
			}
		}
	}

	if !incremental {
		return out, nil, nil
	}
	return out, newCursor, nil
}

// parseDingTalkConfig extracts and validates DingTalk-specific configuration.
func parseDingTalkConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}

	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(credBytes, &cfg); err != nil {
		return nil, fmt.Errorf("parse dingtalk credentials: %w", err)
	}

	if cfg.AppKey == "" || cfg.AppSecret == "" {
		return nil, fmt.Errorf("%w: app_key and app_secret are required", datasource.ErrInvalidCredentials)
	}

	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// sanitizeFileName removes characters that are invalid in filenames and
// truncates at a UTF-8 rune boundary.
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
