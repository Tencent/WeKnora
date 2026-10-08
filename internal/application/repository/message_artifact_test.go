package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newArtifactTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Session{}, &types.Message{}, &types.MessageArtifactRecord{}, &testIMChannelSession{},
	))
	return db
}

func testArtifact(name, sourcePath string, created time.Time) types.MessageArtifact {
	return types.MessageArtifact{
		URL:        "local://blobs/" + name,
		FileName:   name,
		FileType:   ".pptx",
		FileSize:   1024,
		SourcePath: sourcePath,
		// Nanoseconds on purpose: the collector matches files on the exact mtime.
		ModTime:   time.Date(2026, 9, 1, 10, 0, 0, 123456789, time.FixedZone("CST", 8*3600)),
		CreatedAt: created,
	}
}

func createSession(t *testing.T, db *gorm.DB, s *types.Session) string {
	t.Helper()
	require.NoError(t, db.Create(s).Error)
	return s.ID
}

func TestMessageArtifactsRoundTripThroughTable(t *testing.T) {
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

	var stored int64
	require.NoError(t, db.Model(&types.MessageArtifactRecord{}).Where("message_id = ?", msg.ID).Count(&stored).Error)
	require.EqualValues(t, 2, stored, "artifacts are written to message_artifacts")

	got, err := repo.GetMessage(ctx, "s1", msg.ID)
	require.NoError(t, err)
	require.Len(t, got.Artifacts, 2)
	require.Equal(t, "a.pptx", got.Artifacts[0].FileName)
	require.Equal(t, "b.pptx", got.Artifacts[1].FileName)
	require.True(t, got.Artifacts[0].ModTime.Equal(testArtifact("", "", created).ModTime),
		"mtime survives at nanosecond precision")
	require.True(t, got.Artifacts[0].CreatedAt.Equal(created))

	// A partial struct (Artifacts nil) must not wipe the stored rows.
	require.NoError(t, repo.UpdateMessage(ctx, &types.Message{ID: msg.ID, SessionID: "s1", Content: "edited"}))
	got, err = repo.GetMessage(ctx, "s1", msg.ID)
	require.NoError(t, err)
	require.Equal(t, "edited", got.Content)
	require.Len(t, got.Artifacts, 2)

	// A non-nil list replaces them.
	got.Artifacts = types.MessageArtifacts{testArtifact("c.pptx", "/w/c.pptx", created)}
	require.NoError(t, repo.UpdateMessage(ctx, got))
	listed, err := repo.GetMessagesBySession(ctx, "s1", 1, 10)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Len(t, listed[0].Artifacts, 1)
	require.Equal(t, "c.pptx", listed[0].Artifacts[0].FileName)

	session, err := repo.GetSessionArtifacts(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, session, 1)

	// Soft-deleted messages drop out of the session listing.
	require.NoError(t, repo.DeleteMessage(ctx, "s1", msg.ID))
	session, err = repo.GetSessionArtifacts(ctx, "s1")
	require.NoError(t, err)
	require.Empty(t, session)
}

func TestMessagesWithoutArtifactsLoadAsEmpty(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()

	msg, err := repo.CreateMessage(ctx, &types.Message{SessionID: "s1", RequestID: "r1", Role: "user"})
	require.NoError(t, err)
	got, err := repo.GetMessage(ctx, "s1", msg.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Artifacts)
	require.Empty(t, got.Artifacts)
}

func TestRecordRestoredArtifactMtimeUpdatesMatchingRows(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db).(*messageRepository)
	ctx := context.Background()
	created := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	withHash := func(a types.MessageArtifact, hash string) types.MessageArtifact {
		a.ContentHash = hash
		return a
	}
	v1 := withHash(testArtifact("a.pptx", "/w/a.pptx", created), "hash-a")
	v2 := withHash(testArtifact("a.pptx", "/w/a.pptx", created.Add(time.Hour)), "hash-b")
	v2.URL = "local://blobs/a-v2.pptx"
	msg, err := repo.CreateMessage(ctx, &types.Message{
		SessionID: "s1", RequestID: "r1", Role: "assistant",
		Artifacts: types.MessageArtifacts{v1, v2, testArtifact("b.pptx", "/w/b.pptx", created)},
	})
	require.NoError(t, err)
	originalMod := testArtifact("", "", created).ModTime

	restored := time.Date(2026, 9, 2, 8, 30, 0, 987654321, time.UTC)
	require.NoError(t, repo.RecordRestoredArtifactMtime(ctx, "s1", "/w/a.pptx", restored, "hash-a"))
	// An empty hash never matches, so it must change nothing.
	require.NoError(t, repo.RecordRestoredArtifactMtime(ctx, "s1", "/w/b.pptx", restored, ""))

	got, err := repo.GetMessage(ctx, "s1", msg.ID)
	require.NoError(t, err)
	require.True(t, got.Artifacts[0].ModTime.Equal(restored), "the version whose content matches gets the mtime")
	require.Equal(t, "hash-a", got.Artifacts[0].ContentHash)
	require.True(t, got.Artifacts[1].ModTime.Equal(originalMod), "other versions at the path stay put")
	require.Equal(t, "hash-b", got.Artifacts[1].ContentHash, "hashes are never rewritten")
	require.True(t, got.Artifacts[2].ModTime.Equal(originalMod), "empty-hash restores are ignored")
}

func TestCreateForkedCopiesArtifactRows(t *testing.T) {
	db := newArtifactTestDB(t)
	sessions := NewSessionRepository(db)
	messages := NewMessageRepository(db)
	ctx := context.Background()
	created := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	fork := &types.Session{ID: "fork-1", TenantID: 1, UserID: "u1", Title: "fork"}
	copied := []*types.Message{{
		ID: "copy-1", SessionID: "fork-1", Role: "assistant", CreatedAt: created,
		Artifacts: types.MessageArtifacts{testArtifact("a.pptx", "/w/a.pptx", created)},
	}}
	require.NoError(t, sessions.CreateForked(ctx, fork, copied))

	got, err := messages.GetMessage(ctx, "fork-1", "copy-1")
	require.NoError(t, err)
	require.Len(t, got.Artifacts, 1)
	require.Equal(t, "local://blobs/a.pptx", got.Artifacts[0].URL, "the fork points at the same blob")
}

func TestListArtifactLibraryScopesAndGroupsVersions(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)

	mine := createSession(t, db, &types.Session{TenantID: 7, UserID: "alice", Title: "我的会话"})
	legacy := createSession(t, db, &types.Session{TenantID: 7, Title: "旧会话"})
	bobs := createSession(t, db, &types.Session{TenantID: 7, UserID: "bob", Title: "Bob"})
	otherTenant := createSession(t, db, &types.Session{TenantID: 8, UserID: "alice", Title: "别的空间"})
	maintenance := createSession(t, db, &types.Session{
		TenantID: 7, UserID: "alice", Title: "维护", Description: types.SkillMaintenanceSessionMarker + "x",
	})
	// IM sessions are created without an owner, so they look like legacy
	// tenant-level rows; only the IM mapping tells them apart. A cleared chat
	// keeps its soft-deleted mapping and must stay out as well.
	imChat := createSession(t, db, &types.Session{TenantID: 7, Title: "飞书：张三的私聊"})
	clearedIMChat := createSession(t, db, &types.Session{TenantID: 7, Title: "企微：已清空"})
	embedChat := createSession(t, db, &types.Session{
		TenantID: 7, Title: "网页挂件", Description: types.EmbedSessionMarkerPrefix + "ch-1",
	})
	require.NoError(t, db.Create(&testIMChannelSession{
		ID: "ics-1", SessionID: imChat, Platform: "feishu", TenantID: 7,
	}).Error)
	require.NoError(t, db.Create(&testIMChannelSession{
		ID: "ics-2", SessionID: clearedIMChat, Platform: "wecom", TenantID: 7,
	}).Error)
	require.NoError(t, db.Delete(&testIMChannelSession{ID: "ics-2"}).Error)

	add := func(sessionID string, at time.Time, arts ...types.MessageArtifact) *types.Message {
		m, err := repo.CreateMessage(ctx, &types.Message{
			SessionID: sessionID, RequestID: at.String(), Role: "assistant", CreatedAt: at, Artifacts: arts,
		})
		require.NoError(t, err)
		return m
	}
	// report.pptx regenerated twice in the same session: one item, 2 versions.
	add(mine, base, testArtifact("report.pptx", "/w/report.pptx", base))
	// Each regeneration is persisted as a new blob.
	regenerated := testArtifact("report.pptx", "/w/report.pptx", base.Add(time.Hour))
	regenerated.URL = "local://blobs/report-v2.pptx"
	latest := add(mine, base.Add(time.Hour),
		regenerated,
		types.MessageArtifact{
			URL: "local://blobs/data.csv", FileName: "data.csv", FileType: ".csv",
			SourcePath: "/w/data.csv", CreatedAt: base.Add(2 * time.Hour),
		},
	)
	// A later answer that references old.pptx again stores a second row with
	// the same URL; that is not a new version.
	oldDeck := testArtifact("old.pptx", "/w/old.pptx", base.Add(3*time.Hour))
	add(legacy, base.Add(3*time.Hour), oldDeck)
	add(legacy, base.Add(3*time.Hour+time.Minute), oldDeck)
	add(bobs, base, testArtifact("bob.pptx", "/w/bob.pptx", base))
	add(otherTenant, base, testArtifact("elsewhere.pptx", "/w/e.pptx", base))
	add(maintenance, base, testArtifact("maint.pptx", "/w/m.pptx", base))
	add(imChat, base.Add(5*time.Hour), testArtifact("salary.xlsx", "/w/salary.xlsx", base.Add(5*time.Hour)))
	add(clearedIMChat, base.Add(5*time.Hour), testArtifact("cleared.xlsx", "/w/c.xlsx", base.Add(5*time.Hour)))
	add(embedChat, base.Add(5*time.Hour), testArtifact("widget.pdf", "/w/widget.pdf", base.Add(5*time.Hour)))
	deleted := add(mine, base.Add(4*time.Hour), testArtifact("gone.pptx", "/w/gone.pptx", base.Add(4*time.Hour)))
	require.NoError(t, repo.DeleteMessage(ctx, mine, deleted.ID))

	items, total, err := repo.ListArtifactLibrary(ctx, &types.ArtifactLibraryQuery{
		TenantID: 7, UserID: "alice", Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Len(t, items, 3)
	require.Equal(t, []string{"old.pptx", "data.csv", "report.pptx"},
		[]string{items[0].FileName, items[1].FileName, items[2].FileName}, "newest first")
	require.Equal(t, "旧会话", items[0].SessionTitle)
	require.Equal(t, 1, items[0].VersionCount, "re-referencing a file does not add a version")

	report := items[2]
	require.Equal(t, 2, report.VersionCount)
	require.Equal(t, latest.ID, report.MessageID, "the latest version wins")
	require.Equal(t, 0, report.Index)
	require.Equal(t, mine, report.SessionID)
	require.Equal(t, 1, items[1].Index, "index is the position inside the owning message")

	// Filters.
	items, total, err = repo.ListArtifactLibrary(ctx, &types.ArtifactLibraryQuery{
		TenantID: 7, UserID: "alice", FileTypes: []string{".csv"}, Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "data.csv", items[0].FileName)

	items, total, err = repo.ListArtifactLibrary(ctx, &types.ArtifactLibraryQuery{
		TenantID: 7, UserID: "alice", Keyword: "REPORT", Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "report.pptx", items[0].FileName)

	// Paging keeps the total.
	items, total, err = repo.ListArtifactLibrary(ctx, &types.ArtifactLibraryQuery{
		TenantID: 7, UserID: "alice", Page: 2, PageSize: 2,
	})
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Len(t, items, 1)
	require.Equal(t, "report.pptx", items[0].FileName)
}
