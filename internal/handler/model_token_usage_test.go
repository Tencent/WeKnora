package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestModelTokenUsageList(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	migration, err := os.ReadFile("../../migrations/sqlite/000053_model_token_usage.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)

	repo := repository.NewModelTokenUsageRepository(db)
	ctx := context.Background()
	entry := types.ModelTokenUsage{
		ID: "call-1", TenantID: 1, ModelID: "model-1", ModelName: "Model",
		StartedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), UsageDate: "2026-10-01",
		InputTokens: 10, OutputTokens: 5, TotalTokens: 15, UsageKnown: true, Completed: true,
	}
	require.NoError(t, repo.Record(ctx, entry))
	require.NoError(t, repo.Record(ctx, entry))
	require.NoError(t, repo.Record(ctx, types.ModelTokenUsage{
		ID: "call-2", TenantID: 1, ModelID: entry.ModelID, ModelName: entry.ModelName,
		StartedAt: entry.StartedAt, UsageDate: "2026-10-02",
	}))
	entry.ID, entry.TenantID = "call-3", 2
	require.NoError(t, repo.Record(ctx, entry))

	router := gin.New()
	router.GET("/usage", NewModelTokenUsageHandler(repo).List)
	admin := types.WithCaller(ctx, types.Caller{TenantID: 1, Role: types.TenantRoleAdmin})
	viewer := types.WithCaller(ctx, types.Caller{TenantID: 1, Role: types.TenantRoleViewer})
	platform := context.WithValue(viewer, types.SystemAdminContextKey, true)
	for _, tc := range []struct {
		name, query string
		ctx         context.Context
		status      int
		rows        int
	}{
		{"workspace scope", "", admin, http.StatusOK, 1},
		{"cross workspace denied", "&tenant_id=2", admin, http.StatusForbidden, 0},
		{"viewer denied", "", viewer, http.StatusForbidden, 0},
		{"platform scope", "", platform, http.StatusOK, 2},
		{"model filter", "&model_id=other", admin, http.StatusOK, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet,
				"/usage?from=2026-10-01&to=2026-11-01&interval=month"+tc.query, nil).WithContext(tc.ctx)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status != http.StatusOK {
				return
			}
			var result struct {
				Data []repository.ModelTokenUsageSummary `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
			require.Len(t, result.Data, tc.rows)
			if tc.rows != 0 {
				row := result.Data[0]
				require.Equal(t, "2026-10", row.Period)
				require.Equal(t, uint64(1), row.TenantID)
				require.Equal(t, int64(15), row.TotalTokens)
				require.Equal(t, int64(2), row.Calls)
				require.Equal(t, int64(1), row.UnknownCalls)
				require.Equal(t, int64(1), row.IncompleteCalls)
			}
		})
	}
}
