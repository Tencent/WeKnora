package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/api"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var emptyOverlay = json.RawMessage(`{"providers":{}}`)

func catalogFixture(t *testing.T) (*ModelCatalogService, *repository.ModelCatalogRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "catalog.db")), &gorm.Config{})
	require.NoError(t, err)
	ddl, err := os.ReadFile("../../../migrations/sqlite/000031_model_catalog_config.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(ddl)).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := repository.NewModelCatalogRepository(db)
	base := modelruntime.New()
	deployment := `{"providers":{"openai":{` +
		`"models":[{"id":"gpt-5","context_window":12345}],"api_key":"deployment-secret"}}}`
	require.NoError(t, base.Reload([]byte(deployment), ""))
	return &ModelCatalogService{repo: repo, base: base, target: modelruntime.New(), baseline: "test-base"}, repo
}

func TestModelCatalogPreviewPublishRollbackAndReplica(t *testing.T) {
	s, repo := catalogFixture(t)
	ctx := context.Background()
	initial, err := s.State(ctx)
	require.NoError(t, err)
	overlay := `{"providers":{"openai":{"models":[{"id":"gpt-5","context_window":54321}]},` +
		`"lab":{"models":[{"id":"sample"}]}}}`
	req := CatalogUpdate{Version: initial.Version, Baseline: initial.Baseline, Overlay: json.RawMessage(overlay)}
	preview, err := s.Preview(ctx, req)
	require.NoError(t, err)
	require.Equal(t, uint64(0), preview.Version)
	require.Nil(t, preview.History)
	require.NotEmpty(t, preview.Effective)
	row, err := repo.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(0), row.Version)
	resolved, err := s.target.Resolve(modelruntime.Ref{Provider: "openai", Model: "gpt-5"})
	require.NoError(t, err)
	require.Equal(t, 12345, resolved.Spec.ContextWindow)
	published, err := s.Publish(ctx, req)
	require.NoError(t, err)
	require.Equal(t, uint64(1), published.Version)
	require.Len(t, published.History, 1)
	resolved, err = s.target.Resolve(modelruntime.Ref{Provider: "openai", Model: "gpt-5"})
	require.NoError(t, err)
	require.Equal(t, 54321, resolved.Spec.ContextWindow)
	require.Equal(t, "deployment-secret", resolved.Vendor.DefaultAPIKey)
	wire, err := json.Marshal(published)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "deployment-secret")
	_, err = s.Publish(ctx, req)
	require.ErrorIs(t, err, repository.ErrCatalogVersionConflict)
	replica := &ModelCatalogService{repo: repo, base: s.base, target: modelruntime.New(), baseline: "test-base"}
	require.NoError(t, replica.Sync(ctx))
	resolved, err = replica.target.Resolve(modelruntime.Ref{Provider: "openai", Model: "gpt-5"})
	require.NoError(t, err)
	require.Equal(t, 54321, resolved.Spec.ContextWindow)
	_, err = s.Publish(ctx, CatalogUpdate{Version: 1, Baseline: s.baseline, Overlay: published.History[0].Overlay})
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	resolved, err = replica.target.Resolve(modelruntime.Ref{Provider: "openai", Model: "gpt-5"})
	require.NoError(t, err)
	require.Equal(t, 12345, resolved.Spec.ContextWindow)
	_, exists := replica.target.Get("lab")
	require.False(t, exists)
	// A restarted instance loads the persisted rollback, including its history.
	restarted := &ModelCatalogService{repo: repo, base: s.base, target: modelruntime.New(), baseline: "test-base"}
	state, err := restarted.State(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.Version)
	require.Len(t, state.History, 2)
}

func TestModelCatalogInvalidOverlayDoesNotPersistOrPublish(t *testing.T) {
	for name, overlay := range map[string]string{
		"typo":                 `{"providers":{"openai":{"models":[{"id":"gpt-5","compat":{"typo":true}}]}}}`,
		"negative":             `{"providers":{"openai":{"models":[{"id":"gpt-5","context_window":-1}]}}}`,
		"mixed case secret":    `{"providers":{"openai":{"API_KEY":"secret"}}}`,
		"mixed case icon":      `{"providers":{"openai":{"ICON":"icons/provider.svg"}}}`,
		"duplicate member":     `{"providers":{"openai":{"name":"first","name":"second"}}}`,
		"secret":               `{"providers":{"openai":{"api_key":"secret"}}}`,
		"headers":              `{"providers":{"openai":{"headers":{"Authorization":"secret"}}}}`,
		"environment":          `{"providers":{"openai":{"base_url":"https://${SECRET}.example"}}}`,
		"base url":             `{"providers":{"openai":{"base_url":"https://other.example/v1"}}}`,
		"base urls":            `{"providers":{"openai":{"base_urls":{"embedding":"https://other.example"}}}}`,
		"url patterns":         `{"providers":{"openai":{"url_patterns":["other.example"]}}}`,
		"auth":                 `{"providers":{"openai":{"auth":"none"}}}`,
		"file read":            `{"providers":{"openai":{"icon":"icons/provider.svg"}}}`,
		"normalized collision": `{"providers":{"OpenAI":{},"openai":{}}}`,
		"wrong format":         `{"version":1,"providers":{"openai":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, repo := catalogFixture(t)
			require.NoError(t, s.Sync(context.Background()))
			req := CatalogUpdate{Baseline: s.baseline, Overlay: json.RawMessage(overlay)}
			_, err := s.Publish(context.Background(), req)
			require.ErrorIs(t, err, ErrInvalidCatalog)
			row, err := repo.Get(context.Background())
			require.NoError(t, err)
			require.Zero(t, row.Version)
			r, err := s.target.Resolve(modelruntime.Ref{Provider: "openai", Model: "gpt-5"})
			require.NoError(t, err)
			require.Equal(t, 12345, r.Spec.ContextWindow)
		})
	}
}

func TestModelCatalogAcceptsDollarSignsInMetadata(t *testing.T) {
	s, _ := catalogFixture(t)
	ctx := context.Background()
	overlay := `{"providers":{"openai":{"description":"Pro ($20 plan)",` +
		`"models":[{"id":"gpt-5","name":"GPT-5 ($)"}]}}}`
	_, err := s.Publish(ctx, CatalogUpdate{Baseline: s.baseline, Overlay: json.RawMessage(overlay)})
	require.NoError(t, err)
	resolved, err := s.target.Resolve(modelruntime.Ref{Provider: "openai", Model: "gpt-5"})
	require.NoError(t, err)
	require.Equal(t, "GPT-5 ($)", resolved.Spec.Name)
}

func TestModelCatalogStorageCASAndHistoryBound(t *testing.T) {
	s, repo := catalogFixture(t)
	ctx := context.Background()
	for i := uint64(0); i < 22; i++ {
		_, err := s.Publish(ctx, CatalogUpdate{Version: i, Baseline: s.baseline, Overlay: emptyOverlay})
		require.NoError(t, err)
	}
	state, err := s.State(ctx)
	require.NoError(t, err)
	require.Len(t, state.History, 20)
	require.Equal(t, uint64(21), state.History[0].Version)
	require.Equal(t, uint64(2), state.History[19].Version)
	err = repo.Save(ctx, 0, &types.ModelCatalogConfig{
		Version: 99, Overlay: types.JSON(emptyOverlay), History: types.JSON(`[]`),
	})
	require.ErrorIs(t, err, repository.ErrCatalogVersionConflict)
	_, err = s.Publish(ctx, CatalogUpdate{Version: 22, Baseline: "another-file", Overlay: emptyOverlay})
	require.ErrorIs(t, err, repository.ErrCatalogVersionConflict)
}

func TestModelCatalogCanRepairAnOverlayRejectedByCurrentDeployment(t *testing.T) {
	s, repo := catalogFixture(t)
	ctx := context.Background()
	require.NoError(t, s.Sync(ctx))
	require.NoError(t, repo.Save(ctx, 0, &types.ModelCatalogConfig{
		Version: 1,
		Overlay: types.JSON(`{"providers":{"openai":{"api":"unsupported"}}}`), History: types.JSON(`[]`),
	}))
	require.Error(t, s.Sync(ctx))
	// The rejected version is remembered instead of being recompiled each poll.
	require.NoError(t, s.Sync(ctx))
	state, err := s.State(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, state.SyncError)
	require.Equal(t, uint64(1), state.Version)
	require.Zero(t, state.AppliedVersion)
	published, err := s.Publish(ctx, CatalogUpdate{Version: 1, Baseline: s.baseline, Overlay: emptyOverlay})
	require.NoError(t, err)
	require.Equal(t, uint64(2), published.AppliedVersion)
	state, err = s.State(ctx)
	require.NoError(t, err)
	require.Empty(t, state.SyncError)
}

func TestCatalogViewsResolveThinkingLevels(t *testing.T) {
	levelsFor := func(rt *modelruntime.Runtime, provider, model string) ([]api.ReasoningEffort, []api.ReasoningEffort) {
		for _, view := range catalogProviderViews(rt) {
			if view.ID == provider {
				return view.ModelThinkingLevels[model], view.VendorThinkingLevels
			}
		}
		t.Fatalf("provider %s missing", provider)
		return nil, nil
	}
	rt := modelruntime.New()
	base, vendor := levelsFor(rt, "openai", "gpt-5")
	require.Contains(t, base, api.ReasoningHigh)
	require.NotEmpty(t, vendor)

	// A console overlay can drop one level; the others keep inheriting, and
	// a non-reasoning override still reports what enabling thinking offers.
	overlay := `{"providers":{"openai":{"models":[` +
		`{"id":"gpt-5","reasoning":false,"thinking_levels":{"high":null}}]}}}`
	candidate, err := rt.WithOverlay([]byte(overlay), "")
	require.NoError(t, err)
	next, _ := levelsFor(candidate, "openai", "gpt-5")
	require.NotContains(t, next, api.ReasoningHigh)
	require.Contains(t, next, api.ReasoningLow)
	require.Len(t, next, len(base)-1)
}
