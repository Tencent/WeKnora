package chatpipeline

import (
	"context"
	"sync/atomic"

	"github.com/Tencent/WeKnora/internal/types"
)

// ExternalHooks are stages of the pipeline other code can join: plugins'
// pipeline hooks. Each call is bounded and never fails the pipeline; a hook
// that cannot answer leaves things as they were.
type ExternalHooks interface {
	// RewriteQuery returns the question retrieval should use.
	RewriteQuery(ctx context.Context, cm *types.ChatManage, rewritten string) string
	// FilterResults returns what to keep of the retrieved passages; it may
	// drop and reorder, never add.
	FilterResults(ctx context.Context, cm *types.ChatManage, results []*types.SearchResult) []*types.SearchResult
	// AnswerAppendix returns Markdown to add after a finished answer.
	AnswerAppendix(ctx context.Context, cm *types.ChatManage, answer string) string
}

var externalHooks atomic.Pointer[ExternalHooks]

// SetExternalHooks installs the hooks the pipeline calls.
func SetExternalHooks(h ExternalHooks) { externalHooks.Store(&h) }

func hooks() ExternalHooks {
	if h := externalHooks.Load(); h != nil {
		return *h
	}
	return nil
}

// PluginExternalHooks runs the rewriteQuery and filterResults hooks after
// the builtin stages they follow.
type PluginExternalHooks struct{}

// NewPluginExternalHooks registers the hook stages with the pipeline.
func NewPluginExternalHooks(eventManager *EventManager) *PluginExternalHooks {
	p := &PluginExternalHooks{}
	eventManager.Register(p)
	return p
}

// ActivationEvents implements Plugin.
func (p *PluginExternalHooks) ActivationEvents() []types.EventType {
	return []types.EventType{types.QUERY_UNDERSTAND, types.FILTER_TOP_K}
}

// OnEvent implements Plugin: the builtin stage runs first, then the hooks
// see its result.
func (p *PluginExternalHooks) OnEvent(
	ctx context.Context, eventType types.EventType, cm *types.ChatManage, next func() *PluginError,
) *PluginError {
	if err := next(); err != nil {
		return err
	}
	h := hooks()
	if h == nil {
		return nil
	}
	switch eventType {
	case types.QUERY_UNDERSTAND:
		cm.RewriteQuery = h.RewriteQuery(ctx, cm, cm.RewriteQuery)
	case types.FILTER_TOP_K:
		switch {
		case len(cm.MergeResult) > 0:
			cm.MergeResult = h.FilterResults(ctx, cm, cm.MergeResult)
		case len(cm.RerankResult) > 0:
			cm.RerankResult = h.FilterResults(ctx, cm, cm.RerankResult)
		case len(cm.SearchResult) > 0:
			cm.SearchResult = h.FilterResults(ctx, cm, cm.SearchResult)
		}
	}
	return nil
}

// AnswerAppendix is the Markdown hooks add after a finished answer, with
// the blank line that separates it; empty without hooks. Every path that
// finishes an answer (the completion stages, the no-result fallbacks) adds
// it.
func AnswerAppendix(ctx context.Context, cm *types.ChatManage, answer string) string {
	h := hooks()
	if h == nil {
		return ""
	}
	if extra := h.AnswerAppendix(ctx, cm, answer); extra != "" {
		return "\n\n" + extra
	}
	return ""
}
