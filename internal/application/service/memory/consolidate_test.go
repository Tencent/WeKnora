package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// rewrittenProfile is what the model returns when it is asked to rewrite the
// profile. It only has to carry a heading, which is what the read path takes
// sections out of.
const rewrittenProfile = "## 用户画像\n- 在做医学影像的后端\n\n## 用户偏好\n- 回答直接给结论\n"

// newProfileRewriteHarness gives the service a chat model, because rewriting
// the profile is a model call and without one there is nothing to observe.
func newProfileRewriteHarness(t *testing.T) (*Service, *stubTenantRepo, *stubModelService) {
	t.Helper()
	svc, _, tenantRepo := newMemoryHarness(t)
	models := &stubModelService{
		workspaceModels: []*types.Model{
			{ID: "chat-1", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
		},
		response: rewrittenProfile,
	}
	svc.modelService = models
	return svc, tenantRepo, models
}

// seedSettledProfile leaves the subject in the state the background pass
// declines to act on: a profile that was written moments ago, and accounts it
// has already read.
func seedSettledProfile(t *testing.T, svc *Service, ctx context.Context) {
	t.Helper()
	seedDigest(t, svc, ctx, "## 用户画像\n- 在做后端\n")
	scope := scopeFor(t, ctx)

	ids := make([]string, 0, 2)
	for _, episode := range []*types.MemoryEpisode{
		{
			SessionID: "s-import", Slug: "knowledge-import-413", Title: "知识库批量入库报 413",
			Summary:  "用户批量导入时反复报 413，把单批大小调到 20 才通过。",
			Keywords: types.MemoryEpisodeTokens{"入库", "413"},
			ToAt:     time.Now().Add(-24 * time.Hour),
		},
		{
			SessionID: "s-style", Slug: "answer-style", Title: "回答直接给结论",
			Summary:  "用户要求回答直接给结论。",
			Keywords: types.MemoryEpisodeTokens{"回答风格"},
			ToAt:     time.Now().Add(-time.Hour),
		},
	} {
		ids = append(ids, seedEpisode(t, svc, ctx, episode).ID)
	}

	digest, err := svc.repo.GetDigest(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, svc.repo.MarkEpisodesConsolidated(ctx, scope, ids, digest.Revision))
}

// The background pass runs at the tail of every extraction, and new material
// is now the only bar it has to clear, so that check is the only thing
// standing between a quiet store and a whole-profile model call per
// conversation. An account a rewrite has already read must therefore stop
// counting as new the moment it is folded in.
func TestAccountsAProfileAlreadyReadStopCountingAsNewMaterial(t *testing.T) {
	svc, tenantRepo, models := newProfileRewriteHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedDigest(t, svc, ctx, "## 用户画像\n- 在做后端\n")
	scope := scopeFor(t, ctx)

	const accounts = 3
	ids := make([]string, 0, accounts)
	for i := 0; i < accounts; i++ {
		episode := seedEpisode(t, svc, ctx, &types.MemoryEpisode{
			SessionID: fmt.Sprintf("s-%d", i),
			Slug:      fmt.Sprintf("conversation-%d", i),
			Title:     fmt.Sprintf("第 %d 次对话", i),
			Summary:   "用户问了一个问题，得到了答案。",
			Keywords:  types.MemoryEpisodeTokens{"提问"},
			ToAt:      time.Now().Add(-time.Duration(i) * time.Hour),
		})
		ids = append(ids, episode.ID)
	}

	digest, err := svc.repo.GetDigest(ctx, scope)
	require.NoError(t, err)
	require.True(t, svc.digestIsDue(ctx, scope),
		"accounts no profile has read are exactly what a rewrite is for")

	require.NoError(t, svc.repo.MarkEpisodesConsolidated(ctx, scope, ids, digest.Revision))

	require.False(t, svc.digestIsDue(ctx, scope),
		"a profile built from these accounts must not be immediately due again, "+
			"or every extraction run pays for a rewrite that reads the same thing")
	require.Zero(t, models.callCount())
}

// An account rewritten after the profile read it is new material again: the
// conversation continued, so the text the profile summarized is gone.
func TestARewrittenAccountIsNewMaterialAgain(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedDigest(t, svc, ctx, "## 用户画像\n- 在做后端\n")
	scope := scopeFor(t, ctx)

	episode := seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-import", Slug: "knowledge-import-413", Title: "知识库批量入库报 413",
		Summary:  "用户批量导入时报 413。",
		Keywords: types.MemoryEpisodeTokens{"入库"},
		ToAt:     time.Now().Add(-2 * time.Hour),
	})
	digest, err := svc.repo.GetDigest(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, svc.repo.MarkEpisodesConsolidated(
		ctx, scope, []string{episode.ID}, digest.Revision))

	awaiting, err := svc.repo.CountEpisodesAwaitingDigest(ctx, scope)
	require.NoError(t, err)
	require.Zero(t, awaiting)

	// The same conversation, summarized again after it carried on.
	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-import", Slug: "knowledge-import-413", Title: "知识库批量入库报 413",
		Summary:  "用户批量导入时报 413，把单批大小调到 20 才通过。",
		Keywords: types.MemoryEpisodeTokens{"入库", "413"},
		ToAt:     time.Now(),
	})

	awaiting, err = svc.repo.CountEpisodesAwaitingDigest(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, int64(1), awaiting,
		"the profile summarized text that no longer describes the conversation")
}

// The whole point of the button is to overrule the background pass's judgement.
// That pass declines when it has already read every account, which is the
// common case and a correct one for something that spends a large call on a
// document that usually barely changes — and no reason to refuse the person
// who just pressed the button because the profile reads wrong to them.
func TestAskingForAProfileRewriteOverrulesTheScheduleThatWouldDeclineIt(t *testing.T) {
	svc, tenantRepo, models := newProfileRewriteHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedSettledProfile(t, svc, ctx)
	scope := scopeFor(t, ctx)

	require.False(t, svc.digestIsDue(ctx, scope),
		"this only means anything while the background pass would have said no")

	before, err := svc.repo.GetDigest(ctx, scope)
	require.NoError(t, err)

	result, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Empty(t, result.Skipped)
	require.Equal(t, 2, result.Reviewed,
		"the person who asked has to be told how much the rewrite read")
	require.Equal(t, 1, models.callCount())

	after, err := svc.repo.GetDigest(ctx, scope)
	require.NoError(t, err)
	require.Greater(t, after.Revision, before.Revision)
	require.Contains(t, after.Body, "医学影像",
		"a rewrite that reports success has to have replaced the profile")
}

// The endpoint is Viewer-level and each press is worth a whole-profile model
// call, so the button must not be scriptable into a stream of them.
func TestASecondProfileRewriteRightAwayIsRefused(t *testing.T) {
	svc, tenantRepo, models := newProfileRewriteHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedSettledProfile(t, svc, ctx)

	first, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Empty(t, first.Skipped)
	callsAfterFirst := models.callCount()

	second, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Equal(t, types.MemoryConsolidationSkipTooSoon, second.Skipped)
	require.Equal(t, callsAfterFirst, models.callCount(),
		"a refused rewrite must not reach the model at all")
}

// Turning memory off has to stop everything that reads or writes it, including
// the one action a person can trigger by hand.
func TestAProfileRewriteIsRefusedWhenMemoryIsOff(t *testing.T) {
	svc, tenantRepo, models := newProfileRewriteHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedSettledProfile(t, svc, ctx)

	tenantRepo.set(1, &types.MemoryConfig{})

	result, err := svc.ConsolidateNow(ctx)
	require.ErrorIs(t, err, ErrMemoryDisabled)
	require.Nil(t, result)
	require.Zero(t, models.callCount())
}

// Zeroes are the normal outcome of a rewrite, so one that changed nothing has
// to say which kind of nothing it was.
func TestAProfileRewriteWithTooLittleToReadSaysSo(t *testing.T) {
	svc, tenantRepo, models := newProfileRewriteHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	result, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Equal(t, types.MemoryConsolidationSkipTooFewItems, result.Skipped)
	require.Zero(t, result.Reviewed)
	require.Zero(t, models.callCount(),
		"there is nothing to write a profile from, so no call is worth making")
}

// One conversation is enough for a first profile. It used to take two, on the
// reasoning that a profile built from one conversation is a profile of that
// conversation — true, and self-correcting, because the next rewrite replaces
// the whole document. Waiting is not self-correcting: until the second
// conversation exists the person gets nothing injected at all, and whatever
// the first one established about them sits in an account that only a
// semantically similar question would ever reach.
func TestAFirstProfileIsWrittenFromOneConversation(t *testing.T) {
	svc, tenantRepo, models := newProfileRewriteHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-intro", Slug: "self-introduction", Title: "自我介绍",
		Summary:  "用户说自己是 wizard，是程序员。",
		Keywords: types.MemoryEpisodeTokens{"自我介绍"},
		ToAt:     time.Now().Add(-time.Hour),
	})
	models.response = "## 用户画像\n用户是程序员，自称 wizard。\n"

	result, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Empty(t, result.Skipped)
	require.Equal(t, 1, result.Reviewed)

	digest, err := svc.repo.GetDigest(context.Background(), scopeFor(t, ctx))
	require.NoError(t, err)
	require.NotNil(t, digest)
	require.Positive(t, digest.Revision)
	require.Contains(t, digest.Body, "程序员",
		"what the one conversation established has to reach the injected profile")
}

// The model is the part of this most likely to be briefly unreachable, and a
// person who pressed a button needs to be told to try again rather than shown
// a failed request.
func TestAnUnreachableModelLeavesTheProfileAloneAndSaysWhy(t *testing.T) {
	svc, tenantRepo, models := newProfileRewriteHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedSettledProfile(t, svc, ctx)
	scope := scopeFor(t, ctx)
	models.failNext = true

	before, err := svc.repo.GetDigest(ctx, scope)
	require.NoError(t, err)

	result, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Equal(t, types.MemoryConsolidationSkipModelUnavailable, result.Skipped)

	after, err := svc.repo.GetDigest(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, before.Body, after.Body,
		"a failed rewrite keeps the working profile rather than replacing it with nothing")
}
