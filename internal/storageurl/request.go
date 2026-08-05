package storageurl

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// NewRequestRewriter builds the Rewriter for one API request or response stream.
//
// ModeHandle yields a disabled Rewriter, so the default path resolves nothing
// and costs no access-grant rows. The tenant is taken from ctx because a
// reference may live on a tenant-configured storage backend rather than the
// process-wide default.
//
// No extra authorization gate applies here, unlike the `/files` proxy which
// rejects KB-restricted API keys. That gate exists because `/files` takes an
// arbitrary caller-supplied path it cannot bind to a KB allow-list. Here the
// references come from a response the caller is already authorized to receive,
// so the server — not the client — chooses which resources get a URL.
func NewRequestRewriter(
	ctx context.Context,
	mode Mode,
	defaultSvc interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
) *Rewriter {
	if mode != ModePublic {
		return NewRewriter(nil, "API")
	}
	tenant, _ := types.TenantInfoFromContext(ctx)
	var resolvers []interfaces.StorageBackendResolver
	if storageResolver != nil {
		resolvers = append(resolvers, storageResolver)
	}
	resolver := NewFileServiceResolver(tenant, defaultSvc, resolvers...).WithContext(ctx)
	return NewRewriter(resolver, "API")
}

// RewriteMessagesResponse returns rewritten message copies so callers can keep
// the originals untouched (for example objects returned from a service cache).
func (w *Rewriter) RewriteMessagesResponse(ctx context.Context, messages []*types.Message) []*types.Message {
	if !w.Enabled() || len(messages) == 0 {
		return messages
	}
	out := cloneMessages(messages)
	w.RewriteMessages(ctx, out)
	return out
}

// cloneMessages deep-copies messages via JSON so nested slices and agent steps
// are independent of the source slice.
func cloneMessages(messages []*types.Message) []*types.Message {
	data, err := json.Marshal(messages)
	if err != nil {
		return messages
	}
	var out []*types.Message
	if err := json.Unmarshal(data, &out); err != nil {
		return messages
	}
	return out
}

// RewriteMessages replaces storage references in a message history response so
// clients receive loadable image URLs. Messages are mutated in place.
func (w *Rewriter) RewriteMessages(ctx context.Context, messages []*types.Message) {
	if !w.Enabled() {
		return
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		message.Content = w.String(ctx, message.Content)
		for i := range message.Images {
			message.Images[i].URL = w.Ref(ctx, message.Images[i].URL)
			message.Images[i].Caption = w.String(ctx, message.Images[i].Caption)
		}
		message.KnowledgeReferences = w.CopyReferences(ctx, message.KnowledgeReferences)
		w.rewriteAgentSteps(ctx, message.AgentSteps)
	}
}

// CopyReferences returns rewritten copies of retrieval results, covering both the
// chunk text and the structured image_info payload.
//
// Copies rather than in-place edits because an SSE references payload shares its
// *SearchResult pointers with the stream replay buffer and the assistant message
// being persisted; rewriting those in place would corrupt both.
func (w *Rewriter) CopyReferences(ctx context.Context, refs []*types.SearchResult) []*types.SearchResult {
	if !w.Enabled() || refs == nil {
		return refs
	}
	out := make([]*types.SearchResult, len(refs))
	for i, ref := range refs {
		if ref == nil {
			continue
		}
		rewritten := *ref
		rewritten.Content = w.String(ctx, ref.Content)
		rewritten.MatchedContent = w.String(ctx, ref.MatchedContent)
		rewritten.ImageInfo = w.String(ctx, ref.ImageInfo)
		out[i] = &rewritten
	}
	return out
}

// CopyData returns a rewritten copy of an SSE metadata map, or data itself when
// it holds no storage reference. Agent tool results put renderable Markdown into
// this map, and its shape is tool-defined, so every string leaf is rewritten.
func (w *Rewriter) CopyData(ctx context.Context, data map[string]interface{}) map[string]interface{} {
	if !w.Enabled() || data == nil {
		return data
	}
	rewritten, changed := w.copyValue(ctx, data, 0)
	if !changed {
		return data
	}
	out, _ := rewritten.(map[string]interface{})
	return out
}

// maxDataDepth bounds recursion into tool-defined metadata. Renderable content
// sits within a couple of levels; the cap only guards against a pathologically
// nested payload.
const maxDataDepth = 8

func (w *Rewriter) copyValue(ctx context.Context, value interface{}, depth int) (interface{}, bool) {
	if depth > maxDataDepth {
		return value, false
	}
	switch typed := value.(type) {
	case string:
		out := w.String(ctx, typed)
		return out, out != typed
	// The references event carries its results twice: once in
	// StreamResponse.KnowledgeReferences and once in Data. With an in-memory
	// stream manager these arrive as the typed slice rather than the decoded
	// []interface{} a Redis round-trip produces, so both forms need a case or
	// the Data copy leaks handles the caller asked to have resolved.
	case types.References:
		return types.References(w.CopyReferences(ctx, typed)), true
	case []*types.SearchResult:
		return w.CopyReferences(ctx, typed), true
	case []string:
		out := make([]string, len(typed))
		changed := false
		for i, item := range typed {
			out[i] = w.String(ctx, item)
			changed = changed || out[i] != item
		}
		return out, changed
	case map[string]string:
		out := make(map[string]string, len(typed))
		changed := false
		for key, item := range typed {
			out[key] = w.String(ctx, item)
			changed = changed || out[key] != item
		}
		return out, changed
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		changed := false
		for key, item := range typed {
			converted, itemChanged := w.copyValue(ctx, item, depth+1)
			out[key] = converted
			changed = changed || itemChanged
		}
		return out, changed
	case []interface{}:
		out := make([]interface{}, len(typed))
		changed := false
		for i, item := range typed {
			converted, itemChanged := w.copyValue(ctx, item, depth+1)
			out[i] = converted
			changed = changed || itemChanged
		}
		return out, changed
	default:
		return value, false
	}
}

// rewriteAgentSteps covers the reasoning trace, whose tool output embeds Markdown
// images for retrieved figures and generated charts.
func (w *Rewriter) rewriteAgentSteps(ctx context.Context, steps types.AgentSteps) {
	for i := range steps {
		steps[i].Thought = w.String(ctx, steps[i].Thought)
		steps[i].ReasoningContent = w.String(ctx, steps[i].ReasoningContent)
		for j := range steps[i].ToolCalls {
			call := &steps[i].ToolCalls[j]
			call.Reflection = w.String(ctx, call.Reflection)
			if call.Result != nil {
				call.Result.Output = w.String(ctx, call.Result.Output)
			}
		}
	}
}
