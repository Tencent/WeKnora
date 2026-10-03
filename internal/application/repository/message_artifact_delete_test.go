package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Deleting an artifact must not renumber the ones after it: position is the
// index the download endpoint addresses a file by, so a shift would hand an
// existing link the wrong blob.
func TestSoftDeleteKeepsPositionsStable(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	created := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	msg, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: "s1", RequestID: "r1", Role: "assistant",
		Artifacts: types.MessageArtifacts{
			testArtifact("a.pptx", "/w/a.pptx", created),
			testArtifact("b.pptx", "/w/b.pptx", created),
			testArtifact("c.pptx", "/w/c.pptx", created),
		},
	})
	require.NoError(t, err)

	marked, err := repo.SoftDeleteSessionArtifacts(ctx, "s1",
		[]types.ArtifactRef{{MessageID: msg.ID, Position: 1}}, created)
	require.NoError(t, err)
	require.Equal(t, []types.ArtifactRef{{MessageID: msg.ID, Position: 1}}, marked)

	got, err := repo.GetMessage(ctx, "s1", msg.ID)
	require.NoError(t, err)
	// GORM only soft-deletes for fields typed gorm.DeletedAt. Ours is a plain
	// *time.Time precisely so the tombstone keeps loading and holding its slot.
	require.Len(t, got.Artifacts, 3, "the tombstone keeps its slot in the list")
	require.False(t, got.Artifacts[0].Deleted())
	require.True(t, got.Artifacts[1].Deleted())
	require.Equal(t, "c.pptx", got.Artifacts[2].FileName, "index 2 still addresses the third file")

	require.Equal(t, types.MessageArtifacts{got.Artifacts[0], got.Artifacts[2]}, got.Artifacts.Live())
}

// The collector de-duplicates sandbox files against the session's rows. The
// tombstone has to stay in that set, or the next collect re-attaches the file
// the user just deleted — its sandbox mtime has not moved.
func TestSoftDeletedArtifactsStayInSessionDedupSet(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	created := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	msg, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: "s1", RequestID: "r1", Role: "assistant",
		Artifacts: types.MessageArtifacts{testArtifact("a.pptx", "/w/a.pptx", created)},
	})
	require.NoError(t, err)
	_, err = repo.SoftDeleteSessionArtifacts(ctx, "s1",
		[]types.ArtifactRef{{MessageID: msg.ID, Position: 0}}, created)
	require.NoError(t, err)

	all, err := repo.GetSessionArtifacts(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.True(t, all[0].Deleted())
	require.Equal(t, "/w/a.pptx", all[0].SourcePath, "source path and mtime survive for de-duplication")
	require.Empty(t, all.Live())
}

// Rewriting a message's artifacts (any UpdateMessage carrying a non-nil list)
// goes through the same slice, so the tombstone has to round-trip.
func TestWriteMessageArtifactsPreservesTombstones(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	created := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	msg, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: "s1", RequestID: "r1", Role: "assistant",
		Artifacts: types.MessageArtifacts{
			testArtifact("a.pptx", "/w/a.pptx", created),
			testArtifact("b.pptx", "/w/b.pptx", created),
		},
	})
	require.NoError(t, err)
	_, err = repo.SoftDeleteSessionArtifacts(ctx, "s1",
		[]types.ArtifactRef{{MessageID: msg.ID, Position: 0}}, created)
	require.NoError(t, err)

	reloaded, err := repo.GetMessage(ctx, "s1", msg.ID)
	require.NoError(t, err)
	reloaded.Content = "edited"
	require.NoError(t, repo.UpdateMessage(ctx, reloaded))

	got, err := repo.GetMessage(ctx, "s1", msg.ID)
	require.NoError(t, err)
	require.Len(t, got.Artifacts, 2)
	require.True(t, got.Artifacts[0].Deleted(), "the tombstone survives a message rewrite")
	require.False(t, got.Artifacts[1].Deleted())
}

func TestFindSessionArtifactSkipsTombstones(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	created := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	msg, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: "s1", RequestID: "r1", Role: "assistant",
		Artifacts: types.MessageArtifacts{testArtifact("a.pptx", "/w/a.pptx", created)},
	})
	require.NoError(t, err)

	found, err := repo.FindSessionArtifact(ctx, "s1", msg.ID, 0)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, "a.pptx", found.FileName)

	missing, err := repo.FindSessionArtifact(ctx, "s1", msg.ID, 9)
	require.NoError(t, err)
	require.Nil(t, missing)

	_, err = repo.SoftDeleteSessionArtifacts(ctx, "s1",
		[]types.ArtifactRef{{MessageID: msg.ID, Position: 0}}, created)
	require.NoError(t, err)

	gone, err := repo.FindSessionArtifact(ctx, "s1", msg.ID, 0)
	require.NoError(t, err)
	require.Nil(t, gone, "a tombstone reads as absent, so a second delete is a 404")

	// And a second delete claims nothing, so its caller reclaims no blobs.
	marked, err := repo.SoftDeleteSessionArtifacts(ctx, "s1",
		[]types.ArtifactRef{{MessageID: msg.ID, Position: 0}}, created)
	require.NoError(t, err)
	require.Empty(t, marked, "a second delete claims nothing, so its caller reclaims no blobs")
}

// Versions are the rows of one session sharing a sandbox path — what the
// artifact library folds into a single entry with a version count.
func TestFindSessionArtifactVersions(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	first, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: "s1", RequestID: "r1", Role: "assistant", CreatedAt: base,
		Artifacts: types.MessageArtifacts{testArtifact("report.pptx", "/w/report.pptx", base)},
	})
	require.NoError(t, err)
	v2 := testArtifact("report.pptx", "/w/report.pptx", base.Add(time.Hour))
	v2.URL = "local://blobs/report-v2.pptx"
	_, err = repo.CreateMessage(ctx, &types.Message{
		SessionID: "s1", RequestID: "r2", Role: "assistant", CreatedAt: base.Add(time.Hour),
		Artifacts: types.MessageArtifacts{v2, testArtifact("other.csv", "/w/other.csv", base)},
	})
	require.NoError(t, err)
	// Another session's file at the same sandbox path is a different file.
	_, err = repo.CreateMessage(ctx, &types.Message{
		SessionID: "s2", RequestID: "r3", Role: "assistant", CreatedAt: base,
		Artifacts: types.MessageArtifacts{testArtifact("report.pptx", "/w/report.pptx", base)},
	})
	require.NoError(t, err)

	versions, err := repo.FindSessionArtifactVersions(ctx, "s1", "/w/report.pptx")
	require.NoError(t, err)
	require.Len(t, versions, 2)

	// An artifact with no sandbox path has no version group.
	none, err := repo.FindSessionArtifactVersions(ctx, "s1", "")
	require.NoError(t, err)
	require.Empty(t, none)

	_, err = repo.SoftDeleteSessionArtifacts(ctx, "s1",
		[]types.ArtifactRef{{MessageID: first.ID, Position: 0}}, base)
	require.NoError(t, err)
	versions, err = repo.FindSessionArtifactVersions(ctx, "s1", "/w/report.pptx")
	require.NoError(t, err)
	require.Len(t, versions, 1, "already-deleted versions drop out")
}

func TestListArtifactLibraryHidesDeletedArtifacts(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)
	mine := createSession(t, db, &types.Session{TenantID: 7, UserID: "alice", Title: "我的会话"})

	first, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: mine, RequestID: "r1", Role: "assistant", CreatedAt: base,
		Artifacts: types.MessageArtifacts{testArtifact("report.pptx", "/w/report.pptx", base)},
	})
	require.NoError(t, err)
	v2 := testArtifact("report.pptx", "/w/report.pptx", base.Add(time.Hour))
	v2.URL = "local://blobs/report-v2.pptx"
	latest, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: mine, RequestID: "r2", Role: "assistant", CreatedAt: base.Add(time.Hour),
		Artifacts: types.MessageArtifacts{v2},
	})
	require.NoError(t, err)

	query := &types.ArtifactLibraryQuery{TenantID: 7, UserID: "alice", Page: 1, PageSize: 20}
	items, total, err := repo.ListArtifactLibrary(ctx, query)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, 2, items[0].VersionCount)

	// Deleting only the newest version surfaces the one before it — which is
	// why the delete path tombstones every version of a library row at once.
	_, err = repo.SoftDeleteSessionArtifacts(ctx, mine,
		[]types.ArtifactRef{{MessageID: latest.ID, Position: 0}}, base)
	require.NoError(t, err)
	items, total, err = repo.ListArtifactLibrary(ctx, query)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, first.ID, items[0].MessageID)
	require.Equal(t, 1, items[0].VersionCount)

	_, err = repo.SoftDeleteSessionArtifacts(ctx, mine,
		[]types.ArtifactRef{{MessageID: first.ID, Position: 0}}, base)
	require.NoError(t, err)
	items, total, err = repo.ListArtifactLibrary(ctx, query)
	require.NoError(t, err)
	require.EqualValues(t, 0, total)
	require.Empty(t, items)
}

// A stale writer must not be able to undo a delete. writeMessageArtifacts is a
// full replace, so a caller holding a pre-delete snapshot would resurrect the
// row while its bytes are already being reclaimed.
func TestWriteMessageArtifactsCannotResurrectATombstone(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	created := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	msg, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: "s1", RequestID: "r1", Role: "assistant",
		Artifacts: types.MessageArtifacts{
			testArtifact("a.pptx", "/w/a.pptx", created),
			testArtifact("b.pptx", "/w/b.pptx", created),
		},
	})
	require.NoError(t, err)
	// A snapshot taken before the delete, still carrying both as live.
	stale, err := repo.GetMessage(ctx, "s1", msg.ID)
	require.NoError(t, err)

	_, err = repo.SoftDeleteSessionArtifacts(ctx, "s1",
		[]types.ArtifactRef{{MessageID: msg.ID, Position: 0}}, created)
	require.NoError(t, err)

	stale.Content = "edited"
	require.NoError(t, repo.UpdateMessage(ctx, stale))

	got, err := repo.GetMessage(ctx, "s1", msg.ID)
	require.NoError(t, err)
	require.Len(t, got.Artifacts, 2)
	require.True(t, got.Artifacts[0].Deleted(), "the stale write must not bring the deleted row back")
	require.False(t, got.Artifacts[1].Deleted())
}

// Deleting a file's versions is several statements; a failure partway through
// must not leave some of them hidden with their bytes never reclaimed.
func TestSoftDeleteSessionArtifactsIsAtomic(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	created := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	msg, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: "s1", RequestID: "r1", Role: "assistant",
		Artifacts: types.MessageArtifacts{
			testArtifact("a.pptx", "/w/a.pptx", created),
			testArtifact("b.pptx", "/w/b.pptx", created),
		},
	})
	require.NoError(t, err)

	// Dropping the table mid-transaction is the cheapest way to make the
	// second statement fail after the first one succeeded.
	require.NoError(t, db.Exec(
		`CREATE TRIGGER fail_second BEFORE UPDATE ON message_artifacts
		 WHEN NEW.position = 1
		 BEGIN SELECT RAISE(ABORT, 'boom'); END`).Error)

	_, err = repo.SoftDeleteSessionArtifacts(ctx, "s1", []types.ArtifactRef{
		{MessageID: msg.ID, Position: 0},
		{MessageID: msg.ID, Position: 1},
	}, created)
	require.Error(t, err)

	require.NoError(t, db.Exec("DROP TRIGGER fail_second").Error)
	got, err := repo.GetMessage(ctx, "s1", msg.ID)
	require.NoError(t, err)
	require.False(t, got.Artifacts[0].Deleted(), "the first mark must roll back with the failed second one")
	require.False(t, got.Artifacts[1].Deleted())
}

// The last guard before reclaiming bytes: a fork's copied rows carry the
// parent's storage URLs without a binding of their own.
func TestCountLiveArtifactsByURLSpansSessions(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	created := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	shared := testArtifact("report.pptx", "/w/report.pptx", created)
	parent, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: "s1", RequestID: "r1", Role: "assistant",
		Artifacts: types.MessageArtifacts{shared},
	})
	require.NoError(t, err)
	// The fork copy: new session and message, same storage URL.
	forked, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: "s2", RequestID: "r2", Role: "assistant",
		Artifacts: types.MessageArtifacts{shared},
	})
	require.NoError(t, err)

	count, err := repo.CountLiveArtifactsByURL(ctx, shared.URL)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)

	_, err = repo.SoftDeleteSessionArtifacts(ctx, "s1",
		[]types.ArtifactRef{{MessageID: parent.ID, Position: 0}}, created)
	require.NoError(t, err)
	count, err = repo.CountLiveArtifactsByURL(ctx, shared.URL)
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "the fork still needs the bytes")

	_, err = repo.SoftDeleteSessionArtifacts(ctx, "s2",
		[]types.ArtifactRef{{MessageID: forked.ID, Position: 0}}, created)
	require.NoError(t, err)
	count, err = repo.CountLiveArtifactsByURL(ctx, shared.URL)
	require.NoError(t, err)
	require.EqualValues(t, 0, count, "now nothing points at them")
}
