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
		relationPage("case-1", "page-2", "case/one", "case"),
	}
	inputs := []RelationInput{
		relationInput("r-1", "concept-1", "case-1", "example_of"),
		relationInput("r-legacy", "concept-1", "case-1", "supports"),
		relationInput("r-missing", "concept-1", "missing", "example_of"),
	}
	projection := AuditRelations(inputs, pages, "video-1", "gen-1", 10_000)
	require.Len(t, projection.Semantic, 1)
	require.Equal(t, "accepted", projection.Audits[0].Status)
	require.Equal(t, "rejected_legacy_relation_type", projection.Audits[1].Status)
	require.Equal(t, "deferred", projection.Audits[2].Status)
	require.Equal(t, []string{"case-1"}, projection.Orphans)
}

func TestAuditRelationsRejectsTypeConfidenceEvidenceTimeAndGeneration(t *testing.T) {
	pages := []ObjectPageResult{
		relationPage("concept-1", "page-1", "concept/one", "concept"),
		relationPage("case-1", "page-2", "case/one", "case"),
	}
	cases := []struct {
		name   string
		mutate func(*RelationInput)
		reason string
	}{
		{"matrix", func(v *RelationInput) { v.RelationType = "explains" }, "five-type matrix"},
		{"confidence", func(v *RelationInput) { v.Confidence = .69 }, "below 0.70"},
		{"evidence", func(v *RelationInput) { v.EvidenceIDs = nil }, "evidence_ids"},
		{"time", func(v *RelationInput) { v.TimeRange = "00:00:03.000-00:00:01.000" }, "time_range"},
		{"generation", func(v *RelationInput) { v.TranscriptGeneration = "old" }, "active video generation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := relationInput("r", "concept-1", "case-1", "example_of")
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
	pages := []ObjectPageResult{relationPage("concept-1", "page-1", "concept/one", "concept"), relationPage("case-1", "page-2", "case/one", "case")}
	projection := AuditRelations([]RelationInput{relationInput("r-1", "concept-1", "case-1", "example_of")}, pages, "video-1", "gen-1", 10_000)
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
