package trainingorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type clusterGeneratorLLM struct {
	output     string
	calls      int
	lastPrompt string
}

func (f *clusterGeneratorLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	f.lastPrompt = prompt
	return f.output, nil
}

func (*clusterGeneratorLLM) Model() string         { return "cluster-model" }
func (*clusterGeneratorLLM) PromptVersion() string { return "cluster-generation-v1" }

func TestClusterGeneratorBuildsEvidenceClosedDraft(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:catalog",
		Videos: []CatalogVideo{validCatalogVideo("video-1")},
	}
	plan := validAcceptedPlan(snapshot)
	material := ClusterMaterial{
		ContractVersion: MaterialContractVersion, ClusterKey: "cluster-1", SourceVideoIDs: []string{"video-1"},
		SummaryBlocks: []MaterialBlock{{VideoID: "video-1", BlockID: "block-1", Text: "总结材料"}},
		Evidence: []MaterialEvidence{{
			VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "evs:gen-1:one",
			StartMs: 100, EndMs: 200, Text: "证据材料",
		}},
	}
	raw := `{"stages":[{"title":"基础理解","summary":"建立主题基础认知","units":[{"learning_title":"识别核心问题","learner_question":"这个主题解决什么问题？","learning_outcome":"能够用自己的话说明核心问题","evidence_refs":[{"video_id":"video-1","evidence_id":"evs:gen-1:one"}],"confidence":0.92}]}]}`
	llm := &clusterGeneratorLLM{output: raw}

	draft, err := (&ClusterGenerator{LLM: llm}).Generate(context.Background(), plan.TopicClusters[0], material)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if llm.calls != 1 || len(draft.Stages) != 1 || len(draft.Stages[0].Units) != 1 {
		t.Fatalf("unexpected generator result: calls=%d draft=%#v", llm.calls, draft)
	}
	unit := draft.Stages[0].Units[0]
	if unit.EvidenceRefs[0].TranscriptGeneration != "gen-1" || unit.EvidenceRefs[0].StartMs != 100 || unit.EvidenceRefs[0].EndMs != 200 {
		t.Fatalf("generator did not resolve evidence from material: %#v", unit.EvidenceRefs)
	}
	if strings.Contains(llm.lastPrompt, `"cluster_id"`) || strings.Contains(llm.lastPrompt, `"unit_id"`) {
		t.Fatalf("prompt delegated persistent IDs to the model: %s", llm.lastPrompt)
	}
}

func TestClusterGeneratorPromptBoundsOutputForProviderLimit(t *testing.T) {
	plan, material := validClusterGenerationInput()
	prompt, err := (&ClusterGenerator{}).buildPrompt(plan, material)
	if err != nil {
		t.Fatalf("buildPrompt returned error: %v", err)
	}
	for _, expected := range []string{"最多 6 个阶段、16 个学习单元", `"max_stages":6`, `"max_units":16`, `"max_output_tokens":8000`} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("cluster prompt missing output bound %q", expected)
		}
	}
}

func TestClusterGeneratorRejectsUnknownFieldsAndTrailingContent(t *testing.T) {
	plan, material := validClusterGenerationInput()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unknown field",
			raw:  `{"stages":[],"unit_id":"model-owned-id"}`,
			want: "unknown field",
		},
		{
			name: "trailing content",
			raw:  `{"stages":[]} trailing`,
			want: "trailing content",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (&ClusterGenerator{LLM: &clusterGeneratorLLM{output: test.raw}}).Generate(context.Background(), plan, material)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestClusterGeneratorRejectsEvidenceOutsideMaterial(t *testing.T) {
	plan, material := validClusterGenerationInput()
	raw := `{"stages":[{"title":"基础","summary":"摘要","units":[{"learning_title":"单元","learner_question":"问题？","learning_outcome":"结果","evidence_refs":[{"video_id":"video-1","evidence_id":"invented"}],"confidence":0.8}]}]}`
	_, err := (&ClusterGenerator{LLM: &clusterGeneratorLLM{output: raw}}).Generate(context.Background(), plan, material)
	if err == nil || !strings.Contains(err.Error(), "outside the material whitelist") {
		t.Fatalf("expected whitelist rejection, got %v", err)
	}
}

func TestClusterGeneratorCorrectsWhitelistReferenceOnlyOnce(t *testing.T) {
	plan, material := validClusterGenerationInput()
	llm := &clusterGeneratorLLM{
		output: `{"stages":[{"title":"基础","summary":"摘要","units":[{"learning_title":"单元","learner_question":"问题？","learning_outcome":"结果","evidence_refs":[{"video_id":"video-1","evidence_id":"invented"}],"confidence":0.8}]}]}`,
	}
	_, err := (&ClusterGenerator{LLM: llm}).Generate(context.Background(), plan, material)
	if err == nil || llm.calls != 2 || !strings.Contains(err.Error(), "outside the material whitelist") {
		t.Fatalf("expected exactly one whitelist correction, calls=%d err=%v", llm.calls, err)
	}
}

func TestClusterGeneratorCorrectsDuplicateEvidenceReferenceOnce(t *testing.T) {
	plan, material := validClusterGenerationInput()
	invalid := `{"stages":[{"title":"基础","summary":"摘要","units":[{"learning_title":"单元","learner_question":"问题？","learning_outcome":"结果","evidence_refs":[{"video_id":"video-1","evidence_id":"evs:gen-1:one"},{"video_id":"video-1","evidence_id":"evs:gen-1:one"}],"confidence":0.8}]}]}`
	valid := `{"stages":[{"title":"基础","summary":"摘要","units":[{"learning_title":"单元","learner_question":"问题？","learning_outcome":"结果","evidence_refs":[{"video_id":"video-1","evidence_id":"evs:gen-1:one"}],"confidence":0.8}]}]}`
	llm := &scriptedStructuredClusterLLM{outputs: []string{invalid, valid}}

	draft, err := (&ClusterGenerator{LLM: llm}).Generate(context.Background(), plan, material)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if llm.calls != 2 || len(draft.Stages) != 1 || len(draft.Stages[0].Units[0].EvidenceRefs) != 1 {
		t.Fatalf("expected one bounded duplicate-reference correction, calls=%d draft=%#v", llm.calls, draft)
	}
	if !strings.Contains(llm.prompts[1], "repeats evidence") {
		t.Fatalf("correction prompt did not carry duplicate-reference reason: %s", llm.prompts[1])
	}
}

func TestClusterGeneratorClassifiesTruncatedOutput(t *testing.T) {
	plan, material := validClusterGenerationInput()
	llm := &clusterGeneratorLLM{output: "<think>输出未完成"}
	_, err := (&ClusterGenerator{LLM: llm}).Generate(context.Background(), plan, material)
	var generationErr *GenerationError
	if !errors.As(err, &generationErr) || generationErr.Code != "model_output_truncated" {
		t.Fatalf("expected truncated output error, got %v", err)
	}
}

func TestClusterGeneratorCorrectsCompletedInvalidJSONOnce(t *testing.T) {
	plan, material := validClusterGenerationInput()
	valid := `{"stages":[{"title":"基础","summary":"摘要","units":[{"learning_title":"单元","learner_question":"问题？","learning_outcome":"结果","evidence_refs":[{"video_id":"video-1","evidence_id":"evs:gen-1:one"}],"confidence":0.8}]}]}`
	llm := &scriptedStructuredClusterLLM{
		outputs: []string{"", valid},
		errs:    []error{invalidStructuredClusterOutputError{}, nil},
	}

	draft, err := (&ClusterGenerator{LLM: llm}).Generate(context.Background(), plan, material)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if llm.calls != 2 || len(draft.Stages) != 1 || len(draft.Stages[0].Units) != 1 {
		t.Fatalf("expected one bounded correction, calls=%d draft=%#v", llm.calls, draft)
	}
	if !strings.Contains(llm.prompts[1], "Return one corrected JSON object only") ||
		!strings.Contains(llm.prompts[1], "stream completed with invalid JSON syntax") {
		t.Fatalf("correction prompt did not carry structured JSON failure reason: %s", llm.prompts[1])
	}
}

func TestClusterGeneratorDoesNotRepeatInvalidJSONCorrection(t *testing.T) {
	plan, material := validClusterGenerationInput()
	llm := &scriptedStructuredClusterLLM{
		errs: []error{invalidStructuredClusterOutputError{}, invalidStructuredClusterOutputError{}},
	}

	_, err := (&ClusterGenerator{LLM: llm}).Generate(context.Background(), plan, material)
	var generationErr *GenerationError
	if !errors.As(err, &generationErr) || generationErr.Code != "model_output_invalid" {
		t.Fatalf("expected bounded invalid JSON failure, got %v", err)
	}
	if llm.calls != 2 {
		t.Fatalf("expected exactly one correction attempt, calls=%d", llm.calls)
	}
}

func validClusterGenerationInput() (PlanCluster, ClusterMaterial) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:catalog",
		Videos: []CatalogVideo{validCatalogVideo("video-1")},
	}
	plan := validAcceptedPlan(snapshot).TopicClusters[0]
	material := ClusterMaterial{
		ContractVersion: MaterialContractVersion, ClusterKey: plan.ClusterKey, SourceVideoIDs: []string{"video-1"},
		SummaryBlocks: []MaterialBlock{{VideoID: "video-1", BlockID: "block-1", Text: "总结材料"}},
		Evidence: []MaterialEvidence{{
			VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "evs:gen-1:one",
			StartMs: 100, EndMs: 200, Text: "证据材料",
		}},
	}
	return plan, material
}

type invalidStructuredClusterOutputError struct{}

func (invalidStructuredClusterOutputError) Error() string {
	return "llm output invalid: stream completed with invalid JSON syntax"
}

func (invalidStructuredClusterOutputError) InvalidOutput() bool { return true }

type scriptedStructuredClusterLLM struct {
	outputs []string
	errs    []error
	prompts []string
	calls   int
}

func (f *scriptedStructuredClusterLLM) Complete(ctx context.Context, prompt string) (string, error) {
	return f.CompleteJSON(ctx, prompt)
}

func (f *scriptedStructuredClusterLLM) CompleteJSON(_ context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	index := f.calls
	f.calls++
	var err error
	if index < len(f.errs) {
		err = f.errs[index]
	}
	if err != nil {
		return "", err
	}
	if index >= len(f.outputs) {
		return "", errors.New("scripted cluster output exhausted")
	}
	return f.outputs[index], nil
}

func (*scriptedStructuredClusterLLM) Model() string         { return "cluster-model" }
func (*scriptedStructuredClusterLLM) PromptVersion() string { return "cluster-generation-v1" }

func TestClusterGeneratorSplitsMaterialAtCompleteBoundaries(t *testing.T) {
	plan, material := validClusterGenerationInput()
	plan.MaterialRequests[0].SummaryBlockIDs = []string{"block-1", "block-2"}
	plan.MaterialRequests[0].EvidenceIDs = []string{"evs:gen-1:one", "evs:gen-1:two"}
	material.SummaryBlocks = append(material.SummaryBlocks, MaterialBlock{
		VideoID: "video-1", BlockID: "block-2", Text: strings.Repeat("第二个完整总结 block。", 80),
	})
	material.Evidence = append(material.Evidence, MaterialEvidence{
		VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "evs:gen-1:two",
		StartMs: 300, EndMs: 400, Text: strings.Repeat("第二条完整证据。", 80),
	})
	generator := &ClusterGenerator{}
	smallCluster := plan
	smallCluster.MaterialRequests = append([]MaterialRequest(nil), plan.MaterialRequests...)
	smallCluster.MaterialRequests[0].SummaryBlockIDs = []string{"block-1"}
	smallCluster.MaterialRequests[0].EvidenceIDs = []string{"evs:gen-1:one"}
	smallMaterial := ClusterMaterial{
		ContractVersion: MaterialContractVersion, ClusterKey: plan.ClusterKey, SourceVideoIDs: []string{"video-1"},
		SummaryBlocks: material.SummaryBlocks[:1], Evidence: material.Evidence[:1],
	}
	smallPrompt, err := generator.buildPrompt(smallCluster, smallMaterial)
	if err != nil {
		t.Fatal(err)
	}
	smallTokens, err := estimateTokens(smallPrompt)
	if err != nil {
		t.Fatal(err)
	}
	secondCluster := plan
	secondCluster.MaterialRequests = append([]MaterialRequest(nil), plan.MaterialRequests...)
	secondCluster.MaterialRequests[0].SummaryBlockIDs = []string{"block-2"}
	secondCluster.MaterialRequests[0].EvidenceIDs = []string{"evs:gen-1:two"}
	secondMaterial := ClusterMaterial{
		ContractVersion: MaterialContractVersion, ClusterKey: plan.ClusterKey, SourceVideoIDs: []string{"video-1"},
		SummaryBlocks: material.SummaryBlocks[1:], Evidence: material.Evidence[1:],
	}
	secondPrompt, err := generator.buildPrompt(secondCluster, secondMaterial)
	if err != nil {
		t.Fatal(err)
	}
	secondTokens, err := estimateTokens(secondPrompt)
	if err != nil {
		t.Fatal(err)
	}
	limit := smallTokens
	if secondTokens > limit {
		limit = secondTokens
	}
	parts, err := (&ClusterGenerator{MaxInputTokens: limit + 20}).SplitMaterial(plan, material)
	if err != nil {
		t.Fatalf("SplitMaterial returned error: %v", err)
	}
	if len(parts) < 2 {
		t.Fatalf("expected material to split, parts=%d", len(parts))
	}
	for _, part := range parts {
		if err := part.Material.ValidateAgainst(part.Cluster); err != nil {
			t.Fatalf("split part is not independently valid: %v", err)
		}
	}
	blockCount := 0
	evidenceCount := 0
	for _, part := range parts {
		blockCount += len(part.Material.SummaryBlocks)
		evidenceCount += len(part.Material.Evidence)
	}
	if blockCount != len(material.SummaryBlocks) || evidenceCount != len(material.Evidence) {
		t.Fatalf("split lost or duplicated complete material boundaries: blocks=%d/%d evidence=%d/%d", blockCount, len(material.SummaryBlocks), evidenceCount, len(material.Evidence))
	}
}

type truncatedClusterLLM struct {
	calls int
}

func (f *truncatedClusterLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	if strings.Contains(prompt, "evs:gen-1:one") && strings.Contains(prompt, "evs:gen-1:two") {
		return `{"stages":[`, nil
	}
	evidenceID := "evs:gen-1:one"
	if strings.Contains(prompt, "evs:gen-1:two") {
		evidenceID = "evs:gen-1:two"
	}
	return `{"stages":[{"title":"基础","summary":"摘要","units":[{"learning_title":"单元","learner_question":"问题？","learning_outcome":"结果","evidence_refs":[{"video_id":"video-1","evidence_id":"` + evidenceID + `"}],"confidence":0.8}]}]}`, nil
}

func (*truncatedClusterLLM) Model() string         { return "cluster-model" }
func (*truncatedClusterLLM) PromptVersion() string { return "cluster-generation-v1" }

func TestStageFourClusterGenerationShrinksAfterTruncatedJSON(t *testing.T) {
	plan, material := validClusterGenerationInput()
	plan.MaterialRequests[0].SummaryBlockIDs = []string{"block-1", "block-2"}
	plan.MaterialRequests[0].EvidenceIDs = []string{"evs:gen-1:one", "evs:gen-1:two"}
	material.SummaryBlocks = append(material.SummaryBlocks, MaterialBlock{
		VideoID: "video-1", BlockID: "block-2", Text: "第二个完整总结 block。",
	})
	material.Evidence = append(material.Evidence, MaterialEvidence{
		VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "evs:gen-1:two",
		StartMs: 300, EndMs: 400, Text: "第二条完整证据。",
	})
	llm := &truncatedClusterLLM{}
	generator := &ClusterGenerator{LLM: llm}
	orchestrator := &StageFourOrchestrator{
		ClusterGenerator:  generator,
		MaxRecursionDepth: 2,
	}

	drafts, err := orchestrator.generateClusterParts(context.Background(), plan, ClusterGenerationPart{Cluster: plan, Material: material}, 0)
	if err != nil {
		t.Fatalf("generateClusterParts returned error: %v", err)
	}
	if len(drafts) != 2 || llm.calls != 3 {
		t.Fatalf("expected one truncated request and two complete fragments, calls=%d drafts=%#v", llm.calls, drafts)
	}
}

func TestMergeClusterGenerationDraftsKeepsProgramOwnedIdentity(t *testing.T) {
	plan, _ := validClusterGenerationInput()
	draft := ClusterGenerationDraft{
		ContractVersion: ClusterGenerationContractVersion, ClusterKey: plan.ClusterKey, PrimaryTemplate: plan.PrimaryTemplate,
		Stages: []ClusterGenerationStage{{Title: "基础", Summary: "摘要", Units: []ClusterGenerationUnit{{LearningTitle: "单元", LearnerQuestion: "问题", LearningOutcome: "结果"}}}},
	}
	merged, err := mergeClusterGenerationDrafts(plan, []ClusterGenerationDraft{draft, draft})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Stages) != 1 || len(merged.Stages[0].Units) != 2 || merged.ClusterKey != plan.ClusterKey {
		t.Fatalf("unexpected merged draft: %#v", merged)
	}
}

func TestClusterGenerationDraftDoesNotExposeModelEnvelopeFields(t *testing.T) {
	plan, material := validClusterGenerationInput()
	llm := &clusterGeneratorLLM{output: `{"stages":[{"title":"基础","summary":"摘要","units":[{"learning_title":"单元","learner_question":"问题？","learning_outcome":"结果","evidence_refs":[{"video_id":"video-1","evidence_id":"evs:gen-1:one"}],"confidence":0.8}]}]}`}
	draft, err := (&ClusterGenerator{LLM: llm}).Generate(context.Background(), plan, material)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "model-owned-id") {
		t.Fatalf("model-owned identifier leaked into draft: %s", raw)
	}
}

func TestAssembleClusterGenerationDraftAssignsProgramOwnedIDsAndSequences(t *testing.T) {
	plan, material := validClusterGenerationInput()
	llm := &clusterGeneratorLLM{output: `{"stages":[{"title":"基础","summary":"摘要","units":[{"learning_title":"单元","learner_question":"问题？","learning_outcome":"结果","evidence_refs":[{"video_id":"video-1","evidence_id":"evs:gen-1:one"}],"confidence":0.8}]}]}`}
	draft, err := (&ClusterGenerator{LLM: llm}).Generate(context.Background(), plan, material)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:catalog",
		Videos: []CatalogVideo{{VideoID: "video-1", Title: "培训视频", TranscriptGeneration: "gen-1"}},
	}
	cluster, err := AssembleClusterGenerationDraft(snapshot, plan, draft, material)
	if err != nil {
		t.Fatalf("AssembleClusterGenerationDraft returned error: %v", err)
	}
	if cluster.ClusterID != "cluster-1" || cluster.Path.PathID != "path-cluster-1" || cluster.Path.Stages[0].StageID != "stage-cluster-1-001" {
		t.Fatalf("program IDs were not assigned deterministically: %#v", cluster)
	}
	unit := cluster.Path.Stages[0].Units[0]
	if unit.UnitID != "unit-cluster-1-001-001" || unit.Sequence != 1 || unit.ReviewStatus != "passed" {
		t.Fatalf("program unit fields were not assigned: %#v", unit)
	}
	if cluster.MemberTopics[0].TopicID != "topic-cluster-1-001" || cluster.MemberTopics[0].Title != "培训视频" {
		t.Fatalf("member topic identity was not assigned: %#v", cluster.MemberTopics)
	}
}
