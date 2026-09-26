package activate

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// chunkTimeout bounds one plugin chunking call when the caller set no
// deadline. The builtin tiers take over when it runs out.
const chunkTimeout = 2 * time.Minute

// PluginEnabledChecker is whether a workspace has a plugin on (fails closed).
type PluginEnabledChecker interface {
	PluginEnabled(ctx context.Context, tenantID uint64, pluginID string) (bool, error)
}

// PluginChunker reaches plugins' chunkers for chunker.SetPluginSplitter. A
// knowledge base whose chunking strategy is "plugin:<qualified id>" is cut
// by that chunker while the workspace has the plugin on; otherwise the
// error sends chunking back to the builtin tiers.
func PluginChunker(iv *Invoker, reg *registry.Registry, gate PluginEnabledChecker) chunker.PluginSplitter {
	return func(ctx context.Context, name, text string, cfg chunker.SplitterConfig) ([]chunker.Span, error) {
		e, ok := reg.Resolve(manifest.PointChunkers, name)
		if !ok {
			return nil, fmt.Errorf("no chunker %s is installed", name)
		}
		m, ok := reg.Plugin(e.PluginID)
		if !ok {
			return nil, fmt.Errorf("plugin %s is not loaded", e.PluginID)
		}
		tenantID, _ := types.TenantIDFromContext(ctx)
		if on, err := gate.PluginEnabled(ctx, tenantID, m.ID); err != nil || !on {
			return nil, fmt.Errorf("plugin %s is off in this workspace", m.ID)
		}
		ctx, cancel := withDefaultTimeout(ctx, chunkTimeout)
		defer cancel()
		in := pluginapi.ChunkInput{
			Text: text, ChunkSize: cfg.ChunkSize, ChunkOverlap: cfg.ChunkOverlap, Separators: cfg.Separators,
			TokenLimit: cfg.TokenLimit, Languages: cfg.Languages,
		}
		var out pluginapi.ChunkOutput
		if err := iv.Call(ctx, m, pluginapi.ChunkPath(e.Contribution.ID), nil, in, &out); err != nil {
			return nil, err
		}
		spans := make([]chunker.Span, len(out.Chunks))
		for i, c := range out.Chunks {
			spans[i] = chunker.Span{Start: c.Start, End: c.End, ContextHeader: c.ContextHeader}
		}
		return spans, nil
	}
}
