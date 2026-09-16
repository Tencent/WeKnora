package repository

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestModelPriceOverlapIsSerializedAcrossPostgresConnections(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ModelPriceVersion{}))

	tenantID := uint64(time.Now().UnixNano())
	modelID := "price-concurrency-" + uuid.NewString()
	t.Cleanup(func() {
		_ = db.Where("tenant_id = ? AND model_id = ?", tenantID, modelID).
			Delete(&types.ModelPriceVersion{}).Error
	})

	const writers = 8
	start := make(chan struct{})
	errorsByWriter := make(chan error, writers)
	var wait sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			validFrom := time.Now().UTC().Add(time.Duration(index) * time.Millisecond)
			validTo := validFrom.Add(24 * time.Hour)
			errorsByWriter <- NewModelObservabilityRepository(db).CreateModelPrice(
				context.Background(),
				&types.ModelPriceVersion{
					ID: uuid.NewString(), TenantID: tenantID, ModelID: modelID,
					ValidFrom: validFrom, ValidTo: &validTo, Currency: "USD",
				},
			)
		}(writer)
	}
	close(start)
	wait.Wait()
	close(errorsByWriter)

	successes := 0
	for createErr := range errorsByWriter {
		if createErr == nil {
			successes++
			continue
		}
		require.True(t, strings.Contains(createErr.Error(), "overlaps"), fmt.Sprintf("unexpected error: %v", createErr))
	}
	require.Equal(t, 1, successes)
	var count int64
	require.NoError(t, db.Model(&types.ModelPriceVersion{}).
		Where("tenant_id = ? AND model_id = ?", tenantID, modelID).Count(&count).Error)
	require.Equal(t, int64(1), count)
}
