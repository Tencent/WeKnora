package wecom_chat_archive

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.Connector = (*Connector)(nil)

type Connector struct {
	newClient clientFactory
}

type Option func(*Connector)

func NewConnector(options ...Option) *Connector {
	c := &Connector{newClient: newUnavailableClient}
	for _, option := range options {
		option(c)
	}
	return c
}

func WithClientFactory(factory clientFactory) Option {
	return func(c *Connector) {
		if factory != nil {
			c.newClient = factory
		}
	}
}

func (c *Connector) Type() string { return types.ConnectorTypeWeComChatArchive }

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	client := c.newClient(cfg)
	defer client.Close()
	if err := client.Validate(ctx); err != nil {
		return sanitizeConnectorError(cfg, fmt.Errorf("wecom chat archive connection failed: %w", err))
	}
	return nil
}

func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
	if _, err := parseConfig(config); err != nil {
		return nil, err
	}
	if parentID != "" {
		return []types.Resource{}, nil
	}
	return []types.Resource{{
		ExternalID:  virtualResourceAll,
		Name:        "全部已授权会话",
		Type:        "wecom_chat_archive_scope",
		Description: "同步企业微信会话内容存档授权范围内的所有会话",
		Metadata: map[string]interface{}{
			"scope": "all_archived_conversations",
		},
	}}, nil
}

func (c *Connector) ResolveResourceAncestors(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]string, error) {
	return []string{}, nil
}

func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	client := c.newClient(cfg)
	defer client.Close()
	start := time.Now().UTC().AddDate(0, 0, -cfg.Settings.FullSyncDays)
	items, _, err := c.fetchAndAggregate(ctx, client, cfg, 0, start)
	if err != nil {
		return nil, sanitizeConnectorError(cfg, err)
	}
	return items, nil
}

func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	prev := parseCursor(cursor)
	client := c.newClient(cfg)
	defer client.Close()
	items, next, err := c.fetchAndAggregate(ctx, client, cfg, prev.LastSeq+1, time.Time{})
	if err != nil {
		return nil, nil, sanitizeConnectorError(cfg, err)
	}
	if next.LastSeq < prev.LastSeq {
		next.LastSeq = prev.LastSeq
	}
	cursorMap := map[string]interface{}{
		"last_seq":       fmt.Sprintf("%d", next.LastSeq),
		"last_msg_time":  float64(next.LastMsgTime),
		"last_sync_time": next.LastSyncTime,
	}
	if len(next.DayBuckets) > 0 {
		cursorMap["day_buckets"] = next.DayBuckets
	}
	return items, &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: cursorMap}, nil
}

func (c *Connector) fetchAndAggregate(ctx context.Context, client ArchiveClient, cfg *Config, startSeq uint64, minTime time.Time) ([]types.FetchedItem, weComChatArchiveCursor, error) {
	loc, err := time.LoadLocation(cfg.Settings.Timezone)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	buckets := make(map[string]*dayBucket)
	next := weComChatArchiveCursor{LastSyncTime: time.Now().UTC().Format(time.RFC3339), DayBuckets: make(map[string]string)}
	seq := startSeq
	for batch := 0; batch < defaultMaxFetchBatches; batch++ {
		messages, hasMore, err := client.FetchMessages(ctx, seq, defaultFetchBatchSize)
		if err != nil {
			return nil, weComChatArchiveCursor{}, err
		}
		for _, msg := range messages {
			msg = fillConversationFromRoomID(msg)
			if msg.Seq > next.LastSeq {
				next.LastSeq = msg.Seq
			}
			if msg.Seq >= seq {
				seq = msg.Seq + 1
			}
			if !minTime.IsZero() && msg.MsgTime.Before(minTime) {
				continue
			}
			if msg.ConversationID == "" {
				continue
			}
			localTime := msg.MsgTime.In(loc)
			msg.MsgTime = localTime
			key := msg.ConversationID + ":" + localTime.Format("2006-01-02")
			bucket := buckets[key]
			if bucket == nil {
				bucket = newDayBucket(msg)
				buckets[key] = bucket
			}
			normalized := normalizeMessage(msg)
			addToBucket(bucket, normalized)
			if msg.MsgTime.Unix() > next.LastMsgTime {
				next.LastMsgTime = msg.MsgTime.Unix()
			}
		}
		if !hasMore {
			break
		}
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]types.FetchedItem, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		item := bucketItem(bucket)
		items = append(items, item)
		next.DayBuckets[key] = item.ExternalID
	}
	return items, next, nil
}

func fillConversationFromRoomID(msg ArchiveMessageEnvelope) ArchiveMessageEnvelope {
	if msg.RoomID == "" {
		return msg
	}
	if msg.ConversationID == "" {
		msg.ConversationID = msg.RoomID
	}
	if msg.ConversationType == "" || msg.ConversationType == conversationTypeSingle {
		msg.ConversationType = conversationTypeRoom
	}
	return msg
}

func parseCursor(cursor *types.SyncCursor) weComChatArchiveCursor {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return weComChatArchiveCursor{}
	}
	var out weComChatArchiveCursor
	bytes, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		return weComChatArchiveCursor{}
	}
	if err := json.Unmarshal(bytes, &out); err != nil {
		return weComChatArchiveCursor{}
	}
	return out
}
