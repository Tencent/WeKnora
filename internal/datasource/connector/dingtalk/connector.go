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

var _ datasource.Connector = (*Connector)(nil)

// Connector implements datasource.Connector for DingTalk.
type Connector struct{}

func NewConnector() *Connector { return &Connector{} }

func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

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

func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

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
	cli := newClient(cfg)

	spaces, err := cli.ListSpaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dingtalk spaces: %w", err)
	}

	out := make([]types.Resource, 0, len(spaces))
	for _, s := range spaces {
		out = append(out, types.Resource{
			ExternalID:  s.SpaceID,
			Name:        s.Name,
			Type:        "space",
			URL:         s.URL,
			Description: s.Description,
			ModifiedAt:  parseMsTime(s.ModifiedTime),
			Metadata: map[string]interface{}{
				"owner_id":   s.OwnerID,
				"owner_name": s.OwnerName,
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExternalID < out[j].ExternalID })
	return out, nil
}

func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

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

	newCursor := &dingtalkCursor{LastSyncTime: time.Now(), SpaceDocTimes: make(map[string]map[string]int64)}
	var out []types.FetchedItem

	for _, spaceID := range resourceIDs {
		docs, err := cli.ListSpaceDocs(ctx, spaceID)
		if err != nil {
			return nil, nil, fmt.Errorf("list docs for space %s: %w", spaceID, err)
		}

		currentDocs := make(map[string]bool)
		newCursor.SpaceDocTimes[spaceID] = make(map[string]int64)

		var kept int
		for _, d := range docs {
			if d.DocType != "" && d.DocType != "doc" {
				logger.Debugf(ctx, "[DingTalk] skip doc %s (%q): type=%s", d.DocID, d.Name, d.DocType)
				continue
			}
			kept++
			docID := d.DocID
			currentDocs[docID] = true
			contentModifiedMs := d.ContentModifiedTime
			if contentModifiedMs == 0 {
				contentModifiedMs = d.ModifiedTime
			}
			newCursor.SpaceDocTimes[spaceID][docID] = contentModifiedMs

			if incremental && prev != nil && prev.SpaceDocTimes != nil {
				if prevTimes, ok := prev.SpaceDocTimes[spaceID]; ok {
					if prevModified, exists := prevTimes[docID]; exists && prevModified == contentModifiedMs {
						continue
					}
				}
			}

			if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
				return nil, nil, err
			}

			detail, err := cli.GetDocDetail(ctx, spaceID, d.DocID)
			if err != nil {
				out = append(out, types.FetchedItem{
					ExternalID:       docID,
					Title:            d.Name,
					SourceResourceID: spaceID,
					Metadata: map[string]string{
						"error":    err.Error(),
						"channel":  types.ChannelDingtalk,
						"doc_id":   docID,
						"space_id": spaceID,
					},
				})
				continue
			}

			content := detail.Content
			contentType := detail.ContentType
			fileExt := ".md"

			switch contentType {
			case "text/html", "html":
				content = htmlToMarkdown(detail.Content)
				contentType = "text/markdown"
			case "text/markdown", "markdown":
				contentType = "text/markdown"
			default:
				if contentType == "" {
					contentType = "text/markdown"
				}
			}

			out = append(out, types.FetchedItem{
				ExternalID:       docID,
				Title:            detail.Name,
				Content:          []byte(content),
				ContentType:      contentType,
				FileName:         sanitizeFileName(detail.Name) + fileExt,
				URL:              detail.URL,
				UpdatedAt:        parseMsTime(detail.ModifiedTime),
				SourceResourceID: spaceID,
				Metadata: map[string]string{
					"doc_id":     docID,
					"space_id":   spaceID,
					"doc_type":   detail.DocType,
					"creator_id": detail.CreatorID,
					"channel":    types.ChannelDingtalk,
				},
			})
		}

		logger.Infof(ctx, "[DingTalk] space %s: total=%d kept=%d", spaceID, len(docs), kept)

		if incremental && prev != nil && prev.SpaceDocTimes != nil {
			if prevTimes, ok := prev.SpaceDocTimes[spaceID]; ok {
				for prevDocID := range prevTimes {
					if !currentDocs[prevDocID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevDocID,
							IsDeleted:        true,
							SourceResourceID: spaceID,
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

func (c *Connector) FetchIncremental(
	ctx context.Context,
	config *types.DataSourceConfig,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (space IDs) configured")
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
