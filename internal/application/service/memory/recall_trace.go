package memory

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

const recallQueryPreviewRunes = 500

// recallRankingTrace captures how past conversations were ranked for one
// lookup, so a recall that volunteered nothing can be told apart from one that
// never got to ask.
type recallRankingTrace struct {
	LexicalHits int
	VectorHits  int
	// VectorSkipReason names what stopped the semantic pass. Empty means it
	// ran; anything else means the ranking fell back to keywords or to
	// nothing at all.
	VectorSkipReason string
	Matched          int
	Mode             string
}

// scopeDisableReason explains why memory is off for this request. Only called
// when enabledScope returned false.
func (s *Service) scopeDisableReason(ctx context.Context) string {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return "no_principal"
	}
	cfg := s.workspaceConfig(ctx, scope.TenantID)
	if !cfg.MemoryEnabled() {
		return "workspace_disabled"
	}
	if !types.MemoryAllowedForAgent(ctx) {
		return "agent_disabled"
	}
	subject, err := s.repo.GetSubject(ctx, scope)
	if err != nil {
		return "subject_load_failed"
	}
	if subject != nil && !subject.Enabled {
		return "user_disabled"
	}
	return "unknown"
}
