package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.Connector = (*Connector)(nil)

// Connector implements DingTalk knowledge-space discovery and document sync.
type Connector struct {
	newClient func(*Config) *client
}

func NewConnector() *Connector {
	return &Connector{newClient: newClient}
}

func (c *Connector) Type() string { return types.ConnectorTypeDingTalk }

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	if err := c.newClient(cfg).Ping(ctx); err != nil {
		return fmt.Errorf("dingtalk connection failed: %w", err)
	}
	return nil
}

func (c *Connector) ResolveResourceAncestors(
	context.Context,
	*types.DataSourceConfig,
	[]string,
) ([]string, error) {
	// The picker selects knowledge spaces, which are returned as a flat list.
	return []string{}, nil
}

func (c *Connector) ListResources(
	ctx context.Context,
	config *types.DataSourceConfig,
	parentID string,
) ([]types.Resource, error) {
	if parentID != "" {
		return []types.Resource{}, nil
	}
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	spaces, err := c.newClient(cfg).ListSpaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dingtalk knowledge spaces: %w", err)
	}
	resources := make([]types.Resource, 0, len(spaces))
	for _, space := range spaces {
		if strings.TrimSpace(space.ID) == "" {
			continue
		}
		resources = append(resources, types.Resource{
			ExternalID:  space.ID,
			Name:        space.Name,
			Type:        "wiki_space",
			Description: space.Description,
			URL:         space.URL,
			Metadata: map[string]interface{}{
				"space_type": space.Type,
			},
		})
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].ExternalID < resources[j].ExternalID
	})
	return resources, nil
}

func (c *Connector) FetchAll(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

func (c *Connector) FetchIncremental(
	ctx context.Context,
	config *types.DataSourceConfig,
	syncCursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	if config == nil {
		return nil, nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	if len(config.ResourceIDs) == 0 {
		return nil, nil, fmt.Errorf("%w: no DingTalk knowledge spaces selected", datasource.ErrInvalidConfig)
	}

	previous, err := decodeCursor(syncCursor)
	if err != nil {
		return nil, nil, err
	}
	items, next, fetchErr := c.walk(ctx, config, config.ResourceIDs, previous, true)
	if next == nil {
		return items, nil, fetchErr
	}
	nextMap, err := encodeCursor(next)
	if err != nil {
		return nil, nil, err
	}
	return items, &types.SyncCursor{
		LastSyncTime:    next.LastSyncTime,
		ConnectorCursor: nextMap,
	}, fetchErr
}

func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	previous *cursor,
	incremental bool,
) ([]types.FetchedItem, *cursor, error) {
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("%w: no DingTalk knowledge spaces selected", datasource.ErrInvalidConfig)
	}
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := c.newClient(cfg)
	next := &cursor{
		Version:       1,
		LastSyncTime:  time.Now().UTC(),
		SpaceDocTimes: make(map[string]map[string]int64),
	}
	var items []types.FetchedItem
	var spaceErrors []string
	successfulSpaces := 0

	for _, rawSpaceID := range resourceIDs {
		spaceID := strings.TrimSpace(rawSpaceID)
		if spaceID == "" {
			spaceErrors = append(spaceErrors, "empty knowledge space ID")
			continue
		}
		entries, err := cli.ListSpaceEntries(ctx, spaceID)
		if err != nil {
			logger.Warnf(ctx, "[DingTalk] list space %s failed: %v", spaceID, err)
			spaceErrors = append(spaceErrors, fmt.Sprintf("space %s: %v", spaceID, err))
			copySpaceCursor(next, previous, spaceID)
			continue
		}
		successfulSpaces++
		next.SpaceDocTimes[spaceID] = make(map[string]int64)
		currentDocuments := make(map[string]struct{})
		var fetched, unchanged, skipped, failed int

		for _, entry := range entries {
			if !entry.isDocument() {
				skipped++
				continue
			}
			documentID := entry.externalID()
			if documentID == "" {
				skipped++
				continue
			}
			currentDocuments[documentID] = struct{}{}
			previousTimestamp, existed := previousDocumentTimestamp(previous, spaceID, documentID)
			if incremental && existed && entry.UpdatedTime > 0 && previousTimestamp == entry.UpdatedTime {
				next.SpaceDocTimes[spaceID][documentID] = previousTimestamp
				unchanged++
				continue
			}

			blocks, err := cli.GetDocumentBlocks(ctx, entry.contentID())
			if err != nil {
				failed++
				if existed {
					next.SpaceDocTimes[spaceID][documentID] = previousTimestamp
				}
				items = append(items, failedItem(entry, spaceID, documentID, err))
				continue
			}
			markdown, err := blocksToMarkdown(blocks)
			if err != nil {
				failed++
				if existed {
					next.SpaceDocTimes[spaceID][documentID] = previousTimestamp
				}
				items = append(items, failedItem(entry, spaceID, documentID, err))
				continue
			}

			next.SpaceDocTimes[spaceID][documentID] = entry.UpdatedTime
			items = append(items, fetchedItem(entry, spaceID, documentID, markdown))
			fetched++
		}

		if incremental && previous != nil {
			for oldDocumentID := range previous.SpaceDocTimes[spaceID] {
				if _, exists := currentDocuments[oldDocumentID]; exists {
					continue
				}
				items = append(items, types.FetchedItem{
					ExternalID:       oldDocumentID,
					IsDeleted:        true,
					SourceResourceID: spaceID,
					Metadata: map[string]string{
						"channel":  types.ChannelDingtalk,
						"space_id": spaceID,
					},
				})
			}
		}
		logger.Infof(ctx, "[DingTalk] space %s: entries=%d fetched=%d unchanged=%d skipped=%d failed=%d",
			spaceID, len(entries), fetched, unchanged, skipped, failed)
	}

	if successfulSpaces == 0 && len(spaceErrors) > 0 {
		return nil, next, fmt.Errorf("all DingTalk knowledge spaces failed: %s", strings.Join(spaceErrors, "; "))
	}
	if !incremental {
		next = nil
	}
	if len(spaceErrors) > 0 {
		return items, next, &datasource.PartialFetchError{Details: spaceErrors}
	}
	return items, next, nil
}

func fetchedItem(entry dentry, spaceID, documentID, markdown string) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       documentID,
		Title:            firstNonEmpty(entry.Name, "untitled"),
		Content:          []byte(markdown),
		ContentType:      "text/markdown",
		FileName:         sanitizeFileName(entry.Name) + ".md",
		URL:              entry.URL,
		UpdatedAt:        entry.updatedAt(),
		SourceResourceID: spaceID,
		Metadata: map[string]string{
			"channel":      types.ChannelDingtalk,
			"space_id":     spaceID,
			"dentry_id":    entry.DentryID,
			"dentry_uuid":  entry.DentryUUID,
			"doc_key":      entry.DocKey,
			"path":         entry.Path,
			"content_type": entry.ContentType,
			"extension":    entry.Extension,
			"updated_time": strconv.FormatInt(entry.UpdatedTime, 10),
		},
	}
}

func failedItem(entry dentry, spaceID, documentID string, err error) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       documentID,
		Title:            firstNonEmpty(entry.Name, "untitled"),
		URL:              entry.URL,
		UpdatedAt:        entry.updatedAt(),
		SourceResourceID: spaceID,
		Metadata: map[string]string{
			"error":       err.Error(),
			"channel":     types.ChannelDingtalk,
			"space_id":    spaceID,
			"dentry_id":   entry.DentryID,
			"dentry_uuid": entry.DentryUUID,
			"doc_key":     entry.DocKey,
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func previousDocumentTimestamp(previous *cursor, spaceID, documentID string) (int64, bool) {
	if previous == nil || previous.SpaceDocTimes == nil {
		return 0, false
	}
	times, exists := previous.SpaceDocTimes[spaceID]
	if !exists {
		return 0, false
	}
	timestamp, exists := times[documentID]
	return timestamp, exists
}

func copySpaceCursor(next, previous *cursor, spaceID string) {
	if previous == nil || previous.SpaceDocTimes == nil {
		return
	}
	old, exists := previous.SpaceDocTimes[spaceID]
	if !exists {
		return
	}
	copyOfOld := make(map[string]int64, len(old))
	for documentID, timestamp := range old {
		copyOfOld[documentID] = timestamp
	}
	next.SpaceDocTimes[spaceID] = copyOfOld
}

func decodeCursor(syncCursor *types.SyncCursor) (*cursor, error) {
	if syncCursor == nil || syncCursor.ConnectorCursor == nil {
		return nil, nil
	}
	b, err := json.Marshal(syncCursor.ConnectorCursor)
	if err != nil {
		return nil, fmt.Errorf("encode stored dingtalk cursor: %w", err)
	}
	var parsed cursor
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, fmt.Errorf("decode stored dingtalk cursor: %w", err)
	}
	return &parsed, nil
}

func encodeCursor(value *cursor) (map[string]interface{}, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode dingtalk cursor: %w", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, fmt.Errorf("decode encoded dingtalk cursor: %w", err)
	}
	return result, nil
}
