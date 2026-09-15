package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// Memory's claim on retrieval is that it changes what gets retrieved, not only
// what the answer prompt says. The place that has to be true is the query the
// retriever is given: who is asking is applied before anything is matched,
// because afterwards the passages a different question would have found are
// already gone.

type stubRetrievalMemory struct {
	stubMemoryService

	retrieval interfaces.RetrievalContext
}

func (s *stubRetrievalMemory) RetrievalContextFor(context.Context) interfaces.RetrievalContext {
	return s.retrieval
}

func TestWhoIsAskingReachesTheQueryRewriter(t *testing.T) {
	memoryService := &stubRetrievalMemory{
		retrieval: interfaces.RetrievalContext{
			Background: "在做医学影像的后端",
			Interests:  []string{"医学影像分割"},
		},
	}
	plugin := &PluginQueryUnderstand{
		memoryService: memoryService,
		config: &config.Config{Conversation: &config.ConversationConfig{
			RewritePromptSystem: "改写用户的问题。",
			RewritePromptUser:   "{{query}}",
		}},
	}

	chatManage := &types.ChatManage{}
	chatManage.Query = "分割怎么调参"

	_, userPrompt := plugin.buildPrompts(t.Context(), chatManage, nil)

	require.Contains(t, userPrompt, "在做医学影像的后端",
		"the same question means different things to different people, and only "+
			"the rewriter can act on that before retrieval runs")
	require.Contains(t, userPrompt, "医学影像分割")
	require.Contains(t, userPrompt, "分割怎么调参", "the question itself must survive")

	// Conditioning the rewriter is not a recall. The background is fed in
	// whole, relevant or not, so counting it as "memories this answer used"
	// would report unrelated memories on every single turn. That list is
	// MEMORY_RECALL's to build, from what the question actually matched.
	require.Empty(t, chatManage.UsedMemories)
}

func TestQueryRewriterIsUnchangedWithoutMemory(t *testing.T) {
	plugin := &PluginQueryUnderstand{
		memoryService: &stubRetrievalMemory{},
		config: &config.Config{Conversation: &config.ConversationConfig{
			RewritePromptSystem: "改写用户的问题。",
			RewritePromptUser:   "{{query}}",
		}},
	}
	chatManage := &types.ChatManage{}
	chatManage.Query = "分割怎么调参"

	_, userPrompt := plugin.buildPrompts(t.Context(), chatManage, nil)
	require.NotContains(t, userPrompt, "asker_background")
	require.Empty(t, chatManage.UsedMemories)
}
