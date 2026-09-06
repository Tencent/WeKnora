package knowledgeprojection

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/stretchr/testify/require"
)

type memoryWiki struct {
	pages            map[string]weknora.WikiPage
	writeCalls       int
	ensureCalls      int
	failSlug         string
	actualCreateSlug string
	persistThenFail  bool
	mutateWrite      func(*weknora.WikiPage)
}

func (m *memoryWiki) ListAllPages(_ context.Context, _, _ string) ([]weknora.WikiPage, error) {
	result := make([]weknora.WikiPage, 0, len(m.pages))
	for _, page := range m.pages {
		result = append(result, page)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Slug < result[j].Slug })
	return result, nil
}

func (m *memoryWiki) GetPage(_ context.Context, _, slug string) (*weknora.WikiPage, error) {
	page, ok := m.pages[slug]
	if !ok {
		return nil, nil
	}
	copy := page
	return &copy, nil
}

func (m *memoryWiki) EnsurePage(_ context.Context, _ string, input weknora.WikiPageWrite) (*weknora.WikiPage, error) {
	m.ensureCalls++
	if page, exists := m.pages[input.Slug]; exists {
		copy := page
		return &copy, nil
	}
	return m.writePage(input)
}

func (m *memoryWiki) UpsertPage(_ context.Context, _ string, input weknora.WikiPageWrite) (*weknora.WikiPage, error) {
	return m.writePage(input)
}

func (m *memoryWiki) writePage(input weknora.WikiPageWrite) (*weknora.WikiPage, error) {
	if input.Slug == m.failSlug {
		return nil, errors.New("injected write failure")
	}
	m.writeCalls++
	page, exists := m.pages[input.Slug]
	if !exists {
		page = weknora.WikiPage{ID: fmt.Sprintf("page-%d", len(m.pages)+1), Slug: input.Slug, Version: 1}
		if m.actualCreateSlug != "" {
			page.Slug = m.actualCreateSlug
		}
	} else if input.Version > 0 && input.Version != page.Version {
		return nil, errors.New("version conflict")
	} else if page.Title != input.Title || page.PageType != input.PageType || page.Status != input.Status || page.Content != input.Content || page.Summary != input.Summary {
		page.Version++
	}
	page.Title, page.PageType, page.Status = input.Title, input.PageType, input.Status
	page.Content, page.Summary = input.Content, input.Summary
	page.SourceRefs = append([]string(nil), input.SourceRefs...)
	page.ChunkRefs = append([]string(nil), input.ChunkRefs...)
	page.OutLinks = nil
	if m.mutateWrite != nil {
		m.mutateWrite(&page)
	}
	m.pages[page.Slug] = page
	if page.Slug != input.Slug {
		delete(m.pages, input.Slug)
	}
	if m.persistThenFail {
		return nil, errors.New("injected response failure")
	}
	copy := page
	return &copy, nil
}

func TestPublishFirstStageCreatesReadsBackAndIsIdempotent(t *testing.T) {
	wiki := &memoryWiki{pages: map[string]weknora.WikiPage{
		"outline/video-1": {ID: "ordinary-1", Slug: "outline/video-1", PageType: "index", Content: "# Outline"},
		"summary/video-1": {ID: "ordinary-2", Slug: "summary/video-1", PageType: "summary", Content: "# Summary"},
	}}
	publisher := testPublisher(wiki)
	input := testInput("object-concept", "网络效应")

	first, err := publisher.PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, "created", first[0].Action)
	require.NotEmpty(t, first[0].WikiPageID)
	require.Equal(t, 1, first[0].Version)
	require.Equal(t, "真实视频", first[0].SourceVideoTitle)
	require.Equal(t, 1, wiki.writeCalls)
	require.Equal(t, 1, wiki.ensureCalls)

	second, err := publisher.PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, "unchanged", second[0].Action)
	require.Equal(t, first[0].WikiPageID, second[0].WikiPageID)
	require.Equal(t, first[0].Slug, second[0].Slug)
	require.Equal(t, first[0].Version, second[0].Version)
	require.Equal(t, first[0].ContentSHA256, second[0].ContentSHA256)
	require.Equal(t, 1, wiki.writeCalls)

	page := wiki.pages[first[0].Slug]
	require.Equal(t, "published", page.Status)
	require.Equal(t, []string{"doc-1"}, page.SourceRefs)
	require.Equal(t, []string{"chunk-1"}, page.ChunkRefs)
	require.Empty(t, page.OutLinks)
	require.NotContains(t, page.Content, "[[")
	require.Contains(t, page.Content, "page_type: index")
	require.Contains(t, page.Content, "chunk_refs:")
}

func TestPublishFirstStageUpdatesSameIdentityWithoutChangingPageID(t *testing.T) {
	wiki := &memoryWiki{pages: map[string]weknora.WikiPage{}}
	publisher := testPublisher(wiki)
	input := testInput("object-concept", "网络效应")
	first, err := publisher.PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.NoError(t, err)

	input.Object.CoreContent = "用户增加后，连接价值持续上升。"
	second, err := publisher.PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.NoError(t, err)
	require.Equal(t, "updated", second[0].Action)
	require.Equal(t, first[0].WikiPageID, second[0].WikiPageID)
	require.Equal(t, first[0].Version+1, second[0].Version)
}

func TestPublishFirstStageRejectsScopeTypeAndDuplicatePagesBeforeWrite(t *testing.T) {
	input := testInput("object-concept", "网络效应")
	conflicting := validExistingPage(t, input)
	conflicting.Content = strings.Replace(conflicting.Content, "primary_type: concept", "primary_type: insight", 1)
	wiki := &memoryWiki{pages: map[string]weknora.WikiPage{conflicting.Slug: conflicting}}
	_, err := testPublisher(wiki).PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.ErrorContains(t, err, "primary_type conflicts")
	require.Zero(t, wiki.writeCalls)

	foreign := input
	foreign.Object.SourceDocumentID = "doc-foreign"
	foreign.Projection.SourceRefs = []string{"doc-foreign"}
	_, err = testPublisher(&memoryWiki{pages: map[string]weknora.WikiPage{}}).PublishFirstStage(t.Context(), []ObjectPageInput{foreign})
	require.ErrorContains(t, err, "active video generation")

	valid := validExistingPage(t, input)
	duplicate := valid
	duplicate.ID, duplicate.Slug = "duplicate", "other/duplicate"
	wiki = &memoryWiki{pages: map[string]weknora.WikiPage{valid.Slug: valid, duplicate.Slug: duplicate}}
	_, err = testPublisher(wiki).PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.ErrorContains(t, err, "duplicate Wiki pages")
	require.Zero(t, wiki.writeCalls)
}

func TestPublishFirstStageReturnsSuccessfulPrefixOnLaterWriteFailure(t *testing.T) {
	wiki := &memoryWiki{pages: map[string]weknora.WikiPage{}}
	publisher := testPublisher(wiki)
	first := testInput("a-object", "对象 A")
	second := testInput("b-object", "对象 B")
	wiki.failSlug = stablePageSlug(second.Object)

	results, err := publisher.PublishFirstStage(t.Context(), []ObjectPageInput{second, first})
	require.ErrorContains(t, err, "injected write failure")
	require.Len(t, results, 1)
	require.Equal(t, "a-object", results[0].CandidateID)
}

func TestPublishFirstStageRejectsMissingOrForeignFieldEvidence(t *testing.T) {
	input := testInput("object-concept", "网络效应")
	delete(input.FieldEvidence, "mechanism")
	_, err := testPublisher(&memoryWiki{pages: map[string]weknora.WikiPage{}}).PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.ErrorContains(t, err, "requires explicit evidence")

	input = testInput("object-concept", "网络效应")
	input.FieldEvidence["extra"] = []string{"ev-1"}
	_, err = testPublisher(&memoryWiki{pages: map[string]weknora.WikiPage{}}).PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.ErrorContains(t, err, "has no populated structure field")
}

func TestPublishFirstStageRejectsInvalidBatchInputsWithoutWriting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ObjectPageInput)
		match  string
	}{
		{name: "pending", mutate: func(input *ObjectPageInput) { input.Object.AuditStatus = "pending" }, match: "audit_status must be passed"},
		{name: "cross video", mutate: func(input *ObjectPageInput) { input.Object.SourceVideoID = "video-2" }, match: "active video generation"},
		{name: "cross generation", mutate: func(input *ObjectPageInput) { input.Object.TranscriptGeneration = "generation-2" }, match: "active video generation"},
		{name: "wrong source document", mutate: func(input *ObjectPageInput) { input.Object.SourceDocumentID = "doc-2" }, match: "active video generation"},
		{name: "wrong projection identity", mutate: func(input *ObjectPageInput) { input.Projection.CandidateID = "other" }, match: "projection identity mismatch"},
		{name: "wrong evidence", mutate: func(input *ObjectPageInput) { input.Projection.EvidenceIDs = []string{"ev-2"} }, match: "does not match passed object"},
		{name: "wrong video title", mutate: func(input *ObjectPageInput) { input.Projection.SourceVideoTitle = "旧标题" }, match: "title does not match"},
		{name: "empty chunk", mutate: func(input *ObjectPageInput) { input.Projection.ChunkRefs = []string{""} }, match: "empty values"},
		{name: "duplicate chunk", mutate: func(input *ObjectPageInput) { input.Projection.ChunkRefs = []string{"chunk-1", "chunk-1"} }, match: "duplicate values"},
		{name: "time order", mutate: func(input *ObjectPageInput) { input.Projection.TimeRange = "00:00:02.000-00:00:01.000" }, match: "end must be after start"},
		{name: "time over duration", mutate: func(input *ObjectPageInput) { input.Projection.TimeRange = "00:05:56.000-00:05:58.000" }, match: "exceeds video duration"},
		{name: "foreign field evidence", mutate: func(input *ObjectPageInput) { input.FieldEvidence["definition"] = []string{"ev-2"} }, match: "foreign evidence"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := testInput("object-concept", "网络效应")
			test.mutate(&input)
			wiki := &memoryWiki{pages: map[string]weknora.WikiPage{}}
			_, err := testPublisher(wiki).PublishFirstStage(t.Context(), []ObjectPageInput{input})
			require.ErrorContains(t, err, test.match)
			require.Zero(t, wiki.writeCalls)
		})
	}
}

func TestPublishFirstStageRejectsDuplicateInputAndOccupiedStableSlugBeforeWrite(t *testing.T) {
	input := testInput("object-concept", "网络效应")
	wiki := &memoryWiki{pages: map[string]weknora.WikiPage{}}
	_, err := testPublisher(wiki).PublishFirstStage(t.Context(), []ObjectPageInput{input, input})
	require.ErrorContains(t, err, "duplicate object page input")
	require.Zero(t, wiki.writeCalls)

	slug := stablePageSlug(input.Object)
	wiki.pages[slug] = weknora.WikiPage{ID: "unrelated", Slug: slug, PageType: "index", Content: "# unrelated"}
	_, err = testPublisher(wiki).PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.ErrorContains(t, err, "occupied by another Wiki page")
	require.Zero(t, wiki.writeCalls)
}

func TestPublishFirstStageUsesActualCreatedSlugAndRecoversExactCreateRace(t *testing.T) {
	input := testInput("object-concept", "网络效应")
	wiki := &memoryWiki{pages: map[string]weknora.WikiPage{}, actualCreateSlug: "knowledge-object/concept/actual"}
	first, err := testPublisher(wiki).PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.NoError(t, err)
	require.Equal(t, "knowledge-object/concept/actual", first[0].Slug)
	second, err := testPublisher(wiki).PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.NoError(t, err)
	require.Equal(t, "unchanged", second[0].Action)
	require.Equal(t, 1, wiki.writeCalls)

	raceWiki := &memoryWiki{pages: map[string]weknora.WikiPage{}, persistThenFail: true}
	result, err := testPublisher(raceWiki).PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.NoError(t, err)
	require.Equal(t, "unchanged", result[0].Action)
	require.Len(t, raceWiki.pages, 1)

	actualSlugRace := &memoryWiki{pages: map[string]weknora.WikiPage{}, actualCreateSlug: "knowledge-object/concept/raced", persistThenFail: true}
	result, err = testPublisher(actualSlugRace).PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.NoError(t, err)
	require.Equal(t, "unchanged", result[0].Action)
	require.Equal(t, "knowledge-object/concept/raced", result[0].Slug)
}

func TestPublishFirstStageRejectsConcurrentPageWithDifferentPayload(t *testing.T) {
	input := testInput("object-concept", "网络效应")
	wiki := &memoryWiki{pages: map[string]weknora.WikiPage{}, persistThenFail: true,
		mutateWrite: func(page *weknora.WikiPage) { page.Summary = "different" }}
	_, err := testPublisher(wiki).PublishFirstStage(t.Context(), []ObjectPageInput{input})
	require.ErrorContains(t, err, "does not match requested payload")
}

func TestPublishFirstStageRejectsInvalidReadBackMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*weknora.WikiPage)
		match  string
	}{
		{name: "zero version", mutate: func(page *weknora.WikiPage) { page.Version = 0 }, match: "ID does not match"},
		{name: "draft status", mutate: func(page *weknora.WikiPage) { page.Status = "draft" }, match: "content or source metadata"},
		{name: "incoming link", mutate: func(page *weknora.WikiPage) { page.InLinks = []string{"other"} }, match: "in_links and out_links"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wiki := &memoryWiki{pages: map[string]weknora.WikiPage{}, mutateWrite: test.mutate}
			_, err := testPublisher(wiki).PublishFirstStage(t.Context(), []ObjectPageInput{testInput("object-concept", "网络效应")})
			require.ErrorContains(t, err, test.match)
		})
	}
}

func testPublisher(wiki Wiki) ObjectPagePublisher {
	return ObjectPagePublisher{Wiki: wiki, KBID: "kb-1", ExpectedVideoID: "video-1", ExpectedSourceVideoTitle: "真实视频",
		ExpectedSourceDocumentID: "doc-1", ExpectedTranscriptGeneration: "generation-1", ExpectedVideoDurationMs: 357_000}
}

func testInput(candidateID, title string) ObjectPageInput {
	object := knowledge.ClassifiedKnowledge{CandidateID: candidateID, SourceDocumentID: "doc-1", SourceVideoID: "video-1",
		TranscriptGeneration: "generation-1", PrimaryType: knowledge.TypeConcept, Title: title,
		CoreContent: "用户增加提升产品价值。", StructureFields: map[string]string{"definition": "用户增加提升产品价值", "mechanism": "连接增加效用"},
		EvidenceIDs: []string{"ev-1"}, ClassificationConfidence: 1, AuditStatus: "passed"}
	return ObjectPageInput{Object: object, Projection: EvidenceProjection{CandidateID: candidateID, SourceVideoID: "video-1",
		SourceVideoTitle: "真实视频", TranscriptGeneration: "generation-1", EvidenceIDs: []string{"ev-1"},
		ChunkRefs: []string{"chunk-1"}, SourceRefs: []string{"doc-1"}, TimeRange: "00:00:01.000-00:00:02.000"},
		FieldEvidence: map[string][]string{"definition": {"ev-1"}, "mechanism": {"ev-1"}}}
}

func validExistingPage(t *testing.T, input ObjectPageInput) weknora.WikiPage {
	render, err := knowledge.RenderFirstStageObjectPage(knowledge.FirstStagePageInput{Object: input.Object,
		TimeRange: input.Projection.TimeRange, ChunkRefs: input.Projection.ChunkRefs, FieldEvidence: input.FieldEvidence})
	require.NoError(t, err)
	return weknora.WikiPage{ID: "page-existing", Slug: stablePageSlug(input.Object), Title: render.Title,
		PageType: render.PageType, Status: "published", Content: render.Content, Summary: input.Object.CoreContent,
		SourceRefs: input.Projection.SourceRefs, ChunkRefs: input.Projection.ChunkRefs, Version: 1}
}
