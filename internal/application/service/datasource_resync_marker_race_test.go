package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// markerRaceRepo wraps a real SQLite DataSourceRepository and, on the first
// FindByID after arming, commits a concurrent change directly to the stored
// row and then returns the pre-change snapshot — the interleaving a user edit
// (pause, config change) has against the marker cleanup's re-read.
type markerRaceRepo struct {
	interfaces.DataSourceRepository
	onRead func(context.Context, *types.DataSource)
	armed  bool
}

func (r *markerRaceRepo) FindByID(ctx context.Context, id string) (*types.DataSource, error) {
	ds, err := r.DataSourceRepository.FindByID(ctx, id)
	if err == nil && r.armed {
		r.armed = false
		r.onRead(ctx, ds)
	}
	return ds, err
}

func newMarkerRaceFixture(t *testing.T) (interfaces.DataSourceRepository, *types.DataSource) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "weknora.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.DataSource{}))

	repo := repository.NewDataSourceRepository(db)
	cfg := &types.DataSourceConfig{
		Type:     types.ConnectorTypeFeishu,
		Settings: map[string]interface{}{"resync_required": true},
	}
	blob, err := cfg.ToJSON()
	require.NoError(t, err)
	ds := &types.DataSource{
		ID:              "ds-marker-race",
		TenantID:        1,
		KnowledgeBaseID: "kb-marker-race",
		Name:            "Feishu Legacy",
		Type:            types.ConnectorTypeFeishu,
		Status:          types.DataSourceStatusActive,
		Config:          blob,
	}
	require.NoError(t, repo.Create(context.Background(), ds))
	return repo, ds
}

// A pause committed between the cleanup's re-read and write must survive:
// the marker cleanup owns only the marker, not the row's runtime state.
func TestClearResyncMarker_PreservesConcurrentPause(t *testing.T) {
	ctx := context.Background()
	repo, ds := newMarkerRaceFixture(t)
	wrapper := &markerRaceRepo{DataSourceRepository: repo, armed: true}
	wrapper.onRead = func(ctx context.Context, _ *types.DataSource) {
		cur, err := repo.FindByID(ctx, ds.ID)
		require.NoError(t, err)
		cur.Status = types.DataSourceStatusPaused
		require.NoError(t, repo.Update(ctx, cur))
	}

	svc := &DataSourceService{dsRepo: wrapper}
	svc.clearResyncMarker(ctx, ds)

	after, err := repo.FindByID(ctx, ds.ID)
	require.NoError(t, err)
	assert.Equal(t, types.DataSourceStatusPaused, after.Status, "concurrent pause must survive marker cleanup")
	cfg, err := after.ParseConfig()
	require.NoError(t, err)
	assert.NotContains(t, cfg.Settings, "resync_required", "marker must be cleared")
}

// A config edit committed between the cleanup's re-read and write must
// survive as well. The marker is left in place so the next sync can retry
// the cleanup instead of silently dropping the newer configuration.
func TestClearResyncMarker_PreservesConcurrentConfigChange(t *testing.T) {
	ctx := context.Background()
	repo, ds := newMarkerRaceFixture(t)
	wrapper := &markerRaceRepo{DataSourceRepository: repo, armed: true}
	wrapper.onRead = func(ctx context.Context, _ *types.DataSource) {
		cur, err := repo.FindByID(ctx, ds.ID)
		require.NoError(t, err)
		cfg, err := cur.ParseConfig()
		require.NoError(t, err)
		cfg.Settings["parse_mode"] = "block"
		blob, err := cfg.ToJSON()
		require.NoError(t, err)
		cur.Config = blob
		require.NoError(t, repo.Update(ctx, cur))
	}

	svc := &DataSourceService{dsRepo: wrapper}
	svc.clearResyncMarker(ctx, ds)

	after, err := repo.FindByID(ctx, ds.ID)
	require.NoError(t, err)
	cfg, err := after.ParseConfig()
	require.NoError(t, err)
	assert.Equal(t, "block", cfg.Settings["parse_mode"], "concurrent parse_mode edit must survive marker cleanup")
	assert.Contains(t, cfg.Settings, "resync_required", "marker must be left for the next sync when config changed")
}
