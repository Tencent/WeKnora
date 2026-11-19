package background

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

var noticeSession = interfaces.SessionRef{TenantID: 1, SessionID: "s"}

func newNoticeFixture(t *testing.T) (*RunNotices, *MemoryActivityRegistry, *MemoryNoticeInbox) {
	t.Helper()
	reg := NewMemoryActivityRegistry(nil)
	inbox := NewMemoryNoticeInbox(nil)
	return NewRunNotices(noticeSession, reg, inbox), reg, inbox
}

func addRunning(t *testing.T, reg *MemoryActivityRegistry, id, title string, started time.Time) {
	t.Helper()
	require.NoError(t, reg.Add(context.Background(), noticeSession, interfaces.BackgroundActivity{
		ID: id, Kind: "shell_job", Title: title, StartedAt: started,
	}, time.Hour, 0))
}

func pushNotice(t *testing.T, inbox *MemoryNoticeInbox, id, text string) {
	t.Helper()
	_, err := inbox.Push(context.Background(), noticeSession, interfaces.BackgroundNotice{
		ID: id, Kind: "shell_job", Text: text, FinishedAt: time.Now(),
	})
	require.NoError(t, err)
}

func TestRunNoticesListsRunningWorkOnFirstRoundOnly(t *testing.T) {
	r, reg, _ := newNoticeFixture(t)
	base := time.Now()
	addRunning(t, reg, "sj_b", "tests", base.Add(time.Second))
	addRunning(t, reg, "sj_a", "build", base)

	first, err := r.RoundStart(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Background work still running in this session: build (sj_a), tests (sj_b).", first.Text)
	require.Empty(t, first.AckIDs)

	second, err := r.RoundStart(context.Background())
	require.NoError(t, err)
	require.Empty(t, second.Text)
}

func TestRunNoticesDeliversEachNoticeOnce(t *testing.T) {
	r, _, inbox := newNoticeFixture(t)
	pushNotice(t, inbox, "sj_1", "build (sj_1) succeeded.")

	got, err := r.RoundStart(context.Background())
	require.NoError(t, err)
	require.Equal(t, "build (sj_1) succeeded.", got.Text)
	require.Equal(t, []string{"sj_1"}, got.AckIDs)

	pushNotice(t, inbox, "sj_2", "tests (sj_2) failed.")
	got, err = r.BeforeFinal(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tests (sj_2) failed.", got.Text, "sj_1 was already delivered this run")
	require.Equal(t, []string{"sj_2"}, got.AckIDs)
}

func TestRunNoticesSkipsNoticesAToolAlreadyShowed(t *testing.T) {
	r, _, inbox := newNoticeFixture(t)
	pushNotice(t, inbox, "sj_1", "build (sj_1) succeeded.")

	r.MarkSeen("sj_1", "sj_1", "")
	require.Equal(t, []string{"sj_1"}, r.TakeSeen())
	require.Empty(t, r.TakeSeen())

	got, err := r.RoundStart(context.Background())
	require.NoError(t, err)
	require.Empty(t, got.Text)
	require.Empty(t, got.AckIDs)
}

func TestRunNoticesRemindsOnlyWorkThisRunStarted(t *testing.T) {
	r, reg, _ := newNoticeFixture(t)
	base := time.Now()
	addRunning(t, reg, "sj_old", "dev server", base)
	addRunning(t, reg, "sj_new", "build", base.Add(time.Second))
	r.MarkStarted("sj_new")

	got, err := r.BeforeFinal(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Background work you started in this turn is still running: build (sj_new). "+
		"Finish your answer. Its result will arrive when it completes. "+
		"Call work_wait only if you cannot answer without that output.", got.Text)

	again, err := r.BeforeFinal(context.Background())
	require.NoError(t, err)
	require.Empty(t, again.Text, "each item is reminded once")
}

func TestRunNoticesPutsNoticesBeforeOtherLines(t *testing.T) {
	r, reg, inbox := newNoticeFixture(t)
	addRunning(t, reg, "sj_a", "build", time.Now())
	pushNotice(t, inbox, "sj_0", "lint (sj_0) succeeded.")

	got, err := r.RoundStart(context.Background())
	require.NoError(t, err)
	require.Equal(t, "lint (sj_0) succeeded.\nBackground work still running in this session: build (sj_a).", got.Text)
}

type failingRegistry struct {
	interfaces.ActivityRegistry
	err error
}

func (f failingRegistry) List(context.Context, interfaces.SessionRef) ([]interfaces.BackgroundActivity, error) {
	return nil, f.err
}

func TestRunNoticesRetriesTheListingAfterAnError(t *testing.T) {
	reg := NewMemoryActivityRegistry(nil)
	r := NewRunNotices(noticeSession, failingRegistry{ActivityRegistry: reg, err: errors.New("redis down")},
		NewMemoryNoticeInbox(nil))
	addRunning(t, reg, "sj_a", "build", time.Now())

	_, err := r.RoundStart(context.Background())
	require.Error(t, err)

	r.registry = reg // the store recovers
	got, err := r.RoundStart(context.Background())
	require.NoError(t, err)
	require.Contains(t, got.Text, "build (sj_a)", "a failed first boundary must not use up the listing")
}
