package wecom_chat_archive

import (
	"context"
	"fmt"

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
		return fmt.Errorf("wecom chat archive connection failed: %w", err)
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
	return nil, fmt.Errorf("wecom chat archive fetch is not implemented yet")
}

func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	return nil, nil, fmt.Errorf("wecom chat archive incremental fetch is not implemented yet")
}
