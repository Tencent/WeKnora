package chunker

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/Tencent/WeKnora/internal/logger"
)

// StrategyPluginPrefix starts a Strategy that names a plugin's chunker:
// "plugin:<plugin id>/<chunker id>".
const StrategyPluginPrefix = "plugin:"

// TierPlugin is a plugin's chunker, tried before the builtin tiers.
const TierPlugin StrategyTier = "plugin"

// Span is where a plugin cut: [Start, End) in runes of the text.
type Span struct {
	Start, End    int
	ContextHeader string
}

// PluginSplitter asks a plugin's chunker where to cut text.
type PluginSplitter func(ctx context.Context, chunker, text string, cfg SplitterConfig) ([]Span, error)

var pluginSplitter atomic.Pointer[PluginSplitter]

// SetPluginSplitter installs the function that reaches plugin chunkers.
func SetPluginSplitter(f PluginSplitter) { pluginSplitter.Store(&f) }

// IsPluginStrategy reports whether a Strategy names a plugin's chunker.
func IsPluginStrategy(s string) bool { return strings.HasPrefix(s, StrategyPluginPrefix) }

// WithPlugins lets cfg use a plugin chunker for the workspace of ctx. A
// config whose strategy is not a plugin's is returned as is.
func WithPlugins(ctx context.Context, cfg SplitterConfig) SplitterConfig {
	if !IsPluginStrategy(cfg.Strategy) {
		return cfg
	}
	cfg.external = func(chunker, text string, c SplitterConfig) ([]Span, error) {
		f := pluginSplitter.Load()
		if f == nil {
			return nil, errors.New("plugin chunkers are not available")
		}
		return (*f)(ctx, chunker, text, c)
	}
	return cfg
}

// maxPluginChunkFactor bounds a plugin's chunks: one longer than this many
// times the target size is a chunker ignoring the settings.
const maxPluginChunkFactor = 4

// splitByPlugin runs the plugin chunker cfg names. Anything wrong (no
// plugin, an error, a span outside the text) yields nil, so the builtin
// tiers take over.
func splitByPlugin(text string, cfg SplitterConfig) []Chunk {
	if cfg.external == nil {
		return nil
	}
	name := strings.TrimPrefix(cfg.Strategy, StrategyPluginPrefix)
	spans, err := cfg.external(name, text, cfg)
	if err != nil {
		logger.Warnf(context.Background(), "chunker: plugin chunker %s failed, using the builtin tiers: %v", name, err)
		return nil
	}
	runes := []rune(text)
	out := make([]Chunk, 0, len(spans))
	for i, s := range spans {
		if s.Start < 0 || s.End <= s.Start || s.End > len(runes) {
			logger.Warnf(context.Background(),
				"chunker: plugin chunker %s cut [%d, %d) outside the text; using the builtin tiers",
				name, s.Start, s.End)
			return nil
		}
		out = append(out, Chunk{
			Content: string(runes[s.Start:s.End]), ContextHeader: s.ContextHeader,
			Seq: i, Start: s.Start, End: s.End,
		})
	}
	return out
}

// validatePluginChunks is the plugin tier's check: its chunks need not look
// like the builtin splitters', only be usable.
func validatePluginChunks(chunks []Chunk, chunkSize int) ValidationResult {
	if len(chunks) == 0 {
		return ValidationResult{Reason: "no chunks produced"}
	}
	for _, c := range chunks {
		if strings.TrimSpace(c.Content) == "" {
			return ValidationResult{Reason: "empty chunk"}
		}
		if n := len([]rune(c.Content)); n > maxPluginChunkFactor*chunkSize {
			return ValidationResult{Reason: "chunk far over the target size"}
		}
	}
	return ValidationResult{OK: true}
}
