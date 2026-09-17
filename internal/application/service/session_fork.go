// Package service - session fork.
//
// Fork copies a session's history through a chosen user or assistant message
// into a brand new session, and arranges for that new session's first sandbox
// to boot from a snapshot of the source sandbox rolled back to the fork point.
//
// The whole operation is cheap and synchronous: it writes the database and
// takes one provider snapshot. No sandbox is created here — provisioning stays
// lazy, exactly as it is for ordinary sessions.
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
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ForkDegradeReason explains why a fork could not carry the source sandbox's
// state over. A degraded fork still succeeds — it just starts from a brand new
// sandbox — because the alternative (refusing) would block the common cases:
// Docker kills idle containers outright, and plain chat sessions never had a
// sandbox to begin with.
type ForkDegradeReason string

const (
	// ForkDegradeNoCheckpoint means the turn preceding the fork point never
	// produced a git checkpoint (commit failed, or it ran without a sandbox).
	ForkDegradeNoCheckpoint ForkDegradeReason = "NO_CHECKPOINT"

	// ForkDegradeSandboxReplaced means the checkpoint belongs to a sandbox the
	// session no longer uses. In-sandbox git history restarts from zero on
	// every rebuild, so that SHA is unreachable from the current sandbox.
	ForkDegradeSandboxReplaced ForkDegradeReason = "SANDBOX_REPLACED"

	// ForkDegradeSandboxGone means the source session has no live sandbox to
	// snapshot.
	ForkDegradeSandboxGone ForkDegradeReason = "SANDBOX_GONE"

	// ForkDegradeSnapshotUnsupported means snapshotting failed or the backend
	// does not support it.
	ForkDegradeSnapshotUnsupported ForkDegradeReason = "SNAPSHOT_UNSUPPORTED"
)

var (
	// ErrForkSourceBusy reports that the source session has an agent turn in
	// flight. Snapshotting pauses the source sandbox on every backend, which
	// would interrupt a tool mid-execution, so the fork is refused rather than
	// queued. HTTP 409.
	ErrForkSourceBusy = errors.New("session fork: source session has an active turn")

	// ErrForkSessionNotFound covers a missing session and a session the caller
	// does not own. Both map to HTTP 404 so ownership is not enumerable.
	ErrForkSessionNotFound = errors.New("session fork: source session not found")

	// ErrForkMessageNotFound covers a missing message and a message that
	// belongs to a different session. HTTP 404.
	ErrForkMessageNotFound = errors.New("session fork: fork point message not found")

	// ErrForkMessageNotUser is returned when the fork point exists but is
	// neither a user nor an assistant message. HTTP 400.
	ErrForkMessageNotUser = errors.New("session fork: fork point must be a user or assistant message")
)

// forkSnapshotTimeout bounds the provider snapshot plus the persist that
// follows it. Cube pauses a live MicroVM and copies its disk; that routinely
// exceeds the 30s HTTP-client timeout and the browser's default axios budget.
// The work is detached from the incoming request cancel so a client abort
// mid-snapshot cannot leave a 500 after the snapshot already ran.
const forkSnapshotTimeout = 2 * time.Minute

func isForkNotFound(err error) bool {
	return errors.Is(err, apperrors.ErrSessionNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

// ForkResult is what the HTTP layer renders.
type ForkResult struct {
	SessionID string            `json:"session_id"`
	Degraded  bool              `json:"degraded"`
	Reason    ForkDegradeReason `json:"reason,omitempty"`
}

// SessionForkSandboxPort is the narrow sandbox surface fork needs. It is an
// interface so the fork logic is testable without a provider.
type SessionForkSandboxPort interface {
	// BoundSandboxID returns the session's currently bound sandbox without
	// provisioning one.
	BoundSandboxID(ctx context.Context, sessionID string) (string, bool)

	// HasActiveTurn reports whether an agent turn currently holds the
	// session's sandbox lease.
	HasActiveTurn(ctx context.Context, sessionID string) (bool, error)

	// CreateForkSnapshot snapshots the session's live sandbox and returns a
	// snapshot ID usable as a create-time template.
	CreateForkSnapshot(ctx context.Context, sessionID, name string) (string, error)

	// DeleteForkSnapshot removes a snapshot created for sessionID. Used when
	// fork persist fails after the snapshot already exists.
	DeleteForkSnapshot(ctx context.Context, sessionID, snapshotID string) error
}

type forkSessionStore interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
	// CreateForked persists the new session and the copied messages in one
	// transaction. Copied messages arrive with fresh IDs already assigned and
	// SessionID pointing at the new session.
	CreateForked(ctx context.Context, session *types.Session, messages []*types.Message) error
	CreateForkSnapshotLease(ctx context.Context, lease *types.ForkSnapshotLease) error
	DeleteForkSnapshotLease(ctx context.Context, snapshotID string) error
}

type forkMessageStore interface {
	GetMessage(ctx context.Context, sessionID, messageID string) (*types.Message, error)
	ListMessagesBySessionUpTo(
		ctx context.Context, sessionID string, boundary time.Time, boundaryID string,
	) ([]*types.Message, error)
}

// SessionForkService implements the fork decision chain.
type SessionForkService struct {
	sessions forkSessionStore
	messages forkMessageStore
	sandbox  SessionForkSandboxPort
}

// NewSessionForkService wires the service. A nil sandbox port makes every fork
// degrade, which is the correct behaviour for a deployment without sandboxes.
func NewSessionForkService(
	sessions forkSessionStore,
	messages forkMessageStore,
	sandboxPort SessionForkSandboxPort,
) *SessionForkService {
	return &SessionForkService{sessions: sessions, messages: messages, sandbox: sandboxPort}
}

// NewSessionForkServiceFromRepos is the DI-friendly constructor. The
// repository interfaces satisfy the narrow ports structurally.
func NewSessionForkServiceFromRepos(
	sessions interfaces.SessionRepository,
	messages interfaces.MessageRepository,
	sandboxPort SessionForkSandboxPort,
) *SessionForkService {
	return NewSessionForkService(sessions, messages, sandboxPort)
}

// Fork branches sourceSessionID at messageID.
//
// Errors are reserved for conditions the user must act on: a bad request, a
// session they do not own, or a source that is busy. Everything about the
// sandbox degrades instead, and is reported through ForkResult.
func (s *SessionForkService) Fork(
	ctx context.Context,
	tenantID uint64,
	userID string,
	sourceSessionID string,
	messageID string,
	title string,
) (*ForkResult, error) {
	source, err := s.sessions.GetByID(ctx, tenantID, sourceSessionID)
	if err != nil {
		if isForkNotFound(err) {
			return nil, ErrForkSessionNotFound
		}
		return nil, fmt.Errorf("session fork: load source session: %w", err)
	}
	if source == nil {
		return nil, ErrForkSessionNotFound
	}
	// Fork is a write. Empty user_id rows are tenant-level channel/legacy
	// sessions: list/read scope still surfaces them, but copying history into
	// a new session requires an exact owner match. Skipping that when UserID
	// is empty would let any same-tenant caller fork the row.
	if userID == "" || source.UserID != userID {
		return nil, ErrForkSessionNotFound
	}

	forkPoint, err := s.messages.GetMessage(ctx, sourceSessionID, messageID)
	if err != nil {
		if isForkNotFound(err) {
			return nil, ErrForkMessageNotFound
		}
		return nil, fmt.Errorf("session fork: load fork point: %w", err)
	}
	if forkPoint == nil {
		return nil, ErrForkMessageNotFound
	}
	if forkPoint.Role != "user" && forkPoint.Role != "assistant" {
		return nil, ErrForkMessageNotUser
	}
	// An unfinished assistant answer is still the live turn. Snapshotting
	// would pause the sandbox under it; treat it like a busy source.
	if forkPoint.Role == "assistant" && !forkPoint.IsCompleted {
		return nil, ErrForkSourceBusy
	}

	// Refuse before doing anything observable: snapshotting pauses the source
	// sandbox, and interrupting a running tool is worse than making the user
	// wait for the turn to finish.
	if s.sandbox != nil {
		busy, turnErr := s.sandbox.HasActiveTurn(ctx, sourceSessionID)
		if turnErr != nil {
			return nil, fmt.Errorf("session fork: check source turn state: %w", turnErr)
		}
		if busy {
			return nil, ErrForkSourceBusy
		}
	}

	history, err := s.messages.ListMessagesBySessionUpTo(
		ctx, sourceSessionID, forkPoint.CreatedAt, forkPoint.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("session fork: load history: %w", err)
	}
	history = historyThroughForkPoint(history, forkPoint)

	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), forkSnapshotTimeout)
	defer cancel()

	bootstrap, reason, createdSnapshot, prepErr := s.prepareBootstrap(workCtx, source, history)
	if prepErr != nil {
		return nil, prepErr
	}

	if createdSnapshot {
		if err := s.recordSnapshotLease(workCtx, source, bootstrap); err != nil {
			s.abandonCreatedSnapshot(workCtx, source.ID, bootstrap.SnapshotID)
			return nil, err
		}
	}

	newSession := &types.Session{
		ID:                  uuid.New().String(),
		TenantID:            source.TenantID,
		UserID:              source.UserID,
		Title:               forkTitle(title, source.Title),
		Description:         source.Description,
		LastRequestState:    source.LastRequestState,
		SandboxConfigID:     source.SandboxConfigID,
		ParentSessionID:     source.ID,
		ForkedFromMessageID: forkPoint.ID,
		ForkBootstrap:       bootstrap,
	}

	copied := copyMessagesInto(newSession.ID, history)
	if err := s.sessions.CreateForked(workCtx, newSession, copied); err != nil {
		if createdSnapshot {
			s.abandonCreatedSnapshot(workCtx, source.ID, bootstrap.SnapshotID)
		}
		return nil, fmt.Errorf("session fork: persist forked session: %w", err)
	}
	if createdSnapshot {
		s.clearSnapshotLease(workCtx, bootstrap.SnapshotID)
	}

	logger.Infof(ctx,
		"[SessionFork] source=%s fork_point=%s new=%s messages=%d degraded=%v reason=%s",
		sourceSessionID, forkPoint.ID, newSession.ID, len(copied), reason != "", reason)

	return &ForkResult{
		SessionID: newSession.ID,
		Degraded:  reason != "",
		Reason:    reason,
	}, nil
}

// prepareBootstrap runs the decision chain from the design doc §4.2 and, when
// every condition holds, takes the snapshot.
//
// A nil bootstrap with an empty reason means "no sandbox state was needed":
// forking at the very first user message has no prior output to carry, so a
// brand new sandbox is the correct result rather than a degradation.
func (s *SessionForkService) prepareBootstrap(
	ctx context.Context, source *types.Session, history []*types.Message,
) (*types.ForkBootstrap, ForkDegradeReason, bool, error) {
	checkpoint := latestCheckpoint(history)
	if checkpoint == nil {
		if !hasAssistantMessage(history) {
			// Forking at the first user message.
			return nil, "", false, nil
		}
		return nil, ForkDegradeNoCheckpoint, false, nil
	}
	if inherited := inheritUnconsumedForkSnapshot(source, checkpoint); inherited != nil {
		if currentID, ok := s.boundSandboxID(ctx, source.ID); !ok || currentID == "" {
			logger.Infof(ctx, "[SessionFork] inherit unused snapshot %s from source=%s sha=%s",
				inherited.SnapshotID, source.ID, inherited.CommitSHA)
			return inherited, "", false, nil
		}
	}
	if s.sandbox == nil {
		return nil, ForkDegradeSandboxGone, false, nil
	}

	currentID, ok := s.sandbox.BoundSandboxID(ctx, source.ID)
	if !ok || currentID == "" {
		return nil, ForkDegradeSandboxGone, false, nil
	}
	if currentID != checkpoint.SandboxID {
		// The session swapped sandboxes after this checkpoint was written, so
		// the SHA does not exist in the current sandbox's repository.
		return nil, ForkDegradeSandboxReplaced, false, nil
	}

	// Re-check immediately before pausing the sandbox. The earlier check in
	// Fork is a cheap reject; this closes the window after checkpoint lookup.
	busy, turnErr := s.sandbox.HasActiveTurn(ctx, source.ID)
	if turnErr != nil {
		return nil, "", false, fmt.Errorf("session fork: check source turn state: %w", turnErr)
	}
	if busy {
		return nil, "", false, ErrForkSourceBusy
	}

	snapshotID, err := s.sandbox.CreateForkSnapshot(
		ctx, source.ID, forkSnapshotName(source.ID),
	)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, "", false, fmt.Errorf("session fork: snapshot source sandbox: %w", err)
		}
		logger.Warnf(ctx, "[SessionFork] snapshot failed source=%s: %v", source.ID, err)
		return nil, ForkDegradeSnapshotUnsupported, false, nil
	}
	if snapshotID == "" {
		logger.Warnf(ctx, "[SessionFork] snapshot failed source=%s: empty snapshot id", source.ID)
		return nil, ForkDegradeSnapshotUnsupported, false, nil
	}

	return &types.ForkBootstrap{
		SnapshotID:      snapshotID,
		CommitSHA:       checkpoint.CommitSHA,
		SourceSandboxID: checkpoint.SandboxID,
		CreatedAt:       time.Now().UTC(),
	}, "", true, nil
}

func (s *SessionForkService) recordSnapshotLease(
	ctx context.Context, source *types.Session, bootstrap *types.ForkBootstrap,
) error {
	if s.sessions == nil || source == nil || bootstrap == nil || strings.TrimSpace(bootstrap.SnapshotID) == "" {
		return nil
	}
	lease := &types.ForkSnapshotLease{
		SnapshotID:      bootstrap.SnapshotID,
		TenantID:        source.TenantID,
		SandboxConfigID: source.SandboxConfigID,
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.sessions.CreateForkSnapshotLease(ctx, lease); err != nil {
		return fmt.Errorf("session fork: record snapshot lease: %w", err)
	}
	return nil
}

func (s *SessionForkService) clearSnapshotLease(ctx context.Context, snapshotID string) {
	if s.sessions == nil || strings.TrimSpace(snapshotID) == "" {
		return
	}
	if err := s.sessions.DeleteForkSnapshotLease(ctx, snapshotID); err != nil {
		logger.Warnf(ctx, "[SessionFork] clear snapshot lease %s failed: %v", snapshotID, err)
	}
}

// abandonCreatedSnapshot deletes a snapshot this fork just created. On success
// it also drops the lease. On delete failure the lease stays so the reaper
// can retry — there is no session row to hang the ID on.
func (s *SessionForkService) abandonCreatedSnapshot(ctx context.Context, sourceSessionID, snapshotID string) {
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" || s.sandbox == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := s.sandbox.DeleteForkSnapshot(cleanupCtx, sourceSessionID, snapshotID); err != nil {
		if !sandbox.IsRemoteNotFound(err) {
			logger.Warnf(cleanupCtx, "[SessionFork] delete orphan snapshot %s failed; lease kept for reaper: %v",
				snapshotID, err)
			return
		}
	}
	s.clearSnapshotLease(cleanupCtx, snapshotID)
}

func (s *SessionForkService) boundSandboxID(ctx context.Context, sessionID string) (string, bool) {
	if s == nil || s.sandbox == nil {
		return "", false
	}
	return s.sandbox.BoundSandboxID(ctx, sessionID)
}

// inheritUnconsumedForkSnapshot reuses a parent branch's unused snapshot when
// that branch has never provisioned a sandbox. Treating "no binding" as
// SANDBOX_GONE would drop state that still exists on the provider.
func inheritUnconsumedForkSnapshot(source *types.Session, checkpoint *types.SandboxCheckpoint) *types.ForkBootstrap {
	if source == nil || source.ForkBootstrap == nil || source.ForkBootstrap.Consumed() || checkpoint == nil {
		return nil
	}
	snapshotID := strings.TrimSpace(source.ForkBootstrap.SnapshotID)
	sha := strings.TrimSpace(checkpoint.CommitSHA)
	if snapshotID == "" || sha == "" {
		return nil
	}
	parentSandbox := strings.TrimSpace(source.ForkBootstrap.SourceSandboxID)
	checkpointSandbox := strings.TrimSpace(checkpoint.SandboxID)
	if parentSandbox != "" && checkpointSandbox != "" && parentSandbox != checkpointSandbox {
		return nil
	}
	sourceSandbox := checkpointSandbox
	if sourceSandbox == "" {
		sourceSandbox = parentSandbox
	}
	return &types.ForkBootstrap{
		SnapshotID:      snapshotID,
		CommitSHA:       sha,
		SourceSandboxID: sourceSandbox,
		CreatedAt:       time.Now().UTC(),
	}
}

// latestCheckpoint returns the checkpoint of the last assistant message in
// history, or nil when that message has none.
func latestCheckpoint(history []*types.Message) *types.SandboxCheckpoint {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		return history[i].SandboxCheckpoint
	}
	return nil
}

func hasAssistantMessage(history []*types.Message) bool {
	for _, m := range history {
		if m.Role == "assistant" {
			return true
		}
	}
	return false
}

// historyThroughForkPoint is exclusive of a user fork point (the client
// prefills that question) and inclusive of an assistant fork point (the
// branch continues after that answer).
func historyThroughForkPoint(listed []*types.Message, forkPoint *types.Message) []*types.Message {
	if forkPoint == nil || forkPoint.Role != "assistant" {
		return listed
	}
	out := make([]*types.Message, 0, len(listed)+1)
	out = append(out, listed...)
	return append(out, forkPoint)
}

// copyMessagesInto clones history into the new session.
//
// Every copy gets a fresh primary key but keeps its original CreatedAt so the
// branch reads with the same timeline as its parent. RequestIDs are remapped
// per original value so a user/assistant pair stays paired inside the fork
// without colliding with the parent in search partner lookups.
//
// Copies deliberately keep their Artifacts and Attachments. Artifact URLs are
// persisted on the message row, so the forked session's 产物 tab can list and
// download the same files. Attachment storage handles live in
// temporary_documents (still scoped to the parent session) and are resolved
// by ID at preview and staging time when the copied messages reference them.
func copyMessagesInto(newSessionID string, history []*types.Message) []*types.Message {
	copies := make([]*types.Message, 0, len(history))
	requestIDs := make(map[string]string, len(history))
	for _, src := range history {
		clone := *src
		clone.ID = uuid.New().String()
		clone.SessionID = newSessionID
		// The parent already indexed these into the chat-history knowledge
		// base. Clearing the link stops the copy from being re-indexed, which
		// would both duplicate retrieval hits and burn embedding quota.
		clone.KnowledgeID = ""
		if src.RequestID != "" {
			mapped, ok := requestIDs[src.RequestID]
			if !ok {
				mapped = uuid.New().String()
				requestIDs[src.RequestID] = mapped
			}
			clone.RequestID = mapped
		}
		copies = append(copies, &clone)
	}
	return copies
}

func forkTitle(requested, sourceTitle string) string {
	if requested != "" {
		return requested
	}
	return sourceTitle + "（分支）"
}

func forkSnapshotName(sourceSessionID string) string {
	return fmt.Sprintf("fork-%s-%d", sourceSessionID, time.Now().UnixMilli())
}
