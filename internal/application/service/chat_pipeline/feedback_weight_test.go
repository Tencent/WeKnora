package chatpipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// stubWeightRepo is a deterministic in-memory ChunkWeightLookup used to
// drive the feedback weight plugin in tests. Returns pre-baked weights keyed
// by chunk ID; absent keys are treated as 1.0.
type stubWeightRepo struct {
	weights map[string]float64
	err     error
}

func (s *stubWeightRepo) ListChunkRecallWeights(_ context.Context, _ uint64, ids []string) (map[string]float64, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]float64, len(ids))
	for _, id := range ids {
		if w, ok := s.weights[id]; ok {
			out[id] = w
		}
	}
	return out, nil
}

// TestPluginFeedbackWeightIsNoopWhenDisabled verifies the gating logic. A
// disabled tenant should see its input list returned in the original order
// with scores untouched.
func TestPluginFeedbackWeightIsNoopWhenDisabled(t *testing.T) {
	repo := &stubWeightRepo{weights: map[string]float64{"good": 1.5}}
	plugin := &PluginFeedbackWeight{chunkRepo: repo}
	chatManage := &types.ChatManage{
		PipelineState: types.PipelineState{
			RerankResult: []*types.SearchResult{
				{ID: "good", Score: 0.3},
				{ID: "bad", Score: 0.2},
			},
		},
	}
	ctx := context.Background()
	err := plugin.OnEvent(ctx, types.FEEDBACK_WEIGHT, chatManage, func() *PluginError { return nil })
	require.Nil(t, err)
	assert.Equal(t, []string{"good", "bad"}, searchResultIDs(chatManage.RerankResult))
	assert.Equal(t, 0.3, chatManage.RerankResult[0].Score)
	assert.Equal(t, 0.2, chatManage.RerankResult[1].Score)
}

// TestPluginFeedbackWeightIsNoopWithoutRerank ensures the plugin leaves
// SearchResult untouched when no rerank has produced candidates yet (i.e.
// a chat that bypasses retrieval). The plugin should never crash on an
// empty candidate list.
func TestPluginFeedbackWeightIsNoopWithoutRerank(t *testing.T) {
	repo := &stubWeightRepo{weights: map[string]float64{}}
	plugin := &PluginFeedbackWeight{chunkRepo: repo}
	chatManage := &types.ChatManage{}
	err := plugin.OnEvent(context.Background(), types.FEEDBACK_WEIGHT, chatManage, func() *PluginError { return nil })
	require.Nil(t, err)
	assert.Empty(t, chatManage.RerankResult)
}

// TestPluginFeedbackWeightReturnsNextWhenNoRetrieval ensures the no-retrieval
// branch always yields control to the next plugin.
func TestPluginFeedbackWeightReturnsNextWhenNoRetrieval(t *testing.T) {
	repo := &stubWeightRepo{}
	plugin := &PluginFeedbackWeight{chunkRepo: repo}
	// IntentGreeting is the "no retrieval needed" intent.
	chatManage := &types.ChatManage{}
	chatManage.Intent = types.IntentGreeting
	called := false
	err := plugin.OnEvent(context.Background(), types.FEEDBACK_WEIGHT, chatManage, func() *PluginError {
		called = true
		return nil
	})
	require.Nil(t, err)
	assert.True(t, called, "next() should be invoked when no retrieval is needed")
}

// TestActivationEventsIncludesFeedbackWeight makes sure the plugin is wired
// into the pipeline at the right event so the order is preserved.
func TestActivationEventsIncludesFeedbackWeight(t *testing.T) {
	plugin := &PluginFeedbackWeight{}
	events := plugin.ActivationEvents()
	require.Contains(t, events, types.FEEDBACK_WEIGHT)
}