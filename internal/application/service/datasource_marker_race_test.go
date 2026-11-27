package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type markerRaceRepository struct {
	interfaces.DataSourceRepository
	afterRead func()
}

func (r *markerRaceRepository) FindByID(ctx context.Context, id string) (*types.DataSource, error) {
	ds, err := r.DataSourceRepository.FindByID(ctx, id)
	if err == nil && r.afterRead != nil {
		fn := r.afterRead
		r.afterRead = nil
		fn()
	}
	return ds, err
}

func TestClearResyncMarkerPreservesConcurrentChanges(t *testing.T) {
	for _, change := range []string{"pause", "config"} {
		t.Run(change, func(t *testing.T) {
			f := newSQLiteDataSourceDeleteFixture(t)
			ctx := context.Background()
			f.ds.Config = []byte(`{"type":"feishu","settings":{"resync_required":true}}`)
			require.NoError(t, f.dsRepo.Update(ctx, f.ds))
			concurrentConfig := `{"type":"feishu","settings":{"resync_required":true,"parse_mode":"block"}}`
			r := &markerRaceRepository{DataSourceRepository: f.dsRepo}
			r.afterRead = func() {
				values := map[string]interface{}{"status": types.DataSourceStatusPaused}
				if change == "config" {
					values = map[string]interface{}{"config": concurrentConfig}
				}
				require.NoError(t, f.db.Model(&types.DataSource{}).Where("id = ?", f.ds.ID).Updates(values).Error)
			}
			svc := &DataSourceService{dsRepo: r}
			svc.clearResyncMarker(ctx, f.ds)
			saved, err := f.dsRepo.FindByID(ctx, f.ds.ID)
			require.NoError(t, err)
			cfg, err := saved.ParseConfig()
			require.NoError(t, err)
			if change == "pause" {
				require.Equal(t, types.DataSourceStatusPaused, saved.Status)
				require.False(t, resyncRequired(cfg))
			} else {
				require.Equal(t, "block", cfg.Settings["parse_mode"])
				require.True(t, resyncRequired(cfg), "the newly edited config must retain its resync request")
			}
		})
	}
}
