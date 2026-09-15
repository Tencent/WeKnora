package memory

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// newEpisodeSearchHarness gives the service an embedding model, because
// reaching an account by what a question means rather than by which characters
// it shares is the whole reason search exists.
func newEpisodeSearchHarness(
	t *testing.T, vectors map[string][]float32,
) (*Service, *stubTenantRepo) {
	t.Helper()
	svc, tenantRepo, models := newVectorHarness(t)
	models.embedder = &stubEmbedder{vectors: vectors}
	return svc, tenantRepo
}

// The reason search exists at all: recall is ranked once, against the question
// the user opened with. An agent that works its way from that question to a
// different sub-problem is holding memories chosen for a query it has left
// behind, and nothing in the turn's budget can fix that.
func TestSearchFindsWhatTheOpeningQuestionDidNotMatch(t *testing.T) {
	svc, tenantRepo := newEpisodeSearchHarness(t, map[string][]float32{
		"连接池":  {1, 0, 0},
		"数据库":  {1, 0, 0},
		"部署":   {0, 1, 0},
		"蓝绿发布": {0, 1, 0},
	})
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-db", Slug: "postgres-pool", Title: "生产数据库连接池",
		Summary:  "用户在调生产库的连接池，最后定在 200。",
		Keywords: types.MemoryEpisodeTokens{"数据库", "连接池"},
		ToAt:     time.Now().Add(-48 * time.Hour),
	})
	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-deploy", Slug: "blue-green-deploy", Title: "部署走蓝绿发布",
		Summary:  "用户的服务用蓝绿发布上线，切流前要跑一轮回归。",
		Keywords: types.MemoryEpisodeTokens{"部署", "蓝绿发布"},
		ToAt:     time.Now().Add(-24 * time.Hour),
	})

	// The turn opened with a database question, so that is what recall matched
	// against and the deployment conversation is nowhere in the prompt.
	recall := svc.Recall(ctx, "帮我看看数据库连接池的配置")
	require.Contains(t, recall.Prompt, "生产数据库连接池")
	require.NotContains(t, recall.Prompt, "蓝绿发布")

	// Several iterations later the agent is looking at deployment instead.
	result := svc.SearchMemory(ctx, "部署方式", 5)
	require.True(t, result.Available)
	require.Len(t, result.Episodes, 1)
	require.Equal(t, "部署走蓝绿发布", result.Episodes[0].Title)
}

// The other half of the gap: recall admits a couple of excerpts no matter how
// many conversations matched, because it is paid for on every turn. A search is
// paid for only when the model asked for it, so it can afford to answer
// properly — and it answers with whole accounts rather than excerpts.
func TestSearchReachesPastTheTurnBudget(t *testing.T) {
	svc, tenantRepo := newEpisodeSearchHarness(t, map[string][]float32{"网关": {1, 0, 0}})
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	for i := 0; i < 5; i++ {
		seedEpisode(t, svc, ctx, &types.MemoryEpisode{
			SessionID: fmt.Sprintf("s-%d", i),
			Slug:      fmt.Sprintf("gateway-config-%d", i),
			Title:     fmt.Sprintf("网关配置第 %d 项", i),
			Summary:   fmt.Sprintf("用户调了网关配置的第 %d 项。", i),
			Keywords:  types.MemoryEpisodeTokens{"网关配置"},
			ToAt:      time.Now().Add(-time.Duration(i+1) * time.Hour),
		})
	}

	recall := svc.Recall(ctx, "网关配置")
	require.Len(t, recall.Episodes, types.MemoryRecallMaxExcerpts)

	result := svc.SearchMemory(ctx, "网关配置", 5)
	require.True(t, result.Available)
	require.Len(t, result.Episodes, 5)
	require.NotEmpty(t, result.Episodes[0].Summary,
		"a tool call is the model asking, so it gets the account rather than a sentence of it")
}

// "Switched off" and "nothing stored" have to stay distinguishable all the way
// out to the caller. Collapsing them would have the agent tell someone who
// disabled memory that it remembers nothing about them.
func TestSearchTellsDisabledApartFromEmpty(t *testing.T) {
	svc, tenantRepo := newEpisodeSearchHarness(t, map[string][]float32{"数据库": {1, 0, 0}})
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-db", Slug: "postgres-pool", Title: "生产数据库连接池",
		Summary:  "用户在调生产库的连接池。",
		Keywords: types.MemoryEpisodeTokens{"数据库"},
		ToAt:     time.Now().Add(-time.Hour),
	})

	empty := svc.SearchMemory(ctx, "完全无关的题目", 5)
	require.True(t, empty.Available, "memory is on, this user simply has no match")
	require.Empty(t, empty.Episodes)

	disabled := false
	off := svc.SearchMemory(types.ApplyAgentMemoryPreference(ctx, &disabled), "数据库", 5)
	require.False(t, off.Available, "an agent opting out must not be able to search either")
	require.Empty(t, off.Episodes)
}

// MemoryAvailable is what lets a caller decide not to offer a memory feature
// at all. It has to track every switch the read path honours, or the agent
// would keep being handed a tool that can only report that memory is off.
func TestMemoryAvailableTracksAllThreeSwitches(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	require.True(t, svc.MemoryAvailable(ctx))

	// The user's own toggle in 我的记忆.
	require.NoError(t, svc.SetEnabled(ctx, false))
	require.False(t, svc.MemoryAvailable(ctx), "the user opted out")
	require.NoError(t, svc.SetEnabled(ctx, true))
	require.True(t, svc.MemoryAvailable(ctx))

	// The agent handling this request.
	disabled := false
	require.False(t, svc.MemoryAvailable(types.ApplyAgentMemoryPreference(ctx, &disabled)))

	// The workspace setting.
	tenantRepo.set(1, &types.MemoryConfig{Enabled: false})
	require.False(t, svc.MemoryAvailable(ctx), "the workspace switched memory off")
}

// The predicate and the search must never disagree: anything that reports
// available has to be searchable, and anything unavailable has to say so
// rather than come back looking like an empty store.
func TestMemoryAvailableAgreesWithSearch(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "session-1", Slug: "database-choice", Title: "选数据库",
		Summary:  "用户决定生产数据库用 PostgreSQL。",
		Keywords: types.MemoryEpisodeTokens{"数据库"},
		ToAt:     time.Now(),
	})

	require.Equal(t, svc.MemoryAvailable(ctx), svc.SearchMemory(ctx, "数据库", 10).Available)

	require.NoError(t, svc.SetEnabled(ctx, false))
	require.Equal(t, svc.MemoryAvailable(ctx), svc.SearchMemory(ctx, "数据库", 10).Available)
	require.False(t, svc.SearchMemory(ctx, "数据库", 10).Available)
}

func TestSearchWithoutPrincipalIsUnavailable(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	tenantRepo.set(1, &types.MemoryConfig{Enabled: true})
	ctx := t.Context()

	result := svc.SearchMemory(ctx, "数据库", 10)
	require.False(t, result.Available,
		"a request with no principal has no memory space to search")
}

func TestSearchClampsAnAbsurdLimit(t *testing.T) {
	svc, tenantRepo := newEpisodeSearchHarness(t, map[string][]float32{"网关": {1, 0, 0}})
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	for i := 0; i < types.MemorySearchMaxEpisodes+10; i++ {
		seedEpisode(t, svc, ctx, &types.MemoryEpisode{
			SessionID: fmt.Sprintf("s-%d", i),
			Slug:      fmt.Sprintf("gateway-config-%d", i),
			Title:     fmt.Sprintf("网关配置第 %d 项", i),
			Summary:   fmt.Sprintf("用户调了网关配置的第 %d 项。", i),
			Keywords:  types.MemoryEpisodeTokens{"网关配置"},
			ToAt:      time.Now().Add(-time.Duration(i+1) * time.Hour),
		})
	}

	result := svc.SearchMemory(ctx, "网关配置", 10_000)
	require.True(t, result.Available)
	require.LessOrEqual(t, len(result.Episodes), types.MemorySearchMaxEpisodes)
}
