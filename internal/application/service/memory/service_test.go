package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newMemoryHarness builds a service over a real SQLite database. The write
// path is mostly about what ends up in the database after a conflict, so a
// mocked repository would assert the wrong thing.
func newMemoryHarness(t *testing.T) (*Service, *gorm.DB, *stubTenantRepo) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{Logger: logger.Discard},
	)
	require.NoError(t, err)
	// Recall records usage on a background goroutine. A shared-cache SQLite
	// file rejects a concurrent writer, so pin the pool to one connection and
	// let the driver serialize instead of failing the next read.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.MemorySubject{}, &types.MemoryExtractionSession{},
		&types.MemoryEpisode{}, &types.MemoryEpisodeEmbedding{},
		&types.MemoryDigest{}, &types.MemoryNote{}))

	tenantRepo := &stubTenantRepo{
		configs: map[uint64]*types.MemoryConfig{},
	}
	svc := &Service{
		repo:       repository.NewMemoryRepository(db),
		tenantRepo: tenantRepo,
	}
	return svc, db, tenantRepo
}

// enabledCtx returns a request context for one principal in one workspace,
// with memory switched on for that workspace.
func enabledCtx(t *testing.T, tenantRepo *stubTenantRepo, tenantID uint64, userID string) context.Context {
	t.Helper()
	tenantRepo.set(tenantID, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, EmbeddingModelID: "embed-1",
	})
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, tenantID)
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: userID})
}

// seedEpisode files one account directly, for tests about what the read path
// does with an account rather than about how one comes to be written.
func seedEpisode(
	t *testing.T, svc *Service, ctx context.Context, episode *types.MemoryEpisode,
) *types.MemoryEpisode {
	t.Helper()
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, svc.repo.SaveEpisode(ctx, scope, episode))
	// An account with no vector is reachable only by keyword, so seeding goes
	// through the same embedding step the write path does. A harness with no
	// embedding model gets nothing, which is the deployment that falls back to
	// keyword matching anyway.
	svc.storeEpisodeEmbedding(ctx, scope, svc.workspaceConfig(ctx, scope.TenantID), episode)
	return episode
}

// seedDigest installs a consolidated profile without spending a model call on
// producing one.
func seedDigest(t *testing.T, svc *Service, ctx context.Context, body string) {
	t.Helper()
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	lease, err := svc.repo.AcquireDigestLease(ctx, scope, time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, lease)
	_, err = svc.repo.SaveDigest(ctx, scope, lease, body)
	require.NoError(t, err)
}

// What the user asked to have remembered is theirs, so it rides in every turn
// in their own words rather than waiting for a consolidation to reword it.
func TestAVerbatimNoteIsCarriedIntoTheNextTurn(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	require.NoError(t, svc.RememberVerbatim(ctx, "回答请直接给结论，不要铺垫", "session-1", "m1"))

	recall := svc.Recall(ctx, "帮我看看这个报错")
	require.Contains(t, recall.Prompt, "回答请直接给结论")
	require.Len(t, recall.Notes, 1)
	require.Len(t, recall.Used, 1, "the chat UI has to be able to show what memory contributed")
}

// The profile is the layer that rides in every turn, so it is present whatever
// the question happens to share words with.
func TestTheProfileIsInjectedRegardlessOfTheQuestion(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	seedDigest(t, svc, ctx, "## 用户画像\n- 在做医学影像的后端\n")

	require.Contains(t, svc.Recall(ctx, "帮我设计一个接口").Prompt, "医学影像")
	require.Contains(t, svc.Recall(ctx, "今天天气怎么样").Prompt, "医学影像")
}

// An account is detail behind the profile, so unlike the profile it only comes
// along when the question is about it. Volunteering an unrelated conversation
// on every turn is the behaviour that makes people switch memory off.
func TestAPastConversationOnlyComesAlongWhenItIsRelevant(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "session-1", Slug: "knowledge-import-413", Title: "知识库批量入库报 413",
		Summary:  "用户批量导入时反复报 413，最后把单批大小调到 20 才通过。",
		Keywords: types.MemoryEpisodeTokens{"入库", "413"},
		ToAt:     time.Now().Add(-24 * time.Hour),
	})

	require.Contains(t, svc.Recall(ctx, "入库又报 413 了").Prompt, "知识库批量入库报 413")
	require.Empty(t, svc.Recall(ctx, "帮我算一下这个月的账").Prompt,
		"an unrelated conversation must not be volunteered")
}

func TestMemoriesAreIsolatedAcrossSubjectsAndWorkspaces(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)

	aliceInOne := enabledCtx(t, tenantRepo, 1, "alice")
	require.NoError(t, svc.RememberVerbatim(aliceInOne, "记住我在做医疗影像项目", "s1", "m1"))

	// Same workspace, different person.
	bobInOne := enabledCtx(t, tenantRepo, 1, "bob")
	require.Empty(t, svc.Recall(bobInOne, "我在做什么项目").Prompt,
		"another user in the same workspace must not see the memory")

	// Same person, different workspace: the agreed scope is (workspace,
	// principal), so work memories do not follow someone across workspaces.
	aliceInTwo := enabledCtx(t, tenantRepo, 2, "alice")
	require.Empty(t, svc.Recall(aliceInTwo, "我在做什么项目").Prompt,
		"the same user in another workspace must not see the memory")

	require.Contains(t, svc.Recall(aliceInOne, "我在做什么项目").Prompt, "医疗影像")
}

func TestWorkspaceSwitchOffDisablesReadAndWrite(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	require.NoError(t, svc.RememberVerbatim(ctx, "记住的东西", "s1", "m1"))

	tenantRepo.set(1, &types.MemoryConfig{Enabled: false})
	require.Empty(t, svc.Recall(ctx, "记住的东西").Prompt)
	_, err := svc.AddNote(ctx, "新的东西")
	require.ErrorIs(t, err, ErrMemoryDisabled)
}

func TestUserSwitchOffDisablesReadAndWrite(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	require.NoError(t, svc.RememberVerbatim(ctx, "记住的东西", "s1", "m1"))

	require.NoError(t, svc.SetEnabled(ctx, false))
	require.Empty(t, svc.Recall(ctx, "记住的东西").Prompt)
	_, err := svc.AddNote(ctx, "新的东西")
	require.ErrorIs(t, err, ErrMemoryDisabled)

	// Turning it back on restores what was stored: an opt out pauses memory,
	// it does not erase it.
	require.NoError(t, svc.SetEnabled(ctx, true))
	require.Contains(t, svc.Recall(ctx, "记住的东西").Prompt, "记住的东西")
}

func TestAgentOptOutDisablesRecall(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	require.NoError(t, svc.RememberVerbatim(ctx, "只要中文回答", "s1", "m1"))

	disabled := false
	agentCtx := types.ApplyAgentMemoryPreference(ctx, &disabled)
	require.Empty(t, svc.Recall(agentCtx, "帮我写个函数").Prompt)
	require.Contains(t, svc.Recall(ctx, "帮我写个函数").Prompt, "只要中文回答")
}

func TestRecallWithoutPrincipalIsEmpty(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	tenantRepo.set(1, &types.MemoryConfig{Enabled: true})
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	require.Empty(t, svc.Recall(ctx, "任何问题").Prompt,
		"a request with no principal has no memory space to read")
}

func TestGetSettingsReportsMergedState(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "session-1", Slug: "one-account", Title: "一段对话",
		Summary: "用户问了一个问题，得到了答案。", ToAt: time.Now(),
	})

	settings, err := svc.GetSettings(ctx)
	require.NoError(t, err)
	require.True(t, settings.WorkspaceEnabled)
	require.True(t, settings.UserEnabled)
	require.True(t, settings.Effective)
	require.Equal(t, 1, settings.EpisodeCount,
		"the settings screen reports how much history is stored, so it has to match the store")
	require.Equal(t, types.DefaultMemoryMaxEpisodes, settings.MaxEpisodes)

	require.NoError(t, svc.SetEnabled(ctx, false))
	settings, err = svc.GetSettings(ctx)
	require.NoError(t, err)
	require.True(t, settings.WorkspaceEnabled)
	require.False(t, settings.UserEnabled)
	require.False(t, settings.Effective, "either switch being off must make the effective state off")
}

// A turn can carry several kinds of memory at once, and the chat UI has to be
// able to say which is which: a consolidated conclusion about the person and a
// sentence they typed themselves are not claims of the same standing.
func TestRecallReportsEveryKindItInjected(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	seedDigest(t, svc, ctx, "## 用户画像\n- 在做医学影像的后端\n")
	require.NoError(t, svc.RememberVerbatim(ctx, "回答只用中文", "s1", "m1"))
	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "session-1", Slug: "imaging-segmentation", Title: "分割模型调参",
		Summary:  "用户在调分割模型的参数，最后把 batch 调小才收敛。",
		Keywords: types.MemoryEpisodeTokens{"分割模型"},
		ToAt:     time.Now().Add(-time.Hour),
	})

	recall := svc.Recall(ctx, "分割模型怎么调参")
	require.Len(t, recall.Notes, 1)
	require.Len(t, recall.Episodes, 1)

	kinds := make([]string, 0, len(recall.Used))
	for _, used := range recall.Used {
		kinds = append(kinds, used.Kind)
	}
	require.ElementsMatch(t, []string{
		types.UsedMemoryKindDigest, types.UsedMemoryKindNote, types.UsedMemoryKindEpisode,
	}, kinds, "every contribution has to be reportable, not just the last one")
}
