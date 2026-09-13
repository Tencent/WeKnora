package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// digestLeaseTTL bounds how long one rewrite may hold the document. Long
	// enough for a slow model on a large selection, short enough that a worker
	// killed mid-call does not wedge the profile until someone notices.
	digestLeaseTTL = 10 * time.Minute
)

// refreshDigestIfDue rewrites the injected profile when there is new material.
//
// Runs at the tail of the background extraction task, never on the request
// path. The ordering matters: phase one has already filed its account by the
// time this is called, so a rewrite triggered by a conversation includes that
// conversation.
func (s *Service) refreshDigestIfDue(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	payload types.MemoryExtractPayload,
) {
	if !s.digestIsDue(ctx, scope) {
		return
	}
	if _, err := s.rewriteDigest(ctx, scope, cfg, payload); err != nil {
		logger.Warnf(ctx, "memory: rewrite digest failed: %v", err)
	}
}

// digestIsDue decides whether a rewrite is worth its call: there is new
// material, and there is enough of it to write from.
//
// Dirtiness is counted rather than guessed: an account whose digest_revision is
// behind the live profile is one the profile has never read. That is the same
// check Codex makes by asking git whether its memory workspace changed, and it
// has the property that matters — a run that finds nothing new makes no model
// call at all.
//
// New material used to have to clear one of two more bars, four unconsolidated
// accounts or half an hour since the last rewrite, to batch several accounts
// into one call. Both are gone, because the batching they bought was already
// happening and the delay they charged for it was not.
//
// Accounts cannot arrive faster than conversations go quiet: phase one waits
// out episodeIdleWindow, and one run consolidates once no matter how many
// accounts it filed. So the natural rate is already one rewrite per
// conversation ended, and the two bars did not lower it — they only moved it
// later, to a moment that is reliably the wrong one. The profile is what every
// turn injects, so the cost of a stale one is paid exactly when the person
// opens their next conversation, which is the moment the bars made most likely
// to find the profile behind. They also could not be satisfied while nothing
// was running: the follow-up that would notice the half hour had passed is
// only queued while a conversation is still pending, so a rewrite deferred by
// these bars waited for the person to speak again rather than for its timer.
func (s *Service) digestIsDue(ctx context.Context, scope interfaces.MemoryScope) bool {
	dirty, err := s.repo.CountEpisodesAwaitingDigest(ctx, scope)
	if err != nil {
		logger.Warnf(ctx, "memory: count unconsolidated accounts failed: %v", err)
		return false
	}
	return dirty >= int64(digestMinEpisodes)
}

// digestRewrite reports what one phase-two attempt did.
//
// A background rewrite needs only the error. The endpoint someone presses has
// to be able to tell them why their profile did not change, which is a
// different question from whether anything went wrong — declining to rewrite
// is the common, correct outcome.
type digestRewrite struct {
	// Revision is the profile revision that was installed, 0 when none was.
	Revision int64
	// Considered is how many accounts the rewrite read.
	Considered int
	// Skipped is why no profile was written, empty when one was.
	Skipped string
}

// rewriteDigest is phase two: one call replaces the whole profile.
//
// Every early return releases the lease, because the alternative is a profile
// that stops being rewritten for ten minutes each time a rewrite declines to
// run.
func (s *Service) rewriteDigest(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	payload types.MemoryExtractPayload,
) (digestRewrite, error) {
	lease, err := s.repo.AcquireDigestLease(ctx, scope, digestLeaseTTL)
	if err != nil {
		return digestRewrite{}, fmt.Errorf("acquire digest lease: %w", err)
	}
	if lease == "" {
		// Another run holds it. Whatever it produces will include the account
		// this run just filed, because it selects from the store rather than
		// from anything this run is holding. Reported as "too soon" because
		// that is what it means to whoever asked: a rewrite is happening, and
		// theirs would have been the second one.
		return digestRewrite{Skipped: types.MemoryConsolidationSkipTooSoon}, nil
	}
	release := true
	defer func() {
		if release {
			if err := s.repo.ReleaseDigestLease(ctx, scope, lease); err != nil {
				logger.Warnf(ctx, "memory: release digest lease failed: %v", err)
			}
		}
	}()

	digest, err := s.repo.GetDigest(ctx, scope)
	if err != nil {
		return digestRewrite{}, fmt.Errorf("load digest: %w", err)
	}
	episodes, err := s.repo.SelectEpisodesForDigest(
		ctx, scope, types.MemoryDigestMaxEpisodes, types.MemoryEpisodeUnusedDays)
	if err != nil {
		return digestRewrite{}, fmt.Errorf("select accounts: %w", err)
	}
	outcome := digestRewrite{Considered: len(episodes)}
	if len(episodes) < digestMinEpisodes {
		outcome.Skipped = types.MemoryConsolidationSkipTooFewItems
		return outcome, nil
	}

	revision := int64(0)
	if digest != nil {
		revision = digest.Revision
	}
	fresh := 0
	ids := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		ids = append(ids, episode.ID)
		if episode.DigestRevision <= revision-1 || episode.DigestRevision == 0 {
			fresh++
		}
	}

	notes, err := s.repo.ListNotes(ctx, scope, types.MemoryNotesMaxItems)
	if err != nil {
		logger.Warnf(ctx, "memory: load notes for digest failed: %v", err)
	}

	body, err := s.callDigestModel(
		ctx, cfg, payload, digest, episodes, fresh, types.MemoryNoteTexts(notes))
	if err != nil {
		return outcome, err
	}
	body = types.SanitizeMemoryDocument(body, types.MemoryDigestMaxRunes)
	if !digestLooksValid(body) {
		// Keeping the previous profile is the conservative failure. A model
		// that returned prose instead of a document would otherwise replace a
		// working profile with something the read path cannot take sections
		// out of, and the accounts it was built from are all still there for
		// the next attempt.
		return outcome, fmt.Errorf("%w: rewrite did not return a profile", errInvalidExtractionOutput)
	}
	if redacted, changed := types.RedactSensitive(body); changed {
		logger.Infof(ctx, "memory: redacted sensitive material from the profile")
		body = types.SanitizeMemoryDocument(redacted, types.MemoryDigestMaxRunes)
	}

	newRevision, err := s.repo.SaveDigest(ctx, scope, lease, body)
	if err != nil {
		if errors.Is(err, types.ErrMemoryDigestLeaseLost) {
			// Another run took over and has already written a profile that saw
			// at least as much as this one did. Discarding this result is
			// correct; overwriting theirs would lose whichever of the two ran
			// second.
			logger.Infof(ctx, "memory: digest lease moved on, discarding this rewrite")
			outcome.Skipped = types.MemoryConsolidationSkipTooSoon
			return outcome, nil
		}
		return outcome, fmt.Errorf("save digest: %w", err)
	}
	// The lease is cleared by the same write that installed the profile.
	release = false

	if err := s.repo.MarkEpisodesConsolidated(ctx, scope, ids, newRevision); err != nil {
		logger.Warnf(ctx, "memory: mark accounts consolidated failed: %v", err)
	}
	if err := s.repo.SetDigestEpisodeCount(ctx, scope, len(episodes)); err != nil {
		logger.Warnf(ctx, "memory: record digest source count failed: %v", err)
	}
	logger.Infof(ctx, "memory: rewrote the profile for %s at revision %d from %d accounts (%d new)",
		scope.SubjectID, newRevision, len(episodes), fresh)
	outcome.Revision = newRevision
	return outcome, nil
}

// digestLooksValid checks that a rewrite returned a document rather than an
// apology.
//
// One heading is enough. A profile built from two accounts can legitimately
// have nothing to say under three of the four sections, and requiring all of
// them would mean rejecting the correct output and keeping a staler one.
func digestLooksValid(body string) bool {
	if strings.TrimSpace(body) == "" {
		return false
	}
	for _, heading := range []string{
		types.MemoryDigestSectionProfile,
		types.MemoryDigestSectionPreferences,
		types.MemoryDigestSectionTips,
		types.MemoryDigestSectionIndex,
	} {
		if strings.Contains(body, heading) {
			return true
		}
	}
	return false
}

// callDigestModel makes one phase-two call.
//
// The consolidation model is preferred over the extraction model when the
// workspace configured one. This is the asymmetry Codex settled on — a small
// model per conversation, a strong one for the document that every conversation
// reads — and it is the right place to spend: phase one describes, phase two
// judges what generalizes, and misjudging that is what a user actually notices.
func (s *Service) callDigestModel(
	ctx context.Context,
	cfg *types.MemoryConfig,
	payload types.MemoryExtractPayload,
	previous *types.MemoryDigest,
	episodes []*types.MemoryEpisode,
	fresh int,
	notes []string,
) (string, error) {
	modelID := s.digestModelID(ctx, cfg, payload)
	if modelID == "" {
		return "", fmt.Errorf("no chat model available for memory consolidation")
	}
	chatModel, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil {
		return "", fmt.Errorf("get consolidation model: %w", err)
	}

	instructions := ""
	if cfg != nil {
		instructions = cfg.ExtractInstructions
	}
	userPrompt := buildDigestPrompt(previous, episodes, fresh, notes, instructions)

	response, err := s.completeDigest(ctx, chatModel, userPrompt, digestBudgetTokens)
	if err != nil {
		return "", err
	}
	if isTruncated(response) {
		logger.Warnf(ctx, "memory: profile rewrite hit the token ceiling, retrying with %d tokens",
			digestBudgetRetryTokens)
		response, err = s.completeDigest(ctx, chatModel, userPrompt, digestBudgetRetryTokens)
		if err != nil {
			return "", err
		}
		if isTruncated(response) {
			return "", fmt.Errorf("%w: no usable profile within %d tokens",
				errInvalidExtractionOutput, digestBudgetRetryTokens)
		}
	}
	return stripCodeFence(response.Content), nil
}

// digestModelID resolves which model rewrites the profile.
func (s *Service) digestModelID(
	ctx context.Context, cfg *types.MemoryConfig, payload types.MemoryExtractPayload,
) string {
	if cfg != nil && cfg.ConsolidateModelID != "" {
		return cfg.ConsolidateModelID
	}
	return s.extractionModelID(ctx, cfg, payload)
}

// completeDigest issues one rewrite call.
//
// No response schema, unlike every other call in this pipeline. The output is a
// Markdown document, and asking for it inside a JSON string buys nothing while
// costing real quality: the model spends its attention escaping newlines, and a
// single unescaped quote turns a good profile into a parse error.
func (s *Service) completeDigest(
	ctx context.Context, chatModel chat.Chat, userPrompt string, budget int,
) (*types.ChatResponse, error) {
	thinking := false
	ctx = types.WithLLMCallMetadata(ctx, "memory_digest", "")
	response, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "system", Content: digestSystemPrompt},
		{Role: "user", Content: userPrompt},
	}, &chat.ChatOptions{
		Temperature:         0.2,
		MaxCompletionTokens: budget,
		Thinking:            &thinking,
	})
	if err != nil {
		return nil, fmt.Errorf("consolidation model call: %w", err)
	}
	return response, nil
}

// stripCodeFence removes a wrapping fence a model added despite being told not
// to. The content is Markdown, so a fence is never part of the document itself
// — only ever a wrapper around it.
func stripCodeFence(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	if newline := strings.Index(trimmed, "\n"); newline >= 0 {
		trimmed = trimmed[newline+1:]
	}
	if end := strings.LastIndex(trimmed, "```"); end >= 0 {
		trimmed = trimmed[:end]
	}
	return strings.TrimSpace(trimmed)
}
