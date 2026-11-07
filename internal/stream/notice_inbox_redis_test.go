package stream

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/background/backgroundtest"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestRedisNoticeInboxContract(t *testing.T) {
	backgroundtest.RunNoticeInboxContract(t,
		func(t *testing.T) (interfaces.NoticeInbox, backgroundtest.Advance) {
			mgr, mini := newTestRedisStreamManager(t, time.Hour)
			now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
			advance := func(d time.Duration) {
				now = now.Add(d)
				mini.FastForward(d)
			}
			return NewRedisNoticeInbox(mgr, func() time.Time { return now }), advance
		})
}

// The write and the TTL refresh happen in one script, so a stored notice
// always carries the backstop TTL, and a repeated push still refreshes it.
func TestRedisNoticeInboxPushSetsKeyTTL(t *testing.T) {
	mgr, mini := newTestRedisStreamManager(t, time.Hour)
	inbox := NewRedisNoticeInbox(mgr, nil)
	ctx := context.Background()
	s := interfaces.SessionRef{TenantID: 1, SessionID: "sess-1"}
	n := interfaces.BackgroundNotice{ID: "sj_1", Kind: "shell", Text: "done", FinishedAt: time.Now()}

	first, err := inbox.Push(ctx, s, n)
	require.NoError(t, err)
	require.True(t, first)
	require.Equal(t, noticeKeyTTL, mini.TTL(inbox.key(s)))

	mini.FastForward(time.Hour)
	first, err = inbox.Push(ctx, s, n)
	require.NoError(t, err)
	require.False(t, first)
	require.Equal(t, noticeKeyTTL, mini.TTL(inbox.key(s)))
}

// Pending reads, then deletes what it saw as expired. A notice acked and
// pushed again under the same ID in between must survive that delete.
func TestRedisNoticeInboxExpiryKeepsNoticeRewrittenAfterRead(t *testing.T) {
	mgr, mini := newTestRedisStreamManager(t, time.Hour)
	inbox := NewRedisNoticeInbox(mgr, nil)
	ctx := context.Background()
	s := interfaces.SessionRef{TenantID: 1, SessionID: "sess-1"}
	key := inbox.key(s)

	old := time.Now().Add(-2 * interfaces.NoticeRetention)
	_, err := inbox.Push(ctx, s, interfaces.BackgroundNotice{ID: "sj_1", Kind: "shell", Text: "old", FinishedAt: old})
	require.NoError(t, err)
	_, err = inbox.Push(ctx, s, interfaces.BackgroundNotice{ID: "sj_2", Kind: "shell", Text: "old", FinishedAt: old})
	require.NoError(t, err)
	read := map[string]string{"sj_1": mini.HGet(key, "sj_1"), "sj_2": mini.HGet(key, "sj_2")}

	require.NoError(t, inbox.Ack(ctx, s, []string{"sj_1"}))
	fresh := interfaces.BackgroundNotice{ID: "sj_1", Kind: "shell", Text: "fresh", FinishedAt: time.Now()}
	first, err := inbox.Push(ctx, s, fresh)
	require.NoError(t, err)
	require.True(t, first)

	require.NoError(t, inbox.dropIfUnchanged(ctx, key, read))

	got, err := inbox.Pending(ctx, s)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "sj_1", got[0].ID)
	require.Equal(t, "fresh", got[0].Text)
	require.Empty(t, mini.HGet(key, "sj_2"), "an expired notice that did not change must be dropped")
}
