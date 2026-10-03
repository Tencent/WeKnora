package service

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type deleteSessionRepo struct {
	interfaces.SessionRepository
	gotTenant uint64
	gotUser   string
	err       error
}

func (r *deleteSessionRepo) Get(_ context.Context, tenantID uint64, userID, id string) (*types.Session, error) {
	r.gotTenant, r.gotUser = tenantID, userID
	if r.err != nil {
		return nil, r.err
	}
	return &types.Session{ID: id, TenantID: tenantID}, nil
}

type deleteMessageRepo struct {
	interfaces.MessageRepository
	target   *types.MessageArtifactRecord
	versions []types.MessageArtifactRecord
	marked   []types.ArtifactRef
	liveByte map[string]int64
	liveErr  error
	askedURL []string
}

func (r *deleteMessageRepo) FindSessionArtifact(
	_ context.Context, _, _ string, _ int,
) (*types.MessageArtifactRecord, error) {
	return r.target, nil
}

func (r *deleteMessageRepo) FindSessionArtifactVersions(
	_ context.Context, _, _ string,
) ([]types.MessageArtifactRecord, error) {
	return r.versions, nil
}

func (r *deleteMessageRepo) SoftDeleteSessionArtifacts(
	_ context.Context, _ string, refs []types.ArtifactRef, _ time.Time,
) ([]types.ArtifactRef, error) {
	if r.marked != nil {
		return r.marked, nil
	}
	return refs, nil
}

func (r *deleteMessageRepo) CountLiveArtifactsByURL(_ context.Context, url string) (int64, error) {
	r.askedURL = append(r.askedURL, url)
	if r.liveErr != nil {
		return 0, r.liveErr
	}
	return r.liveByte[url], nil
}

func deleteCallerCtx() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	return context.WithValue(ctx, types.UserIDContextKey, "alice")
}

func artifactRow(messageID string, pos int, url, path string) types.MessageArtifactRecord {
	return types.MessageArtifactRecord{
		MessageID: messageID, Position: pos, URL: url, SourcePath: path, FileName: "report.pptx",
	}
}

// Deleting is a write on the session, so it is scoped to its owner — unlike the
// download path, which also honours shared-agent read access.
func TestDeleteSessionArtifactIsScopedToTheSessionOwner(t *testing.T) {
	sessions := &deleteSessionRepo{}
	repo := &deleteMessageRepo{target: ptr(artifactRow("msg-1", 0, "resource://a", "/w/a.pptx"))}
	s := &messageService{sessionRepo: sessions, messageRepo: repo}

	_, err := s.DeleteSessionArtifact(deleteCallerCtx(), &types.ArtifactDeleteRequest{
		SessionID: "sess-1", MessageID: "msg-1", Index: 0,
	})
	require.NoError(t, err)
	require.EqualValues(t, 7, sessions.gotTenant)
	require.Equal(t, "alice", sessions.gotUser, "the owner check must carry the caller, not a tenant-wide lookup")
}

func TestDeleteSessionArtifactRejectsSomeoneElsesSession(t *testing.T) {
	sessions := &deleteSessionRepo{err: apperrors.ErrSessionNotFound}
	repo := &deleteMessageRepo{target: ptr(artifactRow("msg-1", 0, "resource://a", "/w/a.pptx"))}
	s := &messageService{sessionRepo: sessions, messageRepo: repo}

	_, err := s.DeleteSessionArtifact(deleteCallerCtx(), &types.ArtifactDeleteRequest{
		SessionID: "sess-1", MessageID: "msg-1", Index: 0,
	})
	require.ErrorIs(t, err, apperrors.ErrSessionNotFound)
	require.Empty(t, repo.askedURL, "a rejected caller must not reach the reclaim path at all")
}

func TestDeleteSessionArtifactMissingOrAlreadyDeleted(t *testing.T) {
	s := &messageService{sessionRepo: &deleteSessionRepo{}, messageRepo: &deleteMessageRepo{target: nil}}
	_, err := s.DeleteSessionArtifact(deleteCallerCtx(), &types.ArtifactDeleteRequest{
		SessionID: "sess-1", MessageID: "msg-1", Index: 0,
	})
	require.ErrorIs(t, err, ErrArtifactNotFound)

	// Nothing claimed means a concurrent delete got there first; it owns the reclaim.
	repo := &deleteMessageRepo{
		target: ptr(artifactRow("msg-1", 0, "resource://a", "/w/a.pptx")),
		marked: []types.ArtifactRef{},
	}
	s = &messageService{sessionRepo: &deleteSessionRepo{}, messageRepo: repo}
	_, err = s.DeleteSessionArtifact(deleteCallerCtx(), &types.ArtifactDeleteRequest{
		SessionID: "sess-1", MessageID: "msg-1", Index: 0,
	})
	require.ErrorIs(t, err, ErrArtifactNotFound)
	require.Empty(t, repo.askedURL)
}

// A forked session's rows are copies of the parent's, storage URL included, and
// CreateForked does not give them their own catalog binding. Deleting in the
// parent must not pull the bytes out from under the fork.
func TestDeleteSessionArtifactKeepsBytesAnotherRowStillNeeds(t *testing.T) {
	repo := &deleteMessageRepo{
		target:   ptr(artifactRow("msg-1", 0, "resource://shared", "/w/a.pptx")),
		liveByte: map[string]int64{"resource://shared": 1},
	}
	s := &messageService{sessionRepo: &deleteSessionRepo{}, messageRepo: repo}

	result, err := s.DeleteSessionArtifact(deleteCallerCtx(), &types.ArtifactDeleteRequest{
		SessionID: "sess-1", MessageID: "msg-1", Index: 0,
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Deleted, "the row is still removed from every listing")
	require.Empty(t, result.Reclaim, "but its bytes stay: a live row elsewhere still points at them")
}

// An unreadable count keeps the blob, like every other uncertainty here.
func TestDeleteSessionArtifactKeepsBytesWhenTheGuardFails(t *testing.T) {
	repo := &deleteMessageRepo{
		target:  ptr(artifactRow("msg-1", 0, "resource://a", "/w/a.pptx")),
		liveErr: stderrors.New("db down"),
	}
	s := &messageService{sessionRepo: &deleteSessionRepo{}, messageRepo: repo}

	result, err := s.DeleteSessionArtifact(deleteCallerCtx(), &types.ArtifactDeleteRequest{
		SessionID: "sess-1", MessageID: "msg-1", Index: 0,
	})
	require.NoError(t, err)
	require.Empty(t, result.Reclaim)
}

// all_versions collapses the versions to distinct objects, and each object
// carries every deleted message that owned it so all their bindings go.
func TestDeleteSessionArtifactAllVersionsGroupsOwnersPerObject(t *testing.T) {
	repo := &deleteMessageRepo{
		target: ptr(artifactRow("msg-1", 0, "resource://v1", "/w/report.pptx")),
		versions: []types.MessageArtifactRecord{
			artifactRow("msg-1", 0, "resource://v1", "/w/report.pptx"),
			artifactRow("msg-2", 0, "resource://v2", "/w/report.pptx"),
			// A later answer re-attached v1; same object, another owner.
			artifactRow("msg-3", 1, "resource://v1", "/w/report.pptx"),
		},
	}
	s := &messageService{sessionRepo: &deleteSessionRepo{}, messageRepo: repo}

	result, err := s.DeleteSessionArtifact(deleteCallerCtx(), &types.ArtifactDeleteRequest{
		SessionID: "sess-1", MessageID: "msg-1", Index: 0, AllVersions: true,
	})
	require.NoError(t, err)
	require.Equal(t, 3, result.Deleted)
	require.Len(t, result.Reclaim, 2, "three rows, two distinct objects")
	byURL := map[string][]string{}
	for _, ref := range result.Reclaim {
		byURL[ref.URL] = ref.MessageIDs
	}
	require.ElementsMatch(t, []string{"msg-1", "msg-3"}, byURL["resource://v1"])
	require.Equal(t, []string{"msg-2"}, byURL["resource://v2"])
}

// An artifact with no sandbox path has no version group, so all_versions is a
// no-op rather than a session-wide sweep of every pathless file.
func TestDeleteSessionArtifactAllVersionsWithoutASourcePath(t *testing.T) {
	repo := &deleteMessageRepo{target: ptr(artifactRow("msg-1", 2, "resource://a", ""))}
	s := &messageService{sessionRepo: &deleteSessionRepo{}, messageRepo: repo}

	result, err := s.DeleteSessionArtifact(deleteCallerCtx(), &types.ArtifactDeleteRequest{
		SessionID: "sess-1", MessageID: "msg-1", Index: 2, AllVersions: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Deleted)
}

func ptr[T any](v T) *T { return &v }
