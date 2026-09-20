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
	"sync"
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

	// RewindSkipSandboxReplaced means the checkpoint belongs to a sandbox the
	// session no longer uses, so the SHA is unreachable.
	RewindSkipSandboxReplaced RewindSkipReason = "SANDBOX_REPLACED"
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
)

func isRewindNotFound(err error) bool {
	return errors.Is(err, apperrors.ErrSessionNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

// RewindResult is what the HTTP layer renders.
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
	ListMessagesBySessionUpTo(
		ctx context.Context, sessionID string, boundary time.Time, boundaryID string,
	) ([]*types.Message, error)
	DeleteMessagesFrom(
		ctx context.Context, sessionID string, boundary time.Time, boundaryID string, inclusive bool,
	) ([]*types.Message, error)
}

type rewindKnowledgeCleaner interface {
	DeleteMessageKnowledge(ctx context.Context, knowledgeID string)
}

type rewindSuggestionCleaner interface {
	DeleteByMessageID(ctx context.Context, tenantID uint64, sessionID, messageID string) error
}

// SessionRewindService implements in-place rewind.
type SessionRewindService struct {
	sessions    rewindSessionStore
	messages    rewindMessageStore
	sandbox     SessionRewindSandboxPort
	knowledge   rewindKnowledgeCleaner
	suggestions rewindSuggestionCleaner
	snapshots   ForkSnapshotDeleter
	inflight    sync.Map
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
) *SessionRewindService {
	s := NewSessionRewindService(sessions, messages, sandboxPort, knowledge, suggestions)
	s.snapshots = NewResolverForkSnapshotDeleter(resolver, fallback)
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

	history, err := s.messages.ListMessagesBySessionUpTo(
		ctx, sessionID, rewindPoint.CreatedAt, rewindPoint.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("session rewind: load history: %w", err)
	}
	history = historyThroughForkPoint(history, rewindPoint)

	// Client abort must not leave git reset applied and messages intact.
	// Once we are past the cheap busy reject, finish reset+truncate even if
	// the HTTP request is gone.
	persistCtx := context.WithoutCancel(ctx)
	unlock, err := s.lockRewind(persistCtx, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	workspaceReset, reason, resetErr := s.resetWorkspaceIfPossible(persistCtx, sessionID, history)
	if resetErr != nil {
		return nil, resetErr
	}
	if !workspaceReset {
		if err := s.rejectIfBusy(persistCtx, sessionID); err != nil {
			return nil, err
		}
		if reason == RewindSkipNoSandbox {
			if err := s.syncPendingForkBootstrap(persistCtx, source, history); err != nil {
				return nil, err
			}
		}
	}

	inclusive := rewindPoint.Role == "user"
	deleted, err := s.messages.DeleteMessagesFrom(
		persistCtx, sessionID, rewindPoint.CreatedAt, rewindPoint.ID, inclusive,
	)
	if err != nil {
		return nil, fmt.Errorf("session rewind: delete messages: %w", err)
	}

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
	checkpoint := latestCheckpoint(history)
	if checkpoint == nil {
		return false, RewindSkipNoCheckpoint, nil
	}
	if currentID != checkpoint.SandboxID {
		return false, RewindSkipSandboxReplaced, nil
	}

	// Re-check immediately before git reset --hard. The entry check is a
	// cheap reject; a turn can start while we look up the checkpoint.
	if err := s.rejectIfBusy(ctx, sessionID); err != nil {
		return false, "", err
	}

	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workspaceResetTimeout)
	defer cancel()
	if err := resetWorkspaceToCommit(workCtx, s.sandbox, sessionID, checkpoint.CommitSHA); err != nil {
		return false, "", fmt.Errorf("session rewind: reset workspace: %w", err)
	}
	return true, "", nil
}

func (s *SessionRewindService) lockRewind(ctx context.Context, sessionID string) (func(), error) {
	if _, loaded := s.inflight.LoadOrStore(sessionID, struct{}{}); loaded {
		return nil, ErrRewindSourceBusy
	}
	unlockPort := func() {}
	if s.sandbox != nil {
		unlock, err := s.sandbox.TryLockRewind(ctx, sessionID)
		if err != nil {
			s.inflight.Delete(sessionID)
			if errors.Is(err, sandbox.ErrSessionRewindLocked) || errors.Is(err, ErrRewindSourceBusy) {
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
		s.inflight.Delete(sessionID)
	}, nil
}

func (s *SessionRewindService) rejectIfBusy(ctx context.Context, sessionID string) error {
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

// syncPendingForkBootstrap keeps an unopened fork's lazy sandbox in line with
// the conversation just truncated. ForkBootstrap.CommitSHA is applied the
// first time the session provisions a sandbox; leaving the fork-point SHA
// after an earlier rewind would boot the workspace ahead of the remaining
// messages. No remaining checkpoint means the next sandbox should be ordinary,
// same as forking at the first user message.
func (s *SessionRewindService) syncPendingForkBootstrap(
	ctx context.Context, session *types.Session, kept []*types.Message,
) error {
	if s == nil || session == nil || session.ForkBootstrap == nil || session.ForkBootstrap.Consumed() {
		return nil
	}
	pending := *session.ForkBootstrap
	checkpoint := latestCheckpoint(kept)
	if checkpoint == nil || strings.TrimSpace(checkpoint.CommitSHA) == "" {
		return s.abandonPendingForkBootstrap(ctx, session, &pending)
	}
	sha := strings.TrimSpace(checkpoint.CommitSHA)
	if !gitSHAPattern.MatchString(sha) {
		return fmt.Errorf("session rewind: invalid checkpoint sha %q", truncateForLog(sha))
	}
	if sha == strings.TrimSpace(pending.CommitSHA) {
		return nil
	}
	pending.CommitSHA = sha
	if err := s.sessions.UpdateForkBootstrap(ctx, session.ID, &pending); err != nil {
		return fmt.Errorf("session rewind: retarget fork bootstrap: %w", err)
	}
	return nil
}

func (s *SessionRewindService) abandonPendingForkBootstrap(
	ctx context.Context, session *types.Session, pending *types.ForkBootstrap,
) error {
	if err := s.sessions.UpdateForkBootstrap(ctx, session.ID, nil); err != nil {
		return fmt.Errorf("session rewind: clear fork bootstrap: %w", err)
	}
	view := *session
	view.ForkBootstrap = pending
	releaseForkSnapshotOnDelete(ctx, s.sessions, s.snapshots, &view)
	return nil
}

func (s *SessionRewindService) cleanupDeleted(
	ctx context.Context, tenantID uint64, sessionID string, deleted []*types.Message,
) {
	for _, msg := range deleted {
		if msg == nil {
			continue
		}
		if s.suggestions != nil {
			if err := s.suggestions.DeleteByMessageID(ctx, tenantID, sessionID, msg.ID); err != nil {
				logger.Warnf(ctx, "[SessionRewind] delete suggestions for message %s: %v", msg.ID, err)
			}
		}
		if s.knowledge != nil && strings.TrimSpace(msg.KnowledgeID) != "" {
			s.knowledge.DeleteMessageKnowledge(ctx, msg.KnowledgeID)
		}
	}
}
