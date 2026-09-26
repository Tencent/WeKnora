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
	NewPluginExternalHooks(em)
	em.Register(NewPluginFilterTopK(NewEventManager())) // builtin runs as the next plugin

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
