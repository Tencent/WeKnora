package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// suggestionLimitAgentRepo returns a single curated-starter agent so the
// suggestion path resolves without touching chunk/KB repositories.
type suggestionLimitAgentRepo struct {
	interfaces.CustomAgentRepository
	agent *types.CustomAgent
}

func (r *suggestionLimitAgentRepo) GetAgentByID(
	_ context.Context, _ string, _ uint64,
) (*types.CustomAgent, error) {
	return r.agent, nil
}

func newCuratedStarterAgent(count int, items []string) *types.CustomAgent {
	return &types.CustomAgent{
		ID:       "agent-1",
		TenantID: 1,
		Config: types.CustomAgentConfig{
			QuestionSuggestions: &types.QuestionSuggestionConfig{
				Starters: types.StarterSuggestionConfig{
					Enabled: true,
					Mode:    types.SuggestionModeCurated,
					Count:   count,
					Items:   items,
				},
			},
		},
	}
}

// TestGetSuggestedQuestions_ExplicitLimitOverridesStarterCount is the
// regression test for issue #2238: an explicit limit must be honored instead
// of being clamped down to the agent's configured Starters.Count.
func TestGetSuggestedQuestions_ExplicitLimitOverridesStarterCount(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	items := []string{"q1", "q2", "q3", "q4", "q5", "q6", "q7", "q8", "q9", "q10"}
	svc := &customAgentService{
		repo: &suggestionLimitAgentRepo{agent: newCuratedStarterAgent(6, items)},
	}

	got, err := svc.GetSuggestedQuestions(ctx, "agent-1", nil, nil, nil, 10)
	require.NoError(t, err)
	// Before the fix limit was clamped to Starters.Count (6); now the explicit
	// request of 10 is honored.
	assert.Len(t, got, 10)
}

// TestGetSuggestedQuestions_SmallerLimitStillHonored guards the other
// direction: a limit below Starters.Count must also take effect.
func TestGetSuggestedQuestions_SmallerLimitStillHonored(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	items := []string{"q1", "q2", "q3", "q4", "q5", "q6"}
	svc := &customAgentService{
		repo: &suggestionLimitAgentRepo{agent: newCuratedStarterAgent(6, items)},
	}

	got, err := svc.GetSuggestedQuestions(ctx, "agent-1", nil, nil, nil, 2)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// TestGetSuggestedQuestions_UnspecifiedLimitFallsBackToStarterCount confirms
// that omitting the limit (<= 0) still uses the agent's configured count.
func TestGetSuggestedQuestions_UnspecifiedLimitFallsBackToStarterCount(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	items := []string{"q1", "q2", "q3", "q4", "q5", "q6", "q7", "q8"}
	svc := &customAgentService{
		repo: &suggestionLimitAgentRepo{agent: newCuratedStarterAgent(4, items)},
	}

	got, err := svc.GetSuggestedQuestions(ctx, "agent-1", nil, nil, nil, 0)
	require.NoError(t, err)
	assert.Len(t, got, 4)
}

// TestGetSuggestedQuestions_LimitBoundedByMax ensures an oversized limit is
// capped so it cannot inflate the downstream candidate pool without bound.
func TestGetSuggestedQuestions_LimitBoundedByMax(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	items := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		items = append(items, "q"+string(rune('A'+i%26))+string(rune('0'+i/26)))
	}
	svc := &customAgentService{
		repo: &suggestionLimitAgentRepo{agent: newCuratedStarterAgent(6, items)},
	}

	got, err := svc.GetSuggestedQuestions(ctx, "agent-1", nil, nil, nil, 999)
	require.NoError(t, err)
	assert.Len(t, got, suggestionMaxLimit)
}
