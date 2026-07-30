package feedbackweight

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

type statRepoStub struct {
	calls  int
	stats  []types.ChunkFeedbackStat
	err    error
	scopes []types.ChunkFeedbackScope
}

func (s *statRepoStub) ListChunkFeedbackStats(
	_ context.Context, scopes []types.ChunkFeedbackScope,
) ([]types.ChunkFeedbackStat, error) {
	s.calls++
	s.scopes = append([]types.ChunkFeedbackScope(nil), scopes...)
	return s.stats, s.err
}

func candidate(id string, score float64, optIn bool) Candidate {
	return Candidate{
		TenantID: 1, KnowledgeBaseID: "kb", ChunkID: id,
		Score: score, WorkspaceOptIn: optIn,
	}
}

func stat(id string, likes, dislikes int64, stored float64) types.ChunkFeedbackStat {
	return types.ChunkFeedbackStat{
		ChunkFeedbackScope: types.ChunkFeedbackScope{TenantID: 1, KnowledgeBaseID: "kb", ChunkID: id},
		LikeCount:          likes, DislikeCount: dislikes, StoredRecallWeight: stored,
	}
}

func enabledPolicy() *config.FeedbackConfig {
	cfg := config.DefaultFeedbackConfig()
	cfg.RetrievalWeightEnabled = true
	return cfg
}

func TestEffectiveEnabledMatrix(t *testing.T) {
	cfg := enabledPolicy()
	require.False(t, EffectiveEnabled(nil, true))
	cfg.Enabled = false
	require.False(t, EffectiveEnabled(cfg, true))
	cfg.Enabled = true
	cfg.RetrievalWeightEnabled = false
	require.False(t, EffectiveEnabled(cfg, true))
	cfg.RetrievalWeightEnabled = true
	require.False(t, EffectiveEnabled(cfg, false))
	require.True(t, EffectiveEnabled(cfg, true))
}

func TestEffectiveWeightTiersAndMinimumSamples(t *testing.T) {
	cfg := enabledPolicy()
	tests := []struct {
		name            string
		likes, dislikes int64
		want            float64
	}{
		{"no feedback", 0, 0, 1},
		{"below minimum", 3, 1, 1},
		{"high", 4, 1, 1.2},
		{"normal", 3, 2, 1},
		{"low", 2, 3, 0.8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := EffectiveWeight(cfg, tt.likes, tt.dislikes)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestApplyDisabledDoesNotQueryOrMutate(t *testing.T) {
	repo := &statRepoStub{err: errors.New("must not be called")}
	cfg := enabledPolicy()
	cfg.RetrievalWeightEnabled = false
	input := []Candidate{candidate("a", 0.7, true), candidate("b", 0.9, true)}
	out := Apply(context.Background(), cfg, repo, input, 1)
	require.Equal(t, ReasonDisabled, out.Reason)
	require.Zero(t, repo.calls)
	require.Equal(t, 0.7, out.Candidates[0].Score)
	require.Equal(t, "a", out.Candidates[0].ChunkID)
	require.Equal(t, 0.9, out.Candidates[1].Score)
}

func TestApplyUsesEffectiveWeightAndStableOrdering(t *testing.T) {
	repo := &statRepoStub{stats: []types.ChunkFeedbackStat{
		stat("a", 5, 0, 0.8),
		stat("b", 0, 5, 1.2),
		stat("c", 3, 2, 1),
	}}
	input := []Candidate{
		candidate("a", 0.8, true),
		candidate("b", 1.0, true),
		candidate("c", 0.96, true),
	}
	for i := range input {
		input[i].OriginalIndex = i
	}
	out := Apply(context.Background(), enabledPolicy(), repo, input, 2)
	require.NoError(t, out.Err)
	require.True(t, out.Applied)
	require.True(t, out.ChangedOrder)
	require.True(t, out.TopKChanged)
	require.Equal(t, []string{"a", "c", "b"}, []string{
		out.Candidates[0].ChunkID, out.Candidates[1].ChunkID, out.Candidates[2].ChunkID,
	})
	require.Equal(t, 0.8, out.Candidates[0].StoredRecallWeight)
	require.Equal(t, 1.2, out.Candidates[0].EffectiveRecallWeight)
	require.Equal(t, 0.8, out.Candidates[0].Score, "pre-feedback score must remain intact")
}

func TestApplyEqualEffectiveScoresPreserveOriginalOrder(t *testing.T) {
	repo := &statRepoStub{stats: []types.ChunkFeedbackStat{
		stat("a", 3, 2, 1), stat("b", 3, 2, 1),
	}}
	input := []Candidate{candidate("a", 0.9, true), candidate("b", 0.9, true)}
	input[0].OriginalIndex, input[1].OriginalIndex = 0, 1
	out := Apply(context.Background(), enabledPolicy(), repo, input, 2)
	require.Equal(t, "a", out.Candidates[0].ChunkID)
	require.Equal(t, "b", out.Candidates[1].ChunkID)
}

func TestApplyRuntimeFailuresFullyFailOpen(t *testing.T) {
	base := []Candidate{candidate("a", 0.7, true), candidate("b", 0.9, true)}
	base[0].OriginalIndex, base[1].OriginalIndex = 0, 1
	tests := []struct {
		name string
		repo *statRepoStub
		edit func([]Candidate)
	}{
		{"repository error", &statRepoStub{err: errors.New("timeout")}, nil},
		{"duplicate stat", &statRepoStub{stats: []types.ChunkFeedbackStat{stat("a", 5, 0, 1), stat("a", 5, 0, 1)}}, nil},
		{"negative count", &statRepoStub{stats: []types.ChunkFeedbackStat{stat("a", -1, 0, 1)}}, nil},
		{"zero stored weight", &statRepoStub{stats: []types.ChunkFeedbackStat{stat("a", 5, 0, 0)}}, nil},
		{"negative stored weight", &statRepoStub{stats: []types.ChunkFeedbackStat{stat("a", 5, 0, -1)}}, nil},
		{"nan stored weight", &statRepoStub{stats: []types.ChunkFeedbackStat{stat("a", 5, 0, math.NaN())}}, nil},
		{"infinite stored weight", &statRepoStub{stats: []types.ChunkFeedbackStat{stat("a", 5, 0, math.Inf(1))}}, nil},
		{"stat outside requested scope", &statRepoStub{stats: []types.ChunkFeedbackStat{stat("other", 5, 0, 1)}}, nil},
		{"missing scope", &statRepoStub{}, func(in []Candidate) { in[0].TenantID = 0 }},
		{"nan score", &statRepoStub{}, func(in []Candidate) { in[0].Score = math.NaN() }},
		{"positive infinity score", &statRepoStub{}, func(in []Candidate) { in[0].Score = math.Inf(1) }},
		{"negative infinity score", &statRepoStub{}, func(in []Candidate) { in[0].Score = math.Inf(-1) }},
		{
			"effective score overflow",
			&statRepoStub{stats: []types.ChunkFeedbackStat{stat("a", 5, 0, 1)}},
			func(in []Candidate) { in[0].Score = math.MaxFloat64 },
		},
		{"mixed applied state", &statRepoStub{}, func(in []Candidate) { in[0].AlreadyApplied = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := append([]Candidate(nil), base...)
			if tt.edit != nil {
				tt.edit(input)
			}
			out := Apply(context.Background(), enabledPolicy(), tt.repo, input, 1)
			require.False(t, out.Applied)
			require.Equal(t, input[0].ChunkID, out.Candidates[0].ChunkID)
			require.Equal(t, input[1].ChunkID, out.Candidates[1].ChunkID)
			if math.IsNaN(input[0].Score) {
				require.True(t, math.IsNaN(out.Candidates[0].Score))
			} else {
				require.Equal(t, input[0].Score, out.Candidates[0].Score)
			}
			require.Equal(t, input[1].Score, out.Candidates[1].Score)
		})
	}
}

func TestApplyMissingStatsAreNeutralAndMultiKBScopeIsComplete(t *testing.T) {
	repo := &statRepoStub{stats: []types.ChunkFeedbackStat{stat("a", 5, 0, 1.2)}}
	input := []Candidate{
		candidate("a", 0.7, true),
		candidate("b", 0.9, true),
		candidate("a", 0.8, true),
	}
	input[2].TenantID = 2
	input[2].KnowledgeBaseID = "kb-2"
	for i := range input {
		input[i].OriginalIndex = i
	}
	out := Apply(context.Background(), enabledPolicy(), repo, input, 3)
	require.NoError(t, out.Err)
	require.True(t, out.Applied)
	require.Len(t, repo.scopes, 3)
	require.Equal(t, uint64(1), repo.scopes[0].TenantID)
	require.Equal(t, "kb", repo.scopes[0].KnowledgeBaseID)
	require.Equal(t, "a", repo.scopes[0].ChunkID)
	require.Equal(t, uint64(2), repo.scopes[2].TenantID)
	require.Equal(t, "kb-2", repo.scopes[2].KnowledgeBaseID)
	require.Equal(t, "a", repo.scopes[2].ChunkID)
	for _, weighted := range out.Candidates {
		if weighted.TenantID == 1 && weighted.ChunkID == "a" {
			require.Equal(t, 1.2, weighted.EffectiveRecallWeight)
			continue
		}
		require.Equal(t, 1.0, weighted.EffectiveRecallWeight)
		require.Equal(t, weighted.Score, weighted.EffectiveScore)
	}
}

func TestApplyCanceledRepositoryFailurePreservesAllCandidateData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := &statRepoStub{err: context.Canceled}
	input := []Candidate{candidate("a", 0.7, true), candidate("b", 0.9, true)}
	input[0].OriginalIndex, input[1].OriginalIndex = 0, 1
	out := Apply(ctx, enabledPolicy(), repo, input, 1)
	require.ErrorIs(t, out.Err, context.Canceled)
	require.False(t, out.Applied)
	require.Equal(t, "a", out.Candidates[0].ChunkID)
	require.Equal(t, 0.7, out.Candidates[0].Score)
	require.Equal(t, "b", out.Candidates[1].ChunkID)
	require.Equal(t, 0.9, out.Candidates[1].Score)
}

func TestApplyAllAlreadyAppliedIsNoOpWithoutQuery(t *testing.T) {
	repo := &statRepoStub{}
	input := []Candidate{candidate("a", 0.7, true), candidate("b", 0.9, true)}
	for i := range input {
		input[i].OriginalIndex = i
		input[i].AlreadyApplied = true
	}
	out := Apply(context.Background(), enabledPolicy(), repo, input, 1)
	require.Equal(t, ReasonAlreadyApplied, out.Reason)
	require.Zero(t, repo.calls)
}
