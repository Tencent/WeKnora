package activate

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// hookTimeout bounds one pipeline hook call, and stageTimeout all the hooks
// at one stage together: the user is waiting.
const (
	hookTimeout  = 5 * time.Second
	stageTimeout = 10 * time.Second
)

// PipelineHooks calls the pipeline hooks of the plugins a workspace has on,
// in registration order. It implements chatpipeline.ExternalHooks: a hook
// that fails or runs out of time is skipped, except at filterResults, where
// the results are held back unless the hook declares failOpen.
type PipelineHooks struct {
	iv   *Invoker
	reg  *registry.Registry
	gate PluginEnabledChecker
}

// NewPipelineHooks creates the hook caller.
func NewPipelineHooks(iv *Invoker, reg *registry.Registry, gate PluginEnabledChecker) *PipelineHooks {
	return &PipelineHooks{iv: iv, reg: reg, gate: gate}
}

type activeHook struct {
	m        *manifest.Manifest
	id       string
	failOpen bool
}

// active lists the hooks at a stage for the conversation's workspace, and
// the context to call them with, which bounds the stage; cancel releases it.
func (h *PipelineHooks) active(
	ctx context.Context, cm *types.ChatManage, stage string,
) (_ context.Context, cancel context.CancelFunc, _ []activeHook) {
	entries := h.reg.Contributions(manifest.PointPipelineHooks)
	if len(entries) == 0 {
		return ctx, func() {}, nil
	}
	tenantID := cm.TenantID
	if tenantID == 0 {
		tenantID, _ = types.TenantIDFromContext(ctx)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	var out []activeHook
	on := map[string]bool{}
	for _, e := range entries {
		if !slices.Contains(e.Contribution.Stages, stage) {
			continue
		}
		enabled, seen := on[e.PluginID]
		if !seen {
			ok, err := h.gate.PluginEnabled(ctx, tenantID, e.PluginID)
			enabled = err == nil && ok
			on[e.PluginID] = enabled
		}
		if m, loaded := h.reg.Plugin(e.PluginID); enabled && loaded {
			out = append(out, activeHook{m: m, id: e.Contribution.ID, failOpen: e.Contribution.FailOpen})
		}
	}
	if len(out) == 0 {
		return ctx, func() {}, nil
	}
	ctx, cancel = context.WithTimeout(ctx, stageTimeout)
	return ctx, cancel, out
}

func (h *PipelineHooks) call(ctx context.Context, hook activeHook, stage string, in, out any) bool {
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	if err := h.iv.Call(ctx, hook.m, pluginapi.HookPath(hook.id, stage), nil, in, out); err != nil {
		logger.Warnf(ctx, "[plugin] pipeline hook %s/%s at %s failed: %v", hook.m.ID, hook.id, stage, err)
		return false
	}
	return true
}

// RewriteQuery implements chatpipeline.ExternalHooks.
func (h *PipelineHooks) RewriteQuery(ctx context.Context, cm *types.ChatManage, rewritten string) string {
	ctx, cancel, hooks := h.active(ctx, cm, manifest.StageRewriteQuery)
	defer cancel()
	for _, hook := range hooks {
		var out pluginapi.RewriteQueryOutput
		in := pluginapi.RewriteQueryInput{Query: cm.Query, RewrittenQuery: rewritten, SessionID: cm.SessionID}
		if h.call(ctx, hook, manifest.StageRewriteQuery, in, &out) && strings.TrimSpace(out.Query) != "" {
			rewritten = out.Query
		}
	}
	return rewritten
}

// FilterResults implements chatpipeline.ExternalHooks.
func (h *PipelineHooks) FilterResults(
	ctx context.Context, cm *types.ChatManage, results []*types.SearchResult,
) []*types.SearchResult {
	ctx, cancel, hooks := h.active(ctx, cm, manifest.StageFilterResults)
	defer cancel()
	query := cm.RewriteQuery
	if query == "" {
		query = cm.Query
	}
	for _, hook := range hooks {
		in := pluginapi.FilterResultsInput{Query: query, Results: make([]pluginapi.RetrievedResult, len(results))}
		for i, r := range results {
			in.Results[i] = pluginapi.RetrievedResult{
				ID: r.ID, KnowledgeID: r.KnowledgeID, KnowledgeTitle: r.KnowledgeTitle, Content: r.Content,
				Score: r.Score,
			}
		}
		var out pluginapi.FilterResultsOutput
		if !h.call(ctx, hook, manifest.StageFilterResults, in, &out) {
			if hook.failOpen {
				continue
			}
			// The filter may be what keeps passages from this user.
			logger.Warnf(ctx, "[plugin] pipeline hook %s/%s did not filter the results; holding them back",
				hook.m.ID, hook.id)
			return nil
		}
		if out.Keep == nil {
			continue
		}
		results = keepResults(results, out.Keep)
	}
	return results
}

// keepResults is the results named in keep, in its order; unknown and
// repeated IDs are ignored, so a hook can only drop and reorder.
func keepResults(results []*types.SearchResult, keep []string) []*types.SearchResult {
	byID := make(map[string]*types.SearchResult, len(results))
	for _, r := range results {
		if _, dup := byID[r.ID]; !dup {
			byID[r.ID] = r
		}
	}
	out := make([]*types.SearchResult, 0, len(keep))
	for _, id := range keep {
		if r, ok := byID[id]; ok {
			out = append(out, r)
			delete(byID, id)
		}
	}
	return out
}

// AnswerAppendix implements chatpipeline.ExternalHooks.
func (h *PipelineHooks) AnswerAppendix(ctx context.Context, cm *types.ChatManage, answer string) string {
	ctx, cancel, hooks := h.active(ctx, cm, manifest.StageAnswer)
	defer cancel()
	var parts []string
	for _, hook := range hooks {
		var out pluginapi.AnswerOutput
		in := pluginapi.AnswerInput{Query: cm.Query, Answer: answer, SessionID: cm.SessionID}
		if h.call(ctx, hook, manifest.StageAnswer, in, &out) && strings.TrimSpace(out.Append) != "" {
			parts = append(parts, strings.TrimSpace(out.Append))
		}
	}
	return strings.Join(parts, "\n\n")
}
