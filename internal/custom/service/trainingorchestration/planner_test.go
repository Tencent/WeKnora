package trainingorchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/service/summary"
)

type plannerLLM struct {
	output string
	calls  int
	last   string
}

func (f *plannerLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	f.last = prompt
	return f.output, nil
}

func (*plannerLLM) Model() string         { return "planner-model" }
func (*plannerLLM) PromptVersion() string { return "planning-v1" }

type correctionPlannerLLM struct {
	outputs []string
	prompts []string
}

func (f *correctionPlannerLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	index := len(f.prompts) - 1
	if index >= len(f.outputs) {
		return f.outputs[len(f.outputs)-1], nil
	}
	return f.outputs[index], nil
}

func (*correctionPlannerLLM) Model() string         { return "planner-model" }
func (*correctionPlannerLLM) PromptVersion() string { return "planning-v1" }

type invalidStructuredPlanningOutputError struct{}

func (invalidStructuredPlanningOutputError) Error() string {
	return "llm output invalid: stream completed with invalid JSON syntax"
}

func (invalidStructuredPlanningOutputError) InvalidOutput() bool { return true }

type structuredCorrectionPlannerLLM struct {
	outputs []string
	prompts []string
	errs    []error
}

func (f *structuredCorrectionPlannerLLM) CompleteJSON(_ context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	index := len(f.prompts) - 1
	if index < len(f.errs) && f.errs[index] != nil {
		return "", f.errs[index]
	}
	if index >= len(f.outputs) {
		return f.outputs[len(f.outputs)-1], nil
	}
	return f.outputs[index], nil
}

func (f *structuredCorrectionPlannerLLM) Complete(ctx context.Context, prompt string) (string, error) {
	return f.CompleteJSON(ctx, prompt)
}

func (*structuredCorrectionPlannerLLM) Model() string         { return "planner-model" }
func (*structuredCorrectionPlannerLLM) PromptVersion() string { return "planning-v1" }

func TestCollectCatalogUsesExactSummarySlugAndBoundedFields(t *testing.T) {
	collector, wiki, _ := testCollector(t, true)
	snapshot, err := collector.CollectCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if wiki.getCount != 1 || wiki.lastSlug != "typed-summary/"+testVideoID {
		t.Fatalf("unexpected summary reads: count=%d slug=%q", wiki.getCount, wiki.lastSlug)
	}
	if len(snapshot.Videos) != 1 || snapshot.Videos[0].OrchestrationProfile == nil {
		t.Fatalf("snapshot does not contain a bounded routing profile: %#v", snapshot)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	if strings.Contains(encoded, `"signals"`) || strings.Contains(encoded, `"transcript"`) || strings.Contains(encoded, "raw-mps-result-must-not-leak") {
		t.Fatalf("unbounded input leaked into catalog snapshot: %s", encoded)
	}
}

func TestPlannerRejectsBudgetBeforeCallingModel(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	snapshot, err := collector.CollectCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	llm := &plannerLLM{output: `{ "topic_clusters": [], "unselected_videos": [] }`}
	_, err = (&StageOnePlanner{LLM: llm, Config: PlannerConfig{MaxInputTokens: 1}}).Plan(t.Context(), snapshot)
	if err == nil || !strings.Contains(err.Error(), "token limit") {
		t.Fatalf("expected preflight budget error, got %v", err)
	}
	if llm.calls != 0 {
		t.Fatalf("model called before budget gate: %d", llm.calls)
	}
}

func TestPlannerBuildsValidatedPlanFromBoundedSnapshot(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	snapshot, err := collector.CollectCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	video := snapshot.Videos[0]
	unit := video.OrchestrationProfile.TopicUnits[0]
	ref := unit.EvidenceRefs[0]
	output := planningModelOutput{TopicClusters: []PlanCluster{{
		ClusterKey: "candidate-001", Title: "按需学习", Summary: "围绕真实问题选择学习重点。", LearningGoal: "能够根据任务确定学习重点。", PrimaryTemplate: "concept_cognition", Scope: "按需学习的基本判断", InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"}, SourceVideoIDs: []string{video.VideoID}, MaterialRequests: []MaterialRequest{{VideoID: video.VideoID, SummaryWikiPageID: video.SummaryWikiPageID, SummaryVersion: video.SummaryVersion, TranscriptGeneration: video.TranscriptGeneration, SummaryBlockIDs: []string{unit.SummaryBlockIDs[0]}, EvidenceIDs: []string{ref.EvidenceSentenceID}}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}}, UnselectedVideos: []UnselectedVideo{}}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	llm := &plannerLLM{output: string(raw)}
	draft, err := (&StageOnePlanner{LLM: llm}).Plan(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if llm.calls != 1 || len(draft.TopicClusters) != 1 || draft.TopicClusters[0].ReviewStatus != PlanAccepted {
		t.Fatalf("unexpected plan: calls=%d draft=%#v", llm.calls, draft)
	}
	if err := draft.ValidateAgainst(func() CatalogSnapshot {
		copy := snapshot
		copy.SourceFingerprint = draft.SourceFingerprint
		return copy
	}()); err != nil {
		t.Fatal(err)
	}
}

func TestPlannerNormalizesModelOwnedReviewStatus(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	snapshot, err := collector.CollectCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	video := snapshot.Videos[0]
	unit := video.OrchestrationProfile.TopicUnits[0]
	ref := unit.EvidenceRefs[0]
	output := planningModelOutput{TopicClusters: []PlanCluster{{
		ClusterKey: "candidate-001", Title: "按需学习", Summary: "围绕真实问题选择学习重点。", LearningGoal: "能够根据任务确定学习重点。", PrimaryTemplate: "concept_cognition", Scope: "按需学习的基本判断", InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"}, SourceVideoIDs: []string{video.VideoID}, MaterialRequests: []MaterialRequest{{VideoID: video.VideoID, SummaryBlockIDs: []string{unit.SummaryBlockIDs[0]}, EvidenceIDs: []string{ref.EvidenceSentenceID}}}, Confidence: 0.9, ReviewStatus: "accepted",
	}}, UnselectedVideos: []UnselectedVideo{}}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := (&StageOnePlanner{LLM: &plannerLLM{output: string(raw)}}).Plan(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("Plan rejected model-owned review status: %v", err)
	}
	if len(draft.TopicClusters) != 1 || draft.TopicClusters[0].ReviewStatus != PlanAccepted {
		t.Fatalf("program did not own final review status: %#v", draft.TopicClusters)
	}
}

func TestPlannerDerivesImmutableMaterialIdentity(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	snapshot, err := collector.CollectCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	video := snapshot.Videos[0]
	unit := video.OrchestrationProfile.TopicUnits[0]
	ref := unit.EvidenceRefs[0]
	// The model returns only the fields it selects. Immutable page/version and
	// transcript identity are intentionally omitted and filled from the catalog.
	payload := map[string]any{
		"topic_clusters": []any{map[string]any{
			"cluster_key": "candidate-001", "title": "按需学习", "summary": "围绕真实问题选择学习重点。",
			"learning_goal": "能够根据任务确定学习重点。", "primary_template": "concept_cognition",
			"scope": "按需学习的基本判断", "inclusion_criteria": []string{"有直接证据"},
			"exclusion_criteria": []string{"仅标题相似"}, "source_video_ids": []string{video.VideoID},
			"uncertain_video_ids": []string{}, "material_requests": []any{map[string]any{
				"video_id": video.VideoID, "summary_block_ids": []string{unit.SummaryBlockIDs[0]},
				"evidence_ids": []string{ref.EvidenceSentenceID},
			}}, "confidence": 0.9, "review_status": "candidate",
		}},
		"unselected_videos": []any{},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := (&StageOnePlanner{LLM: &plannerLLM{output: string(raw)}}).Plan(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	request := draft.TopicClusters[0].MaterialRequests[0]
	if request.SummaryWikiPageID != video.SummaryWikiPageID || request.SummaryVersion != video.SummaryVersion || request.TranscriptGeneration != video.TranscriptGeneration {
		t.Fatalf("immutable material identity was not derived: %#v", request)
	}
}

func TestPlannerMergeNormalizesModelOwnedReviewStatus(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: "sha256:test",
		Videos:            []CatalogVideo{validCatalogVideo("video-1")},
	}
	cluster := PlanCluster{
		ClusterKey: "candidate-001", Title: "主题", Summary: "摘要", LearningGoal: "目标",
		PrimaryTemplate: "concept_cognition", Scope: "范围",
		InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
		SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
			VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
		}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}
	modelCluster := cluster
	modelCluster.ReviewStatus = "passed"
	raw, err := json.Marshal(planningModelOutput{TopicClusters: []PlanCluster{modelCluster}, UnselectedVideos: []UnselectedVideo{}})
	if err != nil {
		t.Fatal(err)
	}
	planner := &StageOnePlanner{LLM: &plannerLLM{output: string(raw)}, Gate: NewCompletionGate(CompletionGateConfig{})}
	remainingBudget := 35000
	output, err := planner.completeMergeWithCorrection(
		t.Context(), "prompt", planningMergeInput{ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint, Clusters: []PlanCluster{cluster}},
		snapshot, PlannerConfig{MergeMaxInputTokens: 35000}, &remainingBudget, "merge-001",
	)
	if err != nil {
		t.Fatalf("merge rejected model-owned review status: %v", err)
	}
	if len(output.TopicClusters) != 1 || output.TopicClusters[0].ReviewStatus != PlanCandidate {
		t.Fatalf("merge did not normalize review status to candidate: %#v", output.TopicClusters)
	}
}

func TestPlannerCompletesMissingVideoCoverageAsUnselected(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope",
		Videos:          []CatalogVideo{validCatalogVideo("video-1"), validCatalogVideo("video-2")},
	}
	output := planningModelOutput{TopicClusters: []PlanCluster{{
		ClusterKey: "candidate-001", Title: "主题", Summary: "摘要", LearningGoal: "目标",
		PrimaryTemplate: "concept_cognition", Scope: "范围",
		InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
		SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
			VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
		}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}}, UnselectedVideos: []UnselectedVideo{}}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := (&StageOnePlanner{LLM: &plannerLLM{output: string(raw)}}).Plan(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(draft.TopicClusters) != 1 || len(draft.UnselectedVideos) != 1 {
		t.Fatalf("expected one accepted cluster and one completed unselected video, got %#v", draft)
	}
	if draft.UnselectedVideos[0].VideoID != "video-2" || draft.UnselectedVideos[0].Reason == "" {
		t.Fatalf("missing video was not completed as unselected: %#v", draft.UnselectedVideos)
	}
}

func TestPlannerPromptExposesOnlyEvidenceSentenceIDs(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion:   PlanningContractVersion,
		OwnerScopeID:      "scope",
		SourceFingerprint: "sha256:test",
		Videos: []CatalogVideo{{
			VideoID: "video-1", Title: "视频一", VideoType: "training",
			TranscriptGeneration: "generation-1", SummaryWikiPageID: "page-video-1", SummaryVersion: 1,
			OrchestrationProfile: &summary.OrchestrationProfile{
				SchemaVersion: summary.OrchestrationProfileSchemaVersion,
				PrimaryTopic:  "主题",
				TopicUnits: []summary.OrchestrationTopicUnit{{
					Title: "主题一", Abstract: "摘要", ContentForms: []string{"concept_cognition"},
					LearningOutcomes: []string{"结果"}, SummaryBlockIDs: []string{"block-video-1"},
					EvidenceChunkIDs: []string{"chunk-knowledge-1"},
					EvidenceRefs: []summary.EvidenceRef{{
						ChunkID:            "chunk-knowledge-1",
						EvidenceSentenceID: "evs:generation-1:one",
						StartMs:            1,
						EndMs:              2,
					}},
				}},
			},
		}},
	}
	prompt, err := (&StageOnePlanner{LLM: &plannerLLM{}}).buildPrompt(snapshot, snapshot.SourceFingerprint, "request-1", PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	inputPayload := prompt[strings.Index(prompt, "\n\nINPUT:\n")+len("\n\nINPUT:\n"):]
	if strings.Contains(inputPayload, "evidenceChunkIds") || strings.Contains(inputPayload, "chunk-knowledge-1") || strings.Contains(inputPayload, "chunk_id") {
		t.Fatalf("planning input leaked chunk evidence namespace: %s", inputPayload)
	}
	if strings.Contains(inputPayload, "evidenceRefs") || strings.Contains(inputPayload, "evidence_sentence_id") {
		t.Fatalf("planning input exposed the legacy nested evidence shape: %s", inputPayload)
	}
	if !strings.Contains(inputPayload, "allowed_references") || !strings.Contains(inputPayload, "evidence_ids") || !strings.Contains(inputPayload, "evs:generation-1:one") {
		t.Fatalf("planning input did not expose the explicit evidence ID allowlist: %s", inputPayload)
	}
}

func TestPlannerCorrectionPromptListsAllowedEvidenceSentenceIDs(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope",
		Videos:          []CatalogVideo{validCatalogVideo("video-1")},
	}
	invalid := planningModelOutput{TopicClusters: []PlanCluster{{
		ClusterKey: "candidate-001", Title: "主题", Summary: "摘要", LearningGoal: "目标",
		PrimaryTemplate: "concept_cognition", Scope: "范围",
		InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
		SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
			VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"chunk-knowledge-1"},
		}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}}, UnselectedVideos: []UnselectedVideo{}}
	valid := planningModelOutput{TopicClusters: []PlanCluster{{
		ClusterKey: "candidate-001", Title: "主题", Summary: "摘要", LearningGoal: "目标",
		PrimaryTemplate: "concept_cognition", Scope: "范围",
		InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
		SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
			VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
		}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}}, UnselectedVideos: []UnselectedVideo{}}
	invalidRaw, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	validRaw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	llm := &correctionPlannerLLM{outputs: []string{string(invalidRaw), string(validRaw)}}
	draft, err := (&StageOnePlanner{LLM: llm}).Plan(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(draft.TopicClusters) != 1 || len(llm.prompts) != 2 {
		t.Fatalf("unexpected correction flow: prompts=%d draft=%#v", len(llm.prompts), draft)
	}
	correctionPrompt := llm.prompts[1]
	if !strings.Contains(correctionPrompt, "evidence_ids") || !strings.Contains(correctionPrompt, "evs:gen-1:one") {
		t.Fatalf("correction prompt did not list allowed evidence IDs: %s", correctionPrompt)
	}
	correctionTail := correctionPrompt[strings.Index(correctionPrompt, "\n\nCORRECTION:\n")+len("\n\nCORRECTION:\n"):]
	if strings.Contains(correctionTail, "evidenceChunkIds") || strings.Contains(correctionTail, "chunk_id") {
		t.Fatalf("correction hint exposed chunk evidence fields: %s", correctionTail)
	}
}

func TestPlannerCorrectsUnknownFieldOnce(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope",
		Videos:          []CatalogVideo{validCatalogVideo("video-1")},
	}
	valid := planningModelOutput{TopicClusters: []PlanCluster{{
		ClusterKey: "candidate-001", Title: "主题", Summary: "摘要", LearningGoal: "目标",
		PrimaryTemplate: "concept_cognition", Scope: "范围",
		InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
		SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
			VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
		}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}}, UnselectedVideos: []UnselectedVideo{}}
	validRaw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	invalidRaw := strings.Replace(string(validRaw), `{"topic_clusters":`, `{"unexpected":"value","topic_clusters":`, 1)
	llm := &correctionPlannerLLM{outputs: []string{invalidRaw, string(validRaw)}}

	draft, err := (&StageOnePlanner{LLM: llm}).Plan(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(llm.prompts) != 2 || len(draft.TopicClusters) != 1 {
		t.Fatalf("expected one format correction, prompts=%d draft=%#v", len(llm.prompts), draft)
	}
	if !strings.Contains(llm.prompts[1], "unknown field") {
		t.Fatalf("format correction prompt does not include the parser reason: %s", llm.prompts[1])
	}
}

func TestPlannerCorrectsInvalidStructuredOutputOnce(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope",
		Videos:          []CatalogVideo{validCatalogVideo("video-1")},
	}
	cluster := PlanCluster{
		ClusterKey: "candidate-001", Title: "主题", Summary: "摘要", LearningGoal: "目标",
		PrimaryTemplate: "concept_cognition", Scope: "范围",
		InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
		SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
			VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
		}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}
	validRaw, err := json.Marshal(planningModelOutput{
		TopicClusters:    []PlanCluster{cluster},
		UnselectedVideos: []UnselectedVideo{},
	})
	if err != nil {
		t.Fatal(err)
	}
	llm := &structuredCorrectionPlannerLLM{
		outputs: []string{string(validRaw)},
		errs:    []error{invalidStructuredPlanningOutputError{}},
	}

	draft, err := (&StageOnePlanner{LLM: llm}).Plan(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(llm.prompts) != 2 || len(draft.TopicClusters) != 1 {
		t.Fatalf("expected one structured-output correction, prompts=%d draft=%#v", len(llm.prompts), draft)
	}
	if !strings.Contains(llm.prompts[1], "invalid JSON syntax") {
		t.Fatalf("structured-output correction prompt does not include the parser reason: %s", llm.prompts[1])
	}
}

func TestPlannerStopsAfterOneUnknownFieldCorrection(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope",
		Videos:          []CatalogVideo{validCatalogVideo("video-1")},
	}
	invalid := `{"unexpected":"value","topic_clusters":[],"unselected_videos":[]}`
	llm := &correctionPlannerLLM{outputs: []string{invalid, invalid, invalid}}

	_, err := (&StageOnePlanner{LLM: llm}).Plan(t.Context(), snapshot)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown-field failure after correction, got %v", err)
	}
	if len(llm.prompts) != 2 {
		t.Fatalf("expected exactly one correction call, got %d", len(llm.prompts))
	}
}

func TestPlannerMergeCorrectsInvalidStructuredOutputOnce(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: "sha256:test",
		Videos:            []CatalogVideo{validCatalogVideo("video-1")},
	}
	cluster := PlanCluster{
		ClusterKey: "candidate-001", Title: "主题", Summary: "摘要", LearningGoal: "目标",
		PrimaryTemplate: "concept_cognition", Scope: "范围",
		InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
		SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
			VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
		}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}
	input := planningMergeInput{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: snapshot.SourceFingerprint,
		Clusters:          []PlanCluster{cluster},
	}
	validRaw, err := json.Marshal(planningModelOutput{
		TopicClusters:    []PlanCluster{cluster},
		UnselectedVideos: []UnselectedVideo{},
	})
	if err != nil {
		t.Fatal(err)
	}
	llm := &structuredCorrectionPlannerLLM{
		outputs: []string{string(validRaw)},
		errs:    []error{invalidStructuredPlanningOutputError{}},
	}
	planner := &StageOnePlanner{LLM: llm, Gate: NewCompletionGate(CompletionGateConfig{})}
	remainingBudget := 35000

	output, err := planner.completeMergeWithCorrection(
		t.Context(), "prompt", input, snapshot,
		PlannerConfig{MergeMaxInputTokens: 35000}, &remainingBudget, "merge-001",
	)
	if err != nil {
		t.Fatalf("merge returned error: %v", err)
	}
	if len(llm.prompts) != 2 || len(output.TopicClusters) != 1 {
		t.Fatalf("expected one structured-output correction, prompts=%d output=%#v", len(llm.prompts), output)
	}
	if !strings.Contains(llm.prompts[1], "invalid JSON syntax") {
		t.Fatalf("merge structured-output correction prompt does not include the parser reason: %s", llm.prompts[1])
	}
}

func TestPlannerPacksHundredVideosWithinConfiguredLimits(t *testing.T) {
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, OwnerScopeID: "scope"}
	for index := 0; index < 100; index++ {
		id := fmt.Sprintf("video-%03d", index+1)
		snapshot.Videos = append(snapshot.Videos, CatalogVideo{VideoID: id, Title: "主题视频", VideoType: "training", TranscriptGeneration: "generation-1", SummaryWikiPageID: "page-" + id, SummaryVersion: 1, OrchestrationProfile: &summary.OrchestrationProfile{SchemaVersion: summary.OrchestrationProfileSchemaVersion, TopicUnits: []summary.OrchestrationTopicUnit{{Title: "主题", Abstract: "简短主题摘要", ContentForms: []string{"concept_cognition"}, SummaryBlockIDs: []string{"block-" + id}, EvidenceChunkIDs: []string{"ev-" + id}, EvidenceRefs: []summary.EvidenceRef{{ChunkID: "chunk-" + id, EvidenceSentenceID: "ev-" + id, StartMs: 1, EndMs: 2}}}}}})
	}
	planner := &StageOnePlanner{LLM: &plannerLLM{}}
	batches, total, err := planner.pack(normalizeCatalog(snapshot), PlannerConfig{MaxInputTokens: 450000, BatchMaxInputTokens: 35000, MaxVideosPerBatch: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 10 || total <= 0 || total > 450000 {
		t.Fatalf("unexpected packing: batches=%d total=%d", len(batches), total)
	}
}

func TestPlannerDefaultsToLargerSmallMergeGroups(t *testing.T) {
	planner := &StageOnePlanner{LLM: &plannerLLM{}}
	config, err := planner.config()
	if err != nil {
		t.Fatal(err)
	}
	if config.MaxMergeItems != 20 {
		t.Fatalf("expected small merge groups to allow 20 candidates, got %d", config.MaxMergeItems)
	}
	if config.MaxVideosPerBatch != 5 {
		t.Fatalf("expected planning batches to default to 5 videos, got %d", config.MaxVideosPerBatch)
	}
}

type collapsingMergeLLM struct {
	calls int
}

func (f *collapsingMergeLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	marker := "\n\nINPUT:\n"
	index := strings.Index(prompt, marker)
	if index < 0 {
		return "", fmt.Errorf("merge input marker missing")
	}
	var envelope struct {
		Input planningMergeInput `json:"input"`
	}
	if err := json.Unmarshal([]byte(prompt[index+len(marker):]), &envelope); err != nil {
		return "", err
	}
	if len(envelope.Input.Clusters) == 0 {
		return "", fmt.Errorf("merge input is empty")
	}
	merged := envelope.Input.Clusters[0]
	merged.ClusterKey = fmt.Sprintf("candidate-merged-%d", f.calls)
	for _, cluster := range envelope.Input.Clusters[1:] {
		merged.SourceVideoIDs = append(merged.SourceVideoIDs, cluster.SourceVideoIDs...)
		merged.MaterialRequests = append(merged.MaterialRequests, cluster.MaterialRequests...)
	}
	raw, err := json.Marshal(planningModelOutput{TopicClusters: []PlanCluster{merged}, UnselectedVideos: []UnselectedVideo{}})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (*collapsingMergeLLM) Model() string         { return "planner-model" }
func (*collapsingMergeLLM) PromptVersion() string { return "planning-v1" }

func TestPlannerMergesSmallCandidateSetInOneCall(t *testing.T) {
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test"}
	clusters := make([]PlanCluster, 0, 11)
	for index := 1; index <= 11; index++ {
		videoID := fmt.Sprintf("video-%02d", index)
		snapshot.Videos = append(snapshot.Videos, validCatalogVideo(videoID))
		clusters = append(clusters, PlanCluster{
			ClusterKey: fmt.Sprintf("candidate-%02d", index), Title: "主题", Summary: "摘要", LearningGoal: "目标",
			PrimaryTemplate: "concept_cognition", Scope: "范围", SourceVideoIDs: []string{videoID},
			MaterialRequests: []MaterialRequest{{VideoID: videoID, SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1", SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"}}},
			Confidence:       0.9, ReviewStatus: PlanCandidate,
		})
	}
	llm := &collapsingMergeLLM{}
	planner := &StageOnePlanner{LLM: llm, Gate: NewCompletionGate(CompletionGateConfig{})}
	config, err := planner.config()
	if err != nil {
		t.Fatal(err)
	}
	remaining := 35000
	merged, err := planner.merge(t.Context(), snapshot, clusters, snapshot.SourceFingerprint, config, &remaining)
	if err != nil {
		t.Fatal(err)
	}
	if llm.calls != 1 || len(merged) != 1 || len(merged[0].SourceVideoIDs) != 11 {
		t.Fatalf("expected one bounded merge call for 11 candidates, calls=%d merged=%d sources=%d", llm.calls, len(merged), len(merged[0].SourceVideoIDs))
	}
}

type truncatedPlannerLLM struct {
	calls int
}

func (f *truncatedPlannerLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	if strings.Contains(prompt, `"video_id":"video-1"`) && strings.Contains(prompt, `"video_id":"video-2"`) {
		return `{"topic_clusters":[`, nil
	}
	videoID := "video-1"
	if strings.Contains(prompt, `"video_id":"video-2"`) {
		videoID = "video-2"
	}
	output := planningModelOutput{
		TopicClusters: []PlanCluster{{
			ClusterKey: "candidate-" + videoID, Title: "主题 " + videoID, Summary: "主题摘要",
			LearningGoal: "理解主题", PrimaryTemplate: "concept_cognition", Scope: "基础范围",
			InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
			SourceVideoIDs: []string{videoID}, MaterialRequests: []MaterialRequest{{
				VideoID: videoID, SummaryWikiPageID: "page-" + videoID, SummaryVersion: 1,
				TranscriptGeneration: "generation-1", SummaryBlockIDs: []string{"block-" + videoID},
				EvidenceIDs: []string{"evidence-" + videoID},
			}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
		}},
		UnselectedVideos: []UnselectedVideo{},
	}
	raw, _ := json.Marshal(output)
	return string(raw), nil
}

func (*truncatedPlannerLLM) Model() string         { return "planner-model" }
func (*truncatedPlannerLLM) PromptVersion() string { return "planning-v1" }

func TestPlannerShrinksBatchAfterTruncatedJSON(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion, OwnerScopeID: "scope",
		Videos: []CatalogVideo{
			{
				VideoID: "video-1", Title: "视频一", VideoType: "training",
				TranscriptGeneration: "generation-1", SummaryWikiPageID: "page-video-1", SummaryVersion: 1,
				OrchestrationProfile: &summary.OrchestrationProfile{
					SchemaVersion: summary.OrchestrationProfileSchemaVersion,
					TopicUnits: []summary.OrchestrationTopicUnit{{
						Title: "主题一", Abstract: "摘要", ContentForms: []string{"concept_cognition"},
						SummaryBlockIDs: []string{"block-video-1"}, EvidenceChunkIDs: []string{"evidence-video-1"},
						EvidenceRefs: []summary.EvidenceRef{{ChunkID: "chunk-1", EvidenceSentenceID: "evidence-video-1", StartMs: 1, EndMs: 2}},
					}},
				},
			},
			{
				VideoID: "video-2", Title: "视频二", VideoType: "training",
				TranscriptGeneration: "generation-1", SummaryWikiPageID: "page-video-2", SummaryVersion: 1,
				OrchestrationProfile: &summary.OrchestrationProfile{
					SchemaVersion: summary.OrchestrationProfileSchemaVersion,
					TopicUnits: []summary.OrchestrationTopicUnit{{
						Title: "主题二", Abstract: "摘要", ContentForms: []string{"concept_cognition"},
						SummaryBlockIDs: []string{"block-video-2"}, EvidenceChunkIDs: []string{"evidence-video-2"},
						EvidenceRefs: []summary.EvidenceRef{{ChunkID: "chunk-2", EvidenceSentenceID: "evidence-video-2", StartMs: 1, EndMs: 2}},
					}},
				},
			},
		},
	}
	llm := &truncatedPlannerLLM{}
	draft, err := (&StageOnePlanner{LLM: llm}).Plan(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if llm.calls != 4 || len(draft.TopicClusters) != 2 {
		t.Fatalf("expected one truncated batch, two shrunk plans and one merge, calls=%d draft=%#v", llm.calls, draft)
	}
}

type invalidBatchPlannerLLM struct {
	calls int
}

func (f *invalidBatchPlannerLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	if !strings.Contains(prompt, "主题簇归并器") && strings.Contains(prompt, `"video_id":"video-1"`) && strings.Contains(prompt, `"video_id":"video-2"`) {
		return `not json`, nil
	}
	if strings.Contains(prompt, "主题簇归并器") {
		marker := "\n\nINPUT:\n"
		index := strings.Index(prompt, marker)
		if index < 0 {
			return "", fmt.Errorf("merge input marker missing")
		}
		var envelope struct {
			Input planningMergeInput `json:"input"`
		}
		if err := json.Unmarshal([]byte(prompt[index+len(marker):]), &envelope); err != nil {
			return "", err
		}
		merged := envelope.Input.Clusters[0]
		merged.ClusterKey = "candidate-merged"
		for _, cluster := range envelope.Input.Clusters[1:] {
			merged.SourceVideoIDs = append(merged.SourceVideoIDs, cluster.SourceVideoIDs...)
			merged.MaterialRequests = append(merged.MaterialRequests, cluster.MaterialRequests...)
		}
		raw, err := json.Marshal(planningModelOutput{TopicClusters: []PlanCluster{merged}, UnselectedVideos: []UnselectedVideo{}})
		return string(raw), err
	}
	videoID := "video-1"
	if strings.Contains(prompt, `"video_id":"video-2"`) {
		videoID = "video-2"
	}
	output := planningModelOutput{
		TopicClusters: []PlanCluster{{
			ClusterKey: "candidate-" + videoID, Title: "主题 " + videoID, Summary: "主题摘要",
			LearningGoal: "理解主题", PrimaryTemplate: "concept_cognition", Scope: "基础范围",
			InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
			SourceVideoIDs: []string{videoID}, MaterialRequests: []MaterialRequest{{
				VideoID: videoID, SummaryWikiPageID: "page-" + videoID, SummaryVersion: 1,
				TranscriptGeneration: "generation-1", SummaryBlockIDs: []string{"block-" + videoID},
				EvidenceIDs: []string{"evidence-" + videoID},
			}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
		}},
		UnselectedVideos: []UnselectedVideo{},
	}
	raw, _ := json.Marshal(output)
	return string(raw), nil
}

func (f *invalidBatchPlannerLLM) CompleteJSON(ctx context.Context, prompt string) (string, error) {
	if !strings.Contains(prompt, "主题簇归并器") && strings.Contains(prompt, `"video_id":"video-1"`) && strings.Contains(prompt, `"video_id":"video-2"`) {
		f.calls++
		return "", invalidStructuredPlanningOutputError{}
	}
	return f.Complete(ctx, prompt)
}

func (*invalidBatchPlannerLLM) Model() string         { return "planner-model" }
func (*invalidBatchPlannerLLM) PromptVersion() string { return "planning-v1" }

func TestPlannerShrinksBatchAfterInvalidJSONCorrection(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion, OwnerScopeID: "scope",
		Videos: []CatalogVideo{
			{VideoID: "video-1", Title: "视频一", VideoType: "training", TranscriptGeneration: "generation-1", SummaryWikiPageID: "page-video-1", SummaryVersion: 1, OrchestrationProfile: &summary.OrchestrationProfile{SchemaVersion: summary.OrchestrationProfileSchemaVersion, TopicUnits: []summary.OrchestrationTopicUnit{{Title: "主题一", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, SummaryBlockIDs: []string{"block-video-1"}, EvidenceChunkIDs: []string{"evidence-video-1"}, EvidenceRefs: []summary.EvidenceRef{{ChunkID: "chunk-1", EvidenceSentenceID: "evidence-video-1", StartMs: 1, EndMs: 2}}}}}},
			{VideoID: "video-2", Title: "视频二", VideoType: "training", TranscriptGeneration: "generation-1", SummaryWikiPageID: "page-video-2", SummaryVersion: 1, OrchestrationProfile: &summary.OrchestrationProfile{SchemaVersion: summary.OrchestrationProfileSchemaVersion, TopicUnits: []summary.OrchestrationTopicUnit{{Title: "主题二", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, SummaryBlockIDs: []string{"block-video-2"}, EvidenceChunkIDs: []string{"evidence-video-2"}, EvidenceRefs: []summary.EvidenceRef{{ChunkID: "chunk-2", EvidenceSentenceID: "evidence-video-2", StartMs: 1, EndMs: 2}}}}}},
		},
	}
	llm := &invalidBatchPlannerLLM{}
	draft, err := (&StageOnePlanner{LLM: llm}).Plan(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if llm.calls != 5 || len(draft.TopicClusters) != 1 || len(draft.TopicClusters[0].SourceVideoIDs) != 2 {
		t.Fatalf("expected invalid batch, one correction, two smaller plans and one merge, calls=%d draft=%#v", llm.calls, draft)
	}
}

func TestPlannerMergeAcceptsDistinctClustersWithDuplicateTemporaryKeys(t *testing.T) {
	videoOne := validCatalogVideo("video-1")
	videoTwo := validCatalogVideo("video-2")
	snapshot := CatalogSnapshot{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: "sha256:test",
		Videos:            []CatalogVideo{videoOne, videoTwo},
	}
	makeCluster := func(videoID, title string) PlanCluster {
		return PlanCluster{
			ClusterKey: "candidate-001", Title: title, Summary: "摘要", LearningGoal: "目标",
			PrimaryTemplate: "concept_cognition", SourceVideoIDs: []string{videoID},
			MaterialRequests: []MaterialRequest{{
				VideoID:              videoID,
				SummaryWikiPageID:    "page-1",
				SummaryVersion:       3,
				TranscriptGeneration: "gen-1",
				SummaryBlockIDs:      []string{"block-1"},
				EvidenceIDs:          []string{"evs:gen-1:one"},
			}},
			Confidence: 0.9, ReviewStatus: PlanCandidate,
		}
	}
	input := planningMergeInput{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: snapshot.SourceFingerprint,
		Clusters:          []PlanCluster{makeCluster("video-1", "主题一"), makeCluster("video-2", "主题二")},
	}
	output := planningModelOutput{
		TopicClusters:    []PlanCluster{makeCluster("video-1", "主题一"), makeCluster("video-2", "主题二")},
		UnselectedVideos: []UnselectedVideo{},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	planner := &StageOnePlanner{LLM: &plannerLLM{output: string(raw)}, Gate: NewCompletionGate(CompletionGateConfig{})}
	remainingBudget := 35000

	got, err := planner.completeMergeWithCorrection(
		t.Context(), "prompt", input, snapshot,
		PlannerConfig{MergeMaxInputTokens: 35000}, &remainingBudget, "merge-001",
	)
	if err != nil {
		t.Fatalf("merge rejected distinct clusters that reuse temporary keys: %v", err)
	}
	if len(got.TopicClusters) != 2 {
		t.Fatalf("expected two clusters, got %#v", got.TopicClusters)
	}
}

func TestPlannerMergeCorrectsUnknownFieldOnce(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: "sha256:test",
		Videos:            []CatalogVideo{validCatalogVideo("video-1")},
	}
	cluster := PlanCluster{
		ClusterKey: "candidate-001", Title: "主题", Summary: "摘要", LearningGoal: "目标",
		PrimaryTemplate: "concept_cognition", Scope: "范围",
		InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
		SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
			VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
		}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}
	input := planningMergeInput{ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint, Clusters: []PlanCluster{cluster}}
	validRaw, err := json.Marshal(planningModelOutput{TopicClusters: []PlanCluster{cluster}, UnselectedVideos: []UnselectedVideo{}})
	if err != nil {
		t.Fatal(err)
	}
	invalidRaw := strings.Replace(string(validRaw), `{"topic_clusters":`, `{"unexpected":"value","topic_clusters":`, 1)
	llm := &correctionPlannerLLM{outputs: []string{invalidRaw, string(validRaw)}}
	planner := &StageOnePlanner{LLM: llm, Gate: NewCompletionGate(CompletionGateConfig{})}
	remainingBudget := 35000

	output, err := planner.completeMergeWithCorrection(
		t.Context(), "prompt", input, snapshot,
		PlannerConfig{MergeMaxInputTokens: 35000}, &remainingBudget, "merge-001",
	)
	if err != nil {
		t.Fatalf("merge returned error: %v", err)
	}
	if len(llm.prompts) != 2 || len(output.TopicClusters) != 1 {
		t.Fatalf("expected one format correction, prompts=%d output=%#v", len(llm.prompts), output)
	}
	if !strings.Contains(llm.prompts[1], "unknown field") {
		t.Fatalf("merge correction prompt does not include the parser reason: %s", llm.prompts[1])
	}
}

func TestPlannerMergeAllowsOneAdditionalFormatCorrection(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: "sha256:test",
		Videos:            []CatalogVideo{validCatalogVideo("video-1")},
	}
	cluster := PlanCluster{
		ClusterKey: "candidate-001", Title: "主题", Summary: "摘要", LearningGoal: "目标",
		PrimaryTemplate: "concept_cognition", Scope: "范围",
		InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
		SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
			VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
		}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}
	input := planningMergeInput{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: snapshot.SourceFingerprint,
		Clusters:          []PlanCluster{cluster},
	}
	validRaw, err := json.Marshal(planningModelOutput{
		TopicClusters:    []PlanCluster{cluster},
		UnselectedVideos: []UnselectedVideo{},
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidRaw := strings.Replace(string(validRaw), `{"topic_clusters":`, `{"unexpected":"value","topic_clusters":`, 1)
	llm := &structuredCorrectionPlannerLLM{
		outputs: []string{"", invalidRaw, string(validRaw)},
		errs:    []error{invalidStructuredPlanningOutputError{}},
	}
	planner := &StageOnePlanner{LLM: llm, Gate: NewCompletionGate(CompletionGateConfig{})}
	remainingBudget := 35000

	output, err := planner.completeMergeWithCorrection(
		t.Context(), "prompt", input, snapshot,
		PlannerConfig{MergeMaxInputTokens: 35000}, &remainingBudget, "merge-001",
	)
	if err != nil {
		t.Fatalf("merge returned error after bounded second correction: %v", err)
	}
	if len(llm.prompts) != 3 || len(output.TopicClusters) != 1 {
		t.Fatalf("expected two format corrections, prompts=%d output=%#v", len(llm.prompts), output)
	}
	if !strings.Contains(llm.prompts[2], "previous correction still violated") {
		t.Fatalf("second correction prompt did not explain the bounded retry: %s", llm.prompts[2])
	}
}

func TestPlannerMergeStopsAfterSecondFormatCorrection(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: "sha256:test",
		Videos:            []CatalogVideo{validCatalogVideo("video-1")},
	}
	cluster := PlanCluster{
		ClusterKey: "candidate-001", Title: "主题", Summary: "摘要", LearningGoal: "目标",
		PrimaryTemplate: "concept_cognition", Scope: "范围",
		InclusionCriteria: []string{"有直接证据"}, ExclusionCriteria: []string{"仅标题相似"},
		SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
			VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
		}}, Confidence: 0.9, ReviewStatus: PlanCandidate,
	}
	input := planningMergeInput{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: snapshot.SourceFingerprint,
		Clusters:          []PlanCluster{cluster},
	}
	validRaw, err := json.Marshal(planningModelOutput{
		TopicClusters:    []PlanCluster{cluster},
		UnselectedVideos: []UnselectedVideo{},
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidRaw := strings.Replace(string(validRaw), `{"topic_clusters":`, `{"unexpected":"value","topic_clusters":`, 1)
	llm := &structuredCorrectionPlannerLLM{
		outputs: []string{"", invalidRaw, invalidRaw},
		errs:    []error{invalidStructuredPlanningOutputError{}},
	}
	planner := &StageOnePlanner{LLM: llm, Gate: NewCompletionGate(CompletionGateConfig{})}
	remainingBudget := 35000

	_, err = planner.completeMergeWithCorrection(
		t.Context(), "prompt", input, snapshot,
		PlannerConfig{MergeMaxInputTokens: 35000}, &remainingBudget, "merge-001",
	)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict unknown-field failure after second correction, got %v", err)
	}
	if len(llm.prompts) != 3 {
		t.Fatalf("expected exactly two bounded corrections, got %d", len(llm.prompts))
	}
}
