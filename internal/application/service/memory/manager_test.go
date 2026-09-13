package memory

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The point of adding a memory by hand is that the product starts behaving
// differently at once. A manager that writes somewhere the answer path never
// reads tells the user they are in control when they are not.
func TestAHandWrittenNoteReachesThePromptOnTheNextTurn(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	note, err := svc.AddNote(ctx, "  回答请用中文  \n")
	require.NoError(t, err)
	require.Equal(t, "回答请用中文", note.Content, "manual input must be sanitized like any other")
	require.NotEmpty(t, note.ID, "the manager needs an id to delete it again")

	require.Contains(t, svc.Recall(ctx, "帮我写个函数").Prompt, "回答请用中文")
}

func TestANoteThatIsEmptyOrTooLongIsRefused(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.AddNote(ctx, "   \n\t ")
	require.ErrorIs(t, err, ErrEmptyContent,
		"an empty note would occupy a slot in every prompt and say nothing")

	_, err = svc.AddNote(ctx, strings.Repeat("记", types.MemoryNoteMaxRunes+1))
	require.ErrorIs(t, err, ErrContentTooLong,
		"a note is injected verbatim, so it is refused rather than cut off mid-instruction")

	notes, err := svc.ListNotes(ctx, types.MemoryNotesMaxItems)
	require.NoError(t, err)
	require.Empty(t, notes, "a refused note must not be stored anyway")
}

// Every note rides in every turn, so the cap is what keeps the prompt finite.
// Dropping the oldest instead would discard an instruction the user gave and
// never withdrew, without telling them.
func TestTheNoteCapIsReportedRatherThanSilentlyDroppingTheOldest(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	for i := 0; i < types.MemoryNotesMaxItems; i++ {
		_, err := svc.AddNote(ctx, fmt.Sprintf("第 %d 条要记住的事", i))
		require.NoError(t, err)
	}

	_, err := svc.AddNote(ctx, "再记一条")
	require.ErrorIs(t, err, ErrNotesFull)

	notes, err := svc.ListNotes(ctx, types.MemoryNotesMaxItems)
	require.NoError(t, err)
	require.Len(t, notes, types.MemoryNotesMaxItems)
	require.Contains(t, svc.Recall(ctx, "任何问题").Prompt, "第 0 条要记住的事",
		"the earliest instruction must survive a refused addition")

	// Repeating something already stored is a refresh, not growth, so a full
	// store must not refuse it.
	_, err = svc.AddNote(ctx, "第 0 条要记住的事")
	require.NoError(t, err, "re-asking for something already remembered cannot be an error")
}

func TestDeletingANoteStopsItReachingThePrompt(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	note, err := svc.AddNote(ctx, "回答请用中文")
	require.NoError(t, err)
	require.NoError(t, svc.DeleteNote(ctx, note.ID))
	require.Empty(t, svc.Recall(ctx, "帮我写个函数").Prompt)

	bobCtx := enabledCtx(t, tenantRepo, 1, "bob")
	require.ErrorIs(t, svc.DeleteNote(bobCtx, note.ID), ErrNotFound,
		"another subject's note must look exactly like one that does not exist")
}

// An edit the next consolidation quietly reverted would be worse than no
// editor at all, so the edit is recorded and the rewrite is told about it.
func TestAProfileTheUserEditedIsNotSilentlyOverwritten(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	seedDigest(t, svc, ctx, "## 用户画像\n- 在做前端\n")
	revision, err := svc.SaveProfile(ctx, "## 用户画像\n- 在做医学影像的后端\n")
	require.NoError(t, err)
	require.Greater(t, revision, int64(1),
		"an edit advances the revision so already folded-in accounts are not re-proposed")

	profile, err := svc.Profile(ctx)
	require.NoError(t, err)
	require.Contains(t, profile.Body, "医学影像")
	require.NotNil(t, profile.UserEditedAt, "the edit has to be visible to the next rewrite")

	prompt := buildDigestPrompt(profile, nil, 0, nil, "")
	require.Contains(t, prompt, "do not restore what they removed",
		"a rewrite that was not told about the edit would undo it")

	// And the edit is what the next turn injects.
	require.Contains(t, svc.Recall(ctx, "随便问点什么").Prompt, "医学影像")
}

func TestAProfileOverTheRuneCapIsRefused(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	_, err := svc.SaveProfile(ctx, strings.Repeat("画", types.MemoryDigestMaxRunes+1))
	require.ErrorIs(t, err, ErrContentTooLong,
		"the profile rides in every turn, so its size is a budget rather than a preference")

	_, err = svc.SaveProfile(ctx, "   ")
	require.ErrorIs(t, err, ErrEmptyContent)

	profile, err := svc.Profile(ctx)
	require.NoError(t, err)
	require.Nil(t, profile, "a refused edit must not create a profile")
}

func TestDeletingTheProfileKeepsTheAccountsItWasBuiltFrom(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	seedDigest(t, svc, ctx, "## 用户画像\n- 在做医学影像的后端\n")
	seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "session-1", Slug: "imaging-seg", Title: "分割模型调参",
		Summary: "用户在调分割模型的参数。", ToAt: time.Now(),
	})

	require.NoError(t, svc.DeleteProfile(ctx))
	profile, err := svc.Profile(ctx)
	require.NoError(t, err)
	require.Nil(t, profile)

	_, total, err := svc.ListEpisodes(ctx, 20, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), total,
		"clearing the description of someone is not the same as erasing their history")
}

func TestListingAccountsPagesAndReportsTheTotal(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	for i := 0; i < 5; i++ {
		seedEpisode(t, svc, ctx, &types.MemoryEpisode{
			SessionID: fmt.Sprintf("session-%d", i),
			Slug:      fmt.Sprintf("account-%d", i),
			Title:     fmt.Sprintf("第 %d 段对话", i),
			Summary:   fmt.Sprintf("第 %d 段对话的记述。", i),
			ToAt:      time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}

	first, total, err := svc.ListEpisodes(ctx, 2, 0)
	require.NoError(t, err)
	require.Equal(t, int64(5), total, "the manager needs the total to render paging at all")
	require.Len(t, first, 2)

	second, _, err := svc.ListEpisodes(ctx, 2, 2)
	require.NoError(t, err)
	require.Len(t, second, 2)
	require.NotEqual(t, first[0].ID, second[0].ID, "a second page must not repeat the first")

	last, _, err := svc.ListEpisodes(ctx, 2, 4)
	require.NoError(t, err)
	require.Len(t, last, 1, "the final short page is how the caller knows it reached the end")
}

func TestAnotherSubjectsAccountIsAsMissingAsOneThatNeverExisted(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	aliceCtx := enabledCtx(t, tenantRepo, 1, "alice")
	episode := seedEpisode(t, svc, aliceCtx, &types.MemoryEpisode{
		SessionID: "session-1", Slug: "alice-account", Title: "爱丽丝的对话",
		Summary: "只属于爱丽丝的记述。", ToAt: time.Now(),
	})

	bobCtx := enabledCtx(t, tenantRepo, 1, "bob")
	_, err := svc.GetEpisode(bobCtx, episode.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = svc.GetEpisode(bobCtx, "no-such-id")
	require.ErrorIs(t, err, ErrNotFound,
		"the two cases must be indistinguishable, or an id can be probed for existence")
	require.ErrorIs(t, svc.DeleteEpisode(bobCtx, episode.ID), ErrNotFound)

	// And the attempt left it alone.
	stored, err := svc.GetEpisode(aliceCtx, episode.ID)
	require.NoError(t, err)
	require.Equal(t, "爱丽丝的对话", stored.Title)
}

func TestClearLeavesNothingInAnyOfTheThreeStores(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	seedDigest(t, svc, ctx, "## 用户画像\n- 在做医学影像的后端\n")
	for i := 0; i < 2; i++ {
		seedEpisode(t, svc, ctx, &types.MemoryEpisode{
			SessionID: fmt.Sprintf("session-%d", i),
			Slug:      fmt.Sprintf("account-%d", i),
			Title:     fmt.Sprintf("第 %d 段对话", i),
			Summary:   fmt.Sprintf("第 %d 段对话的记述。", i),
			ToAt:      time.Now(),
		})
	}
	_, err := svc.AddNote(ctx, "回答请用中文")
	require.NoError(t, err)

	removed, err := svc.Clear(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(4), removed,
		"the count is what the UI tells the user was dropped, so it covers all three stores")

	profile, err := svc.Profile(ctx)
	require.NoError(t, err)
	require.Nil(t, profile, "a profile left behind would keep describing the user in every turn")
	_, total, err := svc.ListEpisodes(ctx, 20, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	notes, err := svc.ListNotes(ctx, types.MemoryNotesMaxItems)
	require.NoError(t, err)
	require.Empty(t, notes)
	require.Empty(t, svc.Recall(ctx, "我在做什么项目").Prompt)

	settings, err := svc.GetSettings(ctx)
	require.NoError(t, err)
	require.Zero(t, settings.EpisodeCount)
}
