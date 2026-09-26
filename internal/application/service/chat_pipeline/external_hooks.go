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

// PluginExternalHooks runs the rewriteQuery and filterResults hooks right
// after the builtin stages they follow (query understanding, top-k
// filtering) and before the other plugins of those events, so entity
// extraction sees the rewritten question.
type PluginExternalHooks struct{}

// NewPluginExternalHooks registers the hook stages with the pipeline. Like
// every stage, a hook works before it calls the next plugin, so it must be
// registered right after the builtin stages it follows.
func NewPluginExternalHooks(eventManager *EventManager) *PluginExternalHooks {
	p := &PluginExternalHooks{}
	eventManager.Register(p)
	return p
}

// ActivationEvents implements Plugin.
func (p *PluginExternalHooks) ActivationEvents() []types.EventType {
	return []types.EventType{types.QUERY_UNDERSTAND, types.FILTER_TOP_K}
}

// OnEvent implements Plugin: the builtin stage before it has run, the
// hooks see its result and the plugins after it see theirs. Hooks that drop
// every passage leave nothing to answer from, which ends retrieval like an
// empty search (ErrSearchNothing: the fallback answer).
func (p *PluginExternalHooks) OnEvent(
	ctx context.Context, eventType types.EventType, cm *types.ChatManage, next func() *PluginError,
) *PluginError {
	h := hooks()
	if h == nil {
		return next()
	}
	switch eventType {
	case types.QUERY_UNDERSTAND:
		cm.RewriteQuery = h.RewriteQuery(ctx, cm, cm.RewriteQuery)
	case types.FILTER_TOP_K:
		var list *[]*types.SearchResult
		switch {
		case len(cm.MergeResult) > 0:
			list = &cm.MergeResult
		case len(cm.RerankResult) > 0:
			list = &cm.RerankResult
		case len(cm.SearchResult) > 0:
			list = &cm.SearchResult
		}
		if list != nil {
			if *list = h.FilterResults(ctx, cm, *list); len(*list) == 0 {
				return ErrSearchNothing
			}
		}
	}
	return next()
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
