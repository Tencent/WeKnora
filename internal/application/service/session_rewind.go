// Package service - in-place session rewind.
//
// Rewind truncates the current session at a chosen user or assistant message
// and, when the live sandbox still holds that turn's git checkpoint, rolls
// /workspace back to it. Unlike fork it does not snapshot, does not create a
// session, and refuses the whole operation if git reset fails — a half-applied
// rewind (conversation gone, files still ahead) is harder to recover from
// than a 500 the user can retry.
//
// Unopened forks are the exception to "no sandbox means conversation only":
// they still carry ForkBootstrap for the first provision. Rewind retargets
// that SHA to the kept history, or drops the bootstrap when nothing remains
// to reset to, so the lazy sandbox cannot boot ahead of the transcript.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// RewindSkipReason explains why rewind left the workspace alone. The
// conversation is still truncated; the code is so the client can say so.
// These are not shared with ForkDegradeReason: the two operations skip for
// overlapping-but-not-identical reasons and the UI copy is different.
type RewindSkipReason string

const (
	// RewindSkipNoSandbox means the session has no bound sandbox to reset.
	RewindSkipNoSandbox RewindSkipReason = "NO_SANDBOX"

	// RewindSkipNoCheckpoint means the history being kept has no git
	// checkpoint (the image has no git, or every kept turn failed to commit).
	RewindSkipNoCheckpoint RewindSkipReason = "NO_CHECKPOINT"
)

var (
	// ErrRewindSourceBusy reports that the session has an agent turn in
	// flight, or the rewind point is an unfinished assistant message. HTTP 409.
	ErrRewindSourceBusy = errors.New("session rewind: session has an active turn")

	// ErrRewindSessionNotFound covers a missing session and a session the
	// caller does not own. Both map to HTTP 404 so ownership is not enumerable.
	ErrRewindSessionNotFound = errors.New("session rewind: session not found")

	// ErrRewindMessageNotFound covers a missing message and a message that
	// belongs to a different session. HTTP 404.
	ErrRewindMessageNotFound = errors.New("session rewind: rewind point message not found")

	// ErrRewindMessageRole is returned when the rewind point exists but is
	// neither a user nor an assistant message. HTTP 400.
	ErrRewindMessageRole = errors.New("session rewind: rewind point must be a user or assistant message")

	// ErrRewindNoCheckpoint is returned when the session has a live sandbox
	// and kept history includes an assistant turn, but no reachable git SHA.
	// Truncating would leave files ahead of the conversation. HTTP 409.
	ErrRewindNoCheckpoint = errors.New("session rewind: no reachable workspace checkpoint")

	// ErrRewindSandboxReplaced is returned when the kept checkpoint belongs
	// to a sandbox the session no longer uses. Truncating would leave the
	// live workspace ahead of the conversation. HTTP 409.
	ErrRewindSandboxReplaced = errors.New("session rewind: sandbox was replaced")
)

const (
	rewindIncompleteLookback = 512
	rewindDeleteAttempts     = 3
	rewindDeleteRetryWait    = 50 * time.Millisecond
)

func isRewindNotFound(err error) bool {
	return errors.Is(err, apperrors.ErrSessionNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

// RewindResult is what the HTTP layer renders.
// WorkspaceReset is true when the live sandbox was git-reset, or when an
// unopened fork's ForkBootstrap was retargeted so the first provision lands
// on the rewind SHA.
type RewindResult struct {
	DeletedMessages int              `json:"deleted_messages"`
	WorkspaceReset  bool             `json:"workspace_reset"`
	Reason          RewindSkipReason `json:"reason"`
}

// SessionRewindSandboxPort is the narrow sandbox surface rewind needs.
type SessionRewindSandboxPort interface {
	BoundSandboxID(ctx context.Context, sessionID string) (string, bool)
	HasActiveTurn(ctx context.Context, sessionID string) (bool, error)
	TryLockRewind(ctx context.Context, sessionID string) (unlock func(), err error)
	SandboxShellRunner
}

type rewindSessionStore interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
	UpdateForkBootstrap(ctx context.Context, sessionID string, b *types.ForkBootstrap) error
	forkSnapshotReleaseStore
}

type rewindMessageStore interface {
	GetMessage(ctx context.Context, sessionID, messageID string) (*types.Message, error)
	GetRecentMessagesBySession(ctx context.Context, sessionID string, limit int) ([]*types.Message, error)
	ListMessagesBySessionUpTo(
		ctx context.Context, sessionID string, boundary time.Time, boundaryID string,
	) ([]*types.Message, error)
	DeleteMessagesFrom(
		ctx context.Context, sessionID string, boundary time.Time, boundaryID string, inclusive bool,
	) ([]*types.Message, error)
}

type rewindIncompleteFinder interface {
	SessionHasIncompleteAssistant(ctx context.Context, sessionID string) (bool, error)
}

// rewindCheckpointLister is the cheap path for "which checkpoint does kept
// history still reach". Rewind never reads message bodies, so a store that
// can return the assistant turns alone — no content, no artifact join —
// answers it without dragging the whole conversation into memory.
type rewindCheckpointLister interface {
	ListAssistantCheckpointsUpTo(
		ctx context.Context, sessionID string, boundary time.Time, boundaryID string,
	) ([]*types.Message, error)
}

type rewindArtifactJanitor interface {
	ListLiveArtifactsByMessageIDs(
		ctx context.Context, sessionID string, messageIDs []string,
	) ([]types.MessageArtifactRecord, error)
	SoftDeleteSessionArtifacts(
		ctx context.Context, sessionID string, refs []types.ArtifactRef, at time.Time,
	) ([]types.ArtifactRef, error)
}

type rewindLiveRunReader interface {
	GetLiveRun(ctx context.Context, sessionID string) (assistantMessageID, requestID string, err error)
}

type rewindStreamDropper interface {
	DropMessageStreams(ctx context.Context, sessionID string, messageIDs []string) error
}

type rewindKnowledgeCleaner interface {
	DeleteMessageKnowledge(ctx context.Context, knowledgeID string)
}

type rewindSuggestionCleaner interface {
	DeleteByMessageID(ctx context.Context, tenantID uint64, sessionID, messageID string) error
}

// rewindSuggestionBatchCleaner is the one-statement form of the above.
type rewindSuggestionBatchCleaner interface {
	DeleteByMessageIDs(ctx context.Context, tenantID uint64, sessionID string, messageIDs []string) error
}

// SessionRewindService implements in-place rewind.
type SessionRewindService struct {
	sessions    rewindSessionStore
	messages    rewindMessageStore
	sandbox     SessionRewindSandboxPort
	knowledge   rewindKnowledgeCleaner
	suggestions rewindSuggestionCleaner
	snapshots   ForkSnapshotDeleter
	liveRuns    rewindLiveRunReader
	busyGate    *SessionBusyGate
}

// NewSessionRewindService wires the service. A nil sandbox port skips the
// workspace reset with NO_SANDBOX, which is the correct behaviour for a
// deployment without sandboxes.
func NewSessionRewindService(
	sessions rewindSessionStore,
	messages rewindMessageStore,
	sandboxPort SessionRewindSandboxPort,
	knowledge rewindKnowledgeCleaner,
	suggestions rewindSuggestionCleaner,
) *SessionRewindService {
	return &SessionRewindService{
		sessions:    sessions,
		messages:    messages,
		sandbox:     sandboxPort,
		knowledge:   knowledge,
		suggestions: suggestions,
		busyGate:    NewSessionBusyGate(),
	}
}

// NewSessionRewindServiceFromRepos is the DI-friendly constructor.
func NewSessionRewindServiceFromRepos(
	sessions interfaces.SessionRepository,
	messages interfaces.MessageRepository,
	sandboxPort SessionRewindSandboxPort,
	knowledge interfaces.MessageService,
	suggestions interfaces.MessageSuggestionRepository,
	resolver sandbox.TenantSandboxResolver,
	fallback sandbox.Manager,
	streams interfaces.StreamManager,
	busyGate *SessionBusyGate,
) *SessionRewindService {
	s := NewSessionRewindService(sessions, messages, sandboxPort, knowledge, suggestions)
	s.snapshots = NewResolverForkSnapshotDeleter(resolver, fallback)
	s.liveRuns = streams
	if busyGate != nil {
		s.busyGate = busyGate
	}
	return s
}

// Rewind truncates sessionID at messageID and resets the live workspace when
// a reachable checkpoint exists.
//
// Errors are reserved for conditions the user must act on: a bad request, a
// session they do not own, a busy source, or a git reset that failed. Missing
// sandbox state degrades to a conversation-only rewind and is reported
// through RewindResult.Reason.
func (s *SessionRewindService) Rewind(
	ctx context.Context,
	tenantID uint64,
	userID string,
	sessionID string,
	messageID string,
) (*RewindResult, error) {
	source, err := s.sessions.GetByID(ctx, tenantID, sessionID)
	if err != nil {
		if isRewindNotFound(err) {
			return nil, ErrRewindSessionNotFound
		}
		return nil, fmt.Errorf("session rewind: load session: %w", err)
	}
	if source == nil {
		return nil, ErrRewindSessionNotFound
	}
	// Rewind is a write. Empty user_id rows are tenant-level channel/legacy
	// sessions: list/read scope still surfaces them, but mutating history
	// requires an exact owner match.
	if userID == "" || source.UserID != userID {
		return nil, ErrRewindSessionNotFound
	}

	rewindPoint, err := s.messages.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		if isRewindNotFound(err) {
			return nil, ErrRewindMessageNotFound
		}
		return nil, fmt.Errorf("session rewind: load rewind point: %w", err)
	}
	if rewindPoint == nil {
		return nil, ErrRewindMessageNotFound
	}
	if rewindPoint.Role != "user" && rewindPoint.Role != "assistant" {
		return nil, ErrRewindMessageRole
	}
	if rewindPoint.Role == "assistant" && !rewindPoint.IsCompleted {
		return nil, ErrRewindSourceBusy
	}

	if err := s.rejectIfBusy(ctx, sessionID); err != nil {
		return nil, err
	}

	history, err := s.keptCheckpointHistory(ctx, sessionID, rewindPoint)
	if err != nil {
		return nil, err
	}

	// Client abort must not leave git reset applied and messages intact.
	// Once we are past the cheap busy reject, finish reset+truncate even if
	// the HTTP request is gone.
	persistCtx := context.WithoutCancel(ctx)
	unlock, err := s.lockRewind(persistCtx, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	var abandonedBootstrap *types.ForkBootstrap
	workspaceReset, reason, resetErr := s.resetWorkspaceIfPossible(persistCtx, sessionID, history)
	if resetErr != nil {
		return nil, resetErr
	}
	if !workspaceReset {
		if err := s.rejectIfBusy(persistCtx, sessionID); err != nil {
			return nil, err
		}
		if reason == RewindSkipNoSandbox {
			aligned, abandoned, err := s.syncPendingForkBootstrap(persistCtx, source, history)
			if err != nil {
				return nil, err
			}
			if aligned {
				workspaceReset = true
				reason = ""
			} else if abandoned != nil {
				reason = RewindSkipNoCheckpoint
				abandonedBootstrap = abandoned
			}
		}
	}

	inclusive := rewindPoint.Role == "user"
	deleted, err := s.deleteMessagesFrom(
		persistCtx, sessionID, rewindPoint.CreatedAt, rewindPoint.ID, inclusive,
	)
	if err != nil {
		if workspaceReset {
			return nil, fmt.Errorf("session rewind: delete messages after workspace reset: %w", err)
		}
		return nil, fmt.Errorf("session rewind: delete messages: %w", err)
	}

	// Deleting the snapshot is the one step of the bootstrap handover that
	// cannot be undone, so it waits until the truncate has actually landed.
	// Everything above it is a DB write the next rewind attempt can redo.
	s.releaseAbandonedForkSnapshot(persistCtx, source, abandonedBootstrap)

	s.cleanupDeleted(persistCtx, source.TenantID, sessionID, deleted)

	logger.Infof(ctx,
		"[SessionRewind] session=%s rewind_point=%s deleted=%d workspace_reset=%v reason=%s",
		sessionID, rewindPoint.ID, len(deleted), workspaceReset, reason)

	return &RewindResult{
		DeletedMessages: len(deleted),
		WorkspaceReset:  workspaceReset,
		Reason:          reason,
	}, nil
}

// keptCheckpointHistory returns the messages that survive the cut, in the
// shape latestReachableCheckpoint and hasAssistantMessage read them.
//
// Only assistant turns carry a SandboxCheckpoint and only assistant turns
// decide between "reset to a SHA", "empty-reset" and ErrRewindNoCheckpoint,
// so a store that can list just those is asked for just those. The fallback
// reads the full prefix, which is correct but loads every message body and
// artifact row in the session to answer a question about a handful of rows.
func (s *SessionRewindService) keptCheckpointHistory(
	ctx context.Context, sessionID string, rewindPoint *types.Message,
) ([]*types.Message, error) {
	var (
		kept []*types.Message
		err  error
	)
	if lister, ok := s.messages.(rewindCheckpointLister); ok {
		kept, err = lister.ListAssistantCheckpointsUpTo(
			ctx, sessionID, rewindPoint.CreatedAt, rewindPoint.ID,
		)
	} else {
		kept, err = s.messages.ListMessagesBySessionUpTo(
			ctx, sessionID, rewindPoint.CreatedAt, rewindPoint.ID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("session rewind: load history: %w", err)
	}
	return historyThroughForkPoint(kept, rewindPoint), nil
}

func (s *SessionRewindService) resetWorkspaceIfPossible(
	ctx context.Context, sessionID string, history []*types.Message,
) (bool, RewindSkipReason, error) {
	if s.sandbox == nil {
		return false, RewindSkipNoSandbox, nil
	}
	currentID, ok := s.sandbox.BoundSandboxID(ctx, sessionID)
	if !ok || strings.TrimSpace(currentID) == "" {
		return false, RewindSkipNoSandbox, nil
	}
	checkpoint := latestReachableCheckpoint(history)
	if checkpoint == nil {
		if hasAssistantMessage(history) {
			return false, "", ErrRewindNoCheckpoint
		}
		if err := s.rejectIfBusy(ctx, sessionID); err != nil {
			return false, "", err
		}
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workspaceResetTimeout)
		defer cancel()
		if err := resetWorkspaceToEmpty(workCtx, s.sandbox, sessionID, currentID); err != nil {
			return false, "", fmt.Errorf("session rewind: reset workspace: %w", err)
		}
		return true, "", nil
	}
	if currentID != checkpoint.SandboxID {
		return false, "", ErrRewindSandboxReplaced
	}

	// Re-check immediately before git reset --hard. The entry check is a
	// cheap reject; a turn can start while we look up the checkpoint.
	if err := s.rejectIfBusy(ctx, sessionID); err != nil {
		return false, "", err
	}

	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workspaceResetTimeout)
	defer cancel()
	if err := resetWorkspaceToCommit(
		workCtx, s.sandbox, sessionID, checkpoint.CommitSHA, checkpoint.SandboxID,
	); err != nil {
		return false, "", fmt.Errorf("session rewind: reset workspace: %w", err)
	}
	return true, "", nil
}

func (s *SessionRewindService) deleteMessagesFrom(
	ctx context.Context, sessionID string, boundary time.Time, boundaryID string, inclusive bool,
) ([]*types.Message, error) {
	if s == nil || s.messages == nil {
		return nil, nil
	}
	var lastErr error
	for attempt := 1; attempt <= rewindDeleteAttempts; attempt++ {
		deleted, err := s.messages.DeleteMessagesFrom(
			ctx, sessionID, boundary, boundaryID, inclusive,
		)
		if err == nil {
			return deleted, nil
		}
		lastErr = err
		if attempt == rewindDeleteAttempts {
			break
		}
		timer := time.NewTimer(rewindDeleteRetryWait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, err
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (s *SessionRewindService) lockRewind(ctx context.Context, sessionID string) (func(), error) {
	unlockGate, err := s.busyGate.TryLockRewind(sessionID)
	if err != nil {
		return nil, ErrRewindSourceBusy
	}
	unlockPort := func() {}
	if s.sandbox != nil {
		unlock, err := s.sandbox.TryLockRewind(ctx, sessionID)
		if err != nil {
			unlockGate()
			if errors.Is(err, sandbox.ErrSessionRewindLocked) ||
				errors.Is(err, sandbox.ErrSessionTurnActive) ||
				errors.Is(err, ErrRewindSourceBusy) {
				return nil, ErrRewindSourceBusy
			}
			return nil, fmt.Errorf("session rewind: lock session: %w", err)
		}
		if unlock != nil {
			unlockPort = unlock
		}
	}
	return func() {
		unlockPort()
		unlockGate()
	}, nil
}

func (s *SessionRewindService) rejectIfBusy(ctx context.Context, sessionID string) error {
	if s.liveRuns != nil {
		liveID, _, err := s.liveRuns.GetLiveRun(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("session rewind: check live run: %w", err)
		}
		if strings.TrimSpace(liveID) != "" {
			return ErrRewindSourceBusy
		}
	}
	if err := s.rejectIfIncompleteTurn(ctx, sessionID); err != nil {
		return err
	}
	if s.sandbox == nil {
		return nil
	}
	busy, turnErr := s.sandbox.HasActiveTurn(ctx, sessionID)
	if turnErr != nil {
		return fmt.Errorf("session rewind: check turn state: %w", turnErr)
	}
	if busy {
		return ErrRewindSourceBusy
	}
	return nil
}

func (s *SessionRewindService) rejectIfIncompleteTurn(ctx context.Context, sessionID string) error {
	if s.messages == nil {
		return nil
	}
	if finder, ok := s.messages.(rewindIncompleteFinder); ok {
		busy, err := finder.SessionHasIncompleteAssistant(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("session rewind: check incomplete turn: %w", err)
		}
		if busy {
			return ErrRewindSourceBusy
		}
		return nil
	}
	msgs, err := s.messages.GetRecentMessagesBySession(ctx, sessionID, rewindIncompleteLookback)
	if err != nil {
		return fmt.Errorf("session rewind: check incomplete turn: %w", err)
	}
	for _, msg := range msgs {
		if msg != nil && msg.Role == "assistant" && !msg.IsCompleted {
			return ErrRewindSourceBusy
		}
	}
	return nil
}

// syncPendingForkBootstrap keeps an unopened fork's lazy sandbox in line with
// the conversation just truncated. ForkBootstrap.CommitSHA is applied the
// first time the session provisions a sandbox; leaving the fork-point SHA
// after an earlier rewind would boot the workspace ahead of the remaining
// messages. No remaining checkpoint means the next sandbox should be ordinary,
// same as forking at the first user message.
//
// A non-nil abandoned return is the bootstrap that was just cleared; its
// snapshot is still alive and the caller releases it once the truncate has
// landed. Clearing the column here and freeing the snapshot later keeps the
// whole handover redoable while the conversation can still fail to delete.
func (s *SessionRewindService) syncPendingForkBootstrap(
	ctx context.Context, session *types.Session, kept []*types.Message,
) (aligned bool, abandoned *types.ForkBootstrap, err error) {
	if s == nil || session == nil || session.ForkBootstrap == nil || session.ForkBootstrap.Consumed() {
		return false, nil, nil
	}
	pending := *session.ForkBootstrap
	checkpoint := latestReachableCheckpoint(kept)
	if checkpoint == nil || strings.TrimSpace(checkpoint.CommitSHA) == "" {
		if err := s.sessions.UpdateForkBootstrap(ctx, session.ID, nil); err != nil {
			return false, nil, fmt.Errorf("session rewind: clear fork bootstrap: %w", err)
		}
		return false, &pending, nil
	}
	sha := strings.TrimSpace(checkpoint.CommitSHA)
	if !gitSHAPattern.MatchString(sha) {
		return false, nil, fmt.Errorf("session rewind: invalid checkpoint sha %q", truncateForLog(sha))
	}
	if sha == strings.TrimSpace(pending.CommitSHA) {
		return true, nil, nil
	}
	pending.CommitSHA = sha
	if err := s.sessions.UpdateForkBootstrap(ctx, session.ID, &pending); err != nil {
		return false, nil, fmt.Errorf("session rewind: retarget fork bootstrap: %w", err)
	}
	return true, nil, nil
}

// releaseAbandonedForkSnapshot frees the snapshot behind a bootstrap that
// syncPendingForkBootstrap cleared. It runs after the messages are gone
// because the delete is irreversible: dropping the snapshot first and then
// failing to truncate would leave the fork with no workspace to boot from
// while its whole transcript is still there.
func (s *SessionRewindService) releaseAbandonedForkSnapshot(
	ctx context.Context, session *types.Session, pending *types.ForkBootstrap,
) {
	if s == nil || session == nil || pending == nil {
		return
	}
	view := *session
	view.ForkBootstrap = pending
	releaseForkSnapshotOnDelete(ctx, s.sessions, s.snapshots, &view)
}

// deleteSuggestions drops the follow-up questions hanging off the deleted
// messages, in one statement when the store can batch. A long rewind deletes
// hundreds of messages and the rewind lock is held for all of it.
func (s *SessionRewindService) deleteSuggestions(
	ctx context.Context, tenantID uint64, sessionID string, ids []string,
) {
	if s.suggestions == nil {
		return
	}
	if batch, ok := s.suggestions.(rewindSuggestionBatchCleaner); ok {
		if err := batch.DeleteByMessageIDs(ctx, tenantID, sessionID, ids); err != nil {
			logger.Warnf(ctx, "[SessionRewind] delete suggestions for session %s: %v", sessionID, err)
		}
		return
	}
	for _, id := range ids {
		if err := s.suggestions.DeleteByMessageID(ctx, tenantID, sessionID, id); err != nil {
			logger.Warnf(ctx, "[SessionRewind] delete suggestions for message %s: %v", id, err)
		}
	}
}

func (s *SessionRewindService) cleanupDeleted(
	ctx context.Context, tenantID uint64, sessionID string, deleted []*types.Message,
) {
	ids := make([]string, 0, len(deleted))
	for _, msg := range deleted {
		if msg == nil || msg.ID == "" {
			continue
		}
		ids = append(ids, msg.ID)
		if s.knowledge != nil && strings.TrimSpace(msg.KnowledgeID) != "" {
			s.knowledge.DeleteMessageKnowledge(ctx, msg.KnowledgeID)
		}
	}
	if len(ids) == 0 {
		return
	}
	s.deleteSuggestions(ctx, tenantID, sessionID, ids)
	if dropper, ok := s.liveRuns.(rewindStreamDropper); ok {
		if err := dropper.DropMessageStreams(ctx, sessionID, ids); err != nil {
			logger.Warnf(ctx, "[SessionRewind] drop streams for session %s: %v", sessionID, err)
		}
	}
	janitor, ok := s.messages.(rewindArtifactJanitor)
	if !ok {
		return
	}
	records, err := janitor.ListLiveArtifactsByMessageIDs(ctx, sessionID, ids)
	if err != nil {
		logger.Warnf(ctx, "[SessionRewind] list artifacts for session %s: %v", sessionID, err)
		return
	}
	refs := make([]types.ArtifactRef, 0, len(records))
	for _, row := range records {
		if row.MessageID == "" || row.Position < 0 {
			continue
		}
		refs = append(refs, types.ArtifactRef{MessageID: row.MessageID, Position: row.Position})
	}
	if len(refs) == 0 {
		return
	}
	if _, err := janitor.SoftDeleteSessionArtifacts(ctx, sessionID, refs, time.Now()); err != nil {
		logger.Warnf(ctx, "[SessionRewind] tombstone artifacts for session %s: %v", sessionID, err)
	}
}
