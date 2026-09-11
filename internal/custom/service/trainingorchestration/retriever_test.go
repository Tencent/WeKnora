package trainingorchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

type fakeEvidenceQueryReader struct {
	results [][]transcript.Chunk
	err     error
	calls   []evidenceQueryCall
}

type evidenceQueryCall struct {
	videoID    string
	generation string
	query      string
	allowedIDs []string
	limit      int
}

func (f *fakeEvidenceQueryReader) SearchEvidence(_ context.Context, videoID, generation, query string, allowedIDs []string, limit int) ([]transcript.Chunk, error) {
	f.calls = append(f.calls, evidenceQueryCall{
		videoID: videoID, generation: generation, query: query,
		allowedIDs: append([]string(nil), allowedIDs...), limit: limit,
	})
	if f.err != nil {
		return nil, f.err
	}
	index := len(f.calls) - 1
	if index >= len(f.results) {
		return nil, nil
	}
	return append([]transcript.Chunk(nil), f.results[index]...), nil
}

func TestQueryMaterialRetrieverBatchesPlannedCriteriaAndNarrowsEvidence(t *testing.T) {
	cluster, material := retrievalFixture()
	cluster.InclusionCriteria = []string{"要点一", "要点二", "要点三", "要点四", "要点五", "要点六"}
	reader := &fakeEvidenceQueryReader{results: [][]transcript.Chunk{
		{{EvidenceSentenceID: "ev-2"}},
		{{EvidenceSentenceID: "ev-3"}},
	}}
	retriever := &QueryMaterialRetriever{Evidence: reader, MaxCriteriaPerQuery: 5, MaxEvidencePerVideo: 5}

	got, err := retriever.Retrieve(context.Background(), cluster, material)
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if len(reader.calls) != 2 {
		t.Fatalf("expected two bounded retrieval calls, got %d", len(reader.calls))
	}
	if !strings.Contains(reader.calls[0].query, "要点一") || !strings.Contains(reader.calls[0].query, "要点五") || strings.Contains(reader.calls[0].query, "要点六") {
		t.Fatalf("first query did not contain exactly the first criteria batch: %q", reader.calls[0].query)
	}
	if !strings.Contains(reader.calls[1].query, "要点六") {
		t.Fatalf("second query did not contain the remaining criterion: %q", reader.calls[1].query)
	}
	if len(got.Evidence) != 2 || got.Evidence[0].EvidenceID != "ev-2" || got.Evidence[1].EvidenceID != "ev-3" {
		t.Fatalf("retriever did not narrow material to matched evidence: %#v", got.Evidence)
	}
	if err := got.ValidateRetrievedAgainst(cluster); err != nil {
		t.Fatalf("retrieved material is invalid: %v", err)
	}
}

func TestQueryMaterialRetrieverFallsBackToOnePlannedEvidencePerVideo(t *testing.T) {
	cluster, material := retrievalFixture()
	retriever := &QueryMaterialRetriever{Evidence: &fakeEvidenceQueryReader{}, MaxEvidencePerVideo: 5}

	got, err := retriever.Retrieve(context.Background(), cluster, material)
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !got.RetrievalDegraded || got.RetrievalDegradationReason != "empty_result" {
		t.Fatalf("expected empty-result degradation, got degraded=%v reason=%q", got.RetrievalDegraded, got.RetrievalDegradationReason)
	}
	if len(got.Evidence) != len(material.Evidence) {
		t.Fatalf("expected full planned-evidence fallback, got %#v", got.Evidence)
	}
}

func TestQueryMaterialRetrieverRejectsEvidenceOutsidePlan(t *testing.T) {
	cluster, material := retrievalFixture()
	reader := &fakeEvidenceQueryReader{results: [][]transcript.Chunk{{{EvidenceSentenceID: "invented"}}}}
	_, err := (&QueryMaterialRetriever{Evidence: reader}).Retrieve(context.Background(), cluster, material)
	if err == nil || !strings.Contains(err.Error(), "outside the planned evidence whitelist") {
		t.Fatalf("expected retrieval whitelist rejection, got %v", err)
	}
}

func TestQueryMaterialRetrieverRejectsAdapterScopeViolation(t *testing.T) {
	cluster, material := retrievalFixture()
	reader := &fakeEvidenceQueryReader{err: errors.New(`search result knowledge "other" is outside video "video-1" generation "gen-1"`)}
	_, err := (&QueryMaterialRetriever{Evidence: reader}).Retrieve(context.Background(), cluster, material)
	var generationErr *GenerationError
	if !errors.As(err, &generationErr) || generationErr.Code != "invalid_reference" {
		t.Fatalf("expected invalid_reference for adapter scope violation, got %v", err)
	}
}

func TestQueryMaterialRetrieverClassifiesSearchFailure(t *testing.T) {
	cluster, material := retrievalFixture()
	readerErr := errors.New("search unavailable")
	got, err := (&QueryMaterialRetriever{Evidence: &fakeEvidenceQueryReader{err: readerErr}}).Retrieve(context.Background(), cluster, material)
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !got.RetrievalDegraded || got.RetrievalDegradationReason != "search_unavailable" {
		t.Fatalf("expected unavailable search degradation, got degraded=%v reason=%q", got.RetrievalDegraded, got.RetrievalDegradationReason)
	}
	if len(got.Evidence) != len(material.Evidence) {
		t.Fatalf("expected search failure to use full planned evidence, got %#v", got.Evidence)
	}
}

func TestQueryMaterialRetrieverDoesNotDegradeLocalEvidenceContractFailure(t *testing.T) {
	cluster, material := retrievalFixture()
	material.Evidence = material.Evidence[:1]
	_, err := (&QueryMaterialRetriever{Evidence: &fakeEvidenceQueryReader{err: errors.New("search unavailable")}}).Retrieve(context.Background(), cluster, material)
	if err == nil || strings.Contains(err.Error(), "search_unavailable") {
		t.Fatalf("expected complete local material validation failure, got %v", err)
	}
}

func retrievalFixture() (PlanCluster, ClusterMaterial) {
	cluster, material := validClusterGenerationInput()
	cluster.Title = "结构化复盘"
	cluster.LearningGoal = "能够完成一次结构化复盘"
	cluster.Scope = "复盘方法与判断标准"
	cluster.InclusionCriteria = []string{"识别事实", "分析原因"}
	cluster.MaterialRequests[0].EvidenceIDs = []string{"ev-1", "ev-2", "ev-3"}
	material.Evidence = []MaterialEvidence{
		{VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "ev-1", StartMs: 100, EndMs: 200, Text: "证据一"},
		{VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "ev-2", StartMs: 300, EndMs: 400, Text: "证据二"},
		{VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "ev-3", StartMs: 500, EndMs: 600, Text: "证据三"},
	}
	return cluster, material
}
