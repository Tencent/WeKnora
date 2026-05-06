// Package chunker - strategy.go is the public entry point for adaptive
// chunking. Callers invoke Split / SplitParentChild instead of the legacy
// SplitText / SplitTextParentChild functions; the strategy resolver picks
// a tier based on document profile and the SplitterConfig.Strategy hint.
//
// The legacy entry points still exist in splitter.go for backwards
// compatibility — strategy.go simply layers a tier-selector on top.
package chunker

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

// Strategy values for SplitterConfig.Strategy.
const (
	StrategyAuto      = "auto"
	StrategyHeading   = "heading"
	StrategyHeuristic = "heuristic"
	StrategyRecursive = "recursive"
	StrategyLegacy    = "legacy"
)

// Split chunks text using the strategy configured in cfg. When cfg.Strategy
// is empty or "auto" the document profiler picks the tier. The function
// always returns a non-nil result: on tier failure the chain falls through
// to the legacy splitter, which is the original Tier 3 implementation.
//
// Hot path: avoids the Diagnostics struct allocation that
// SplitWithDiagnostics performs (matters in SplitParentChild where
// Split is called once per parent).
func Split(text string, cfg SplitterConfig) []Chunk {
	if text == "" {
		return nil
	}
	cfg = ensureDefaults(cfg)
	chain, profile := resolveChainWithProfile(text, cfg)
	totalChars := len([]rune(text))

	var lastOut []Chunk
	for i, tier := range chain {
		out := runTier(tier, text, cfg, profile)
		if v := ValidateChunks(out, totalChars, cfg.ChunkSize); v.OK {
			return out
		} else {
			logger.Debugf(context.Background(), "chunker: tier %s rejected: %s", tier, v.Reason)
		}
		if tier == TierLegacy && i == len(chain)-1 {
			lastOut = out
		}
	}
	if lastOut != nil {
		return lastOut
	}
	return SplitText(text, cfg)
}

// TierRejection records why a tier was rejected by the validator and the
// chain advanced to the next tier. Surfaced by SplitWithDiagnostics for
// the debug/preview endpoint.
type TierRejection struct {
	Tier   StrategyTier `json:"tier"`
	Reason string       `json:"reason"`
}

// Diagnostics captures which tier produced the returned chunks plus the
// chain that was attempted, any rejected tiers along the way, and the
// document profile that drove tier selection.
//
// Useful for surfacing in a debug UI; not produced by the normal Split
// path. The JSON shape is part of the public preview-endpoint API — keep
// field names stable.
type Diagnostics struct {
	SelectedTier StrategyTier    `json:"selected_tier"`
	TierChain    []StrategyTier  `json:"tier_chain"`
	Rejected     []TierRejection `json:"rejected"`
	// Profile is set when the auto strategy resolved the chain via the
	// document profiler. nil when an explicit Strategy bypassed profiling.
	Profile *DocProfile `json:"profile,omitempty"`
}

// SplitWithDiagnostics is the same as Split but also returns the
// diagnostic trace (selected tier, full chain, rejection reasons,
// profile when available). Use this for the chunker preview endpoint
// where the caller wants to know which tier won and why others lost.
func SplitWithDiagnostics(text string, cfg SplitterConfig) ([]Chunk, *Diagnostics) {
	// Default selected tier to legacy so an empty diag never carries the
	// zero string — that would render as a blank tag in the debug UI.
	diag := &Diagnostics{SelectedTier: TierLegacy}
	if text == "" {
		return nil, diag
	}
	cfg = ensureDefaults(cfg)
	chain, profile := resolveChainWithProfile(text, cfg)
	diag.TierChain = chain
	diag.Profile = profile
	totalChars := len([]rune(text))

	var lastOut []Chunk
	var lastTier StrategyTier
	for i, tier := range chain {
		out := runTier(tier, text, cfg, profile)
		v := ValidateChunks(out, totalChars, cfg.ChunkSize)
		if v.OK {
			diag.SelectedTier = tier
			return out, diag
		}
		diag.Rejected = append(diag.Rejected, TierRejection{Tier: tier, Reason: v.Reason})
		logger.Debugf(context.Background(), "chunker: tier %s rejected: %s", tier, v.Reason)
		if tier == TierLegacy && i == len(chain)-1 {
			lastOut = out
			lastTier = tier
		}
	}
	if lastOut != nil {
		diag.SelectedTier = lastTier
		return lastOut, diag
	}
	// Defensive last-ditch fallback.
	return SplitText(text, cfg), diag
}

// SplitParentChild is the strategy-aware analog of SplitTextParentChild.
// It runs the tier selector for parent splitting, then re-splits each
// parent into children with the small-chunk config.
//
// Child splitting honours childCfg.Strategy. If it is empty/auto and a
// parent has its own internal structure (sub-headings, numbered sub-
// sections), the appropriate tier picks it up so child chunks carry a
// finer-grained breadcrumb than the parent's. Re-profiling each parent
// is bounded by O(sum(parent_size)) ≈ O(N) total, which is the same
// order as the original parent profiling pass.
func SplitParentChild(text string, parentCfg, childCfg SplitterConfig) ParentChildResult {
	if text == "" {
		return ParentChildResult{}
	}
	parentCfg = ensureDefaults(parentCfg)
	childCfg = ensureDefaults(childCfg)

	parents := Split(text, parentCfg)
	if len(parents) == 0 {
		return ParentChildResult{}
	}

	var newParents []Chunk
	var children []ChildChunk
	childSeq := 0
	for _, parent := range parents {
		subs := Split(parent.Content, childCfg)

		parentIndex := -1
		if len(subs) > 1 || (len(subs) == 1 && subs[0].Content != parent.Content) {
			parentIndex = len(newParents)
			newParents = append(newParents, parent)
		}
		for _, sub := range subs {
			sub.Seq = childSeq
			sub.Start += parent.Start
			sub.End += parent.Start
			sub.ContextHeader = mergeBreadcrumbs(parent.ContextHeader, sub.ContextHeader)
			children = append(children, ChildChunk{Chunk: sub, ParentIndex: parentIndex})
			childSeq++
		}
	}
	return ParentChildResult{Parents: newParents, Children: children}
}

// mergeBreadcrumbs combines the parent and child heading breadcrumbs into a
// single ContextHeader. When the child re-runs heading detection on parent
// content, its first breadcrumb line typically duplicates the parent's last
// line (the parent's leading heading sits at the top of the child's input);
// drop that duplicate so the embedding context isn't redundant.
func mergeBreadcrumbs(parent, child string) string {
	if parent == "" {
		return child
	}
	if child == "" {
		return parent
	}
	parentLines := strings.Split(parent, "\n")
	childLines := strings.Split(child, "\n")
	if len(parentLines) > 0 && len(childLines) > 0 &&
		strings.TrimSpace(parentLines[len(parentLines)-1]) == strings.TrimSpace(childLines[0]) {
		childLines = childLines[1:]
	}
	if len(childLines) == 0 {
		return parent
	}
	return parent + "\n" + strings.Join(childLines, "\n")
}

// resolveChainWithProfile returns the strategy chain to attempt and, when
// the chain was selected by the profiler (auto strategy), the DocProfile
// that drove the selection. Profile is nil for explicit non-auto strategies
// so callers don't pay for an unused profiling pass.
func resolveChainWithProfile(text string, cfg SplitterConfig) ([]StrategyTier, *DocProfile) {
	switch cfg.Strategy {
	case StrategyHeading:
		return []StrategyTier{TierHeading, TierLegacy}, nil
	case StrategyHeuristic:
		return []StrategyTier{TierHeuristic, TierLegacy}, nil
	case StrategyRecursive:
		// "recursive" is a public-API alias for "legacy": both invoke
		// SplitText. Kept for backwards compatibility with stored configs.
		return []StrategyTier{TierLegacy}, nil
	case StrategyLegacy, "":
		// Empty == legacy preserves backwards compatibility with stored
		// ChunkingConfig rows that pre-date the Strategy field.
		return []StrategyTier{TierLegacy}, nil
	case StrategyAuto:
		fallthrough
	default:
		profile := ProfileDocument(text)
		return SelectStrategy(profile), profile
	}
}

// runTier dispatches the splitter implementation for the given tier.
// splitByHeadings / splitByHeuristics are package-level vars overridden
// from heading_splitter.go / heuristic_splitter.go via init(); legacy
// runs SplitText. The default branch is defensive for future
// StrategyTier additions.
//
// profile may be nil when the caller did not run the document profiler
// (explicit non-auto strategies skip profiling); splitters that need a
// profile compute one on demand.
func runTier(tier StrategyTier, text string, cfg SplitterConfig, profile *DocProfile) []Chunk {
	switch tier {
	case TierHeading:
		return splitByHeadings(text, cfg, profile)
	case TierHeuristic:
		return splitByHeuristics(text, cfg, profile)
	case TierLegacy:
		return SplitText(text, cfg)
	}
	return SplitText(text, cfg)
}

// ensureDefaults fills in zero-value config fields with sane defaults.
// Mirrors buildSplitterConfig in internal/application/service/knowledge.go
// so direct callers of this package get the same numbers.
//
// When cfg.TokenLimit is set, ChunkSize is clamped to the character budget
// that fits within that token limit (with a 10% safety factor). This makes
// chunks safe for embedding APIs that have hard token caps.
func ensureDefaults(cfg SplitterConfig) SplitterConfig {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = DefaultChunkSize
	}
	if cfg.ChunkOverlap <= 0 {
		cfg.ChunkOverlap = DefaultChunkOverlap
	}
	if len(cfg.Separators) == 0 {
		cfg.Separators = []string{"\n\n", "\n", "。"}
	}
	if cfg.TokenLimit > 0 {
		lang := LangMixed
		if len(cfg.Languages) > 0 {
			lang = cfg.Languages[0]
		}
		charBudget := CharsForTokenLimit(cfg.TokenLimit, lang)
		if charBudget > 0 && (cfg.ChunkSize == 0 || charBudget < cfg.ChunkSize) {
			cfg.ChunkSize = charBudget
		}
	}
	// Guard against pathological overlap configurations: if Overlap exceeds
	// half of ChunkSize, almost every chunk is duplicate content. Cap it at
	// ChunkSize/2 so Overlap stays a useful smoothing band rather than a
	// near-clone of the previous chunk.
	if cfg.ChunkOverlap > cfg.ChunkSize/2 && cfg.ChunkSize > 0 {
		cfg.ChunkOverlap = cfg.ChunkSize / 2
	}
	return cfg
}

// splitByHeadings is overridden by heading_splitter.go. The default no-op
// fallback ignores the profile (profile may be nil; the override computes
// one on demand when so).
var splitByHeadings = func(text string, cfg SplitterConfig, _ *DocProfile) []Chunk {
	return SplitText(text, cfg)
}

// splitByHeuristics is overridden by heuristic_splitter.go. profile may be nil.
var splitByHeuristics = func(text string, cfg SplitterConfig, _ *DocProfile) []Chunk {
	return SplitText(text, cfg)
}
