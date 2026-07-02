package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
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

// Validate verifies the given credentials by pinging the workspaces endpoint.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return err
	}
	cli := newClient(cfg)
	if err := cli.Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	return nil
}

// ResolveResourceAncestors has nothing to do for DingTalk workspaces: workspaces are a flat
// list with no nesting, so a selection has no ancestors to reveal.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

// ListResources returns all workspaces accessible to the credentials.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	// DingTalk resources are a flat list of workspaces (no nesting), so a
	// lazy-load request for a specific parent has nothing extra to return.
	if parentID != "" {
		return []types.Resource{}, nil
	}

	cfg, err := parseDingTalkConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	// Get workspaces - use operatorID from config if provided
	operatorID := cfg.OperatorID
	workspaces, err := cli.ListWorkspaces(ctx, operatorID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}

	out := make([]types.Resource, 0, len(workspaces))
	for _, w := range workspaces {
		out = append(out, types.Resource{
			ExternalID:  w.WorkspaceID,
			Name:        w.Name,
			Type:        "workspace",
			URL:         w.URL,
			Description: w.Description,
			ModifiedAt:  parseTime(w.ModifiedTime),
			Metadata: map[string]interface{}{
				"workspace_type": w.Type,
				"root_node_id":   w.RootNodeID,
				"corp_id":       w.CorpID,
			},
		})
	}

	// Stable, deterministic order for UI rendering
	sort.Slice(out, func(i, j int) bool { return out[i].ExternalID < out[j].ExternalID })
	return out, nil
}

// FetchAll performs a full sync of all workspaces specified in resourceIDs.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
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
	cli := newClient(cfg)
	operatorID := cfg.OperatorID

	newCursor := &dingtalkCursor{
		LastSyncTime:   time.Now(),
		WorkspaceTimes: make(map[string]map[string]time.Time),
	}
	var out []types.FetchedItem

	for _, workspaceID := range resourceIDs {
		newCursor.WorkspaceTimes[workspaceID] = make(map[string]time.Time)

		// Get root node ID for this workspace
		workspaces, err := cli.ListWorkspaces(ctx, operatorID)
		if err != nil {
			logger.Warnf(ctx, "[DingTalk] failed to get workspace %s info: %v", workspaceID, err)
			continue
		}

		var rootNodeID string
		for _, w := range workspaces {
			if w.WorkspaceID == workspaceID {
				rootNodeID = w.RootNodeID
				break
			}
		}
		if rootNodeID == "" {
			logger.Warnf(ctx, "[DingTalk] workspace %s not found or has no root node", workspaceID)
			continue
		}

		// List all nodes recursively
		allNodes, err := cli.ListAllNodes(ctx, rootNodeID, operatorID)
		if err != nil {
			return nil, nil, fmt.Errorf("list nodes for workspace %s: %w", workspaceID, err)
		}

		var skippedFolder, skippedNonDoc, kept int
		var sampleSkip string
		for _, node := range allNodes {
			newCursor.WorkspaceTimes[workspaceID][node.NodeID] = parseTime(node.ModifiedTime)

			// Skip folders - they are listed but not synced as content
			if node.NodeType == "FOLDER" {
				skippedFolder++
				continue
			}

			// Only sync document types (ALIDOC = 钉钉文档, DOCUMENT = 本地文档)
			if node.Category != "ALIDOC" && node.Category != "DOCUMENT" {
				skippedNonDoc++
				if sampleSkip == "" {
					sampleSkip = fmt.Sprintf("nodeId=%s type=%s category=%s name=%q", node.NodeID, node.NodeType, node.Category, node.Name)
				}
				continue
			}

			// Incremental: skip if content hasn't changed
			if incremental && prev != nil && prev.WorkspaceTimes != nil {
				if prevTimes, ok := prev.WorkspaceTimes[workspaceID]; ok {
					if prevModTime, ok := prevTimes[node.NodeID]; ok {
						currentModTime := parseTime(node.ModifiedTime)
						if !currentModTime.After(prevModTime) {
							continue
						}
					}
				}
			}

			kept++

			// Rate limit protection
			if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
				return nil, nil, err
			}

			// For now, we create a placeholder item since full content fetching
			// requires additional API calls to get document blocks.
			// The content will be represented as metadata since DingTalk's document
			// content API requires specific permissions and additional calls.
			out = append(out, types.FetchedItem{
				ExternalID:       node.NodeID,
				Title:            node.Name,
				Content:          []byte(fmt.Sprintf("# %s\n\n*Source: [DingTalk](%s)*\n\n---\n\n> This document was synced from DingTalk Wiki.\n> Node ID: `%s`\n> Category: %s\n> Modified: %s",
					node.Name, node.URL, node.NodeID, node.Category, node.ModifiedTime)),
				ContentType:      "text/markdown",
				FileName:         sanitizeFileName(node.Name) + ".md",
				URL:              node.URL,
				UpdatedAt:        parseTime(node.ModifiedTime),
				SourceResourceID: workspaceID,
				Metadata: map[string]string{
					"node_id":     node.NodeID,
					"workspace_id": workspaceID,
					"node_type":   node.NodeType,
					"category":    node.Category,
					"word_count":  fmt.Sprintf("%d", node.WordCount),
					"channel":     types.ChannelDingtalk,
				},
			})
		}

		logger.Infof(ctx, "[DingTalk] workspace %s: total=%d kept=%d skipped_folder=%d skipped_non_doc=%d sample_skip={%s}",
			workspaceID, len(allNodes), kept, skippedFolder, skippedNonDoc, sampleSkip)

		// Deletion detection (incremental only)
		if incremental && prev != nil && prev.WorkspaceTimes != nil {
			if prevTimes, ok := prev.WorkspaceTimes[workspaceID]; ok {
				for prevNodeID := range prevTimes {
					found := false
					for _, node := range allNodes {
						if node.NodeID == prevNodeID {
							found = true
							break
						}
					}
					if !found {
						out = append(out, types.FetchedItem{
							ExternalID:       prevNodeID,
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

	// Decode prior cursor (if any)
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

	// Marshal newCursor into a generic map for the SyncCursor wrapper
	cursorMap := make(map[string]interface{})
	b, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(b, &cursorMap)

	return items, &types.SyncCursor{
		LastSyncTime:    newCursor.LastSyncTime,
		ConnectorCursor: cursorMap,
	}, nil
}
