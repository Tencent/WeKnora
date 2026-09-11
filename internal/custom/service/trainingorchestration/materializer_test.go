package trainingorchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

type fakeEvidenceReader struct {
	chunks map[string][]transcript.Chunk
	calls  []struct {
		videoID    string
		generation string
		ids        []string
	}
	err error
}

func (f *fakeEvidenceReader) ReadEvidence(_ context.Context, videoID, generation string, ids []string) ([]transcript.Chunk, error) {
	f.calls = append(f.calls, struct {
		videoID    string
		generation string
		ids        []string
	}{videoID: videoID, generation: generation, ids: append([]string(nil), ids...)})
	if f.err != nil {
		return nil, f.err
	}
	return append([]transcript.Chunk(nil), f.chunks[videoID+"\x00"+generation]...), nil
}

func TestMaterializerReadsOnlyPlannedSummaryBlocksAndEvidence(t *testing.T) {
	page := validSummaryPage(t)
	page.ID = "page-1"
	page.Version = 3
	page.Content = strings.ReplaceAll(page.Content, testGeneration, "gen-1")
	wiki := &fakeWiki{pages: map[string]weknora.WikiPage{page.Slug: page}}
	evidence := &fakeEvidenceReader{chunks: map[string][]transcript.Chunk{
		"video-1\x00gen-1": {{ID: "chunk-1", EvidenceSentenceID: "evs:gen-1:one", Content: "## 原文\n\n证据原文", StartMs: 100, EndMs: 200}},
	}}
	materializer := &Materializer{Wiki: wiki, Evidence: evidence, KnowledgeBaseID: testKBID}
	video := validCatalogVideo("video-1")
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{video}}
	plan := validAcceptedPlan(snapshot)

	materials, err := materializer.Materialize(context.Background(), snapshot, plan)
	if err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}
	if len(materials) != 1 || len(materials[0].SummaryBlocks) != 1 || len(materials[0].Evidence) != 1 {
		t.Fatalf("unexpected material: %#v", materials)
	}
	if materials[0].SummaryBlocks[0].BlockID != "block-1" || materials[0].Evidence[0].Text != "证据原文" {
		t.Fatalf("material did not preserve selected content: %#v", materials[0])
	}
	if len(evidence.calls) != 1 || len(evidence.calls[0].ids) != 1 || evidence.calls[0].ids[0] != "evs:gen-1:one" {
		t.Fatalf("evidence reader received an unexpected whitelist: %#v", evidence.calls)
	}
	if wiki.lastSlug != "typed-summary/video-1" {
		t.Fatalf("materializer read an unexpected Wiki slug: %q", wiki.lastSlug)
	}
}

func TestMaterializerRejectsChangedSummaryIdentityBeforeReadingEvidence(t *testing.T) {
	page := validSummaryPage(t)
	page.ID = "different-page"
	page.Version = 3
	wiki := &fakeWiki{pages: map[string]weknora.WikiPage{page.Slug: page}}
	evidence := &fakeEvidenceReader{chunks: map[string][]transcript.Chunk{}}
	materializer := &Materializer{Wiki: wiki, Evidence: evidence, KnowledgeBaseID: testKBID}
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{validCatalogVideo("video-1")}}

	_, err := materializer.Materialize(context.Background(), snapshot, validAcceptedPlan(snapshot))
	if err == nil || !strings.Contains(err.Error(), "identity or version changed") {
		t.Fatalf("expected changed summary identity error, got %v", err)
	}
	if len(evidence.calls) != 0 {
		t.Fatalf("evidence was read after summary identity failure: %#v", evidence.calls)
	}
}

func TestMaterializerSupportsEvidenceOnlyCompatibilityProfile(t *testing.T) {
	video := CatalogVideo{
		VideoID: "video-1", VideoType: "training", TranscriptGeneration: "gen-1",
		CompatibilityProfile: &summary.OrchestrationProfile{
			SchemaVersion: 1, PrimaryTopic: "兼容主题",
			TopicUnits: []summary.OrchestrationTopicUnit{{
				Title: "兼容单元", Abstract: "兼容摘要", ContentForms: []string{"concept_cognition"},
				LearningOutcomes: []string{"理解主题"}, EvidenceChunkIDs: []string{"chunk-1"},
				EvidenceRefs: []summary.EvidenceRef{{
					ChunkID:            "chunk-1",
					EvidenceSentenceID: "evs:gen-1:one",
					StartMs:            100,
					EndMs:              200,
				}},
			}},
		},
	}
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{video}}
	plan := PlanDraft{
		ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint,
		TopicClusters: []PlanCluster{{
			ClusterKey: "cluster-compat", Title: "兼容主题", LearningGoal: "理解主题", PrimaryTemplate: "concept_cognition",
			ReviewStatus: PlanAccepted, SourceVideoIDs: []string{"video-1"},
			MaterialRequests: []MaterialRequest{{VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceIDs: []string{"evs:gen-1:one"}}},
		}},
	}
	evidence := &fakeEvidenceReader{chunks: map[string][]transcript.Chunk{
		"video-1\x00gen-1": {{ID: "chunk-1", EvidenceSentenceID: "evs:gen-1:one", Content: "## 原文\n\n兼容证据", StartMs: 100, EndMs: 200}},
	}}
	materializer := &Materializer{Wiki: &fakeWiki{}, Evidence: evidence, KnowledgeBaseID: testKBID}

	materials, err := materializer.Materialize(context.Background(), snapshot, plan)
	if err != nil {
		t.Fatalf("Materialize rejected evidence-only compatibility material: %v", err)
	}
	if len(materials) != 1 || len(materials[0].SummaryBlocks) != 0 || len(materials[0].Evidence) != 1 {
		t.Fatalf("unexpected compatibility material: %#v", materials)
	}
}

func TestMaterializerPropagatesEvidenceReadFailure(t *testing.T) {
	video := validCatalogVideo("video-1")
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{video}}
	evidenceErr := errors.New("evidence store unavailable")
	materializer := &Materializer{
		Wiki: &fakeWiki{pages: map[string]weknora.WikiPage{"typed-summary/video-1": func() weknora.WikiPage {
			page := validSummaryPage(t)
			page.ID = "page-1"
			page.Version = 3
			page.Content = strings.ReplaceAll(page.Content, testGeneration, "gen-1")
			return page
		}()}},
		Evidence:        &fakeEvidenceReader{err: evidenceErr},
		KnowledgeBaseID: testKBID,
	}

	_, err := materializer.Materialize(context.Background(), snapshot, validAcceptedPlan(snapshot))
	if err == nil || !errors.Is(err, evidenceErr) {
		t.Fatalf("expected evidence read failure, got %v", err)
	}
}
