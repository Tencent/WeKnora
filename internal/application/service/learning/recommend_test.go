package learning

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type fakeMemory struct {
	interfaces.MemoryService
	enabled     bool
	reads       int
	knowledgeID string
}

func (m *fakeMemory) MemoryAvailable(context.Context) bool { return m.enabled }
func (m *fakeMemory) DocumentAffinity(context.Context, []string) map[string]int {
	m.reads++
	return map[string]int{m.knowledgeID: types.MemoryDocAffinityMinHits}
}
func (m *fakeMemory) RetrievalContextFor(context.Context) interfaces.RetrievalContext {
	m.reads++
	return interfaces.RetrievalContext{Background: "PERSONAL_MEMORY_SECRET", Interests: []string{"transactions"}}
}

func TestLearningMemoryIsOptionalAndNeverMastery(t *testing.T) {
	f := newFixture(t)
	memory := &fakeMemory{knowledgeID: f.chunk.KnowledgeID}
	f.svc = NewService(f.repo, fakeModels{client: f.model}, f.tasks, memory)
	_, err := f.svc.SetEnabled(f.ctx, true)
	require.NoError(t, err)
	for _, enabled := range []bool{false, true} {
		memory.enabled = enabled
		n, err := f.svc.Node(f.ctx, f.page.ID)
		require.NoError(t, err)
		require.Equal(t, enabled, n.Familiar)
		require.Zero(t, n.Mastery.Attempts)
		require.Equal(t, .2, n.Mastery.PMastery)
		recs, err := f.svc.Recommend(f.ctx, f.kb.ID, 5)
		require.NoError(t, err)
		require.Len(t, recs, 1)
		if enabled {
			require.Equal(t, 1., recs[0].Components.InterestMatch)
		} else {
			require.Zero(t, memory.reads)
		}
	}
	reads := memory.reads
	f.ready(t)
	require.Equal(t, reads, memory.reads, "quiz generation must not retrieve memory")
}

func TestLearningRecommendationsWeightsDiversityExplorationDeterminism(t *testing.T) {
	now := time.Now()
	nodes := []*types.LearningNode{}
	views := []*types.LearningNodeView{}
	for i, kind := range []string{"concept", "concept", "entity", "comparison", "synthesis"} {
		p := &types.WikiPage{ID: fmt.Sprint(i), Title: "Transactions", PageType: kind, Summary: "summary", ChunkRefs: types.StringArray{"a", "b", "c"}}
		n := &types.LearningNode{Page: p, Related: i < 2}
		v := types.LearningNodePublic(n, now)
		if i < 4 {
			v.Mastery.State, v.Mastery.PMastery = "review_due", .5
		}
		nodes, views = append(nodes, n), append(views, v)
	}
	result := rankRecommendations(nodes, views, []string{"transactions"}, 4)
	require.Len(t, result, 4)
	require.Equal(t, "4", result[3].PageID)
	require.InDelta(t, 1., result[0].Score, 1e-12)
	require.Equal(t, "1", result[1].PageID, "a due concept must not be displaced by type diversity")
	for _, r := range result {
		require.InDelta(t, .45*r.Components.ReviewNeed+.25*r.Components.GraphFrontier+.20*r.Components.InterestMatch+.10*r.Components.ContentQuality, r.Score, 1e-12)
	}
	for i, j := 0, len(nodes)-1; i < j; i, j = i+1, j-1 {
		nodes[i], nodes[j] = nodes[j], nodes[i]
		views[i], views[j] = views[j], views[i]
	}
	require.Equal(t, result, rankRecommendations(nodes, views, []string{"transactions"}, 4))
}

func TestLearningRecommendationsNeverDisplaceDuePracticeWithMasteredKinds(t *testing.T) {
	nodes := []*types.LearningNode{}
	views := []*types.LearningNodeView{}
	for i, kind := range []string{"concept", "concept", "entity", "comparison"} {
		node := &types.LearningNode{Page: &types.WikiPage{ID: fmt.Sprint(i), Title: "Topic", PageType: kind, Summary: "Summary"}}
		view := types.LearningNodePublic(node, time.Now())
		view.Mastery.State = "mastered"
		nodes, views = append(nodes, node), append(views, view)
	}
	require.Empty(t, rankRecommendations(nodes, views, nil, 5))
	views[0].Mastery.State, views[1].Mastery.State = "review_due", "review_due"
	result := rankRecommendations(nodes, views, nil, 5)
	require.Len(t, result, 2)
	for _, r := range result {
		require.Equal(t, "review_due", r.Mastery.State)
		require.NotContains(t, r.ReasonCodes, "source_backed", "summary-only topics have no source citation")
	}
}

func TestLearningOverlayBoundsOverviewAndUUIDIdentity(t *testing.T) {
	f := newFixture(t)
	f.ready(t)
	require.NoError(t, f.svc.RecordView(f.ctx, f.page.ID))
	for _, kind := range []string{"entity", "synthesis", "comparison", "summary", "index"} {
		p := *f.page
		p.ID, p.Slug, p.PageType = uuid.NewString(), kind+"/test", kind
		require.NoError(t, f.db.Create(&p).Error)
	}
	view, err := f.svc.Overview(f.ctx, f.kb.ID)
	require.NoError(t, err)
	require.Equal(t, 4, view.TotalNodes)
	require.Equal(t, 4, view.Counts.Unseen)
	_, err = f.svc.Overlay(f.ctx, f.kb.ID, make([]string, 2001))
	require.ErrorIs(t, err, types.ErrLearningInvalid)
	nodes, err := f.svc.Overlay(f.ctx, f.kb.ID, []string{f.page.Slug, "summary/test", "index/test", "absent"})
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.NoError(t, f.db.Model(f.page).Update("slug", "concept/renamed").Error)
	n, err := f.svc.Node(f.ctx, f.page.ID)
	require.NoError(t, err)
	require.True(t, n.Familiar)
	require.Equal(t, "concept/renamed", n.Slug)
}

func TestLearningLeaseCASAndOptOutFence(t *testing.T) {
	f := newFixture(t)
	q := f.prepare(t)
	payload := types.LearningGeneratePayload{QuizID: q.ID, Epoch: 2}
	first, err := f.repo.Claim(context.Background(), payload)
	require.NoError(t, err)
	require.NotNil(t, first)
	duplicate, err := f.repo.Claim(context.Background(), payload)
	require.NoError(t, err)
	require.Nil(t, duplicate)
	require.NoError(t, f.db.Model(&types.LearningQuiz{}).Where("id = ?", q.ID).Update("lease_until", time.Now().UTC().Add(-time.Minute)).Error)
	second, err := f.repo.Claim(context.Background(), payload)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.NotEqual(t, first.Quiz.LeaseToken, second.Quiz.LeaseToken)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	questions, code := generate(types.WithLLMContentRedacted(ctx), f.model, &second.Source)
	require.Empty(t, code)
	require.ErrorIs(t, f.repo.Publish(context.Background(), first, questions), types.ErrLearningStale)
	_, err = f.svc.SetEnabled(f.ctx, false)
	require.NoError(t, err)
	_, err = f.svc.SetEnabled(f.ctx, true)
	require.NoError(t, err)
	require.ErrorIs(t, f.repo.Publish(context.Background(), second, questions), types.ErrLearningStale)
}
