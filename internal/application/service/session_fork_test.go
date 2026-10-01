package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type fakeForkSandboxPort struct {
	boundID        string
	bound          bool
	activeTurn     bool
	activeTurnErr  error
	forceBusyAfter int
	hasActiveCalls int
	snapshotID     string
	snapshotErr    error
	snapshotCalls  int
	snapshotNameIn string
	cancelParent   context.CancelFunc
	snapshotCtxErr error
	deleted        []string
	deleteErr      error
}

func (f *fakeForkSandboxPort) BoundSandboxID(context.Context, string) (string, bool) {
	return f.boundID, f.bound
}

func (f *fakeForkSandboxPort) HasActiveTurn(context.Context, string) (bool, error) {
	f.hasActiveCalls++
	if f.activeTurnErr != nil {
		return false, f.activeTurnErr
	}
	if f.forceBusyAfter > 0 && f.hasActiveCalls >= f.forceBusyAfter {
		return true, nil
	}
	return f.activeTurn, nil
}

func (f *fakeForkSandboxPort) CreateForkSnapshot(ctx context.Context, _, name string) (string, error) {
	f.snapshotCalls++
	f.snapshotNameIn = name
	if f.cancelParent != nil {
		f.cancelParent()
	}
	f.snapshotCtxErr = ctx.Err()
	if f.snapshotErr != nil {
		return "", f.snapshotErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return f.snapshotID, nil
}

func (f *fakeForkSandboxPort) DeleteForkSnapshot(_ context.Context, _, snapshotID string) error {
	f.deleted = append(f.deleted, snapshotID)
	return f.deleteErr
}

// fakeSessionStore is the narrow session port SessionForkService needs.
type fakeSessionStore struct {
	source           *types.Session
	created          *types.Session
	copiedMessages   []*types.Message
	copiedOrigIDs    []string
	origIDsFrom      *fakeMessageStore
	getErr           error
	updatedBootstrap *types.ForkBootstrap
	bootstrapCleared bool
	clearErr         error
	updateErr        error
	unconsumed       []*types.Session
	createErr        error
	leases           []*types.ForkSnapshotLease
}

func newFakeSessionStore(src *types.Session) *fakeSessionStore {
	return &fakeSessionStore{source: src}
}

func (f *fakeSessionStore) GetByID(_ context.Context, tenantID uint64, id string) (*types.Session, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.source == nil || f.source.ID != id || f.source.TenantID != tenantID {
		return nil, nil
	}
	return f.source, nil
}

func (f *fakeSessionStore) UpdateForkBootstrap(_ context.Context, _ string, b *types.ForkBootstrap) error {
	if b == nil {
		if f.clearErr != nil {
			return f.clearErr
		}
		f.bootstrapCleared = true
		f.updatedBootstrap = nil
		if f.source != nil {
			f.source.ForkBootstrap = nil
		}
		return nil
	}
	if f.updateErr != nil {
		return f.updateErr
	}
	copied := *b
	f.updatedBootstrap = &copied
	if f.source != nil {
		f.source.ForkBootstrap = &copied
	}
	return nil
}

func (f *fakeSessionStore) ListUnconsumedForks(_ context.Context, _ time.Time) ([]*types.Session, error) {
	return f.unconsumed, nil
}

func (f *fakeSessionStore) UnconsumedForkSnapshotHolders(_ context.Context, snapshotID string) ([]string, error) {
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return nil, nil
	}
	seen := map[string]struct{}{}
	var ids []string
	candidates := []*types.Session{f.source, f.created}
	candidates = append(candidates, f.unconsumed...)
	for _, s := range candidates {
		if s == nil || s.ForkBootstrap == nil || s.ForkBootstrap.Consumed() {
			continue
		}
		if s.ForkBootstrap.SnapshotID != snapshotID {
			continue
		}
		if _, ok := seen[s.ID]; ok {
			continue
		}
		seen[s.ID] = struct{}{}
		ids = append(ids, s.ID)
	}
	return ids, nil
}

func (f *fakeSessionStore) HasOtherUnconsumedForkSnapshot(
	ctx context.Context, snapshotID, excludeSessionID string,
) (bool, error) {
	holders, err := f.UnconsumedForkSnapshotHolders(ctx, snapshotID)
	if err != nil {
		return false, err
	}
	for _, id := range holders {
		if id != excludeSessionID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeSessionStore) CreateForkSnapshotLease(_ context.Context, lease *types.ForkSnapshotLease) error {
	if lease == nil {
		return nil
	}
	copied := *lease
	f.leases = append(f.leases, &copied)
	return nil
}

func (f *fakeSessionStore) DeleteForkSnapshotLease(_ context.Context, snapshotID string) error {
	out := f.leases[:0]
	for _, lease := range f.leases {
		if lease != nil && lease.SnapshotID != snapshotID {
			out = append(out, lease)
		}
	}
	f.leases = out
	return nil
}

func (f *fakeSessionStore) ListStaleForkSnapshotLeases(
	_ context.Context, olderThan time.Time,
) ([]*types.ForkSnapshotLease, error) {
	var out []*types.ForkSnapshotLease
	for _, lease := range f.leases {
		if lease == nil || lease.CreatedAt.After(olderThan) {
			continue
		}
		out = append(out, lease)
	}
	return out, nil
}

func (f *fakeSessionStore) CreateForked(ctx context.Context, session *types.Session, messages []*types.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.createErr != nil {
		return f.createErr
	}
	f.created = session
	f.copiedMessages = append([]*types.Message(nil), messages...)
	// Copies already have fresh IDs; original IDs come from the listing the
	// service used as the copy source.
	if f.origIDsFrom != nil {
		f.copiedOrigIDs = append([]string(nil), f.origIDsFrom.lastListedIDs...)
	}
	return nil
}

func (f *fakeSessionStore) copiedFromIDs() []string {
	return f.copiedOrigIDs
}

// fakeMessageStore is the narrow message port SessionForkService needs.
type fakeMessageStore struct {
	messages            []*types.Message
	lastListedIDs       []string
	rewriteErr          error
	deleteFromCalls     int
	lastDeleteInclusive *bool
	deleteFromErr       error
	deleteFromFailTimes int
	tombstoned          []types.ArtifactRef
	liveArtifacts       []types.MessageArtifactRecord
}

func newFakeMessageStore(messages []*types.Message) *fakeMessageStore {
	return &fakeMessageStore{messages: messages}
}

func (f *fakeMessageStore) GetMessage(_ context.Context, sessionID, messageID string) (*types.Message, error) {
	for _, m := range f.messages {
		if m != nil && m.SessionID == sessionID && m.ID == messageID {
			return m, nil
		}
	}
	return nil, nil
}

func (f *fakeMessageStore) GetMessagesBySession(
	_ context.Context, sessionID string, page, pageSize int,
) ([]*types.Message, error) {
	var out []*types.Message
	for _, m := range f.messages {
		if m != nil && m.SessionID == sessionID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		return out, nil
	}
	start := (page - 1) * pageSize
	if start >= len(out) {
		return []*types.Message{}, nil
	}
	end := start + pageSize
	if end > len(out) {
		end = len(out)
	}
	return out[start:end], nil
}

func (f *fakeMessageStore) ListMessagesBySessionUpTo(
	_ context.Context, sessionID string, boundary time.Time, boundaryID string,
) ([]*types.Message, error) {
	var out []*types.Message
	for _, m := range f.messages {
		if m == nil || m.SessionID != sessionID {
			continue
		}
		if m.CreatedAt.Before(boundary) || (m.CreatedAt.Equal(boundary) && m.ID < boundaryID) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	f.lastListedIDs = make([]string, 0, len(out))
	for _, m := range out {
		f.lastListedIDs = append(f.lastListedIDs, m.ID)
	}
	return out, nil
}

func (f *fakeMessageStore) GetSessionArtifacts(ctx context.Context, sessionID string) (types.MessageArtifacts, error) {
	messages, err := f.GetMessagesBySession(ctx, sessionID, 1, 0)
	if err != nil {
		return nil, err
	}
	var out types.MessageArtifacts
	for _, m := range messages {
		if m == nil || len(m.Artifacts) == 0 {
			continue
		}
		out = append(out, m.Artifacts...)
	}
	if out == nil {
		out = types.MessageArtifacts{}
	}
	return out, nil
}

func (f *fakeMessageStore) DeleteMessagesFrom(
	ctx context.Context, sessionID string, boundary time.Time, boundaryID string, inclusive bool,
) ([]*types.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.deleteFromCalls++
	inc := inclusive
	f.lastDeleteInclusive = &inc
	if f.deleteFromErr != nil {
		return nil, f.deleteFromErr
	}
	if f.deleteFromFailTimes > 0 {
		f.deleteFromFailTimes--
		return nil, errors.New("delete messages: temporary failure")
	}
	var deleted, kept []*types.Message
	for _, m := range f.messages {
		if m == nil {
			continue
		}
		if m.SessionID != sessionID {
			kept = append(kept, m)
			continue
		}
		after := m.CreatedAt.After(boundary) || (m.CreatedAt.Equal(boundary) && m.ID > boundaryID)
		atBoundary := m.CreatedAt.Equal(boundary) && m.ID == boundaryID
		if after || (inclusive && atBoundary) {
			deleted = append(deleted, m)
			for i, art := range m.Artifacts {
				if art.DeletedAt != nil {
					continue
				}
				f.liveArtifacts = append(f.liveArtifacts, types.MessageArtifactRecord{
					SessionID: sessionID,
					MessageID: m.ID,
					Position:  i,
					URL:       art.URL,
					FileName:  art.FileName,
				})
			}
			continue
		}
		kept = append(kept, m)
	}
	sort.Slice(deleted, func(i, j int) bool {
		if !deleted[i].CreatedAt.Equal(deleted[j].CreatedAt) {
			return deleted[i].CreatedAt.Before(deleted[j].CreatedAt)
		}
		return deleted[i].ID < deleted[j].ID
	})
	f.messages = kept
	return deleted, nil
}

func (f *fakeMessageStore) GetRecentMessagesBySession(
	ctx context.Context, sessionID string, limit int,
) ([]*types.Message, error) {
	all, err := f.GetMessagesBySession(ctx, sessionID, 1, 0)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || len(all) <= limit {
		return all, nil
	}
	return all[len(all)-limit:], nil
}

func (f *fakeMessageStore) SessionHasIncompleteAssistant(
	_ context.Context, sessionID string,
) (bool, error) {
	for _, m := range f.messages {
		if m != nil && m.SessionID == sessionID && m.Role == "assistant" && !m.IsCompleted {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeMessageStore) ListLiveArtifactsByMessageIDs(
	_ context.Context, sessionID string, messageIDs []string,
) ([]types.MessageArtifactRecord, error) {
	want := make(map[string]struct{}, len(messageIDs))
	for _, id := range messageIDs {
		if id != "" {
			want[id] = struct{}{}
		}
	}
	var out []types.MessageArtifactRecord
	for _, row := range f.liveArtifacts {
		if row.SessionID != sessionID {
			continue
		}
		if _, ok := want[row.MessageID]; ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeMessageStore) SoftDeleteSessionArtifacts(
	_ context.Context, _ string, refs []types.ArtifactRef, _ time.Time,
) ([]types.ArtifactRef, error) {
	f.tombstoned = append(f.tombstoned, refs...)
	return refs, nil
}

func (f *fakeMessageStore) RewriteSandboxCheckpoints(_ context.Context, sessionID, oldID, newID string) error {
	if f.rewriteErr != nil {
		return f.rewriteErr
	}
	if sessionID == "" || oldID == "" || newID == "" || oldID == newID {
		return nil
	}
	for _, m := range f.messages {
		if m == nil || m.SessionID != sessionID || m.SandboxCheckpoint == nil {
			continue
		}
		if m.SandboxCheckpoint.SandboxID != oldID {
			continue
		}
		cp := *m.SandboxCheckpoint
		cp.SandboxID = newID
		m.SandboxCheckpoint = &cp
	}
	return nil
}

var (
	_ forkSessionStore          = (*fakeSessionStore)(nil)
	_ forkBootstrapSessionStore = (*fakeSessionStore)(nil)
	_ reaperSessionStore        = (*fakeSessionStore)(nil)
	_ forkMessageStore          = (*fakeMessageStore)(nil)
	_ forkBootstrapMessageStore = (*fakeMessageStore)(nil)
	_ rewindSessionStore        = (*fakeSessionStore)(nil)
	_ rewindMessageStore        = (*fakeMessageStore)(nil)
	_ SessionForkSandboxPort    = (*fakeForkSandboxPort)(nil)
)

// --- fixture helpers -------------------------------------------------------

var forkBase = time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

func checkpointedTurn(userID, assistantID, sandboxID, sha string, offset time.Duration) []*types.Message {
	return []*types.Message{
		{ID: userID, SessionID: "src", Role: "user", CreatedAt: forkBase.Add(offset)},
		{
			ID: assistantID, SessionID: "src", Role: "assistant",
			CreatedAt: forkBase.Add(offset + time.Second),
			SandboxCheckpoint: &types.SandboxCheckpoint{
				SandboxID: sandboxID, CommitSHA: sha, CommittedAt: forkBase.Add(offset + time.Second),
			},
		},
	}
}

func newForkFixture(t *testing.T, port SessionForkSandboxPort, messages []*types.Message) (
	*SessionForkService, *fakeSessionStore, *fakeMessageStore,
) {
	t.Helper()
	sessions := newFakeSessionStore(&types.Session{
		ID: "src", TenantID: 1, UserID: "u1", Title: "原会话", SandboxConfigID: "cfg-1",
	})
	msgs := newFakeMessageStore(messages)
	sessions.origIDsFrom = msgs
	return NewSessionForkService(sessions, msgs, port), sessions, msgs
}

// hostForkSandboxPort is a fork sandbox that does not version the workspace.
// Host backends share the user's real directory across sessions, so fork must
// copy messages only — never snapshot or roll the tree back.
type hostForkSandboxPort struct {
	fakeForkSandboxPort
}

func (h *hostForkSandboxPort) VersionsWorkspace(context.Context, string) bool {
	return false
}

// --- tests -----------------------------------------------------------------

func TestForkHappyPathTakesSnapshotAndCopiesHistory(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.False(t, got.Degraded)
	require.Empty(t, got.Reason)
	require.Equal(t, 1, port.snapshotCalls)

	created := sessions.created
	require.NotNil(t, created)
	require.Equal(t, "src", created.ParentSessionID)
	require.Equal(t, "u-msg-2", created.ForkedFromMessageID)
	require.Equal(t, "cfg-1", created.SandboxConfigID, "必须继承 sandbox_config_id，否则可能落到别的 backend")
	require.Equal(t, "原会话（分支）", created.Title)
	require.NotNil(t, created.ForkBootstrap)
	require.Equal(t, "snap-1", created.ForkBootstrap.SnapshotID)
	require.Equal(t, "sha1", created.ForkBootstrap.CommitSHA)
	require.Equal(t, "sbx-1", created.ForkBootstrap.SourceSandboxID)
	require.False(t, created.ForkBootstrap.Consumed())

	// 只复制分叉点之前的消息，且都换了新 ID
	require.Equal(t, []string{"u-msg-1", "a-msg-1"}, sessions.copiedFromIDs())
	for _, m := range sessions.copiedMessages {
		require.NotEmpty(t, m.ID)
		require.Equal(t, created.ID, m.SessionID)
	}
}

func TestCopyMessagesIntoRemapsRequestIDsPerTurn(t *testing.T) {
	history := []*types.Message{
		{ID: "u1", SessionID: "src", Role: "user", RequestID: "req-a", Content: "q1"},
		{ID: "a1", SessionID: "src", Role: "assistant", RequestID: "req-a", Content: "a1"},
		{ID: "u2", SessionID: "src", Role: "user", RequestID: "req-b", Content: "q2"},
		{ID: "a2", SessionID: "src", Role: "assistant", RequestID: "req-b", Content: "a2"},
		{ID: "u3", SessionID: "src", Role: "user", Content: "no-request-id"},
	}

	copies := copyMessagesInto("fork-1", history)

	require.Len(t, copies, 5)
	require.NotEqual(t, "req-a", copies[0].RequestID)
	require.Equal(t, copies[0].RequestID, copies[1].RequestID, "same turn must still pair")
	require.NotEqual(t, copies[0].RequestID, copies[2].RequestID, "distinct turns must not share an ID")
	require.Equal(t, copies[2].RequestID, copies[3].RequestID)
	require.Empty(t, copies[4].RequestID)
	require.Equal(t, "fork-1", copies[0].SessionID)
	require.NotEqual(t, "u1", copies[0].ID)
}

func TestForkCopiesArtifactsAndAttachmentsOntoNewSession(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	turn[0].Attachments = types.MessageAttachments{{
		ID: "doc-1", FileName: "brief.pdf", FileType: ".pdf", FileSize: 12,
	}}
	turn[1].Artifacts = types.MessageArtifacts{{
		URL:        "local://tenant/deck",
		FileName:   "deck.pptx",
		FileType:   ".pptx",
		FileSize:   32,
		SourcePath: "/workspace/output/deck.pptx",
	}}
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	var copiedUser, copiedAssistant *types.Message
	for _, m := range sessions.copiedMessages {
		switch m.Role {
		case "user":
			copiedUser = m
		case "assistant":
			copiedAssistant = m
		}
	}
	require.NotNil(t, copiedUser)
	require.Equal(t, turn[0].Attachments, copiedUser.Attachments)
	require.NotNil(t, copiedAssistant)
	require.Equal(t, turn[1].Artifacts, copiedAssistant.Artifacts)
}

func TestForkUsesProvidedTitle(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "我的分支")

	require.NoError(t, err)
	require.Equal(t, "我的分支", sessions.created.Title)
}

// 分叉点是第一条 user 消息：前面本就无产物，全新沙箱才是正确结果，不算降级。
func TestForkAtFirstUserMessageIsNotDegraded(t *testing.T) {
	first := &types.Message{ID: "u-msg-1", SessionID: "src", Role: "user", CreatedAt: forkBase}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, []*types.Message{first})

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-1", "")

	require.NoError(t, err)
	require.False(t, got.Degraded)
	require.Empty(t, got.Reason)
	require.Zero(t, port.snapshotCalls, "没有前置产物就不该浪费一个快照")
	require.Nil(t, sessions.created.ForkBootstrap)
	require.Empty(t, sessions.copiedFromIDs())
}

func TestForkDegradesWhenPrecedingTurnHasNoCheckpoint(t *testing.T) {
	messages := []*types.Message{
		{ID: "u-msg-1", SessionID: "src", Role: "user", CreatedAt: forkBase},
		{ID: "a-msg-1", SessionID: "src", Role: "assistant", CreatedAt: forkBase.Add(time.Second)},
		{ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second)},
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, messages)

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.True(t, got.Degraded)
	require.Equal(t, ForkDegradeNoCheckpoint, got.Reason)
	require.Zero(t, port.snapshotCalls)
	require.Nil(t, sessions.created.ForkBootstrap)
	require.Equal(t, []string{"u-msg-1", "a-msg-1"}, sessions.copiedFromIDs(), "降级也要复制消息")
}

// 会话中途换过沙箱：旧 sha 在新沙箱的仓库里根本不存在。
func TestForkDegradesWhenSandboxWasReplaced(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-OLD", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-NEW", bound: true, snapshotID: "snap-1"}
	svc, _, _ := newForkFixture(t, port, append(turn, forkPoint))

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.True(t, got.Degraded)
	require.Equal(t, ForkDegradeSandboxReplaced, got.Reason)
	require.Zero(t, port.snapshotCalls)
}

func TestForkDegradesWhenSandboxGone(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{bound: false}
	svc, _, _ := newForkFixture(t, port, append(turn, forkPoint))

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.True(t, got.Degraded)
	require.Equal(t, ForkDegradeSandboxGone, got.Reason)
}

func TestForkInheritsUnconsumedBootstrapWhenSourceHasNoSandbox(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha-early", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{bound: false, snapshotID: "must-not-create"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))
	sessions.source.ForkBootstrap = &types.ForkBootstrap{
		SnapshotID:      "snap-shared",
		CommitSHA:       "sha-later",
		SourceSandboxID: "sbx-1",
		CreatedAt:       forkBase,
	}

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.False(t, got.Degraded)
	require.Zero(t, port.snapshotCalls, "unused fork has no live sandbox to snapshot")
	require.NotNil(t, sessions.created.ForkBootstrap)
	require.Equal(t, "snap-shared", sessions.created.ForkBootstrap.SnapshotID)
	require.Equal(t, "sha-early", sessions.created.ForkBootstrap.CommitSHA)
	require.Equal(t, "sbx-1", sessions.created.ForkBootstrap.SourceSandboxID)
	require.False(t, sessions.created.ForkBootstrap.Consumed())
}

func TestForkDoesNotInheritConsumedBootstrapWhenSandboxGone(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	consumedAt := forkBase.Add(time.Minute)
	port := &fakeForkSandboxPort{bound: false}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))
	sessions.source.ForkBootstrap = &types.ForkBootstrap{
		SnapshotID:      "snap-old",
		CommitSHA:       "sha1",
		SourceSandboxID: "sbx-1",
		CreatedAt:       forkBase,
		ConsumedAt:      &consumedAt,
	}

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.True(t, got.Degraded)
	require.Equal(t, ForkDegradeSandboxGone, got.Reason)
	require.Zero(t, port.snapshotCalls)
}

func TestForkDegradesWhenSnapshotFails(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{
		boundID: "sbx-1", bound: true, snapshotErr: errors.New("provider does not support snapshots"),
	}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err, "快照失败是降级，不是报错")
	require.True(t, got.Degraded)
	require.Equal(t, ForkDegradeSnapshotUnsupported, got.Reason)
	require.Nil(t, sessions.created.ForkBootstrap)
}

func TestForkDoesNotDegradeWhenSnapshotIsCanceled(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{
		boundID: "sbx-1", bound: true, snapshotErr: context.Canceled,
	}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, sessions.created, "canceled snapshot must not persist a degraded fork")
}

func TestForkFinishesSnapshotAfterCallerCancels(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	ctx, cancel := context.WithCancel(context.Background())
	port := &fakeForkSandboxPort{
		boundID: "sbx-1", bound: true, snapshotID: "snap-1", cancelParent: cancel,
	}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	got, err := svc.Fork(ctx, 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.NoError(t, port.snapshotCtxErr, "snapshot must not inherit the canceled HTTP request")
	require.False(t, got.Degraded)
	require.Equal(t, "snap-1", sessions.created.ForkBootstrap.SnapshotID)
}

// 源沙箱正在跑 agent 时打快照会暂停它、打断执行中的工具。
func TestForkRefusesWhileSourceTurnIsActive(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, activeTurn: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.ErrorIs(t, err, ErrForkSourceBusy)
	require.Zero(t, port.snapshotCalls)
	require.Nil(t, sessions.created, "拒绝时不得留下半个会话")
}

func TestForkAtAssistantCopiesTurnAndUsesItsCheckpoint(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	turn[1].IsCompleted = true
	later := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, later))

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "a-msg-1", "")

	require.NoError(t, err)
	require.False(t, got.Degraded)
	require.Equal(t, 1, port.snapshotCalls)
	require.Equal(t, "a-msg-1", sessions.created.ForkedFromMessageID)
	require.Len(t, sessions.copiedMessages, 2, "assistant fork must include the clicked answer, not stop before it")
	require.Equal(t, "user", sessions.copiedMessages[0].Role)
	require.Equal(t, "assistant", sessions.copiedMessages[1].Role)
	require.Equal(t, "sha1", sessions.copiedMessages[1].SandboxCheckpoint.CommitSHA)
	require.Equal(t, "sbx-1", sessions.created.ForkBootstrap.SourceSandboxID)
}

func TestForkAtAssistantUsesThisTurnsCheckpointNotAnEarlierOne(t *testing.T) {
	first := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha-old", 0)
	first[1].IsCompleted = true
	second := checkpointedTurn("u-msg-2", "a-msg-2", "sbx-1", "sha-new", 10*time.Second)
	second[1].IsCompleted = true
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-2"}
	svc, sessions, _ := newForkFixture(t, port, append(first, second...))

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "a-msg-2", "")

	require.NoError(t, err)
	require.Len(t, sessions.copiedMessages, 4)
	require.Equal(t, "assistant", sessions.copiedMessages[3].Role)
	require.Equal(t, "sha-new", sessions.copiedMessages[3].SandboxCheckpoint.CommitSHA)
}

func TestForkAtAssistantWithoutCheckpointDegrades(t *testing.T) {
	turn := []*types.Message{
		{ID: "u-msg-1", SessionID: "src", Role: "user", CreatedAt: forkBase},
		{ID: "a-msg-1", SessionID: "src", Role: "assistant", CreatedAt: forkBase.Add(time.Second), IsCompleted: true},
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, turn)

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "a-msg-1", "")

	require.NoError(t, err)
	require.True(t, got.Degraded)
	require.Equal(t, ForkDegradeNoCheckpoint, got.Reason)
	require.Zero(t, port.snapshotCalls)
	require.Len(t, sessions.copiedMessages, 2)
	require.Equal(t, "assistant", sessions.copiedMessages[1].Role)
	require.Nil(t, sessions.created.ForkBootstrap)
}

func TestForkRejectsIncompleteAssistantAsForkPoint(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, turn)

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "a-msg-1", "")

	require.ErrorIs(t, err, ErrForkSourceBusy)
	require.Zero(t, port.snapshotCalls)
	require.Nil(t, sessions.created)
}

func TestForkRejectsNonUserNonAssistantForkPoint(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	turn = append(turn, &types.Message{
		ID: "sys-1", SessionID: "src", Role: "system", CreatedAt: forkBase.Add(2 * time.Second),
	})
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, _, _ := newForkFixture(t, port, turn)

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "sys-1", "")

	require.ErrorIs(t, err, ErrForkMessageNotUser)
	require.Zero(t, port.snapshotCalls)
}

func TestForkRejectsForeignSession(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, _, _ := newForkFixture(t, port, turn)

	_, err := svc.Fork(context.Background(), 1, "someone-else", "src", "u-msg-1", "")

	require.ErrorIs(t, err, ErrForkSessionNotFound)
	require.Zero(t, port.snapshotCalls)
}

func TestForkRejectsEmptyOwnerSession(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, turn)
	sessions.source.UserID = ""

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-1", "")

	require.ErrorIs(t, err, ErrForkSessionNotFound)
	require.Zero(t, port.snapshotCalls)
	require.Nil(t, sessions.created)
}

func TestForkRejectsEmptyCaller(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, turn)
	sessions.source.UserID = ""

	_, err := svc.Fork(context.Background(), 1, "", "src", "u-msg-1", "")

	require.ErrorIs(t, err, ErrForkSessionNotFound)
	require.Zero(t, port.snapshotCalls)
	require.Nil(t, sessions.created)
}

func TestForkRejectsMissingSession(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, _, _ := newForkFixture(t, port, turn)

	_, err := svc.Fork(context.Background(), 1, "u1", "missing", "u-msg-1", "")

	require.ErrorIs(t, err, ErrForkSessionNotFound)
}

func TestForkRejectsMissingMessage(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, _, _ := newForkFixture(t, port, turn)

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "no-such-msg", "")

	require.ErrorIs(t, err, ErrForkMessageNotFound)
}

func TestForkRefusesWhenSourceBecomesBusyBeforeSnapshot(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{
		boundID: "sbx-1", bound: true, snapshotID: "snap-1", forceBusyAfter: 2,
	}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.ErrorIs(t, err, ErrForkSourceBusy)
	require.GreaterOrEqual(t, port.hasActiveCalls, 2)
	require.Zero(t, port.snapshotCalls)
	require.Nil(t, sessions.created)
}

func TestForkDeletesCreatedSnapshotWhenPersistFails(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))
	sessions.createErr = errors.New("db down")

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.Error(t, err)
	require.Nil(t, sessions.created)
	require.Equal(t, 1, port.snapshotCalls)
	require.Equal(t, []string{"snap-1"}, port.deleted)
	require.Empty(t, sessions.leases, "deleted snapshot must not leave a lease behind")
}

func TestForkKeepsLeaseWhenPersistAndDeleteBothFail(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{
		boundID: "sbx-1", bound: true, snapshotID: "snap-1", deleteErr: errors.New("provider timeout"),
	}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))
	sessions.createErr = errors.New("db down")

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.Error(t, err)
	require.Equal(t, []string{"snap-1"}, port.deleted)
	require.Len(t, sessions.leases, 1)
	require.Equal(t, "snap-1", sessions.leases[0].SnapshotID)
	require.Equal(t, uint64(1), sessions.leases[0].TenantID)
	require.Equal(t, "cfg-1", sessions.leases[0].SandboxConfigID)
}

func TestForkDoesNotDeleteInheritedSnapshotWhenPersistFails(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha-early", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{bound: false, snapshotID: "must-not-create"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))
	sessions.source.ForkBootstrap = &types.ForkBootstrap{
		SnapshotID:      "snap-shared",
		CommitSHA:       "sha-later",
		SourceSandboxID: "sbx-1",
		CreatedAt:       forkBase,
	}
	sessions.createErr = errors.New("db down")

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.Error(t, err)
	require.Zero(t, port.snapshotCalls)
	require.Empty(t, port.deleted, "parent still needs the inherited snapshot")
	require.Empty(t, sessions.leases)
}

func TestForkClearsLeaseAfterPersistSucceeds(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.Empty(t, port.deleted)
	require.Empty(t, sessions.leases, "session.fork_bootstrap now owns the snapshot")
}

func TestForkOnHostCopiesMessagesWithoutWorkspaceRollback(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "host-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &hostForkSandboxPort{fakeForkSandboxPort: fakeForkSandboxPort{
		boundID: "host-1", bound: true, snapshotID: "snap-should-not-fire",
	}}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.False(t, got.Degraded, "host fork is message-only by design, not a degraded remote fork")
	require.Empty(t, got.Reason)
	require.Zero(t, port.snapshotCalls)
	require.Nil(t, sessions.created.ForkBootstrap)
	require.Equal(t, []string{"u-msg-1", "a-msg-1"}, sessions.copiedFromIDs())
}

func TestForkOnHostDoesNotDegradeWhenPrecedingTurnHasNoCheckpoint(t *testing.T) {
	messages := []*types.Message{
		{ID: "u-msg-1", SessionID: "src", Role: "user", CreatedAt: forkBase},
		{ID: "a-msg-1", SessionID: "src", Role: "assistant", CreatedAt: forkBase.Add(time.Second)},
		{ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second)},
	}
	port := &hostForkSandboxPort{}
	svc, sessions, _ := newForkFixture(t, port, messages)

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.False(t, got.Degraded)
	require.Empty(t, got.Reason)
	require.Equal(t, []string{"u-msg-1", "a-msg-1"}, sessions.copiedFromIDs())
}

// A branch of a project session keeps operating on the same project.
func TestForkInheritsHostWorkspaceDir(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))
	sessions.source.HostWorkspaceDir = "/Users/dev/My Project"

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.NotNil(t, sessions.created)
	require.Equal(t, "/Users/dev/My Project", sessions.created.HostWorkspaceDir)
}
