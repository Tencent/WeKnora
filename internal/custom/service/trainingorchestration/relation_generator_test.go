package trainingorchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type relationGeneratorLLM struct {
	output string
	calls  int
	prompt string
}

func (f *relationGeneratorLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	f.prompt = prompt
	return f.output, nil
}

func (*relationGeneratorLLM) Model() string         { return "relation-model" }
func (*relationGeneratorLLM) PromptVersion() string { return "relation-v1" }

func TestRelationGeneratorAssignsIDsAndCanonicalizesUndirectedRelations(t *testing.T) {
	clusters := validRelationClusters()
	llm := &relationGeneratorLLM{output: `{"relations":[{"source_cluster_id":"cluster-2","target_cluster_id":"cluster-1","relation_type":"complementary","summary":"两个主题互补","confidence":0.88,"source_evidence_refs":[{"video_id":"video-2","evidence_id":"ev-2"}],"target_evidence_refs":[{"video_id":"video-1","evidence_id":"ev-1"}]}]}`}

	relations, err := (&RelationGenerator{LLM: llm}).Generate(context.Background(), clusters)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if llm.calls != 1 || len(relations) != 1 {
		t.Fatalf("unexpected relation result: calls=%d relations=%#v", llm.calls, relations)
	}
	relation := relations[0]
	if relation.RelationID != "cluster-relation-001" || relation.SourceClusterID != "cluster-1" || relation.TargetClusterID != "cluster-2" {
		t.Fatalf("relation identity was not normalized: %#v", relation)
	}
	if relation.ReviewStatus != "passed" || relation.SourceEvidenceRefs[0].EvidenceID != "ev-1" || relation.TargetEvidenceRefs[0].EvidenceID != "ev-2" {
		t.Fatalf("relation fields were not program-owned: %#v", relation)
	}
	if strings.Contains(llm.prompt, "learning_title") || strings.Contains(llm.prompt, "learner_question") {
		t.Fatalf("relation prompt included detailed learning material: %s", llm.prompt)
	}
}

func TestRelationGeneratorDeduplicatesCanonicalRelations(t *testing.T) {
	clusters := validRelationClusters()
	llm := &relationGeneratorLLM{output: `{"relations":[{"source_cluster_id":"cluster-1","target_cluster_id":"cluster-2","relation_type":"complementary","summary":"较弱描述","confidence":0.7,"source_evidence_refs":[{"video_id":"video-1","evidence_id":"ev-1"}],"target_evidence_refs":[{"video_id":"video-2","evidence_id":"ev-2"}]},{"source_cluster_id":"cluster-2","target_cluster_id":"cluster-1","relation_type":"complementary","summary":"较强描述","confidence":0.9,"source_evidence_refs":[{"video_id":"video-2","evidence_id":"ev-2"}],"target_evidence_refs":[{"video_id":"video-1","evidence_id":"ev-1"}]}]}`}

	relations, err := (&RelationGenerator{LLM: llm}).Generate(context.Background(), clusters)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if len(relations) != 1 || relations[0].Confidence != 0.9 || relations[0].Summary != "较强描述" {
		t.Fatalf("duplicate canonical relation was not reduced deterministically: %#v", relations)
	}
}

func TestRelationGeneratorRejectsEvidenceOutsideEndpointCluster(t *testing.T) {
	clusters := validRelationClusters()
	llm := &relationGeneratorLLM{output: `{"relations":[{"source_cluster_id":"cluster-1","target_cluster_id":"cluster-2","relation_type":"application","summary":"应用","confidence":0.8,"source_evidence_refs":[{"video_id":"video-2","evidence_id":"ev-2"}],"target_evidence_refs":[{"video_id":"video-2","evidence_id":"ev-2"}]}]}`}

	_, err := (&RelationGenerator{LLM: llm}).Generate(context.Background(), clusters)
	if err == nil || !strings.Contains(err.Error(), "outside its endpoint cluster") {
		t.Fatalf("expected endpoint evidence rejection, got %v", err)
	}
}

func TestRelationGeneratorCorrectsWhitelistReferenceOnlyOnce(t *testing.T) {
	clusters := validRelationClusters()
	llm := &relationGeneratorLLM{output: `{"relations":[{"source_cluster_id":"cluster-1","target_cluster_id":"cluster-2","relation_type":"application","summary":"应用","confidence":0.8,"source_evidence_refs":[{"video_id":"video-2","evidence_id":"ev-2"}],"target_evidence_refs":[{"video_id":"video-2","evidence_id":"ev-2"}]}]}`}
	_, err := (&RelationGenerator{LLM: llm}).Generate(context.Background(), clusters)
	if err == nil || llm.calls != 2 || !strings.Contains(err.Error(), "outside its endpoint cluster") {
		t.Fatalf("expected exactly one relation correction, calls=%d err=%v", llm.calls, err)
	}
}

func TestRelationGeneratorBatchesClustersAndKeepsBatchWhitelist(t *testing.T) {
	clusters := validRelationClusters()
	llm := &relationGeneratorLLM{output: `{"relations":[]}`}
	relations, err := (&RelationGenerator{LLM: llm, MaxClustersPerBatch: 1}).Generate(context.Background(), clusters)
	if err != nil {
		t.Fatal(err)
	}
	if len(relations) != 0 || llm.calls != 2 {
		t.Fatalf("expected two independent relation batches, calls=%d relations=%#v", llm.calls, relations)
	}
	if strings.Contains(llm.prompt, `"cluster_id":"cluster-1"`) && strings.Contains(llm.prompt, `"cluster_id":"cluster-2"`) {
		t.Fatalf("last relation batch unexpectedly contained both clusters: %s", llm.prompt)
	}
}

type contextLimitRelationLLM struct {
	calls int
}

func (f *contextLimitRelationLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	if strings.Contains(prompt, `"cluster_id":"cluster-1"`) && strings.Contains(prompt, `"cluster_id":"cluster-2"`) {
		return "", errors.New("provider maximum context length exceeded")
	}
	return `{"relations":[]}`, nil
}

func (*contextLimitRelationLLM) Model() string         { return "relation-model" }
func (*contextLimitRelationLLM) PromptVersion() string { return "relation-v1" }

func TestRelationGeneratorShrinksBatchAfterProviderContextLimit(t *testing.T) {
	llm := &contextLimitRelationLLM{}
	relations, err := (&RelationGenerator{LLM: llm, MaxClustersPerBatch: 12}).Generate(context.Background(), validRelationClusters())
	if err != nil {
		t.Fatal(err)
	}
	if len(relations) != 0 || llm.calls != 3 {
		t.Fatalf("expected one failed batch and two shrunk batches, calls=%d relations=%#v", llm.calls, relations)
	}
}

type truncatedRelationLLM struct {
	calls int
}

func (f *truncatedRelationLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	if strings.Contains(prompt, `"cluster_id":"cluster-1"`) && strings.Contains(prompt, `"cluster_id":"cluster-2"`) {
		return `{"relations":[`, nil
	}
	return `{"relations":[]}`, nil
}

func (*truncatedRelationLLM) Model() string         { return "relation-model" }
func (*truncatedRelationLLM) PromptVersion() string { return "relation-v1" }

func TestRelationGeneratorShrinksBatchAfterTruncatedJSON(t *testing.T) {
	llm := &truncatedRelationLLM{}
	relations, err := (&RelationGenerator{LLM: llm, MaxClustersPerBatch: 12}).Generate(context.Background(), validRelationClusters())
	if err != nil {
		t.Fatal(err)
	}
	if len(relations) != 0 || llm.calls != 3 {
		t.Fatalf("expected one truncated batch and two shrunk batches, calls=%d relations=%#v", llm.calls, relations)
	}
}

func TestRelationGeneratorRejectsRequiredBeforeCycle(t *testing.T) {
	clusters := validRelationClusters()
	llm := &relationGeneratorLLM{output: `{"relations":[{"source_cluster_id":"cluster-1","target_cluster_id":"cluster-2","relation_type":"required_before","summary":"一先于二","confidence":0.8,"source_evidence_refs":[{"video_id":"video-1","evidence_id":"ev-1"}],"target_evidence_refs":[{"video_id":"video-2","evidence_id":"ev-2"}]},{"source_cluster_id":"cluster-2","target_cluster_id":"cluster-1","relation_type":"required_before","summary":"二先于一","confidence":0.8,"source_evidence_refs":[{"video_id":"video-2","evidence_id":"ev-2"}],"target_evidence_refs":[{"video_id":"video-1","evidence_id":"ev-1"}]}]}`}

	_, err := (&RelationGenerator{LLM: llm}).Generate(context.Background(), clusters)
	if err == nil || !strings.Contains(err.Error(), "required_before relations contain a cycle") {
		t.Fatalf("expected cycle rejection, got %v", err)
	}
}

func TestRelationGeneratorSkipsModelWhenThereAreFewerThanTwoClusters(t *testing.T) {
	llm := &relationGeneratorLLM{}
	relations, err := (&RelationGenerator{LLM: llm}).Generate(context.Background(), validRelationClusters()[:1])
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if len(relations) != 0 || llm.calls != 0 {
		t.Fatalf("relation generator called for one cluster: relations=%#v calls=%d", relations, llm.calls)
	}
}

func validRelationClusters() []TopicCluster {
	return []TopicCluster{
		{
			ClusterID: "cluster-1", Title: "主题一", Summary: "主题一摘要", LearningGoal: "理解主题一",
			LearningContentType: "concept_cognition", Confidence: 0.9, ReviewStatus: "passed",
			EvidenceRefs: []EvidenceRef{{VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "ev-1", StartMs: 100, EndMs: 200}},
		},
		{
			ClusterID: "cluster-2", Title: "主题二", Summary: "主题二摘要", LearningGoal: "应用主题二",
			LearningContentType: "skill_method", Confidence: 0.85, ReviewStatus: "passed",
			EvidenceRefs: []EvidenceRef{{VideoID: "video-2", TranscriptGeneration: "gen-2", EvidenceID: "ev-2", StartMs: 300, EndMs: 400}},
		},
	}
}
