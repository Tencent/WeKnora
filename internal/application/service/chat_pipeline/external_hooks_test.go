package chatpipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

type fakeHooks struct{ answers []string }

func (f *fakeHooks) RewriteQuery(_ context.Context, _ *types.ChatManage, rewritten string) string {
	return rewritten + "!"
}

func (f *fakeHooks) FilterResults(
	_ context.Context, _ *types.ChatManage, results []*types.SearchResult,
) []*types.SearchResult {
	return results[:1]
}

func (f *fakeHooks) AnswerAppendix(_ context.Context, _ *types.ChatManage, answer string) string {
	f.answers = append(f.answers, answer)
	return "_note_"
}

func withHooks(t *testing.T, h ExternalHooks) {
	t.Helper()
	prev := externalHooks.Load()
	SetExternalHooks(h)
	t.Cleanup(func() { externalHooks.Store(prev) })
}

func TestExternalHooksFollowTheBuiltinStages(t *testing.T) {
	withHooks(t, &fakeHooks{})
	em := NewEventManager()
	em.Register(NewPluginFilterTopK(NewEventManager())) // the builtin stage runs first
	NewPluginExternalHooks(em)

	cm := &types.ChatManage{}
	cm.RewriteQuery = "price"
	require.Nil(t, em.Trigger(context.Background(), types.QUERY_UNDERSTAND, cm))
	require.Equal(t, "price!", cm.RewriteQuery)

	cm.RerankTopK = 3
	cm.MergeResult = []*types.SearchResult{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	require.Nil(t, em.Trigger(context.Background(), types.FILTER_TOP_K, cm))
	require.Len(t, cm.MergeResult, 1)
}

func TestStreamAppendsTheHooksNote(t *testing.T) {
	hooks := &fakeHooks{}
	withHooks(t, hooks)
	bus := &syncEventBus{}
	model := &openStreamChat{closeStream: true, chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "It costs "},
		{ResponseType: types.ResponseTypeAnswer, Content: "10.", Done: true},
	}}
	cm := &types.ChatManage{}
	cm.SessionID, cm.EventBus = "sess-hooks", bus
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
	require.Nil(t, plugin.OnEvent(context.Background(), types.CHAT_COMPLETION_STREAM, cm,
		func() *PluginError { return nil }))
	require.Eventually(t, func() bool { return len(bus.finalAnswerContents()) == 2 }, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, []string{"It costs ", "10.\n\n_note_"}, bus.finalAnswerContents())
	require.Equal(t, []string{"It costs 10."}, hooks.answers)
}

type dropAllHooks struct{ fakeHooks }

func (dropAllHooks) FilterResults(context.Context, *types.ChatManage, []*types.SearchResult) []*types.SearchResult {
	return nil
}

// Hooks that drop every passage end retrieval like an empty search, so the
// fallback answer is given instead of an answer from nothing.
func TestExternalHooksDroppingEverythingFindNothing(t *testing.T) {
	withHooks(t, &dropAllHooks{})
	em := NewEventManager()
	em.Register(NewPluginFilterTopK(NewEventManager()))
	NewPluginExternalHooks(em)
	cm := &types.ChatManage{}
	cm.RerankTopK = 3
	cm.MergeResult = []*types.SearchResult{{ID: "a"}}
	require.Equal(t, ErrSearchNothing, em.Trigger(context.Background(), types.FILTER_TOP_K, cm))
	require.Empty(t, cm.MergeResult)

	// Nothing retrieved in the first place is not the hooks' doing.
	require.Nil(t, em.Trigger(context.Background(), types.FILTER_TOP_K, &types.ChatManage{}))
}

// stagePlugin stands for a builtin stage: it works, then calls the next plugin.
type stagePlugin struct {
	event types.EventType
	work  func(*types.ChatManage)
}

func (s stagePlugin) ActivationEvents() []types.EventType { return []types.EventType{s.event} }

func (s stagePlugin) OnEvent(
	_ context.Context, _ types.EventType, cm *types.ChatManage, next func() *PluginError,
) *PluginError {
	s.work(cm)
	return next()
}

// The plugins after query understanding (entity extraction) see the
// question the hooks rewrote.
func TestRewrittenQueryReachesLaterStages(t *testing.T) {
	withHooks(t, &fakeHooks{})
	em := NewEventManager()
	em.Register(stagePlugin{types.QUERY_UNDERSTAND, func(cm *types.ChatManage) { cm.RewriteQuery = "price" }})
	NewPluginExternalHooks(em)
	var seen string
	em.Register(stagePlugin{types.QUERY_UNDERSTAND, func(cm *types.ChatManage) { seen = cm.RewriteQuery }})
	require.Nil(t, em.Trigger(context.Background(), types.QUERY_UNDERSTAND, &types.ChatManage{}))
	require.Equal(t, "price!", seen)
}
