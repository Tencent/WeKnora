package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"

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
	require.NoError(t, db.AutoMigrate(&types.MemorySubject{}, &types.MemoryItem{}, &types.MemoryTombstone{},
		&types.MemoryTopicStat{}, &types.MemoryDocAffinity{},
		&types.MemoryItemEmbedding{}))

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

func TestRememberStoresAndRecalls(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindPreference, Content: "回答请直接给结论，不要铺垫", Importance: 4,
	})
	require.NoError(t, err)

	recall := svc.Recall(ctx, "帮我看看这个报错")
	require.Contains(t, recall.Prompt, "回答请直接给结论")
	require.Len(t, recall.Items, 1)
}

// TestExplicitMemoryIsAlwaysAvailable pins the rule that a user who said
// "remember this" gets it back regardless of how they phrase the next
// question. Leaving it to lexical matching means the one memory the user
// deliberately asked for is the one most likely to go missing.
func TestExplicitMemoryIsAlwaysAvailable(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind:    types.MemoryKindFact,
		Content: "my editor is Neovim and my terminal is WezTerm",
		Origin:  types.MemoryOriginExplicit,
	})
	require.NoError(t, err)

	// Not one word in common with the memory.
	recall := svc.Recall(ctx, "what tools do I use daily")
	require.Contains(t, recall.Prompt, "Neovim")
	require.Len(t, recall.Items, 1)
}

// TestExtractedFactStillNeedsAQueryMatch is the counterpart: memory the user
// never asked for must stay out of context unless it is relevant, or the block
// grows without bound.
func TestExtractedFactStillNeedsAQueryMatch(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind:    types.MemoryKindFact,
		Topic:   "编辑器",
		Content: "用的编辑器是 Neovim",
		Origin:  types.MemoryOriginExtracted,
	})
	require.NoError(t, err)

	require.Empty(t, svc.Recall(ctx, "帮我算一下这个月的账").Prompt)
	require.Contains(t, svc.Recall(ctx, "编辑器怎么配置").Prompt, "Neovim")
}

// TestRecalledItemIsNotListedTwice guards the seam between the resident block
// and query matching: an explicit fact is in both candidate sets.
func TestRecalledItemIsNotListedTwice(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind:    types.MemoryKindFact,
		Content: "生产数据库是 PostgreSQL 17",
		Origin:  types.MemoryOriginExplicit,
	})
	require.NoError(t, err)

	recall := svc.Recall(ctx, "数据库连接池配多大")
	require.Len(t, recall.Items, 1, "the same memory must not be reported twice")
	require.Equal(t, 1, strings.Count(recall.Prompt, "生产数据库是 PostgreSQL 17"))
}

func TestRecallMatchesSituationalItemsByQuery(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Content: "生产数据库是 PostgreSQL 17，部署在法兰克福",
	})
	require.NoError(t, err)
	_, err = svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Content: "前端用 Vue 3 加 Vite",
	})
	require.NoError(t, err)

	recall := svc.Recall(ctx, "数据库连接超时应该怎么排查")
	require.Contains(t, recall.Prompt, "PostgreSQL 17")
	// An unrelated fact must stay out: injecting it would spend context and
	// invite the model to use it.
	require.NotContains(t, recall.Prompt, "Vue 3")
}

func TestContradictionSupersedesRatherThanDeletes(t *testing.T) {
	svc, db, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	first, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "在用的数据库", Content: "我们用的是 MySQL",
	})
	require.NoError(t, err)
	second, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "在用的数据库", Content: "我们已经迁到 PostgreSQL",
	})
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)

	var old types.MemoryItem
	require.NoError(t, db.First(&old, "id = ?", first.ID).Error)
	require.Equal(t, types.MemoryStatusSuperseded, old.Status,
		"the outdated statement must be superseded, not left active")
	require.Equal(t, second.ID, old.SupersededBy)
	require.NotNil(t, old.InvalidAt, "a superseded item must record when it stopped being true")
	require.Equal(t, "我们用的是 MySQL", old.Content,
		"history must stay readable, so the old content is preserved")

	recall := svc.Recall(ctx, "我们的数据库是什么")
	require.Contains(t, recall.Prompt, "PostgreSQL")
	require.NotContains(t, recall.Prompt, "MySQL")
}

func TestRepeatedIdenticalStatementDoesNotChurn(t *testing.T) {
	svc, db, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	first, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindProfile, Topic: "职位", Content: "后端工程师",
	})
	require.NoError(t, err)
	second, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindProfile, Topic: "职位", Content: "后端工程师",
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID, "an unchanged statement must not create a new row")

	var count int64
	require.NoError(t, db.Model(&types.MemoryItem{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

// TestRestatedFactDoesNotDuplicate covers the shape seen in real use: the user
// says "remember X" and the background distillation later produces the same
// fact with slightly different wording. They carry different topic keys, so
// without a containment check the user's memory list shows the same thing
// twice.
func TestRestatedFactDoesNotDuplicate(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	explicit, err := svc.Remember(ctx, types.MemoryItem{
		Kind:    types.MemoryKindFact,
		Content: "我们的生产数据库是 PostgreSQL 17，部署在法兰克福",
		Origin:  types.MemoryOriginExplicit,
	})
	require.NoError(t, err)

	// The distillation restates it more tersely and under its own topic.
	extracted, err := svc.Remember(ctx, types.MemoryItem{
		Kind:    types.MemoryKindFact,
		Topic:   "生产数据库",
		Content: "生产数据库是 PostgreSQL 17，部署在法兰克福",
		Origin:  types.MemoryOriginExtracted,
	})
	require.NoError(t, err)
	require.Equal(t, explicit.ID, extracted.ID,
		"a restatement contained in an existing memory must not create a second row")

	_, total, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
}

// TestMoreSpecificRestatementSupersedes is the other direction: when the new
// statement contains the old one it carries strictly more information, so it
// replaces it instead of sitting beside it.
func TestMoreSpecificRestatementSupersedes(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	short, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "生产数据库", Content: "生产数据库是 PostgreSQL 17",
	})
	require.NoError(t, err)
	long, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Content: "生产数据库是 PostgreSQL 17，部署在法兰克福",
	})
	require.NoError(t, err)
	require.NotEqual(t, short.ID, long.ID)

	items, total, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, "生产数据库是 PostgreSQL 17，部署在法兰克福", items[0].Content)
}

// TestDifferentFactsSharingWordsAreKept guards the containment rule from being
// too eager: two genuinely different statements must both survive.
func TestDifferentFactsSharingWordsAreKept(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "生产数据库", Content: "生产数据库是 PostgreSQL 17",
	})
	require.NoError(t, err)
	_, err = svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "测试数据库", Content: "测试数据库是 PostgreSQL 15",
	})
	require.NoError(t, err)

	_, total, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
}

func TestMemoriesAreIsolatedAcrossSubjectsAndWorkspaces(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)

	aliceInOne := enabledCtx(t, tenantRepo, 1, "alice")
	_, err := svc.Remember(aliceInOne, types.MemoryItem{
		Kind: types.MemoryKindProfile, Content: "爱丽丝在做医疗影像项目",
	})
	require.NoError(t, err)

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

func TestListItemsIsScopedToTheCaller(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	aliceCtx := enabledCtx(t, tenantRepo, 1, "alice")
	_, err := svc.Remember(aliceCtx, types.MemoryItem{Kind: types.MemoryKindFact, Content: "爱丽丝的秘密"})
	require.NoError(t, err)

	bobCtx := enabledCtx(t, tenantRepo, 1, "bob")
	items, total, err := svc.ListItems(bobCtx, "", 50, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, items)
}

func TestDeleteAnotherUsersMemoryIsNotFound(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	aliceCtx := enabledCtx(t, tenantRepo, 1, "alice")
	item, err := svc.Remember(aliceCtx, types.MemoryItem{Kind: types.MemoryKindFact, Content: "爱丽丝的秘密"})
	require.NoError(t, err)

	bobCtx := enabledCtx(t, tenantRepo, 1, "bob")
	require.ErrorIs(t, svc.DeleteItem(bobCtx, item.ID), ErrItemNotFound)

	// And the item survives the attempt.
	require.Contains(t, svc.Recall(aliceCtx, "爱丽丝的秘密").Prompt, "爱丽丝的秘密")
}

func TestWorkspaceSwitchOffDisablesReadAndWrite(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	_, err := svc.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindFact, Content: "记住的东西"})
	require.NoError(t, err)

	tenantRepo.set(1, &types.MemoryConfig{Enabled: false})
	require.Empty(t, svc.Recall(ctx, "记住的东西").Prompt)
	_, err = svc.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindFact, Content: "新的东西"})
	require.ErrorIs(t, err, ErrMemoryDisabled)
}

func TestUserSwitchOffDisablesReadAndWrite(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	_, err := svc.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindFact, Content: "记住的东西"})
	require.NoError(t, err)

	require.NoError(t, svc.SetEnabled(ctx, false))
	require.Empty(t, svc.Recall(ctx, "记住的东西").Prompt)
	_, err = svc.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindFact, Content: "新的东西"})
	require.ErrorIs(t, err, ErrMemoryDisabled)

	// Turning it back on restores what was stored: an opt out pauses memory,
	// it does not erase it.
	require.NoError(t, svc.SetEnabled(ctx, true))
	require.Contains(t, svc.Recall(ctx, "记住的东西").Prompt, "记住的东西")
}

func TestAgentOptOutDisablesRecall(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	_, err := svc.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindPreference, Content: "只要中文回答"})
	require.NoError(t, err)

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

func TestCapacityCapArchivesLowestRanked(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{Enabled: true, WriteMode: types.MemoryWriteAuto, MaxItems: 3})

	// The important one is written first so recency alone would evict it.
	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "关键事实", Content: "最重要的事实", Importance: 5,
	})
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_, err := svc.Remember(ctx, types.MemoryItem{
			Kind:       types.MemoryKindFact,
			Topic:      fmt.Sprintf("次要事实-%d", i),
			Content:    fmt.Sprintf("次要事实 %d", i),
			Importance: 1,
		})
		require.NoError(t, err)
	}

	active, total, err := svc.ListItems(ctx, types.MemoryStatusActive, 50, 0)
	require.NoError(t, err)
	require.Equal(t, int64(3), total, "the cap must be enforced")

	var kept []string
	for _, item := range active {
		kept = append(kept, item.Content)
	}
	require.Contains(t, kept, "最重要的事实", "importance must outrank recency")

	// Overflow is archived, not deleted, so it stays visible in the manager.
	_, archivedTotal, err := svc.ListItems(ctx, types.MemoryStatusArchived, 50, 0)
	require.NoError(t, err)
	require.Equal(t, int64(3), archivedTotal)
}

func TestClearForgetsEverything(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	for i := 0; i < 3; i++ {
		_, err := svc.Remember(ctx, types.MemoryItem{
			Kind: types.MemoryKindFact, Topic: fmt.Sprintf("k%d", i), Content: fmt.Sprintf("事实 %d", i),
		})
		require.NoError(t, err)
	}
	removed, err := svc.Clear(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(3), removed)

	require.Empty(t, svc.Recall(ctx, "事实").Prompt)
	settings, err := svc.GetSettings(ctx)
	require.NoError(t, err)
	require.Zero(t, settings.ItemCount)

	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, InterestThreshold: 3,
	})
	svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"})
	_, err = svc.Clear(ctx)
	require.NoError(t, err)
	topics, total, err := svc.ListTopics(ctx, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, topics)
}

func TestGetSettingsReportsMergedState(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	_, err := svc.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindFact, Content: "一条记忆"})
	require.NoError(t, err)

	settings, err := svc.GetSettings(ctx)
	require.NoError(t, err)
	require.True(t, settings.WorkspaceEnabled)
	require.True(t, settings.UserEnabled)
	require.True(t, settings.Effective)
	require.Equal(t, 1, settings.ItemCount)

	require.NoError(t, svc.SetEnabled(ctx, false))
	settings, err = svc.GetSettings(ctx)
	require.NoError(t, err)
	require.True(t, settings.WorkspaceEnabled)
	require.False(t, settings.UserEnabled)
	require.False(t, settings.Effective, "either switch being off must make the effective state off")
}

func TestUpdateItemMarksMemoryAsManual(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	item, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindPreference, Content: "喜欢很长的解释",
	})
	require.NoError(t, err)

	updated, err := svc.UpdateItem(ctx, item.ID, "喜欢简短的解释", 5)
	require.NoError(t, err)
	require.Equal(t, "喜欢简短的解释", updated.Content)
	require.Equal(t, 5, updated.Importance)
	require.Equal(t, types.MemoryOriginManual, updated.Origin,
		"a corrected memory must be marked manual so extraction does not undo it")

	require.Contains(t, svc.Recall(ctx, "随便问点什么").Prompt, "喜欢简短的解释")
}

func TestResidentBlockSurvivesCacheLoss(t *testing.T) {
	svc, db, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	_, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindProfile, Content: "在做医疗影像",
	})
	require.NoError(t, err)

	// Simulate a row written before a block-rebuild failure.
	require.NoError(t, db.Model(&types.MemorySubject{}).
		Where("1 = 1").Update("block_text", "").Error)

	require.Contains(t, svc.Recall(ctx, "随便问").Prompt, "在做医疗影像",
		"an empty block cache must not silently drop the user's memories")
}

// An interest is a standing property of the person, so it has to be present
// whatever they ask. The case that forced this: "what am I focused on" shares
// no words with "小微SDK设备接入", so query matching can never reach it, and
// the assistant claimed to know nothing about a memory the user could see
// listed in the memory manager.
func TestInterestIsPresentRegardlessOfTheQuestion(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	_, err := svc.CreateItem(ctx, types.MemoryKindInterest, "小微SDK设备接入", 3)
	require.NoError(t, err)

	require.Contains(t, svc.Recall(ctx, "我关注哪些事情？").Prompt, "小微SDK设备接入")
	require.Contains(t, svc.Recall(ctx, "我关注哪些事情？").Prompt, "Long-term focus")
	require.Contains(t, svc.Recall(ctx, "今天天气怎么样").Prompt, "小微SDK设备接入")
}

// Being injected and being reported are different things. An interest that is
// present only because the cap left room is standing background; listing it as
// a memory this answer recalled would put something unrelated to the question
// on the chat timeline every single turn.
func TestUnrelatedInterestIsInjectedButNotReported(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	_, err := svc.CreateItem(ctx, types.MemoryKindInterest, "小微SDK设备接入", 3)
	require.NoError(t, err)

	unrelated := svc.Recall(ctx, "今天天气怎么样")
	require.Contains(t, unrelated.Prompt, "小微SDK设备接入")
	require.Empty(t, unrelated.Items)

	related := svc.Recall(ctx, "小微SDK怎么接入设备")
	require.Contains(t, related.Prompt, "小微SDK设备接入")
	require.Len(t, related.Items, 1, "an interest the question matches is a real recall")
}

// Relevance does not decide whether interests appear, it decides which ones
// survive the cap.
func TestInterestsBeyondTheCapAreChosenByRelevance(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	topics := []string{
		"医学影像分割", "数据库调优", "前端构建速度", "指标监控告警",
		"日志采集链路", "小微SDK设备接入",
	}
	for _, topic := range topics {
		_, err := svc.CreateItem(ctx, types.MemoryKindInterest, topic, 3)
		require.NoError(t, err)
	}

	recall := svc.Recall(ctx, "小微SDK怎么接入设备")
	require.Contains(t, recall.Prompt, "小微SDK设备接入",
		"the interest the question is about must not be the one dropped by the cap")

	var injected int
	for _, topic := range topics {
		if strings.Contains(recall.Prompt, topic) {
			injected++
		}
	}
	require.Equal(t, types.MemoryResidentInterestMaxItems, injected,
		"the block carries at most the cap, not every interest ever promoted")
}

func TestCreateItemFromManagerGoesThroughTheWritePath(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.CreateItem(ctx, types.MemoryKindPreference, "  回答请用中文  \n", 0)
	require.NoError(t, err)

	items, _, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "回答请用中文", items[0].Content, "manual input must be sanitized like any other")
	require.Equal(t, types.MemoryOriginManual, items[0].Origin)
	require.False(t, strings.Contains(items[0].Content, "\n"))
}
