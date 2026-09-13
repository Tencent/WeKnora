package memory

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMemoryConsistencyRealMessagePaging(t *testing.T) {
	s, db, tr := newMemoryHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	require.NoError(t, db.AutoMigrate(&types.Message{}))
	at := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 85; i++ {
		require.NoError(t, db.Exec("INSERT INTO messages (id, session_id, role, content, created_at) VALUES (?, ?, ?, ?, ?)", fmt.Sprintf("m%03d", i), "s", "user", "hello", at).Error)
	}
	require.NoError(t, db.Exec("INSERT INTO messages (id, session_id, role, content, created_at) VALUES (?, ?, ?, ?, ?)", "other", "unrelated", "user", "private", at).Error)
	require.NoError(t, db.Create(&types.Message{
		ID: "deleted", SessionID: "s", Role: "user", CreatedAt: at, DeletedAt: gorm.DeletedAt{Time: at, Valid: true},
	}).Error)
	s.messageRepo = repository.NewMessageRepository(db)
	var cursor types.MemoryMessageCursor
	seen := map[string]bool{}
	for {
		rows, err := s.messageRepo.ListMessagesBySessionAfterCursor(ctx, "s", cursor, 40)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			require.False(t, seen[row.ID])
			require.Equal(t, "s", row.SessionID)
			seen[row.ID] = true
			cursor = types.MemoryMessageCursor{At: row.CreatedAt, ID: row.ID}
		}
	}
	require.Len(t, seen, 85)
}

func TestMemoryConsistencyLeaseRecoveryRetainsProgress(t *testing.T) {
	s, db, tr := newMemoryHarness(t)
	at := time.Now()
	ctx := enabledCtx(t, tr, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := s.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	_, _, err = s.repo.EnqueuePendingSession(ctx, scope, "s", time.Minute)
	require.NoError(t, err)
	first, err := s.repo.ClaimPendingSessions(ctx, scope, "s", "dead-worker", time.Minute)
	require.NoError(t, err)
	cursor := types.MemoryMessageCursor{At: at.Add(-time.Hour), ID: "completed"}
	require.NoError(t, s.repo.CheckpointExtraction(ctx, scope, "dead-worker", first.Sessions[0], cursor, false))
	subject, err := s.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	subject.ExtractionState.LeaseUntil = time.Now().Add(-time.Minute)
	require.NoError(t, db.Model(subject).Update("extraction_state", subject.ExtractionState).Error)
	recovered, err := s.repo.ClaimPendingSessions(ctx, scope, "s", "recovery", time.Minute)
	require.NoError(t, err)
	require.Len(t, recovered.Sessions, 1)
	require.True(t, recovered.Sessions[0].Cursor.At.Equal(cursor.At))
	require.Equal(t, cursor.ID, recovered.Sessions[0].Cursor.ID)
	require.ErrorIs(t, s.repo.FinishExtraction(ctx, scope, "dead-worker"), types.ErrMemoryExtractionLeaseLost)
	require.NoError(t, s.repo.CheckpointExtraction(ctx, scope, "recovery", recovered.Sessions[0], cursor, true))
}

func TestMemoryConsistencyRedeliveryWaitsForCrashedWorkerLease(t *testing.T) {
	s, tr, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	scope := scopeFor(t, ctx)
	models.response = accountResponse("重启前的会话", "用户在重启前说了两句话。")
	messages.set("s", settledConversation("s", "must-survive-restart", "还有一句也要活下来"))
	s.ScheduleExtraction(ctx, "s", "m", "model")
	original := queue.pop()
	_, err := s.repo.ClaimPendingSessions(ctx, scope, "s", "crashed", time.Minute)
	require.NoError(t, err)
	require.NoError(t, s.Handle(ctx, original))
	require.Zero(t, models.callCount())
	retry := queue.pop()
	require.NotNil(t, retry, "a busy lease must defer redelivery rather than consume the only surviving task")
	require.Greater(t, queue.options[len(queue.options)-1].processIn, 50*time.Second)
	// Simulate the crashed worker's lease being released by recovery.
	require.NoError(t, s.repo.ReleaseExtractionSlot(ctx, scope, "crashed"))
	require.NoError(t, s.Handle(ctx, retry))
	require.Equal(t, 1, models.callsContaining(episodeTranscriptHeading),
		"the redelivered task has to write the account the crashed one did not")
}

func execMemoryMigration(t *testing.T, db *gorm.DB, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	var sql strings.Builder
	for _, line := range strings.Split(string(contents), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			sql.WriteString(line)
			sql.WriteByte('\n')
		}
	}
	for _, stmt := range strings.Split(sql.String(), ";") {
		if strings.TrimSpace(stmt) != "" {
			require.NoError(t, db.Exec(stmt).Error, stmt)
		}
	}
}

// seedUsedAccount files an account and leaves it looking like history the
// profile already carries: read at least once, and folded into a revision.
func seedUsedAccount(
	t *testing.T, svc *Service, ctx context.Context, session, slug string, age time.Duration,
) *types.MemoryEpisode {
	t.Helper()
	scope := scopeFor(t, ctx)
	episode := seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: session, Slug: slug, Title: slug,
		Summary:  "用户问了一个问题，得到了答案。",
		Keywords: types.MemoryEpisodeTokens{"提问"},
		ToAt:     time.Now().Add(-age),
	})
	require.NoError(t, svc.repo.TouchEpisodes(ctx, scope, []string{episode.ID}))
	require.NoError(t, svc.repo.MarkEpisodesConsolidated(ctx, scope, []string{episode.ID}, 1))
	return episode
}

// Ranking the store by reads only works if a read means something asked for
// the account. Recall is this system guessing that an account is relevant, and
// since the guess is made from similarity to the question, counting it would
// rank the store by how often it guessed — an account matching a question the
// person keeps asking would climb past one that mattered once and decisively.
func TestRecallIsRelevanceRatherThanARead(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)
	episode := seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-1", Slug: "import-413", Title: "入库报 413",
		Summary: "用户批量导入时反复报 413。",
		ToAt:    time.Now().Add(-time.Hour),
	})

	require.NoError(t, svc.repo.MarkEpisodesRecalled(ctx, scope, []string{episode.ID}))

	recalled, err := svc.repo.EpisodeBySession(ctx, scope, "s-1")
	require.NoError(t, err)
	require.Zero(t, recalled.UseCount, "being injected is not being asked for")
	require.NotNil(t, recalled.LastUsedAt,
		"but it was relevant, which is what keeps it inside the selection window")

	require.NoError(t, svc.repo.TouchEpisodes(ctx, scope, []string{episode.ID}))

	searched, err := svc.repo.EpisodeBySession(ctx, scope, "s-1")
	require.NoError(t, err)
	require.Equal(t, 1, searched.UseCount,
		"the search tool asked for this account by question, and that counts")
}

func slugsInStore(t *testing.T, svc *Service, ctx context.Context) []string {
	t.Helper()
	episodes, _, err := svc.repo.ListEpisodes(ctx, scopeFor(t, ctx), 100, 0)
	require.NoError(t, err)
	slugs := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		slugs = append(slugs, episode.Slug)
	}
	return slugs
}

// The cap is enforced on the least-read accounts, and an account filed a
// moment ago has by definition never been read. In a store where everything
// else has been recalled, that puts the newest account at the front of the
// deletion queue — so the one thing the cap must never drop is the account the
// run that triggered it just paid a model call to write.
func TestTheCapDoesNotDropTheAccountJustFiled(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)

	seedUsedAccount(t, svc, ctx, "s-old", "oldest-conversation", 72*time.Hour)
	seedUsedAccount(t, svc, ctx, "s-mid", "middle-conversation", 48*time.Hour)
	fresh := seedEpisode(t, svc, ctx, &types.MemoryEpisode{
		SessionID: "s-new", Slug: "just-filed", Title: "刚写下的账目",
		Summary:  "用户排查了一个导入失败的问题。",
		Keywords: types.MemoryEpisodeTokens{"导入"},
		ToAt:     time.Now(),
	})

	removed, err := svc.repo.PruneEpisodes(ctx, scope, 2)
	require.NoError(t, err)
	require.Equal(t, int64(1), removed)

	require.Contains(t, slugsInStore(t, svc, ctx), fresh.Slug,
		"the account this run just wrote must survive the cap it triggered")
	require.NotContains(t, slugsInStore(t, svc, ctx), "oldest-conversation",
		"the cap has to fall on the least useful history instead")
}

// Material the profile has not read yet is the only copy of that conversation.
// Dropping it to satisfy the cap would lose it for good, because consolidation
// reads the store rather than a queue.
func TestTheCapWaitsForAccountsTheProfileHasNotReadYet(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)

	for i := 0; i < 3; i++ {
		seedEpisode(t, svc, ctx, &types.MemoryEpisode{
			SessionID: fmt.Sprintf("s-%d", i),
			Slug:      fmt.Sprintf("unconsolidated-%d", i),
			Title:     fmt.Sprintf("第 %d 次对话", i),
			Summary:   "用户问了一个问题，得到了答案。",
			Keywords:  types.MemoryEpisodeTokens{"提问"},
			ToAt:      time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}

	removed, err := svc.repo.PruneEpisodes(ctx, scope, 1)
	require.NoError(t, err)
	require.Zero(t, removed,
		"a cap that deletes what no profile has summarized would lose the conversation entirely")
	require.Len(t, slugsInStore(t, svc, ctx), 3)
}
