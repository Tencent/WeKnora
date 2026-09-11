package trainingorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeCompletionClient struct {
	output     string
	calls      int
	lastPrompt string
}

func (f *fakeCompletionClient) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	f.lastPrompt = prompt
	if !strings.Contains(prompt, testEvidenceID) || !strings.Contains(prompt, `"knowledge_object_ids":[]`) {
		return "", context.Canceled
	}
	return f.output, nil
}
func (*fakeCompletionClient) Model() string         { return "real-model" }
func (*fakeCompletionClient) PromptVersion() string { return "training-v1" }

type fakeStreamingCompletionClient struct {
	fakeCompletionClient
	streamCalls int
}

func (f *fakeStreamingCompletionClient) Stream(_ context.Context, prompt string, _ func(string) error) (string, error) {
	f.streamCalls++
	f.lastPrompt = prompt
	return f.output, nil
}

type fakeJSONCompletionClient struct {
	fakeStreamingCompletionClient
	jsonCalls int
	jsonErr   error
}

func (f *fakeJSONCompletionClient) CompleteJSON(_ context.Context, prompt string) (string, error) {
	f.jsonCalls++
	f.lastPrompt = prompt
	return f.output, f.jsonErr
}

type temporaryCompletionError struct{}

func (temporaryCompletionError) Error() string   { return "temporary provider failure" }
func (temporaryCompletionError) Temporary() bool { return true }

func TestGeneratorPublishesOnlyWhitelistedModelReferences(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	raw, _ := json.Marshal(modelOutput)
	llm := &fakeCompletionClient{output: string(raw)}
	generator := &Generator{LLM: llm, PromptVersion: "training-v1", Now: func() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) }}

	doc, err := generator.Generate(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	projection := doc.TrainingPathProjection
	if llm.calls != 1 || projection.Statistics.SelectedVideos != 1 || projection.Statistics.SelectedKnowledgeCount != 0 || projection.Statistics.LearningDurationSecs != 1 {
		t.Fatalf("unexpected generated projection: %#v", projection)
	}
	if err := ValidateProjection(doc, input); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratorUsesStreamingCompletionWhenAvailable(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	raw, _ := json.Marshal(modelOutput)
	llm := &fakeStreamingCompletionClient{fakeCompletionClient: fakeCompletionClient{output: string(raw)}}

	if _, err := (&Generator{LLM: llm}).Generate(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if llm.streamCalls != 1 || llm.calls != 0 {
		t.Fatalf("stream calls=%d complete calls=%d", llm.streamCalls, llm.calls)
	}
}

func TestGeneratorPrefersCompleteJSONWhenAvailable(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	raw, _ := json.Marshal(modelOutput)
	llm := &fakeJSONCompletionClient{fakeStreamingCompletionClient: fakeStreamingCompletionClient{fakeCompletionClient: fakeCompletionClient{output: string(raw)}}}

	if _, err := (&Generator{LLM: llm}).Generate(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if llm.jsonCalls != 1 || llm.streamCalls != 0 || llm.calls != 0 {
		t.Fatalf("json calls=%d stream calls=%d complete calls=%d", llm.jsonCalls, llm.streamCalls, llm.calls)
	}
}

func TestGeneratorStripsMiniMaxReasoningPrefix(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	raw, _ := json.Marshal(modelOutput)
	llm := &fakeCompletionClient{output: "<think>先分析输入。</think>\n```json\n" + string(raw) + "\n```"}

	if _, err := (&Generator{LLM: llm}).Generate(t.Context(), input); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratorStripsMiniMaxFinalWrapper(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	raw, _ := json.Marshal(modelOutput)
	llm := &fakeCompletionClient{output: "<think>先分析输入。</think>\n<final>\n```json\n" + string(raw) + "\n```\n</final>"}

	if _, err := (&Generator{LLM: llm}).Generate(t.Context(), input); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratorRejectsUnrecognizedMarkupWrapper(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	raw, _ := json.Marshal(modelOutput)
	llm := &fakeCompletionClient{output: "<html>" + string(raw) + "</html>"}

	_, err = (&Generator{LLM: llm}).Generate(t.Context(), input)
	var failure *GenerationError
	if !errors.As(err, &failure) || failure.Code != "model_output_invalid" || !strings.Contains(err.Error(), "invalid character '<'") {
		t.Fatalf("expected unrecognized markup rejection, got %v", err)
	}
}

func TestGeneratorClassifiesUnclosedReasoningAsTruncated(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	llm := &fakeCompletionClient{output: "<think>reasoning exceeded the output budget"}

	_, err = (&Generator{LLM: llm}).Generate(t.Context(), input)
	var failure *GenerationError
	if !errors.As(err, &failure) || failure.Code != "model_output_truncated" {
		t.Fatalf("expected truncated model output, got %v", err)
	}
}

func TestGeneratorClassifiesIncompleteJSONAsTruncated(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	llm := &fakeCompletionClient{output: `{"topic_clusters":[`}

	_, err = (&Generator{LLM: llm}).Generate(t.Context(), input)
	var failure *GenerationError
	if !errors.As(err, &failure) || failure.Code != "model_output_truncated" {
		t.Fatalf("expected incomplete JSON to be classified as truncated, got %v", err)
	}
}

func TestGeneratorClassifiesTemporaryCompletionFailure(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	llm := &fakeJSONCompletionClient{jsonErr: temporaryCompletionError{}}

	_, err = (&Generator{LLM: llm}).Generate(t.Context(), input)
	var failure *GenerationError
	if !errors.As(err, &failure) || failure.Code != "model_transport_failed" {
		t.Fatalf("expected temporary model transport failure, got %v", err)
	}
}

func TestGeneratorRejectsHallucinatedEvidence(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	modelOutput.TopicClusters[0].Path.Stages[0].Units[0].EvidenceRefs[0].EvidenceID = "invented-evidence"
	raw, _ := json.Marshal(modelOutput)
	_, err = (&Generator{LLM: &fakeCompletionClient{output: string(raw)}}).Generate(t.Context(), input)
	if err == nil || !strings.Contains(err.Error(), "outside the current input whitelist") {
		t.Fatalf("expected hallucinated evidence rejection, got %v", err)
	}
}

func TestGeneratorIgnoresKnowledgeReferenceInModelOutput(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	modelOutput.TopicClusters[0].KnowledgeObjectIDs = []string{"invented-object"}
	raw, _ := json.Marshal(modelOutput)
	doc, err := (&Generator{LLM: &fakeCompletionClient{output: string(raw)}}).Generate(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.TrainingPathProjection.TopicClusters[0].KnowledgeObjectIDs) != 0 {
		t.Fatalf("knowledge reference reached the projection: %#v", doc)
	}
}

func TestGeneratorIgnoresKnowledgeObjectsInInput(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	input.QualifiedVideos[0].KnowledgeSignals = []KnowledgeSignal{{KnowledgeObjectID: "injected-object", CoreContent: "must not reach the model"}}
	modelOutput := validModelOutput(input)
	raw, _ := json.Marshal(modelOutput)
	llm := &fakeCompletionClient{output: string(raw)}
	if _, err = (&Generator{LLM: llm}).Generate(t.Context(), input); err != nil || llm.calls != 1 {
		t.Fatalf("knowledge input blocked generation, calls=%d err=%v", llm.calls, err)
	}
	if strings.Contains(llm.lastPrompt, "injected-object") || strings.Contains(llm.lastPrompt, "must not reach the model") {
		t.Fatalf("knowledge input reached the model prompt: %s", llm.lastPrompt)
	}
}

func TestSourceFingerprintIgnoresKnowledgeObjects(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := SourceFingerprint(input, "real-model", "training-v1")
	if err != nil {
		t.Fatal(err)
	}
	input.QualifiedVideos[0].KnowledgeIndex = WikiReference{WikiPageID: "knowledge-index", Version: 7}
	input.QualifiedVideos[0].KnowledgeSignals = []KnowledgeSignal{{KnowledgeObjectID: "injected-object"}}
	if input.QualifiedVideos[0].Summary != nil {
		input.QualifiedVideos[0].Summary.Signals[0].KnowledgeRefs = []string{"knowledge-page"}
	}
	withKnowledge, err := SourceFingerprint(input, "real-model", "training-v1")
	if err != nil {
		t.Fatal(err)
	}
	if withKnowledge != baseline {
		t.Fatalf("knowledge metadata changed the source fingerprint: baseline=%s got=%s", baseline, withKnowledge)
	}
}

func TestGeneratorIgnoresLearningUnitKnowledgeReference(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	modelOutput.TopicClusters[0].Path.Stages[0].Units[0].KnowledgeRefs = []KnowledgeRef{{KnowledgeObjectID: "invented-object", WikiPageID: "invented-page", KnowledgeType: "concept"}}
	raw, _ := json.Marshal(modelOutput)
	doc, err := (&Generator{LLM: &fakeCompletionClient{output: string(raw)}}).Generate(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.TrainingPathProjection.TopicClusters[0].Path.Stages[0].Units[0].KnowledgeRefs) != 0 {
		t.Fatalf("learning unit knowledge reference reached the projection: %#v", doc)
	}
}

func TestGeneratorNormalizesNullKnowledgeCompatibilityField(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	modelOutput.TopicClusters[0].KnowledgeObjectIDs = nil
	raw, _ := json.Marshal(modelOutput)
	doc, err := (&Generator{LLM: &fakeCompletionClient{output: string(raw)}}).Generate(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if doc.TrainingPathProjection.TopicClusters[0].KnowledgeObjectIDs == nil {
		t.Fatalf("null knowledge field was not normalized: %#v", doc)
	}
}

func TestGeneratorAcceptsEvidenceOnlyRelation(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutputWithRelation(input)
	raw, _ := json.Marshal(modelOutput)

	doc, err := (&Generator{LLM: &fakeCompletionClient{output: string(raw)}}).Generate(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.TrainingPathProjection.TopicClusterRelations) != 1 || doc.TrainingPathProjection.Statistics.SelectedKnowledgeCount != 0 {
		t.Fatalf("unexpected evidence-only relation projection: %#v", doc)
	}
}

func TestGeneratorIgnoresRelationKnowledgeReferences(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*TopicClusterRelation)
	}{
		{name: "non-empty", mutate: func(relation *TopicClusterRelation) { relation.SourceKnowledgeRefs = []string{"invented-page"} }},
		{name: "null", mutate: func(relation *TopicClusterRelation) { relation.TargetKnowledgeRefs = nil }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			modelOutput := validModelOutputWithRelation(input)
			testCase.mutate(&modelOutput.TopicClusterRelations[0])
			raw, _ := json.Marshal(modelOutput)
			doc, err := (&Generator{LLM: &fakeCompletionClient{output: string(raw)}}).Generate(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			relation := doc.TrainingPathProjection.TopicClusterRelations[0]
			if relation.SourceKnowledgeRefs == nil || relation.TargetKnowledgeRefs == nil || len(relation.SourceKnowledgeRefs) != 0 || len(relation.TargetKnowledgeRefs) != 0 {
				t.Fatalf("relation knowledge references were not normalized: %#v", relation)
			}
		})
	}
}

func TestGeneratorRejectsDuplicateProjectionIDs(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutputWithRelation(input)
	modelOutput.TopicClusters[1].Path.PathID = modelOutput.TopicClusters[0].Path.PathID
	raw, _ := json.Marshal(modelOutput)
	_, err = (&Generator{LLM: &fakeCompletionClient{output: string(raw)}}).Generate(t.Context(), input)
	if err == nil || !strings.Contains(err.Error(), "duplicate projection id") {
		t.Fatalf("expected duplicate projection ID rejection, got %v", err)
	}
}

func TestGeneratorDoesNotCallModelForEmptyInput(t *testing.T) {
	input := InputPackage{SchemaVersion: SchemaVersion, OwnerScopeID: "single-tenant", SkipReasonCounts: emptySkipCounts(), TopicSourceCounts: emptyTopicSourceCounts()}
	llm := &fakeCompletionClient{}
	doc, err := (&Generator{LLM: llm}).Generate(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if llm.calls != 0 || len(doc.TrainingPathProjection.TopicClusters) != 0 {
		t.Fatalf("unexpected empty result: %#v", doc)
	}
}

func TestGeneratorRejectsNullRootArrays(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&Generator{LLM: &fakeCompletionClient{output: `{"topic_clusters":null,"topic_cluster_relations":[]}`}}).Generate(t.Context(), input)
	if err == nil || !strings.Contains(err.Error(), "must be JSON arrays") {
		t.Fatalf("expected null root array rejection, got %v", err)
	}
}

func TestBuildPromptUsesVersionedTemplateAndAppendsInput(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := buildPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		strings.TrimSpace(trainingOrchestrationPromptV2),
		"只能包含 topic_clusters 和 topic_cluster_relations",
		"video_id、transcript_generation、evidence_id 和时间范围必须逐字复制 INPUT",
		`"knowledge_object_ids":[]`,
		testEvidenceID,
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("training orchestration prompt missing %q", expected)
		}
	}
	if strings.Count(prompt, "\nINPUT:\n") != 1 {
		t.Fatal("training orchestration input boundary must appear once")
	}
}

func validModelOutput(input InputPackage) modelProjection {
	video := input.QualifiedVideos[0]
	evidenceSignal := video.EvidenceSignals[0]
	evidenceRef := EvidenceRef{VideoID: video.VideoID, TranscriptGeneration: video.TranscriptGeneration, EvidenceID: evidenceSignal.EvidenceID, StartMs: evidenceSignal.StartMs, EndMs: evidenceSignal.EndMs}
	unit := LearningUnit{UnitID: "unit-001", LearningTitle: "根据问题选择学习内容", LearnerQuestion: "如何判断当前需要学习什么？", LearningOutcome: "能够根据当前任务识别知识缺口", Sequence: 1, KnowledgeRefs: []KnowledgeRef{}, EvidenceRefs: []EvidenceRef{evidenceRef}, Confidence: .9, ReviewStatus: "passed"}
	cluster := TopicCluster{
		ClusterID: "cluster-001", Title: "按需学习", Summary: "围绕真实问题选择学习内容。",
		LearningGoal: "能够根据任务确定学习重点。", LearningContentType: "concept_cognition",
		MemberTopics:   []MemberTopic{{TopicID: "topic-001", Title: "按需学习"}},
		SourceVideoIDs: []string{video.VideoID}, KnowledgeObjectIDs: []string{},
		EvidenceRefs: []EvidenceRef{evidenceRef}, Confidence: .9, ReviewStatus: "passed",
		Path: LearningPath{PathID: "path-001", PrimaryTemplate: "concept_cognition", Stages: []LearningStage{{
			StageID: "stage-001", Title: "理解核心观点", Summary: "先理解按需学习。", Sequence: 1,
			Units: []LearningUnit{unit},
		}}},
	}
	return modelProjection{TopicClusters: []TopicCluster{cluster}, TopicClusterRelations: []TopicClusterRelation{}}
}

func validModelOutputWithRelation(input InputPackage) modelProjection {
	modelOutput := validModelOutput(input)
	second := modelOutput.TopicClusters[0]
	second.ClusterID = "cluster-002"
	second.MemberTopics = []MemberTopic{{TopicID: "topic-002", Title: "知识组织边界"}}
	second.Path.PathID = "path-002"
	secondStage := second.Path.Stages[0]
	secondUnit := secondStage.Units[0]
	secondUnit.UnitID = "unit-002"
	secondStage.StageID = "stage-002"
	secondStage.Units = []LearningUnit{secondUnit}
	second.Path.Stages = []LearningStage{secondStage}
	modelOutput.TopicClusters = append(modelOutput.TopicClusters, second)
	modelOutput.TopicClusterRelations = []TopicClusterRelation{{
		RelationID: "cluster-relation-001", SourceClusterID: "cluster-001", TargetClusterID: "cluster-002",
		RelationType: "recommended_before", Summary: "先理解方法，再判断资料边界。",
		SourceKnowledgeRefs: []string{}, TargetKnowledgeRefs: []string{},
		SourceEvidenceRefs: modelOutput.TopicClusters[0].EvidenceRefs,
		TargetEvidenceRefs: modelOutput.TopicClusters[1].EvidenceRefs,
		Confidence:         .86, ReviewStatus: "passed",
	}}
	return modelOutput
}

func emptySkipCounts() map[SkipReason]int {
	return map[SkipReason]int{SkipProcessing: 0, SkipProcessingFailed: 0, SkipFormalContentNotReady: 0, SkipFormalContentValidationFailed: 0, SkipEvidenceMissing: 0, SkipInaccessible: 0}
}
func emptyTopicSourceCounts() map[TopicSource]int {
	return map[TopicSource]int{TopicSourceFinalSummary: 0, TopicSourceNormalizedTranscript: 0}
}
