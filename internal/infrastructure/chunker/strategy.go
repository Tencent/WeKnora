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
func Split(text string, cfg SplitterConfig) []Chunk {
	chunks, _ := SplitWithDiagnostics(text, cfg)
	return chunks
}

// TierRejection records why a tier was rejected by the validator and the
// chain advanced to the next tier. Surfaced by SplitWithDiagnostics for
// the debug/preview endpoint.
type TierRejection struct {
	Tier   StrategyTier `json:"tier"`
	Reason string       `json:"reason"`
}

// Diagnostics captures which tier produced the returned chunks plus the
// chain that was attempted and any rejected tiers along the way. Useful
// for surfacing in a debug UI; not produced by the normal Split path.
type Diagnostics struct {
	SelectedTier StrategyTier    `json:"selected_tier"`
	TierChain    []StrategyTier  `json:"tier_chain"`
	Rejected     []TierRejection `json:"rejected"`
}

// SplitWithDiagnostics is the same as Split but also returns the
// diagnostic trace (selected tier, full chain, rejection reasons). Use
// this for the chunker preview endpoint where the caller wants to know
// which tier won and why others lost.
func SplitWithDiagnostics(text string, cfg SplitterConfig) ([]Chunk, *Diagnostics) {
	diag := &Diagnostics{}
	if text == "" {
		return nil, diag
	}
	cfg = ensureDefaults(cfg)
	chain := resolveChain(text, cfg)
	diag.TierChain = chain
	totalChars := len([]rune(text))

	var lastOut []Chunk
	var lastTier StrategyTier
	for i, tier := range chain {
		out := runTier(tier, text, cfg)
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
	diag.SelectedTier = TierLegacy
	return SplitText(text, cfg), diag
}

// SplitParentChild is the strategy-aware analog of SplitTextParentChild.
// It runs the tier selector for parent splitting, then re-splits each
// parent into children with the small-chunk config.
//
// Children are forced to recursive splitting — they are sub-pieces of an
// already-segmented parent, so re-profiling per parent would cost N extra
// O(N) document scans without any real chance of picking a better tier.
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

	// Pin children to the recursive tier so SplitText runs directly without
	// the profiler/strategy chain re-deciding per parent. Preserves Tier-1
	// breadcrumb context (already attached to each parent) since we only
	// re-split parent content, not re-detect headings.
	childCfg.Strategy = StrategyRecursive

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
			// Children inherit the parent's breadcrumb so embedding still
			// sees the section context — but only if the child itself does
			// not already carry one (sub-splits don't, but defensive).
			if sub.ContextHeader == "" {
				sub.ContextHeader = parent.ContextHeader
			}
			children = append(children, ChildChunk{Chunk: sub, ParentIndex: parentIndex})
			childSeq++
		}
	}
	return ParentChildResult{Parents: newParents, Children: children}
}

// resolveChain returns the strategy chain to attempt. An explicit non-auto
// strategy bypasses the profiler entirely and pins to the requested tier.
func resolveChain(text string, cfg SplitterConfig) []StrategyTier {
	switch cfg.Strategy {
	case StrategyHeading:
		return []StrategyTier{TierHeading, TierLegacy}
	case StrategyHeuristic:
		return []StrategyTier{TierHeuristic, TierLegacy}
	case StrategyRecursive:
		return []StrategyTier{TierRecursive, TierLegacy}
	case StrategyLegacy, "":
		// Empty == legacy preserves backwards compatibility with stored
		// ChunkingConfig rows that pre-date the Strategy field.
		return []StrategyTier{TierLegacy}
	case StrategyAuto:
		fallthrough
	default:
		profile := ProfileDocument(text)
		return SelectStrategy(profile)
	}
}

// runTier dispatches the splitter implementation for the given tier.
// Heading and heuristic tiers are stubbed in this scaffold and currently
// fall through to the legacy splitter — they are filled in in later phases.
func runTier(tier StrategyTier, text string, cfg SplitterConfig) []Chunk {
	switch tier {
	case TierHeading:
		return splitByHeadings(text, cfg)
	case TierHeuristic:
		return splitByHeuristics(text, cfg)
	case TierRecursive, TierLegacy:
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

// splitByHeadings is overridden by heading_splitter.go.
var splitByHeadings = func(text string, cfg SplitterConfig) []Chunk {
	return SplitText(text, cfg)
}

// splitByHeuristics is overridden by heuristic_splitter.go.
var splitByHeuristics = func(text string, cfg SplitterConfig) []Chunk {
	return SplitText(text, cfg)
}
