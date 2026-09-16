package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestModelObservabilityRepositoryPersistsOneTerminalCompletion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:model-observability?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ModelCallRecord{}, &types.ModelPriceVersion{}))
	repository := NewModelObservabilityRepository(db)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	record := &types.ModelCallRecord{
		ID: "call-1", TenantID: 7, ModelID: "model-1", ModelSnapshot: types.JSON(`{"id":"model-1"}`),
		Purpose: "evaluation", Operation: "chat", StartedAt: now, Status: types.ModelCallStatusStarted,
		ProviderCacheStatus: "unreported", ApplicationCacheStatus: "unavailable", CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repository.StartModelCall(context.Background(), record))
	cost := int64(12)
	completion := types.ModelCallCompletion{
		ID: "call-1", EndedAt: now.Add(time.Second), DurationMs: 1000,
		Status: types.ModelCallStatusSuccess, ProviderCacheStatus: "miss",
		ApplicationCacheStatus: "unavailable", CostMicrounits: &cost, AccountingComplete: true,
	}
	require.NoError(t, repository.CompleteModelCall(context.Background(), completion))
	require.NoError(t, repository.CompleteModelCall(context.Background(), completion))

	var stored types.ModelCallRecord
	require.NoError(t, db.First(&stored, "id = ?", "call-1").Error)
	assert.Equal(t, types.ModelCallStatusSuccess, stored.Status)
	assert.Equal(t, int64(1000), *stored.DurationMs)
	assert.Equal(t, int64(12), *stored.CostMicrounits)
	assert.True(t, stored.AccountingComplete)
}

func TestModelObservabilityRepositoryResolvesEffectivePrice(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:model-price?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ModelCallRecord{}, &types.ModelPriceVersion{}))
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&types.ModelPriceVersion{
		ID: "price-old", TenantID: 7, ModelID: "model-1", ValidFrom: now.Add(-2 * time.Hour),
		InputMicrounitsPerMillion: 1, OutputMicrounitsPerMillion: 2, Currency: "USD", CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&types.ModelPriceVersion{
		ID: "price-current", TenantID: 7, ModelID: "model-1", ValidFrom: now.Add(-time.Hour),
		InputMicrounitsPerMillion: 3, OutputMicrounitsPerMillion: 4, Currency: "USD", CreatedAt: now,
	}).Error)

	price, err := NewModelObservabilityRepository(db).EffectiveModelPrice(context.Background(), 7, "model-1", now)
	require.NoError(t, err)
	require.NotNil(t, price)
	assert.Equal(t, "price-current", price.ID)
}

func TestModelObservabilityRepositoryRejectsOverlappingPriceVersions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:model-price-overlap?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ModelCallRecord{}, &types.ModelPriceVersion{}))
	repository := NewModelObservabilityRepository(db)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	firstEnd := now.Add(time.Hour)
	require.NoError(t, repository.CreateModelPrice(context.Background(), &types.ModelPriceVersion{
		TenantID: 7, ModelID: "model-1", ValidFrom: now, ValidTo: &firstEnd,
		InputMicrounitsPerMillion: 1, OutputMicrounitsPerMillion: 2, Currency: "usd",
	}))
	err = repository.CreateModelPrice(context.Background(), &types.ModelPriceVersion{
		TenantID: 7, ModelID: "model-1", ValidFrom: now.Add(30 * time.Minute),
		InputMicrounitsPerMillion: 3, OutputMicrounitsPerMillion: 4, Currency: "USD",
	})
	require.ErrorContains(t, err, "overlaps")
	prices, err := repository.ListModelPrices(context.Background(), 7, "model-1")
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.Equal(t, "USD", prices[0].Currency)
	modelPriceScopeLocks.Lock()
	defer modelPriceScopeLocks.Unlock()
	assert.Empty(t, modelPriceScopeLocks.values)
}

func TestModelObservabilityRepositoryRequiresASCIICurrencyCode(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:model-price-currency?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ModelPriceVersion{}))
	err = NewModelObservabilityRepository(db).CreateModelPrice(context.Background(), &types.ModelPriceVersion{
		TenantID: 7, ModelID: "model-1", ValidFrom: time.Now(), Currency: "12$",
	})
	require.ErrorContains(t, err, "three letters")
}
