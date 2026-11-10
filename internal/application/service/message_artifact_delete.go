package service

import (
	"context"
	"slices"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ErrArtifactNotFound is returned when the addressed artifact does not exist,
// belongs to a message that is gone, or was already deleted. Handlers map it to
// 404 so a caller cannot probe which of the three it was.
var ErrArtifactNotFound = errors.NewNotFoundError("artifact not found")

// DeleteSessionArtifact removes a skill-generated file from the session.
//
// The row is tombstoned rather than dropped. Position is the index the download
// endpoint addresses a file by, so removing a row would shift every later file
// in the message and hand an old link the wrong blob; and the tombstone is what
// keeps ArtifactCollector from re-attaching the sandbox copy, whose mtime has
// not changed just because the user deleted the stored version.
//
// The bytes are a separate concern: this method reports them in Reclaim and the
// caller — which has the tenant's file service and the resource catalog —
// releases the binding and deletes the object. Doing it here would mean giving
// the message service a storage dependency it otherwise has no use for.
//
// Reclaim must run only after this returns. A failed reclaim leaves an orphaned
// blob that a GC pass can still find through the tombstone's url; a reclaim
// that ran first could delete bytes for a tombstone that was never written.
func (s *messageService) DeleteSessionArtifact(
	ctx context.Context, req *types.ArtifactDeleteRequest,
) (*types.ArtifactDeleteResult, error) {
	if req == nil || req.SessionID == "" || req.MessageID == "" || req.Index < 0 {
		return nil, errors.NewBadRequestError("session_id, message_id and index are required")
	}

	// Ownership: the session must belong to the caller. Deleting is not covered
	// by the shared-agent read access the download endpoint honours — a viewer
	// of someone else's shared session may fetch a file, not destroy it.
	tenantID := types.MustTenantIDFromContext(ctx)
	if _, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), req.SessionID); err != nil {
		return nil, err
	}

	target, err := s.messageRepo.FindSessionArtifact(ctx, req.SessionID, req.MessageID, req.Index)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": req.SessionID,
			"message_id": req.MessageID,
			"index":      req.Index,
		})
		return nil, err
	}
	if target == nil {
		return nil, ErrArtifactNotFound
	}

	rows, err := s.artifactDeleteSet(ctx, req, *target)
	if err != nil {
		return nil, err
	}

	refs := make([]types.ArtifactRef, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, types.ArtifactRef{MessageID: row.MessageID, Position: row.Position})
	}
	marked, err := s.messageRepo.SoftDeleteSessionArtifacts(ctx, req.SessionID, refs, time.Now())
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": req.SessionID,
			"message_id": req.MessageID,
		})
		return nil, err
	}
	if len(marked) == 0 {
		// A concurrent delete tombstoned these rows first. It owns the reclaim.
		return nil, ErrArtifactNotFound
	}

	logger.Infof(ctx, "Deleted artifact %s from session %s: %d row(s)",
		target.FileName, req.SessionID, len(marked))
	return &types.ArtifactDeleteResult{
		FileName: target.FileName,
		Deleted:  len(marked),
		// Only the rows this call claimed: a row a concurrent delete marked
		// first is its reclaim to do, and doing it twice would try to remove
		// the same object from under it.
		Reclaim: s.reclaimableBlobs(ctx, artifactReclaimList(rows, marked)),
	}, nil
}

// reclaimableBlobs drops the objects some other row still points at.
//
// The catalog's binding count is the primary guard, but it only knows about
// bindings, and rows come to share a URL in ways that never created one: a
// forked session's rows are copies of the parent's, storage URL included, and
// CreateForked does not re-Bind them; a deployment storing raw provider paths
// has no catalog entries at all. Deleting in the parent session would then take
// the bytes out from under the fork. A count of live rows catches all of it.
//
// A failed count keeps the blob, like every other uncertainty on this path.
func (s *messageService) reclaimableBlobs(
	ctx context.Context, candidates []types.ArtifactBlobRef,
) []types.ArtifactBlobRef {
	out := make([]types.ArtifactBlobRef, 0, len(candidates))
	for _, ref := range candidates {
		live, err := s.messageRepo.CountLiveArtifactsByURL(ctx, ref.URL)
		if err != nil {
			logger.Warnf(ctx, "Artifact reclaim check failed for %s, keeping the blob: %v", ref.URL, err)
			continue
		}
		if live > 0 {
			logger.Infof(ctx, "Keeping artifact blob %s: %d live row(s) still point at it", ref.URL, live)
			continue
		}
		out = append(out, ref)
	}
	return out
}

// artifactDeleteSet expands the request into the rows to tombstone: the one
// addressed row, or every live version of the same sandbox file when the caller
// asked for all of them. An artifact with no source path has no version group —
// nothing else in the session is the "same file" — so it deletes alone.
func (s *messageService) artifactDeleteSet(
	ctx context.Context, req *types.ArtifactDeleteRequest, target types.MessageArtifactRecord,
) ([]types.MessageArtifactRecord, error) {
	if !req.AllVersions || target.SourcePath == "" {
		return []types.MessageArtifactRecord{target}, nil
	}
	versions, err := s.messageRepo.FindSessionArtifactVersions(ctx, req.SessionID, target.SourcePath)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id":  req.SessionID,
			"source_path": target.SourcePath,
		})
		return nil, err
	}
	if len(versions) == 0 {
		return []types.MessageArtifactRecord{target}, nil
	}
	return versions, nil
}

// artifactReclaimList groups the rows this call tombstoned by the object behind
// them. A later answer that re-attaches an earlier file stores another row with
// the same URL and its own resource binding, so one object can come from
// several messages: the bytes are reclaimed once, but every one of those
// messages has to give up its claim first.
func artifactReclaimList(
	rows []types.MessageArtifactRecord, marked []types.ArtifactRef,
) []types.ArtifactBlobRef {
	claimed := make(map[types.ArtifactRef]bool, len(marked))
	for _, ref := range marked {
		claimed[ref] = true
	}
	at := make(map[string]int, len(rows))
	out := make([]types.ArtifactBlobRef, 0, len(rows))
	for _, row := range rows {
		if row.URL == "" || !claimed[(types.ArtifactRef{MessageID: row.MessageID, Position: row.Position})] {
			continue
		}
		i, ok := at[row.URL]
		if !ok {
			at[row.URL] = len(out)
			out = append(out, types.ArtifactBlobRef{URL: row.URL, MessageIDs: []string{row.MessageID}})
			continue
		}
		if !slices.Contains(out[i].MessageIDs, row.MessageID) {
			out[i].MessageIDs = append(out[i].MessageIDs, row.MessageID)
		}
	}
	return out
}
