package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type previewRepo struct {
	interfaces.DataSourceRepository
	ds *types.DataSource
}

func (r *previewRepo) Update(_ context.Context, ds *types.DataSource) error { r.ds = ds; return nil }

func (r *previewRepo) FindByID(context.Context, string) (*types.DataSource, error) { return r.ds, nil }

type previewTestConnector struct {
	datasource.Connector
	config *types.DataSourceConfig
	parent string
}

func (*previewTestConnector) Type() string { return "preview-test" }
func (c *previewTestConnector) Validate(_ context.Context, config *types.DataSourceConfig) error {
	c.config = config
	return nil
}
func (c *previewTestConnector) ListResources(_ context.Context, config *types.DataSourceConfig, parent string) ([]types.Resource, error) {
	c.config, c.parent = config, parent
	return []types.Resource{}, nil
}

type confluencePreviewConnector struct{ previewTestConnector }

func (*confluencePreviewConnector) Type() string { return types.ConnectorTypeConfluence }

func TestConfluenceReplacementValidatesUpdatedPublicFields(t *testing.T) {
	stored, err := (&types.DataSourceConfig{Credentials: map[string]interface{}{"username": "old", "password": "old"}, Settings: map[string]interface{}{"username": "old"}}).ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	repo := &previewRepo{ds: &types.DataSource{ID: "one", Type: types.ConnectorTypeConfluence, Config: stored}}
	connector := &confluencePreviewConnector{}
	registry := datasource.NewConnectorRegistry()
	if err := registry.Register(connector); err != nil {
		t.Fatal(err)
	}
	s := &DataSourceService{dsRepo: repo, connectorRegistry: registry}
	if _, err := s.UpdateDataSourceCredentials(context.Background(), "one", map[string]interface{}{"username": "new", "password": "new"}); err != nil {
		t.Fatal(err)
	}
	if connector.config.Settings["username"] != "new" {
		t.Fatalf("validation used old username: %#v", connector.config.Settings)
	}
}
func (c *previewTestConnector) ResolveResourceAncestors(_ context.Context, config *types.DataSourceConfig, ids []string) ([]string, error) {
	c.config = config
	return ids, nil
}

func TestResourcePreviewUsesUnsavedConfigWithoutPersisting(t *testing.T) {
	stored, err := (&types.DataSourceConfig{Credentials: map[string]interface{}{"token": "old"}, Settings: map[string]interface{}{"username": "old"}}).ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	ds := &types.DataSource{ID: "one", Type: "preview-test", Config: stored}
	connector := &previewTestConnector{}
	registry := datasource.NewConnectorRegistry()
	if err := registry.Register(connector); err != nil {
		t.Fatal(err)
	}
	s := &DataSourceService{dsRepo: &previewRepo{ds: ds}, connectorRegistry: registry}
	preview := &types.DataSourceConfig{Credentials: map[string]interface{}{"token": "new"}, Settings: map[string]interface{}{"username": "new"}}
	ctx := context.Background()
	if _, err := s.PreviewAvailableResources(ctx, ds.ID, "parent", preview); err != nil {
		t.Fatal(err)
	}
	if connector.parent != "parent" || connector.config.Credentials["token"] != "new" || connector.config.Settings["username"] != "new" {
		t.Fatalf("preview was not applied: %#v", connector.config)
	}
	if _, err := s.PreviewResourceAncestors(ctx, ds.ID, []string{"child"}, preview); err != nil {
		t.Fatal(err)
	}
	if connector.config.Credentials["token"] != "new" {
		t.Fatal("ancestors did not receive preview credentials")
	}
	if _, err := s.ListAvailableResources(ctx, ds.ID, ""); err != nil {
		t.Fatal(err)
	}
	if connector.config.Credentials["token"] != "old" || connector.config.Settings["username"] != "old" {
		t.Fatal("preview changed stored configuration")
	}
}
