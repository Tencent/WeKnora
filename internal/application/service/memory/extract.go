package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	// extractMaxSessionsPerRun bounds the model calls one run makes.
	// Conversations beyond it stay queued and are picked up by the follow-up
	// run, so the cap bounds one run rather than what eventually gets
	// distilled.
	extractMaxSessionsPerRun = 3
	// extractInFlightGrace is added to the configured delay to decide when an
	// in-flight claim is stale. Without it, a worker that died between claiming
	// and running would wedge the subject permanently.
	extractInFlightGrace = 10 * time.Minute
	// extractFollowUpDelay is the wait before a run that hit its message cap,
	// or that saw new turns arrive while it worked, queues its successor.
	extractFollowUpDelay = 15 * time.Second
	// extractDeferralPoll caps how far out a deferred conversation's successor
	// is queued, and is what turns the idle window from a countdown into a
	// granularity.
	//
	// A deferral used to be queued for the exact moment the window expired,
	// which is the cheapest way to wait and the wrong shape. The reason to
	// write an account early is that the person moved on, and moving on is
	// something they do after the run decided how long to sleep — the only
	// task in flight for this subject was already committed to waking up
	// twelve minutes later, so a conversation abandoned one minute in stayed
	// unwritten for eleven more. Waking sooner cannot be requested by a turn
	// without enqueueing a task per turn, which is the pile-up the queued
	// window exists to prevent, so the run wakes on its own.
	//
	// A wake that finds the conversation still going makes no model call: it
	// reads the pending rows and the newest messages and goes back to sleep.
	// Ninety seconds spends a handful of those per conversation and bounds how
	// stale a finished one can be.
	extractDeferralPoll = 90 * time.Second
)

// ScheduleExtraction records that a turn needs distilling and, when nobody
// else has already done so, queues the run.
//
// The important property is that a turn is never dropped. Earlier this method
// compared the current time against the last run and returned early inside the
// interval, which silently discarded every turn in that window. Now the turn is
// always recorded against the subject; the timers only decide *when* a run
// happens, never *whether* a message is considered.
//
// Everything the handler needs travels in the payload. Both asynq and the Lite
// executor hand the handler a bare context, so any scope the request knew and
// the payload does not carry is gone by the time the task runs.
func (s *Service) ScheduleExtraction(ctx context.Context, sessionID, messageID, chatModelID string) {
	scope, cfg, ok := s.enabledScope(ctx)
	if !ok {
		return
	}
	if !cfg.AutoExtractEnabled() {
		return
	}
	if sessionID == "" || messageID == "" {
		return
	}
	if s.enqueuer == nil {
		logger.Warnf(ctx, "memory: no task enqueuer configured, skipping extraction")
		return
	}

	// The subject row serializes queue mutations, so it must exist before
	// the first per-session progress row is recorded.
	subject, err := s.repo.EnsureSubject(ctx, scope)
	if err != nil {
		logger.Warnf(ctx, "memory: ensure subject for extraction failed: %v", err)
		return
	}
	if !subject.Enabled {
		return
	}

	delay := cfg.ExtractDelay()
	previous, shouldEnqueue, err := s.repo.EnqueuePendingSession(
		ctx, scope, sessionID, extractQueuedWindow(cfg, delay),
	)
	if err != nil {
		logger.Warnf(ctx, "memory: record pending session failed: %v", err)
		return
	}
	if !shouldEnqueue {
		// A run is already coming and will drain the queue this turn just
		// joined, so there is nothing left to do.
		return
	}

	// The minimum interval only defers: if the previous run was recent, the
	// task is queued further out rather than the turn being discarded.
	if previous != nil && previous.LastExtractedAt != nil {
		if remaining := cfg.ExtractMinInterval() - time.Now().Sub(*previous.LastExtractedAt); remaining > delay {
			delay = remaining
		}
	}

	s.enqueueExtraction(ctx, scope, sessionID, messageID, chatModelID, delay)
}

// enqueueExtraction pushes one distillation task. The in-flight slot is
// released when the enqueue itself fails, otherwise a lost task would block
// the subject until the claim expired.
func (s *Service) enqueueExtraction(
	ctx context.Context,
	scope interfaces.MemoryScope,
	sessionID, messageID, chatModelID string,
	delay time.Duration,
) error {
	payload := types.MemoryExtractPayload{
		TenantID:    scope.TenantID,
		SubjectID:   scope.SubjectID,
		SessionID:   sessionID,
		MessageID:   messageID,
		ChatModelID: chatModelID,
		Language:    types.LanguageNameFromContext(ctx),
	}
	langfuse.InjectTracing(ctx, &payload)
	body, err := json.Marshal(payload)
	if err != nil {
		logger.Warnf(ctx, "memory: marshal extraction payload failed: %v", err)
		s.releaseSlot(ctx, scope)
		return err
	}

	task := asynq.NewTask(types.TypeMemoryExtract, body)
	if _, err := s.enqueuer.Enqueue(task,
		asynq.Queue(types.QueueMemory),
		asynq.ProcessIn(delay),
		asynq.MaxRetry(2),
	); err != nil {
		logger.Warnf(ctx, "memory: enqueue extraction failed: %v", err)
		s.releaseSlot(ctx, scope)
		return err
	}
	return nil
}

// extractQueuedWindow is how long an already-queued run stays recognized as
// queued, which is what keeps a second task from being enqueued for the same
// subject while the first is still waiting to start.
//
// It has to cover the longest a queued task can actually sit there, and since
// a run that finds an active conversation re-queues itself one idle window
// out, that is part of the answer. Leaving the idle window out of it made the
// window shorter than the delay it was supposed to describe, so every turn
// during a long conversation enqueued another task.
func extractQueuedWindow(cfg *types.MemoryConfig, delay time.Duration) time.Duration {
	return cfg.ExtractMinInterval() + delay + episodeIdleWindow + extractInFlightGrace
}

func (s *Service) releaseSlot(ctx context.Context, scope interfaces.MemoryScope) {
	if err := s.repo.ReleaseExtractionSlot(ctx, scope, ""); err != nil {
		logger.Warnf(ctx, "memory: release extraction slot failed: %v", err)
	}
}

var errInvalidExtractionOutput = errors.New("invalid memory extraction output")

// Handle runs one distillation pass.
func (s *Service) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.MemoryExtractPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal memory extract payload: %w", err)
	}
	scope := interfaces.MemoryScope{TenantID: payload.TenantID, SubjectID: payload.SubjectID}
	if !scope.Valid() {
		// A payload without scope cannot be attributed to anyone. Retrying
		// would never fix it, so drop it rather than burn the retry budget.
		logger.Warnf(ctx, "memory: extraction payload has no scope, dropping")
		return nil
	}

	// Rebuild the request scope the worker never had. Tenant id is what every
	// downstream repository filters on, and the model service reads it from
	// the context to pick the workspace's model.
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	cfg := s.workspaceConfig(ctx, payload.TenantID)
	if !cfg.AutoExtractEnabled() {
		s.releaseSlot(ctx, scope)
		return nil
	}
	// A task can outlive the row it was queued for (workspace reset, restore
	// from backup), and the queue/watermark bookkeeping below needs a row to
	// write to, so recreate it rather than failing the task forever.
	subject, err := s.repo.EnsureSubject(ctx, scope)
	if err != nil {
		return fmt.Errorf("load memory subject: %w", err)
	}
	if !subject.Enabled {
		s.releaseSlot(ctx, scope)
		return nil
	}

	// A lease serializes duplicate tasks, while the durable session queue
	// stays intact until each segment has actually been applied.
	leaseID := uuid.NewString()
	batch, err := s.repo.ClaimPendingSessions(ctx, scope, payload.SessionID, leaseID, extractInFlightGrace)
	if err != nil {
		return fmt.Errorf("claim pending sessions: %w", err)
	}
	if batch == nil {
		return nil
	}
	if !batch.RetryAt.IsZero() {
		// A redelivery can arrive before a dead worker's lease expires. Simply
		// acknowledging it here would strand the durable queue forever.
		if s.enqueuer == nil {
			return fmt.Errorf("memory extraction is leased until %s", batch.RetryAt)
		}
		return s.enqueueExtraction(ctx, scope, payload.SessionID, payload.MessageID, payload.ChatModelID, time.Until(batch.RetryAt)+time.Second)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.repo.ReleaseExtractionSlot(cleanup, scope, leaseID); err != nil {
			logger.Warnf(cleanup, "memory: release worker lease failed: %v", err)
		}
	}()
	// Stop model calls before the lease can expire and another worker starts.
	ctx, cancel := context.WithTimeout(ctx, extractInFlightGrace-time.Minute)
	defer cancel()

	// One conversation, one account, rewritten in place. The loop is over
	// sessions rather than over stretches of a session because that is the
	// unit an account is written for; a session that has grown gets its
	// account revised, not a second one appended.
	processed := 0
	var retryErr error
	// The soonest a deferred conversation becomes eligible. The follow-up is
	// queued for then rather than for the usual short delay, so a conversation
	// someone is in the middle of is not re-examined every few seconds.
	var deferredUntil time.Time
	for i, session := range batch.Sessions {
		// Claimed oldest-touched first, so every session but the last has had
		// a turn arrive somewhere newer since its own. That is the batch
		// saying the person moved on from it.
		superseded := i < len(batch.Sessions)-1
		cursor, retryAt, err := s.distillSession(ctx, scope, cfg, payload, session, superseded)
		if !retryAt.IsZero() {
			// Still going. Leave the watermark, and do not spend the run's
			// model-call budget on a session that made no call.
			if deferredUntil.IsZero() || retryAt.Before(deferredUntil) {
				deferredUntil = retryAt
			}
			logger.Infof(ctx, "memory: session %s still active, account deferred to %s",
				session.SessionID, retryAt.Format(time.RFC3339))
			continue
		}
		if err != nil {
			if !errors.Is(err, errInvalidExtractionOutput) {
				return err
			}
			failure := interfaces.MemoryExtractionFailure{
				Session: session, End: cursor, Code: "invalid_model_output",
			}
			skip, recordErr := s.repo.RecordExtractionFailure(ctx, scope, leaseID, failure)
			if recordErr != nil {
				return recordErr
			}
			if !skip {
				// Leave the watermark where it is so the conversation is
				// retried, and let the other sessions in this batch progress.
				retryErr = err
				processed++
				continue
			}
			logger.Warnf(ctx, "memory: giving up on session %s after repeated invalid output",
				session.SessionID)
		}
		if err := s.repo.CheckpointExtraction(ctx, scope, leaseID, session, cursor, true); err != nil {
			return err
		}
		processed++
		if processed >= extractMaxSessionsPerRun {
			break
		}
	}
	if err := s.repo.FinishExtraction(ctx, scope, leaseID); err != nil {
		return err
	}
	if err := s.scheduleFollowUpIfNeeded(ctx, scope, cfg, payload, deferredUntil); err != nil {
		return err
	}
	if retryErr != nil && s.enqueuer == nil {
		return retryErr
	}
	s.backfillEpisodeEmbeddings(ctx, scope, cfg)
	// Phase two runs after phase one has filed its account, so a rewrite
	// triggered by this conversation includes this conversation.
	s.refreshDigestIfDue(ctx, scope, cfg, payload)
	return nil
}

// distillSession writes the account of one conversation and reports how far
// the watermark may advance.
//
// The cursor comes back even on failure, so the caller can record which range
// produced unusable output without having to reconstruct it. It is the newest
// message in the conversation either way: an account covers a conversation
// rather than a range of it, so there is no partial progress to preserve.
//
// A non-zero retryAt means nothing was written because the conversation is
// still going. The caller must leave the watermark alone in that case, or the
// turns it declined to read would be marked as read.
func (s *Service) distillSession(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	payload types.MemoryExtractPayload,
	session types.MemoryExtractionSession,
	superseded bool,
) (cursor types.MemoryMessageCursor, retryAt time.Time, err error) {
	cursor = session.Cursor
	segment, hasNew, err := s.collectSessionTranscript(ctx, session)
	if err != nil {
		return cursor, time.Time{}, err
	}
	if !segment.end.IsZero() {
		cursor = types.MemoryMessageCursor{At: segment.end, ID: segment.endID}
	}
	if !hasNew || segment.userLineCount() < episodeMinUserLines {
		// Nothing to write and nothing to wait for. The watermark advances so
		// this conversation is not re-examined until the user says something.
		return cursor, time.Time{}, nil
	}
	// The idle window exists because a conversation cannot say whether it is
	// over, so the account waits rather than being rewritten after every turn.
	// A superseded conversation can say: the person has been talking somewhere
	// newer, so whatever they were doing here, they have moved on from it.
	//
	// The waiver is narrowed to conversations with no account yet, and the
	// narrowing is what keeps it cheap. Someone working in two conversations
	// at once is superseded on every switch, and waiving the window for all of
	// them would rewrite the other account each time — a model call per
	// switch, to restate what that account mostly already said. A conversation
	// with no account is where waiting actually costs something: nothing it
	// established is searchable, none of it has reached the profile, and the
	// next conversation is the one that needed it. "我是 wizard，我是程序员" in
	// a chat the person has since left is remembered when they open the next
	// one rather than twelve minutes later.
	if !(superseded && session.Cursor.At.IsZero()) {
		if wait := episodeDeferral(segment, session.Cursor, time.Now()); wait > 0 {
			return cursor, time.Now().Add(wait), nil
		}
	}

	previous, err := s.repo.EpisodeBySession(ctx, scope, session.SessionID)
	if err != nil {
		return cursor, time.Time{}, fmt.Errorf("load previous episode: %w", err)
	}
	response, err := s.callEpisodeModel(ctx, cfg, payload, segment, previous)
	if err != nil {
		return cursor, time.Time{}, err
	}
	if err := s.storeEpisode(ctx, scope, cfg, segment, previous, response); err != nil {
		return cursor, time.Time{}, err
	}
	return cursor, time.Time{}, nil
}

// episodeDeferral reports how long to wait before writing this conversation's
// account, or zero when it is ready now.
//
// Ready means one of two things: the conversation has gone quiet for
// episodeIdleWindow, or it has been running for episodeMaxDeferral without an
// account and is no longer allowed to postpone one.
//
// The long-running case measures from the last account rather than from the
// start of the conversation, because the cursor is where the previous account
// left off. A conversation with no account yet measures from its first turn.
func episodeDeferral(
	segment transcriptSegment, cursor types.MemoryMessageCursor, now time.Time,
) time.Duration {
	if segment.end.IsZero() {
		return 0
	}
	since := now.Sub(segment.end)
	if since >= episodeIdleWindow {
		return 0
	}
	from := cursor.At
	if from.IsZero() {
		from = segment.start()
	}
	if !from.IsZero() && segment.end.Sub(from) >= episodeMaxDeferral {
		return 0
	}
	// Clock skew between the database and this process can put the newest
	// message in the future; waiting the whole window from now is the safe
	// reading of that.
	if since < 0 {
		since = 0
	}
	return episodeIdleWindow - since
}

// scheduleFollowUpIfNeeded queues the next run when work remains.
// notBefore, when set, is when a deferred conversation becomes eligible; the
// successor waits toward then, capped by extractDeferralPoll, instead of
// coming back in seconds to find the conversation still going.
func (s *Service) scheduleFollowUpIfNeeded(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	payload types.MemoryExtractPayload,
	notBefore time.Time,
) error {
	if s.enqueuer == nil {
		return nil
	}
	pending, err := s.repo.HasPendingExtraction(ctx, scope)
	if err != nil {
		return fmt.Errorf("load pending memory extraction: %w", err)
	}
	if !pending {
		return nil
	}
	sessionID := payload.SessionID
	// Claim the slot again for the successor; FinishExtraction just cleared it.
	if _, shouldEnqueue, err := s.repo.EnqueuePendingSession(
		ctx, scope, "", extractQueuedWindow(cfg, cfg.ExtractDelay()),
	); err != nil || !shouldEnqueue {
		return err
	}
	delay := extractFollowUpDelay
	if !notBefore.IsZero() {
		if wait := time.Until(notBefore); wait > delay {
			delay = wait
		}
		// Wake before the window expires, so a conversation the person leaves
		// in the meantime is written when they leave it rather than when the
		// countdown that started before they left happens to finish.
		if delay > extractDeferralPoll {
			delay = extractDeferralPoll
		}
	}
	logger.Infof(ctx, "memory: queueing follow-up distillation for subject %s in %s",
		scope.SubjectID, delay)
	return s.enqueueExtraction(ctx, scope, sessionID, payload.MessageID, payload.ChatModelID, delay)
}

// extractionModelID resolves which model the memory pipeline should use.
//
// The settings UI says a blank extraction model means "use the model the
// conversation used", so blank must resolve rather than disable anything. Every
// caller in this package has to go through here: when only the extraction call
// applied the fallback, the topic resolver quietly lost its model tier on every
// workspace that had not picked a model — which is all of them by default.
func (s *Service) extractionModelID(
	ctx context.Context, cfg *types.MemoryConfig, payload types.MemoryExtractPayload,
) string {
	if cfg != nil && cfg.ExtractModelID != "" {
		return cfg.ExtractModelID
	}
	if payload.ChatModelID != "" {
		return payload.ChatModelID
	}
	// The turn that produced this task does not always carry the model that
	// answered it — the effective model is resolved inside the QA pipeline and
	// is not written back onto the message. Falling back to the workspace's own
	// QA model keeps the documented "blank means use the conversation model"
	// behaviour from degrading into "memory quietly does nothing".
	return s.workspaceChatModelID(ctx)
}

// workspaceChatModelID picks a usable QA model for this workspace.
//
// The choice is logged because it is a guess: without an explicitly configured
// extraction model there is no record of which model the workspace wants used
// for background work, and silently picking one is only acceptable if it is
// visible afterwards.
func (s *Service) workspaceChatModelID(ctx context.Context) string {
	if s.modelService == nil {
		return ""
	}
	models, err := s.modelService.ListModels(ctx)
	if err != nil {
		logger.Warnf(ctx, "memory: list models for extraction fallback failed: %v", err)
		return ""
	}
	for _, model := range models {
		if model == nil || model.Type != types.ModelTypeKnowledgeQA {
			continue
		}
		if model.Status != "" && model.Status != types.ModelStatusActive {
			continue
		}
		logger.Infof(ctx, "memory: no extraction model configured, using workspace model %s", model.ID)
		return model.ID
	}
	return ""
}

func isTruncated(response *types.ChatResponse) bool {
	if response == nil {
		return true
	}
	if strings.TrimSpace(response.Content) == "" {
		return true
	}
	return response.FinishReason == "length"
}

// unwrapJSONObject pulls the JSON object out of a model response that may have
// wrapped it in a code fence or surrounded it with prose. Returns the empty
// string when there is no object in there at all, which callers treat as
// "nothing to record" rather than as a failure — a model that answers with
// nothing has, in this pipeline, usually answered correctly.
func unwrapJSONObject(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	if fence := strings.Index(trimmed, "```"); fence >= 0 {
		rest := trimmed[fence+3:]
		if newline := strings.Index(rest, "\n"); newline >= 0 {
			rest = rest[newline+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		trimmed = strings.TrimSpace(rest)
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return ""
	}
	return trimmed[start : end+1]
}
