package memory

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// A model asked to name a topic will not name it the same way twice. Counting
// the raw string is therefore not a small inaccuracy — it is the difference
// between the feature working and the feature silently never promoting
// anything. These tests use the drift that actually shows up in practice.

func TestTopicKeyIgnoresCosmeticDifferences(t *testing.T) {
	same := [][2]string{
		{"门店排班管理", "门店的排班管理"},
		{"PostgreSQL 连接池", "PostgreSQL 连接池问题"},
		{"postgresql连接池", "PostgreSQL 连接池"},
		{"数据库迁移", "数据库的迁移"},
	}
	for _, pair := range same {
		require.Equal(t, types.NormalizeTopicKey(pair[0]), types.NormalizeTopicKey(pair[1]),
			"%q and %q are the same subject", pair[0], pair[1])
	}

	different := [][2]string{
		{"PostgreSQL 连接池", "PostgreSQL 备份恢复"},
		{"门店排班管理", "成人马拉松报名"},
	}
	for _, pair := range different {
		require.NotEqual(t, types.NormalizeTopicKey(pair[0]), types.NormalizeTopicKey(pair[1]),
			"%q and %q are different subjects", pair[0], pair[1])
	}
}

// The old key sorted and de-duplicated characters, which is why one extra
// character produced a different topic. Order has to survive.
func TestTopicKeyIsNotACharacterBag(t *testing.T) {
	require.NotEqual(t,
		types.NormalizeTopicKey("上海到北京"),
		types.NormalizeTopicKey("北京到上海"),
	)
}

func TestRephrasedTopicStillCountsTowardsTheSameSubject(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, InterestThreshold: 3,
	})
	scope := scopeFor(t, ctx)

	// Three sightings, three different wordings, one subject. The first is an
	// exact match after normalisation, the second is close enough for the
	// bigram tier; neither needs a model.
	require.Empty(t, svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"}))
	require.Empty(t, svc.ObserveQuestionTopics(ctx, []string{"门店的排班管理"}))
	promoted := svc.ObserveQuestionTopics(ctx, []string{"连锁门店排班管理"})

	require.Equal(t, []string{"门店排班管理"}, promoted,
		"three wordings of one subject must reach the threshold together")

	stats, err := svc.repo.TopTopics(context.Background(), scope, 10)
	require.NoError(t, err)
	require.Len(t, stats, 1, "one subject, one row")
	require.Equal(t, 3, stats[0].Hits)
	require.Equal(t, "门店排班管理", stats[0].Topic,
		"the label stays the one it was first recorded under, so the list does not churn")
	require.True(t, stats[0].Aliases.Has("连锁门店排班管理"),
		"the other wordings are kept, both as an audit trail and as a fast path")
}

func TestDifferentSubjectsAreStillCountedApart(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, InterestThreshold: 2,
	})
	scope := scopeFor(t, ctx)

	svc.ObserveQuestionTopics(ctx, []string{"PostgreSQL 连接池"})
	svc.ObserveQuestionTopics(ctx, []string{"PostgreSQL 备份恢复"})

	stats, err := svc.repo.TopTopics(context.Background(), scope, 10)
	require.NoError(t, err)
	require.Len(t, stats, 2,
		"sharing a product name is not being about the same thing")
	for _, stat := range stats {
		require.Equal(t, 1, stat.Hits)
	}
}

// Merging two subjects that are not the same corrupts the count that decides
// what becomes a memory, and it is invisible once done. The cheap tier is
// therefore held well above where two labels merely look alike.
func TestLooseMatchingIsConservative(t *testing.T) {
	existing := []*types.MemoryTopicStat{
		{Topic: "门店排班管理", NormalizedKey: types.NormalizeTopicKey("门店排班管理")},
	}
	require.NotNil(t, matchTopicLoosely("连锁门店排班管理", existing))
	require.Nil(t, matchTopicLoosely("店员班次安排", existing),
		"a synonym is not something character overlap can decide; that is the model's job")
	require.Nil(t, matchTopicLoosely("供应商结算流程", existing))
}

// Short labels carry too little information for overlap to mean anything, so
// they skip the cheap tier rather than produce a false merge.
func TestShortLabelsDoNotMatchLoosely(t *testing.T) {
	existing := []*types.MemoryTopicStat{
		{Topic: "排班", NormalizedKey: types.NormalizeTopicKey("排班")},
	}
	require.Nil(t, matchTopicLoosely("游戏", existing))
	require.False(t, types.TopicIsSpecificEnoughToMatchLoosely("排班"))
}

func TestAliasGivesAnExactMatchNextTime(t *testing.T) {
	existing := []*types.MemoryTopicStat{
		{
			Topic:         "门店排班管理",
			NormalizedKey: types.NormalizeTopicKey("门店排班管理"),
			Aliases:       types.MemoryTopicAliases{"店员班次安排"},
		},
	}
	// A synonym the model decided on once must not be re-adjudicated forever.
	require.NotNil(t, matchTopicExactly("店员班次安排", existing))
	require.NotNil(t, matchTopicExactly("店员的班次安排", existing))
}

func TestTwoNewWordingsInOneRunBecomeOneTopic(t *testing.T) {
	resolutions := []topicResolution{
		{Surface: "门店排班管理"},
		{Surface: "门店的排班管理"},
	}
	collapseNewTopicsWithinRun(resolutions)
	require.Equal(t, resolutions[0].Surface, resolutions[1].Surface,
		"one run must not create two rows it then has to keep apart forever")
}

// A synonym is not something character overlap can decide. "店员班次安排"
// and "门店排班管理" share one bigram out of fourteen, so the only tier
// that can resolve them is the model — and once it has, the answer is stored as
// an alias so it is never asked again.
func TestASynonymIsResolvedByTheModelAndThenRemembered(t *testing.T) {
	svc, tenantRepo, _, models, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
		ExtractModelID: "model-1", InterestThreshold: 3,
	})
	scope := scopeFor(t, ctx)
	models.responseFor = map[string]string{
		"你在维护一个人的关注主题列表": `{"resolutions":[{"index":0,"same_as":0}]}`,
	}

	svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"})
	callsBefore := models.callCount()

	svc.ObserveQuestionTopics(ctx, []string{"店员班次安排"})
	require.Greater(t, models.callCount(), callsBefore,
		"nothing cheaper could have decided this, so the model must have been asked")

	stats, err := svc.repo.TopTopics(context.Background(), scope, 10)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, 2, stats[0].Hits)
	require.True(t, stats[0].Aliases.Has("店员班次安排"))

	// Third sighting of the same synonym: the alias now answers it, so the
	// model is not consulted again.
	callsBefore = models.callCount()
	promoted := svc.ObserveQuestionTopics(ctx, []string{"店员班次安排"})
	require.Equal(t, callsBefore, models.callCount(),
		"a decision the model already made must not be paid for twice")
	require.Equal(t, []string{"门店排班管理"}, promoted)
}

// The model gets a veto, not a free hand: if it says two subjects are distinct,
// they stay distinct, and it is never asked about labels an earlier tier
// already resolved.
func TestTheModelCanDeclineToMergeTopics(t *testing.T) {
	svc, tenantRepo, _, models, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
		ExtractModelID: "model-1", InterestThreshold: 3,
	})
	scope := scopeFor(t, ctx)
	models.responseFor = map[string]string{
		"你在维护一个人的关注主题列表": `{"resolutions":[{"index":0,"same_as":null}]}`,
	}

	svc.ObserveQuestionTopics(ctx, []string{"PostgreSQL 连接池"})
	svc.ObserveQuestionTopics(ctx, []string{"PostgreSQL 备份恢复"})

	stats, err := svc.repo.TopTopics(context.Background(), scope, 10)
	require.NoError(t, err)
	require.Len(t, stats, 2)
}

// A model that answers about a label it was not asked about must not be able to
// overwrite a match a more reliable tier already made.
func TestAdjudicationCannotOverrideACheaperTier(t *testing.T) {
	svc, tenantRepo, _, models, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
		ExtractModelID: "model-1", InterestThreshold: 5,
	})
	scope := scopeFor(t, ctx)
	models.responseFor = map[string]string{
		"你在维护一个人的关注主题列表": `{"resolutions":[{"index":0,"same_as":0},{"index":1,"same_as":0}]}`,
	}

	svc.ObserveQuestionTopics(ctx, []string{"PostgreSQL 连接池"})
	// One label matches by alias-free exact key, one is genuinely new. Only the
	// new one is up for adjudication.
	svc.ObserveQuestionTopics(ctx, []string{"PostgreSQL 连接池问题", "完全无关的园艺话题"})

	stats, err := svc.repo.TopTopics(context.Background(), scope, 10)
	require.NoError(t, err)
	require.Len(t, stats, 1, "the model merged the one label it was asked about")
	require.Equal(t, 3, stats[0].Hits)
}

// The label a merge leaves behind used to be whichever wording arrived first,
// which is arbitrary and not cosmetic: interests are fed to the query rewriter
// as this person's vocabulary. When one of the two names is plainly better, the
// model may say so.
func TestAMergeCanAdoptTheBetterName(t *testing.T) {
	svc, tenantRepo, _, models, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
		ExtractModelID: "model-1", InterestThreshold: 2,
	})
	scope := scopeFor(t, ctx)
	models.responseFor = map[string]string{
		"你在维护一个人的关注主题列表": `{"resolutions":[{"index":0,"same_as":0,"label":"持续集成流水线"}]}`,
	}

	svc.ObserveQuestionTopics(ctx, []string{"CI 流水线"})
	promoted := svc.ObserveQuestionTopics(ctx, []string{"持续集成流水线"})

	stats, err := svc.repo.TopTopics(context.Background(), scope, 10)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, "持续集成流水线", stats[0].Topic, "the fuller name should win")
	require.Equal(t, 2, stats[0].Hits, "renaming must not lose the count")
	require.True(t, stats[0].Aliases.Has("CI 流水线"),
		"the old label is what earlier sightings were counted under; dropping it makes that "+
			"wording look new again")
	require.False(t, stats[0].Aliases.Has("持续集成流水线"),
		"a subject must not be listed as an alias of itself")
	require.Equal(t, []string{"持续集成流水线"}, promoted)
}

// A model asked what two labels have in common will reach for something broader
// every time. Left unchecked, each merge widens the subject until it is an
// umbrella that means nothing — the exact failure this feature was just fixed
// for, arriving through a different door.
func TestAMergeCannotMakeTheSubjectVaguer(t *testing.T) {
	svc, tenantRepo, _, models, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
		ExtractModelID: "model-1", InterestThreshold: 5,
	})
	scope := scopeFor(t, ctx)
	models.responseFor = map[string]string{
		"你在维护一个人的关注主题列表": `{"resolutions":[{"index":0,"same_as":0,"label":"排班"}]}`,
	}

	svc.ObserveQuestionTopics(ctx, []string{"门店排班管理"})
	svc.ObserveQuestionTopics(ctx, []string{"店员班次安排"})

	stats, err := svc.repo.TopTopics(context.Background(), scope, 10)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, "门店排班管理", stats[0].Topic,
		"the merge stands, but the label may not become a category")
	require.Equal(t, 2, stats[0].Hits)
}

func TestProposedLabelsAreJudgedOnDirectionNotNovelty(t *testing.T) {
	canonical, incoming := "PostgreSQL 连接池", "PostgreSQL 连接池调优"

	require.True(t, types.TopicLabelIsAnImprovement(canonical, incoming, "PostgreSQL 连接池调优"),
		"more specific is the direction a label is allowed to move in")
	require.False(t, types.TopicLabelIsAnImprovement(canonical, incoming, "数据库"),
		"a category is not a better name for the same subject")
	require.False(t, types.TopicLabelIsAnImprovement(canonical, incoming, "PostgreSQL"),
		"dropping what makes the subject specific is generalising")
	require.False(t, types.TopicLabelIsAnImprovement(canonical, incoming, "运维相关的一些话题"),
		"a name grounded in neither label is an invention, not a merge")
	require.False(t, types.TopicLabelIsAnImprovement(canonical, incoming, canonical),
		"proposing the name it already has is not a rename")
}

// A promoted interest is a row the user can see and edit. Renaming the subject
// behind it has to keep the two in step, but must not overwrite wording the
// user chose.
func TestRenamingASubjectDoesNotOverwriteAnEditedInterest(t *testing.T) {
	svc, tenantRepo, _, models, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
		ExtractModelID: "model-1", InterestThreshold: 1,
	})
	models.responseFor = map[string]string{
		"你在维护一个人的关注主题列表": `{"resolutions":[{"index":0,"same_as":0,"label":"持续集成流水线"}]}`,
	}

	svc.ObserveQuestionTopics(ctx, []string{"CI 流水线"})
	items, _, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)

	_, err = svc.UpdateItem(ctx, items[0].ID, "我自己写的说法", 4)
	require.NoError(t, err)

	svc.ObserveQuestionTopics(ctx, []string{"持续集成流水线"})

	after, _, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Equal(t, "我自己写的说法", after[0].Content,
		"the user's own wording outranks a better generated one")
}
