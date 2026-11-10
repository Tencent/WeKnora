package memory

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func trackingConfig(threshold int) *types.MemoryConfig {
	return &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, InterestThreshold: threshold,
	}
}

func TestListTopicsShowsUnpromotedSubjects(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, trackingConfig(3))

	require.Empty(t, svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"}))
	require.Empty(t, svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"}))

	topics, total, err := svc.ListTopics(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, topics, 1)
	require.Equal(t, "门店排班管理", topics[0].Topic)
	require.Equal(t, 2, topics[0].Hits)
	require.Equal(t, 3, topics[0].Threshold)
	require.NotEmpty(t, topics[0].ID)

	require.Equal(t, []string{"门店排班管理"}, svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"}))

	topics, total, err = svc.ListTopics(ctx, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total, "a promoted subject is already a memory and must leave this list")
	require.Empty(t, topics)
}

func TestListTopicsDoesNotLeakAcrossPeople(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	alice := enabledCtx(t, tenantRepo, 1, "alice")
	bob := enabledCtx(t, tenantRepo, 1, "bob")
	tenantRepo.set(1, trackingConfig(3))

	svc.ObserveQuestionTopics(alice, []string{"医学影像分割"})
	topics, total, err := svc.ListTopics(bob, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, topics)
}

func TestPromoteTopicCreatesAnInterestImmediately(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, trackingConfig(5))

	require.Empty(t, svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"}))
	topics, _, err := svc.ListTopics(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, topics, 1)

	item, err := svc.PromoteTopic(ctx, topics[0].ID)
	require.NoError(t, err)
	require.Equal(t, types.MemoryKindInterest, item.Kind)
	require.Equal(t, types.MemoryOriginManual, item.Origin)
	require.Equal(t, "门店排班管理", item.Content)

	left, total, err := svc.ListTopics(ctx, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, left)

	items, _, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, item.ID, items[0].ID)

	_, err = svc.PromoteTopic(ctx, topics[0].ID)
	require.ErrorIs(t, err, ErrItemNotFound, "promoting twice must not create a second interest")
}

func TestDeleteTopicStopsAutomaticPromotion(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, trackingConfig(3))

	svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"})
	svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"})
	topics, _, err := svc.ListTopics(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, topics, 1)

	require.NoError(t, svc.DeleteTopic(ctx, topics[0].ID))
	left, total, err := svc.ListTopics(ctx, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, left)

	for i := 0; i < 3; i++ {
		svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"})
	}
	items, itemTotal, err := svc.ListItems(ctx, "", 10, 0)
	require.NoError(t, err)
	require.Zero(t, itemTotal)
	require.Empty(t, items, "dismissing a subject has to be remembered, or it promotes itself again")

	reappeared, total, err := svc.ListTopics(ctx, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, reappeared, "a dismissed subject must not reappear as a counter the user already rejected")
}

func TestClearDropsUnpromotedTopics(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, trackingConfig(3))

	svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"})
	svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"})
	topics, total, err := svc.ListTopics(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, topics, 1)

	_, err = svc.Clear(ctx)
	require.NoError(t, err)

	left, total, err := svc.ListTopics(ctx, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total, "clearing memory must also drop subjects that were still being counted")
	require.Empty(t, left)
}
