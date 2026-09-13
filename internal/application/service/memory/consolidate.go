package memory

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// forcedConsolidateInterval is the floor between two rewrites one person asks
// for. Short enough that a real retry — fix the model, press again — is not
// blocked, long enough that the button cannot be held down: the endpoint is
// Viewer-level and each press is worth a whole-profile model call.
const forcedConsolidateInterval = time.Minute

// ConsolidateNow rewrites this person's consolidated profile immediately,
// instead of waiting for the background pass to decide a rewrite is due.
//
// Only Reviewed and Skipped can be filled in: Reviewed is how many accounts the
// rewrite read, and Skipped says why the profile was left alone. Merged,
// Demoted and Expired describe a per-statement merge that does not happen and
// stay zero. The result shape is due to be replaced when the memory API is
// redesigned around the document layer.
func (s *Service) ConsolidateNow(ctx context.Context) (*types.MemoryConsolidationResult, error) {
	scope, cfg, ok := s.enabledScope(ctx)
	if !ok {
		return nil, ErrMemoryDisabled
	}
	subject, err := s.repo.EnsureSubject(ctx, scope)
	if err != nil {
		return nil, err
	}

	result := &types.MemoryConsolidationResult{}
	// A clock of its own, not the one the background pass marks. Maintenance
	// nobody asked for must not be able to report that this person only just
	// asked for something they never asked for.
	if last := subject.ForcedConsolidatedAt; last != nil &&
		time.Since(*last) < forcedConsolidateInterval {
		result.Skipped = types.MemoryConsolidationSkipTooSoon
		return result, nil
	}
	if err := s.repo.MarkForcedConsolidated(ctx, scope); err != nil {
		logger.Warnf(ctx, "memory: mark forced consolidation failed: %v", err)
	}

	// Straight to the rewrite, past the check that asks whether enough has
	// changed to be worth the call — that question is what the person pressing
	// the button has just answered. Everything below it still applies: the
	// lease, the floor on how many accounts a profile may be built from, and
	// the cap on how many the rewrite reads, which is what keeps this bounded
	// on a request.
	outcome, err := s.rewriteDigest(ctx, scope, cfg, types.MemoryExtractPayload{})
	result.Reviewed = outcome.Considered
	if err != nil {
		// The model is the only part of this that can fail in a way the person
		// can do something about, so it is reported rather than returned: a
		// 500 would read as "the button is broken" when the answer is "try
		// again once the model is reachable".
		logger.Warnf(ctx, "memory: requested profile rewrite failed: %v", err)
		result.Skipped = types.MemoryConsolidationSkipModelUnavailable
		return result, nil
	}
	result.Skipped = outcome.Skipped
	return result, nil
}
