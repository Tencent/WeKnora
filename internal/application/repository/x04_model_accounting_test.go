package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestX04TerminalConflictAndCachePricePersistence(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:x04-terminal?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()
	require.NoError(t, db.AutoMigrate(&types.ModelCallRecord{}, &types.ModelPriceVersion{}))
	r := NewModelObservabilityRepository(db)
	now := time.Now().UTC()
	require.NoError(
		t,
		r.StartModelCall(
			context.Background(),
			&types.ModelCallRecord{
				ID:       "x04-call",
				TenantID: 7,
				ModelID:  "x04-model",
				ModelSnapshot: types.JSON(
					`{}`,
				),
				Purpose:   "evaluation",
				Operation: "chat",
				StartedAt: now,
				Status:    "started",
			},
		),
	)
	completion := types.ModelCallCompletion{
		ModelSnapshot: types.JSON("{\"billing_usage\":{\"prompt_tokens\":0,\"cache_write_5m_tokens\":0," +
			"\"cache_write_1h_tokens\":0,\"reported_cost\":\"0.00001721\",\"cost_source\":\"openrouter_usage_cost\"," +
			"\"price_estimate_microunits\":18}}"),
		ID: "x04-call", EndedAt: now.Add(time.Second), DurationMs: 1000, Status: "success",
	}
	require.NoError(t, r.CompleteModelCall(context.Background(), completion))
	require.NoError(t, r.CompleteModelCall(context.Background(), completion))
	changed := completion
	changed.ModelSnapshot = types.JSON(`{"id":"forged","billing_usage":{}}`)
	require.ErrorContains(t, r.CompleteModelCall(context.Background(), changed), "frozen identity")
	completion.Status = "error"
	require.ErrorContains(t, r.CompleteModelCall(context.Background(), completion), "conflict")
	zero := int64(0)
	price := &types.ModelPriceVersion{
		TenantID:  7,
		ModelID:   "x04-model",
		ValidFrom: now,
		Currency:  "CNY",
		CachePricing: &types.ModelCachePricing{
			Version:                  1,
			ReadMicrounitsPerMillion: &zero,
		},
	}
	require.NoError(t, r.CreateModelPrice(context.Background(), price))
	stored, err := r.EffectiveModelPrice(context.Background(), 7, "x04-model", now)
	require.NoError(t, err)
	require.NotNil(t, stored.CachePricing)
	require.NotNil(t, stored.CachePricing.ReadMicrounitsPerMillion)
	require.Zero(t, *stored.CachePricing.ReadMicrounitsPerMillion)
	require.Nil(t, stored.CachePricing.Write5mMicrounitsPerMillion)
}
