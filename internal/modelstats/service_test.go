package modelstats

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type serviceStore struct {
	query types.ModelUsageQuery
	price *types.ModelPriceVersion
}

func (s *serviceStore) QueryModelUsage(
	_ context.Context,
	query types.ModelUsageQuery,
) ([]types.ModelUsageStatistics, error) {
	s.query = query
	return []types.ModelUsageStatistics{{ModelID: "b"}, {ModelID: "a"}}, nil
}

func (s *serviceStore) CreateModelPrice(_ context.Context, price *types.ModelPriceVersion) error {
	s.price = price
	return nil
}

func (*serviceStore) ListModelPrices(context.Context, uint64, string) ([]*types.ModelPriceVersion, error) {
	return []*types.ModelPriceVersion{}, nil
}

func TestUsageNormalizesIDsAndWindow(t *testing.T) {
	store := &serviceStore{}
	service := NewService(store)
	service.now = func() time.Time { return time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC) }
	response, err := service.Usage(context.Background(), 7, []string{" b ", "a", "b"}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, store.query.ModelIDs)
	require.Equal(t, "a", response.Items[0].ModelID)
	require.Equal(t, 30*24*time.Hour, response.To.Sub(response.From))
}

func TestUsageRejectsOversizedWindow(t *testing.T) {
	service := NewService(&serviceStore{})
	from := time.Now().Add(-367 * 24 * time.Hour)
	to := time.Now()
	_, err := service.Usage(context.Background(), 7, nil, &from, &to)
	require.ErrorContains(t, err, "366 days")
}

func TestPutPriceBindsTenantAndModel(t *testing.T) {
	store := &serviceStore{}
	price := &types.ModelPriceVersion{
		ID: "client-id", ModelID: "other", TenantID: 9,
		ValidFrom: time.Now(), Currency: "USD",
	}
	require.NoError(t, NewService(store).PutPrice(context.Background(), 7, "model-1", price))
	require.Equal(t, uint64(7), store.price.TenantID)
	require.Equal(t, "model-1", store.price.ModelID)
	require.Empty(t, store.price.ID)
}
