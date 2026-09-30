package mastery

import (
	"math"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// projectEvidence is the single place where ledger rows become per-page evidence.
// The graph overlay and the page-view echo both go through it, so these tests pin
// down the projection rules the README states: a multi-source page takes the
// strongest citation, likes accumulate across sources, and views land by slug.

func TestProjectEvidenceTakesStrongestCitationSource(t *testing.T) {
	slugSources := map[string][]string{"page": {"doc1", "doc2"}}
	citations := []*types.MemoryCitation{
		{KnowledgeID: "doc1", CiteCount: 2},
		{KnowledgeID: "doc2", CiteCount: 3},
	}
	got := projectEvidence(slugSources, citations, nil, nil, nil, nil)
	if e := got["page"]; e.Citations != 3 {
		t.Fatalf("multi-source page must take the strongest source (3), got %d", e.Citations)
	}
}

func TestProjectEvidenceKeepsLatestCitationTime(t *testing.T) {
	older := time.Now().Add(-48 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	slugSources := map[string][]string{"page": {"doc1", "doc2"}}
	citations := []*types.MemoryCitation{
		{KnowledgeID: "doc1", CiteCount: 1, LastCitedAt: newer},
		{KnowledgeID: "doc2", CiteCount: 1, LastCitedAt: older},
	}
	got := projectEvidence(slugSources, citations, nil, nil, nil, nil)
	if e := got["page"]; !e.LastCitedAt.Equal(newer) {
		t.Fatalf("citation recency must be the latest of the sources, got %v", e.LastCitedAt)
	}
}

func TestProjectEvidenceAccumulatesLikesAcrossSources(t *testing.T) {
	slugSources := map[string][]string{"page": {"doc1", "doc2"}}
	likes := []*types.MemoryAnswerLike{
		{
			MessageID: "msg1",
			Allocations: types.LikeAllocations{
				{KnowledgeID: "doc1", CreditedWeight: 0.6},
				{KnowledgeID: "doc2", CreditedWeight: 0.4},
			},
		},
	}
	got := projectEvidence(slugSources, nil, likes, nil, nil, nil)
	if e := got["page"]; math.Abs(e.Likes-1.0) > 1e-9 {
		t.Fatalf("likes from several sources must accumulate to 1.0, got %v", e.Likes)
	}
}

func TestProjectEvidenceIgnoresUnrelatedDocuments(t *testing.T) {
	// A citation on a document this page is not built from must not leak in.
	slugSources := map[string][]string{"page": {"doc1"}}
	citations := []*types.MemoryCitation{{KnowledgeID: "other", CiteCount: 9}}
	got := projectEvidence(slugSources, citations, nil, nil, nil, nil)
	if e := got["page"]; e.Citations != 0 {
		t.Fatalf("citation on an unrelated document must not be projected, got %d", e.Citations)
	}
}

func TestProjectEvidenceViewsMatchBySlug(t *testing.T) {
	slugSources := map[string][]string{"page": {"doc1"}}
	views := []*types.MemoryPageView{{Slug: "page", ViewCount: 4, TotalDuration: 120}}
	got := projectEvidence(slugSources, nil, nil, views, nil, nil)
	e := got["page"]
	if e.Views != 4 || e.Duration != 120 {
		t.Fatalf("view evidence must land by slug, got views=%d duration=%d", e.Views, e.Duration)
	}
}

func TestProjectEvidenceCarriesDailySlices(t *testing.T) {
	slugSources := map[string][]string{"page": {"doc1"}}
	viewSlices := map[string][]ViewSlice{"page": {{Views: 1, Duration: 30}}}
	spreadSlices := map[string][]SpreadSlice{"page": {{Seconds: 60}}}
	got := projectEvidence(slugSources, nil, nil, nil, viewSlices, spreadSlices)
	e := got["page"]
	if len(e.ViewSlices) != 1 || len(e.SpreadSlices) != 1 {
		t.Fatalf("daily slices must be carried through, got views=%d spread=%d",
			len(e.ViewSlices), len(e.SpreadSlices))
	}
}

func TestProjectEvidenceEverySlugAppearsEvenWithoutEvidence(t *testing.T) {
	// Every requested slug must be present, so callers never see a "missing" page
	// turn into "no such node" further down the line.
	slugSources := map[string][]string{"page": {"doc1"}, "empty": {"doc2"}}
	got := projectEvidence(slugSources, nil, nil, nil, nil, nil)
	if len(got) != 2 {
		t.Fatalf("expected one entry per requested slug, got %d", len(got))
	}
	if e, ok := got["empty"]; !ok || e.Citations != 0 || e.Views != 0 || e.Likes != 0 {
		t.Fatalf("slug without evidence must map to a zero Evidence, got %+v (present=%v)", e, ok)
	}
}
