package knowledgeprojection

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/stretchr/testify/require"
)

func relationPage(candidate, id, slug string, typ knowledge.KnowledgeType) ObjectPageResult {
	return ObjectPageResult{CandidateID: candidate, WikiPageID: id, Slug: slug, PrimaryType: typ, SourceVideoID: "video-1", TranscriptGeneration: "gen-1"}
}

func relationInput(id, source, target, typ string) RelationInput {
	return RelationInput{RelationID: id, SourceCandidateID: source, TargetCandidateID: target, RelationType: typ, EvidenceIDs: []string{"ev-1"}, Confidence: .9, TimeRange: "00:00:01.000-00:00:03.000", SourceVideoID: "video-1", TranscriptGeneration: "gen-1"}
}

func TestAuditRelationsSeparatesAcceptedLegacyMatrixAndMissingTargets(t *testing.T) {
	pages := []ObjectPageResult{
		relationPage("concept-1", "page-1", "concept/one", "concept"),
		relationPage("method-1", "page-2", "methodology/one", "methodology"),
	}
	inputs := []RelationInput{
		relationInput("r-1", "concept-1", "method-1", "explains"),
		relationInput("r-legacy", "concept-1", "method-1", "derived_from"),
		relationInput("r-missing", "concept-1", "missing", "explains"),
	}
	projection := AuditRelations(inputs, pages, "video-1", "gen-1", 10_000)
	require.Len(t, projection.Semantic, 1)
	require.Equal(t, "accepted", projection.Audits[0].Status)
	require.Equal(t, "rejected_legacy_relation_type", projection.Audits[1].Status)
	require.Equal(t, "deferred", projection.Audits[2].Status)
	require.Equal(t, []string{"method-1"}, projection.Orphans)
}

func TestAuditRelationsMapsLegacyCaseDerivedFromInsightToSupports(t *testing.T) {
	pages := []ObjectPageResult{
		relationPage("case-1", "page-1", "case/one", knowledge.TypeCase),
		relationPage("insight-1", "page-2", "insight/one", knowledge.TypeInsight),
	}
	projection := AuditRelations(
		[]RelationInput{relationInput("r-legacy", "case-1", "insight-1", "derived_from")},
		pages, "video-1", "gen-1", 10_000,
	)
	require.Len(t, projection.Semantic, 1)
	require.Equal(t, "supports", projection.Semantic[0].RelationType)
	require.Equal(t, "accepted", projection.Audits[0].Status)
}

func TestAuditRelationsRejectsTypeConfidenceEvidenceTimeAndGeneration(t *testing.T) {
	pages := []ObjectPageResult{
		relationPage("concept-1", "page-1", "concept/one", "concept"),
		relationPage("method-1", "page-2", "methodology/one", "methodology"),
	}
	cases := []struct {
		name   string
		mutate func(*RelationInput)
		reason string
	}{
		{"matrix", func(v *RelationInput) { v.RelationType = "example_of" }, "five-type matrix"},
		{"confidence", func(v *RelationInput) { v.Confidence = .69 }, "below 0.70"},
		{"evidence", func(v *RelationInput) { v.EvidenceIDs = nil }, "evidence_ids"},
		{"time", func(v *RelationInput) { v.TimeRange = "00:00:03.000-00:00:01.000" }, "time_range"},
		{"generation", func(v *RelationInput) { v.TranscriptGeneration = "old" }, "active video generation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := relationInput("r", "concept-1", "method-1", "explains")
			tc.mutate(&input)
			projection := AuditRelations([]RelationInput{input}, pages, "video-1", "gen-1", 10_000)
			require.Empty(t, projection.Semantic)
			require.Contains(t, projection.Audits[0].Reason, tc.reason)
		})
	}
}

type relationMemoryWiki struct {
	pages map[string]weknora.WikiPage
	fail  bool
}

func (m *relationMemoryWiki) ListAllPages(context.Context, string, string) ([]weknora.WikiPage, error) {
	return nil, nil
}
func (m *relationMemoryWiki) GetPage(_ context.Context, _, slug string) (*weknora.WikiPage, error) {
	page, ok := m.pages[slug]
	if !ok {
		return nil, nil
	}
	copy := page
	return &copy, nil
}
func (m *relationMemoryWiki) EnsurePage(context.Context, string, weknora.WikiPageWrite) (*weknora.WikiPage, error) {
	return nil, errors.New("unused")
}
func (m *relationMemoryWiki) UpsertPage(_ context.Context, _ string, input weknora.WikiPageWrite) (*weknora.WikiPage, error) {
	if m.fail {
		return nil, errors.New("injected relation write failure")
	}
	page := m.pages[input.Slug]
	page.Title, page.PageType, page.Status, page.Content, page.Version = input.Title, input.PageType, input.Status, input.Content, page.Version+1
	m.pages[input.Slug] = page
	copy := page
	return &copy, nil
}

func TestPublishRelationsIsIdempotentAndPreservesSemanticReadingLayers(t *testing.T) {
	content := "---\npage_type: index\ntype: concept\nprimary_type: concept\nknowledge_object_id: concept-1\nrelations: []\nrelated_content: []\n---\n\n# 网络效应\n"
	wiki := &relationMemoryWiki{pages: map[string]weknora.WikiPage{"concept/one": {ID: "page-1", Slug: "concept/one", Title: "网络效应", PageType: "index", Status: "published", Content: content, Version: 1}, "case/one": {ID: "page-2", Slug: "case/one", Title: "案例", PageType: "index", Status: "published", Content: content, Version: 1}}}
	pages := []ObjectPageResult{relationPage("concept-1", "page-1", "concept/one", "concept"), relationPage("method-1", "page-2", "case/one", "methodology")}
	projection := AuditRelations([]RelationInput{relationInput("r-1", "concept-1", "method-1", "explains")}, pages, "video-1", "gen-1", 10_000)
	publisher := RelationPagePublisher{Wiki: wiki, KBID: "kb-1"}
	first, err := publisher.PublishRelations(t.Context(), pages, projection)
	require.NoError(t, err)
	require.Equal(t, "updated", first[0].Action)
	require.Contains(t, wiki.pages["concept/one"].Content, "target_wiki_page_id: page-2")
	version := wiki.pages["concept/one"].Version
	second, err := publisher.PublishRelations(t.Context(), pages, projection)
	require.NoError(t, err)
	require.Equal(t, "unchanged", second[0].Action)
	require.Equal(t, version, wiki.pages["concept/one"].Version)
}

func TestPublishRelationsPreservesEvidenceContributionsAcrossVideoGenerations(t *testing.T) {
	content := `---
page_type: index
type: concept
primary_type: concept
knowledge_object_id: concept-1
source_video_id: video-1
transcript_generation: gen-1
relations:
  - relation_id: relation-1
    relation_type: explains
    target_object_id: methodology-1
    target_wiki_page_id: page-2
    evidence_contributions:
      - video_id: video-1
        transcript_generation: gen-1
        evidence_ids: [ev-1]
        time_range: 00:00:01.000-00:00:03.000
        confidence: 0.9
        quality_status: passed
related_content: []
---

# 网络效应
`
	wiki := &relationMemoryWiki{pages: map[string]weknora.WikiPage{
		"concept/one":     {ID: "page-1", Slug: "concept/one", Title: "网络效应", PageType: "index", Status: "published", Content: content, Version: 1},
		"methodology/one": {ID: "page-2", Slug: "methodology/one", Title: "增长方法", PageType: "index", Status: "published", Content: content, Version: 1},
	}}
	pages := []ObjectPageResult{
		{CandidateID: "concept-1", WikiPageID: "page-1", Slug: "concept/one", PrimaryType: knowledge.TypeConcept, SourceVideoID: "video-2", TranscriptGeneration: "gen-2"},
		{CandidateID: "methodology-1", WikiPageID: "page-2", Slug: "methodology/one", PrimaryType: knowledge.TypeMethodology, SourceVideoID: "video-2", TranscriptGeneration: "gen-2"},
	}
	input := relationInput("relation-2", "concept-1", "methodology-1", "explains")
	input.SourceVideoID = "video-2"
	input.TranscriptGeneration = "gen-2"
	input.EvidenceIDs = []string{"ev-2"}
	projection := AuditRelations([]RelationInput{input}, pages, "video-2", "gen-2", 10_000)

	_, err := (RelationPagePublisher{Wiki: wiki, KBID: "kb-1"}).PublishRelations(t.Context(), pages, projection)
	require.NoError(t, err)
	relations, err := knowledge.ParseWikiObjectRelations(wiki.pages["concept/one"].Content)
	require.NoError(t, err)
	require.Len(t, relations, 1)
	require.Len(t, relations[0].EvidenceContributions, 2)
	require.Equal(t, "video-1", relations[0].EvidenceContributions[0].VideoID)
	require.Equal(t, []string{"ev-1"}, relations[0].EvidenceContributions[0].EvidenceIDs)
	require.Equal(t, "video-2", relations[0].EvidenceContributions[1].VideoID)
	require.Equal(t, []string{"ev-2"}, relations[0].EvidenceContributions[1].EvidenceIDs)
}

func TestPublishRelationsPreflightsTargetsBeforeWrite(t *testing.T) {
	content := "---\npage_type: index\ntype: concept\nprimary_type: concept\nknowledge_object_id: concept-1\nrelations: []\nrelated_content: []\n---\n\n# 网络效应\n"
	wiki := &relationMemoryWiki{pages: map[string]weknora.WikiPage{"concept/one": {ID: "page-1", Slug: "concept/one", Title: "网络效应", PageType: "index", Status: "published", Content: content, Version: 1}}}
	pages := []ObjectPageResult{relationPage("concept-1", "page-1", "concept/one", "concept"), relationPage("case-1", "page-2", "case/one", "case")}
	projection := RelationProjection{
		Audits:   []RelationAuditRecord{{RelationInput: RelationInput{RelationID: "r"}, SourceWikiPageID: "page-1", TargetWikiPageID: "page-2", Status: "accepted"}},
		Semantic: []SemanticRelation{{RelationID: "r", SourceObjectID: "concept-1", SourceWikiPageID: "page-1", TargetObjectID: "case-1", TargetWikiPageID: "page-2", RelationType: "example_of", EvidenceIDs: []string{"ev"}, Confidence: .9, TimeRange: "00:00:01.000-00:00:02.000"}},
	}
	_, err := (RelationPagePublisher{Wiki: wiki, KBID: "kb-1"}).PublishRelations(t.Context(), pages, projection)
	require.ErrorContains(t, err, "page-2")
	require.NotContains(t, wiki.pages["concept/one"].Content, "target_wiki_page_id")
}

func TestPublishRelationsRejectsUnauditedSemanticEdge(t *testing.T) {
	pages := []ObjectPageResult{relationPage("concept-1", "page-1", "concept/one", "concept"), relationPage("case-1", "page-2", "case/one", "case")}
	projection := RelationProjection{Semantic: []SemanticRelation{{RelationID: "r", SourceWikiPageID: "page-1", TargetWikiPageID: "page-2"}}}
	_, err := (RelationPagePublisher{Wiki: &relationMemoryWiki{pages: map[string]weknora.WikiPage{}}, KBID: "kb-1"}).PublishRelations(t.Context(), pages, projection)
	require.ErrorContains(t, err, "no matching accepted audit")
}

func TestProjectReadingAssociationsDoesNotPromoteDoubleLinks(t *testing.T) {
	pages := []weknora.WikiPage{
		{ID: "page-1", Slug: "concept/one", Title: "一", Content: "关联 [[case/two|案例二]] 和 [[missing|不存在]]。"},
		{ID: "page-2", Slug: "case/two", Title: "案例二", Content: "# 案例"},
	}
	valid, invalid := ProjectReadingAssociations(pages)
	require.Len(t, valid, 1)
	require.Equal(t, "page-2", valid[0].TargetWikiPageID)
	require.Len(t, invalid, 1)
	require.False(t, invalid[0].Valid)
}
