package trainingorchestration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	summarycontract "github.com/Tencent/WeKnora/internal/custom/service/summary"
)

type localContractRegressionCase struct {
	name string
	run  func(*testing.T)
}

// TestLocalContractRegression100 is a deterministic, offline contract corpus.
// It deliberately exercises the public generation and validation boundaries
// with fixed fixtures; it never calls a provider or writes acceptance data.
func TestLocalContractRegression100(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	cases := make([]localContractRegressionCase, 0, 100)
	cases = append(cases, generatorContractCases(input)...)
	cases = append(cases, planningContractCases()...)
	cases = append(cases, materialContractCases()...)
	cases = append(cases, summaryContractCases()...)
	if len(cases) != 100 {
		t.Fatalf("contract regression corpus contains %d cases, want 100", len(cases))
	}
	for _, testCase := range cases {
		t.Run(testCase.name, testCase.run)
	}
}

func generatorContractCases(input InputPackage) []localContractRegressionCase {
	valid := validModelOutput(input)
	validRaw := mustMarshalRegression(valid)
	validWithRelation := validModelOutputWithRelation(input)

	withModel := func(name, raw, want string) localContractRegressionCase {
		return localContractRegressionCase{
			name: "generator/" + name,
			run: func(t *testing.T) {
				t.Helper()
				llm := &fakeCompletionClient{output: raw}
				_, err := (&Generator{
					LLM:           llm,
					PromptVersion: "training-v1",
					Now:           func() time.Time { return time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC) },
				}).Generate(t.Context(), input)
				if want == "" {
					if err != nil {
						t.Fatalf("valid output rejected: %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
					t.Fatalf("error=%v, want substring %q", err, want)
				}
			},
		}
	}

	mutateModel := func(name string, mutate func(*modelProjection), want string) localContractRegressionCase {
		output := cloneRegressionValueNoT(valid)
		mutate(&output)
		return withModel(name, mustMarshalRegression(output), want)
	}

	cases := []localContractRegressionCase{
		withModel("valid-json", validRaw, ""),
		withModel("think-wrapper", "<think>reasoning</think>\n"+validRaw, ""),
		withModel("final-wrapper", "<think>reasoning</think>\n<final>\n```json\n"+validRaw+"\n```\n</final>", ""),
		withModel("unknown-markup-wrapper", "<html>"+validRaw+"</html>", "invalid character"),
		withModel("empty-output", "", "decode training orchestration model output"),
		withModel("whitespace-output", " \n\t ", "decode training orchestration model output"),
		withModel("null-root", "null", "topic arrays"),
		withModel("array-root", "[]", "cannot unmarshal"),
		withModel("opening-brace-only", "{", "unexpected EOF"),
		withModel("truncated-array", `{"topic_clusters":[`, "unexpected EOF"),
		withModel("unknown-root-field", `{"unexpected":true,"topic_clusters":[],"topic_cluster_relations":[]}`, "unknown field"),
		withModel("trailing-text", validRaw+"\ntrailing", "trailing content"),
		withModel("trailing-json-value", validRaw+"{}", "trailing content"),
		withModel("null-topic-array", `{"topic_clusters":null,"topic_cluster_relations":[]}`, "topic arrays"),
		withModel("null-relation-array", `{"topic_clusters":[],"topic_cluster_relations":null}`, "topic arrays"),
		mutateModel("empty-cluster-id", func(output *modelProjection) {
			output.TopicClusters[0].ClusterID = ""
		}, "required cluster fields"),
		mutateModel("duplicate-cluster-id", func(output *modelProjection) {
			output.TopicClusters = append(output.TopicClusters, output.TopicClusters[0])
		}, "duplicate projection id"),
		mutateModel("empty-path-id", func(output *modelProjection) {
			output.TopicClusters[0].Path.PathID = ""
		}, "cluster collections must not be empty"),
		mutateModel("empty-unit-id", func(output *modelProjection) {
			output.TopicClusters[0].Path.Stages[0].Units[0].UnitID = ""
		}, "learning unit is invalid"),
		mutateModel("invalid-unit-sequence", func(output *modelProjection) {
			output.TopicClusters[0].Path.Stages[0].Units[0].Sequence = 0
		}, "learning unit is invalid"),
		mutateModel("invalid-confidence", func(output *modelProjection) {
			output.TopicClusters[0].Path.Stages[0].Units[0].Confidence = 1.1
		}, "learning unit is invalid"),
		mutateModel("unsupported-template", func(output *modelProjection) {
			output.TopicClusters[0].Path.PrimaryTemplate = "unsupported"
		}, "learning content type and path template must match"),
		mutateModel("unknown-evidence", func(output *modelProjection) {
			output.TopicClusters[0].EvidenceRefs[0].EvidenceID = "invented-evidence"
		}, "outside the current input whitelist"),
		withModel("unknown-relation-target", mustMarshalRegression(func() modelProjection {
			output := cloneRegressionValueNoT(validWithRelation)
			output.TopicClusterRelations[0].TargetClusterID = "cluster-unknown"
			return output
		}()), "target cluster does not exist"),
		withModel("required-before-cycle", mustMarshalRegression(func() modelProjection {
			output := cloneRegressionValueNoT(validWithRelation)
			output.TopicClusterRelations[0].RelationType = "required_before"
			output.TopicClusterRelations = append(output.TopicClusterRelations, TopicClusterRelation{
				RelationID:          "cluster-relation-002",
				SourceClusterID:     "cluster-002",
				TargetClusterID:     "cluster-001",
				RelationType:        "required_before",
				Summary:             "反向顺序",
				SourceKnowledgeRefs: []string{}, TargetKnowledgeRefs: []string{},
				SourceEvidenceRefs: output.TopicClusters[1].EvidenceRefs,
				TargetEvidenceRefs: output.TopicClusters[0].EvidenceRefs,
				Confidence:         0.8,
				ReviewStatus:       "passed",
			})
			return output
		}()), "cycle"),
	}
	return cases
}

func planningContractCases() []localContractRegressionCase {
	snapshot := CatalogSnapshot{
		ContractVersion:   PlanningContractVersion,
		OwnerScopeID:      "scope",
		SourceFingerprint: "sha256:test",
		Videos:            []CatalogVideo{validCatalogVideo("video-1")},
	}
	base := validAcceptedPlan(snapshot)

	withPlan := func(name string, mutateSnapshot func(*CatalogSnapshot), mutatePlan func(*PlanDraft), want string) localContractRegressionCase {
		return localContractRegressionCase{
			name: "planning/" + name,
			run: func(t *testing.T) {
				t.Helper()
				currentSnapshot := cloneRegressionValue(t, snapshot)
				currentPlan := cloneRegressionValue(t, base)
				if mutateSnapshot != nil {
					mutateSnapshot(&currentSnapshot)
				}
				if mutatePlan != nil {
					mutatePlan(&currentPlan)
				}
				err := currentPlan.ValidateAgainst(currentSnapshot)
				if want == "" {
					if err != nil {
						t.Fatalf("valid plan rejected: %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
					t.Fatalf("error=%v, want substring %q", err, want)
				}
			},
		}
	}

	return []localContractRegressionCase{
		withPlan("valid", nil, nil, ""),
		withPlan("wrong-plan-contract", nil, func(plan *PlanDraft) { plan.ContractVersion = "wrong" }, "unsupported plan contract"),
		withPlan("wrong-catalog-contract", func(snapshot *CatalogSnapshot) { snapshot.ContractVersion = "wrong" }, nil, "unsupported catalog contract"),
		withPlan("empty-fingerprint", nil, func(plan *PlanDraft) { plan.SourceFingerprint = "" }, "source fingerprint"),
		withPlan("mismatched-fingerprint", nil, func(plan *PlanDraft) { plan.SourceFingerprint = "sha256:other" }, "source fingerprint"),
		withPlan("empty-cluster-key", nil, func(plan *PlanDraft) { plan.TopicClusters[0].ClusterKey = "" }, "identity"),
		withPlan("empty-cluster-title", nil, func(plan *PlanDraft) { plan.TopicClusters[0].Title = "" }, "identity"),
		withPlan("empty-learning-goal", nil, func(plan *PlanDraft) { plan.TopicClusters[0].LearningGoal = "" }, "learning goal"),
		withPlan("unsupported-template", nil, func(plan *PlanDraft) { plan.TopicClusters[0].PrimaryTemplate = "unsupported" }, "unsupported template"),
		withPlan("confidence-below-zero", nil, func(plan *PlanDraft) { plan.TopicClusters[0].Confidence = -0.1 }, "confidence"),
		withPlan("confidence-above-one", nil, func(plan *PlanDraft) { plan.TopicClusters[0].Confidence = 1.1 }, "confidence"),
		withPlan("unsupported-review-status", nil, func(plan *PlanDraft) { plan.TopicClusters[0].ReviewStatus = "unknown" }, "review status"),
		withPlan("abstained-with-source", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].ReviewStatus = PlanAbstained
		}, "cannot carry source"),
		withPlan("rejected-with-material", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].ReviewStatus = PlanRejected
		}, "cannot carry source"),
		withPlan("candidate-without-source", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].SourceVideoIDs = nil
			plan.TopicClusters[0].MaterialRequests = nil
		}, "no source videos"),
		withPlan("source-video-outside-catalog", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].SourceVideoIDs[0] = "video-unknown"
		}, "outside catalog"),
		withPlan("duplicate-source-video", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].SourceVideoIDs = append(plan.TopicClusters[0].SourceVideoIDs, "video-1")
		}, "repeats source video"),
		withPlan("material-count-mismatch", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].MaterialRequests = nil
		}, "exactly one material request"),
		withPlan("material-video-outside-catalog", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].MaterialRequests[0].VideoID = "video-unknown"
		}, "outside catalog"),
		withPlan("duplicate-material-request", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].MaterialRequests = append(plan.TopicClusters[0].MaterialRequests, plan.TopicClusters[0].MaterialRequests[0])
		}, "exactly one material request"),
		withPlan("generation-mismatch", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].MaterialRequests[0].TranscriptGeneration = "generation-other"
		}, "does not match catalog version"),
		withPlan("empty-evidence", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].MaterialRequests[0].EvidenceIDs = nil
		}, "is empty"),
		withPlan("evidence-outside-profile", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].MaterialRequests[0].EvidenceIDs = []string{"outside-evidence"}
		}, "outside orchestration profile"),
		withPlan("chunk-id-instead-of-sentence-id", nil, func(plan *PlanDraft) {
			plan.TopicClusters[0].MaterialRequests[0].EvidenceIDs = []string{"chunk-knowledge-1"}
		}, "outside orchestration profile"),
		withPlan("selected-and-unselected-overlap", nil, func(plan *PlanDraft) {
			plan.UnselectedVideos = []UnselectedVideo{{VideoID: "video-1", Reason: "重复"}}
		}, "both selected and unselected"),
	}
}

func materialContractCases() []localContractRegressionCase {
	cluster, material := localRegressionClusterMaterial()

	withMaterial := func(name string, mutateCluster func(*PlanCluster), mutateMaterial func(*ClusterMaterial), want string) localContractRegressionCase {
		return localContractRegressionCase{
			name: "material/" + name,
			run: func(t *testing.T) {
				t.Helper()
				currentCluster := cloneRegressionValue(t, cluster)
				currentMaterial := cloneRegressionValue(t, material)
				if mutateCluster != nil {
					mutateCluster(&currentCluster)
				}
				if mutateMaterial != nil {
					mutateMaterial(&currentMaterial)
				}
				err := currentMaterial.ValidateAgainst(currentCluster)
				if want == "" {
					if err != nil {
						t.Fatalf("valid material rejected: %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
					t.Fatalf("error=%v, want substring %q", err, want)
				}
			},
		}
	}

	return []localContractRegressionCase{
		withMaterial("valid", nil, nil, ""),
		withMaterial("wrong-contract", nil, func(material *ClusterMaterial) { material.ContractVersion = "wrong" }, "unsupported material contract"),
		withMaterial("wrong-cluster-key", nil, func(material *ClusterMaterial) { material.ClusterKey = "wrong" }, "key does not match"),
		withMaterial("cluster-not-accepted", func(cluster *PlanCluster) { cluster.ReviewStatus = PlanCandidate }, nil, "requires an accepted"),
		withMaterial("source-video-count-mismatch", nil, func(material *ClusterMaterial) { material.SourceVideoIDs = nil }, "source video count"),
		withMaterial("source-video-outside-plan", nil, func(material *ClusterMaterial) { material.SourceVideoIDs = []string{"video-2"} }, "outside plan"),
		withMaterial("duplicate-source-video", nil, func(material *ClusterMaterial) { material.SourceVideoIDs = []string{"video-1", "video-1"} }, "source video count"),
		withMaterial("no-evidence", nil, func(material *ClusterMaterial) { material.Evidence = nil }, "must contain evidence"),
		withMaterial("no-summary-blocks", nil, func(material *ClusterMaterial) { material.SummaryBlocks = nil }, "must contain requested summary blocks"),
		withMaterial("summary-block-empty-video", nil, func(material *ClusterMaterial) { material.SummaryBlocks[0].VideoID = "" }, "summary block 1 is incomplete"),
		withMaterial("summary-block-empty-id", nil, func(material *ClusterMaterial) { material.SummaryBlocks[0].BlockID = "" }, "summary block 1 is incomplete"),
		withMaterial("summary-block-empty-text", nil, func(material *ClusterMaterial) { material.SummaryBlocks[0].Text = "" }, "summary block 1 is incomplete"),
		withMaterial("summary-block-outside-plan", nil, func(material *ClusterMaterial) { material.SummaryBlocks[0].BlockID = "outside-block" }, "outside plan"),
		withMaterial("duplicate-summary-block", nil, func(material *ClusterMaterial) {
			material.SummaryBlocks = append(material.SummaryBlocks, material.SummaryBlocks[0])
		}, "repeats summary block"),
		withMaterial("missing-summary-block", nil, func(material *ClusterMaterial) { material.SummaryBlocks = nil }, "must contain requested summary blocks"),
		withMaterial("evidence-empty-video", nil, func(material *ClusterMaterial) { material.Evidence[0].VideoID = "" }, "evidence 1 is incomplete"),
		withMaterial("evidence-empty-id", nil, func(material *ClusterMaterial) { material.Evidence[0].EvidenceID = "" }, "evidence 1 is incomplete"),
		withMaterial("evidence-empty-text", nil, func(material *ClusterMaterial) { material.Evidence[0].Text = "" }, "evidence 1 is incomplete"),
		withMaterial("evidence-negative-start", nil, func(material *ClusterMaterial) { material.Evidence[0].StartMs = -1 }, "evidence 1 is incomplete"),
		withMaterial("evidence-invalid-range", nil, func(material *ClusterMaterial) { material.Evidence[0].EndMs = material.Evidence[0].StartMs }, "evidence 1 is incomplete"),
		withMaterial("evidence-empty-generation", nil, func(material *ClusterMaterial) { material.Evidence[0].TranscriptGeneration = "" }, "evidence 1 is incomplete"),
		withMaterial("evidence-outside-plan", nil, func(material *ClusterMaterial) { material.Evidence[0].EvidenceID = "outside-evidence" }, "outside plan"),
		withMaterial("evidence-generation-mismatch", nil, func(material *ClusterMaterial) { material.Evidence[0].TranscriptGeneration = "generation-other" }, "outside plan"),
		withMaterial("duplicate-evidence", nil, func(material *ClusterMaterial) { material.Evidence = append(material.Evidence, material.Evidence[0]) }, "repeats evidence"),
		withMaterial("missing-evidence", nil, func(material *ClusterMaterial) { material.Evidence = nil }, "must contain evidence"),
	}
}

func summaryContractCases() []localContractRegressionCase {
	base, known := localRegressionSummary()

	withSummary := func(name string, mutate func(*summarycontract.Document, map[string]struct{}), want string) localContractRegressionCase {
		return localContractRegressionCase{
			name: "summary/" + name,
			run: func(t *testing.T) {
				t.Helper()
				document := cloneRegressionValue(t, base)
				currentKnown := cloneRegressionValue(t, known)
				if mutate != nil {
					mutate(&document, currentKnown)
				}
				err := summarycontract.ValidateGenerated(document, "training", currentKnown)
				if want == "" {
					if err != nil {
						t.Fatalf("valid summary rejected: %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
					t.Fatalf("error=%v, want substring %q", err, want)
				}
			},
		}
	}

	return []localContractRegressionCase{
		withSummary("valid", nil, ""),
		withSummary("legacy-schema", func(document *summarycontract.Document, _ map[string]struct{}) { document.SchemaVersion = 1 }, "schema version"),
		withSummary("unsupported-video-type", func(document *summarycontract.Document, _ map[string]struct{}) { document.VideoType = "unknown" }, "unsupported video type"),
		withSummary("expected-video-type-mismatch", func(document *summarycontract.Document, _ map[string]struct{}) { document.VideoType = "general" }, "video type"),
		withSummary("missing-classification", func(document *summarycontract.Document, _ map[string]struct{}) { document.Classification = nil }, "classification"),
		withSummary("classification-confidence-low", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.Classification.Confidence = -0.1
		}, "confidence"),
		withSummary("classification-confidence-high", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.Classification.Confidence = 1.1
		}, "confidence"),
		withSummary("classification-unknown-evidence", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.Classification.EvidenceChunkIDs = []string{"unknown"}
		}, "unknown evidence"),
		withSummary("missing-sections", func(document *summarycontract.Document, _ map[string]struct{}) { document.Sections = nil }, "section"),
		withSummary("wrong-section-id", func(document *summarycontract.Document, _ map[string]struct{}) { document.Sections[0].ID = "wrong" }, "section"),
		withSummary("wrong-section-title", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.Sections[0].Title = "错误标题"
		}, "section"),
		withSummary("duplicate-section-id", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.Sections[1].ID = document.Sections[0].ID
		}, "must be"),
		withSummary("empty-block-id", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.Sections[0].Blocks[0].ID = ""
		}, "block"),
		withSummary("unsupported-block-kind", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.Sections[0].Blocks[0].Kind = "unsupported"
		}, "block"),
		withSummary("empty-block-text", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.Sections[0].Blocks[0].Text = ""
		}, "incomplete"),
		withSummary("unknown-block-evidence", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.Sections[0].Blocks[0].EvidenceChunkIDs = []string{"unknown"}
		}, "unknown evidence"),
		withSummary("missing-orchestration-profile", func(document *summarycontract.Document, _ map[string]struct{}) { document.OrchestrationProfile = nil }, "orchestration profile"),
		withSummary("profile-wrong-schema", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.OrchestrationProfile.SchemaVersion = 99
		}, "orchestration profile schema"),
		withSummary("profile-empty-primary-topic", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.OrchestrationProfile.PrimaryTopic = ""
		}, "primary topic"),
		withSummary("profile-no-topic-units", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.OrchestrationProfile.TopicUnits = nil
		}, "topic unit count"),
		withSummary("profile-duplicate-title", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.OrchestrationProfile.TopicUnits = append(document.OrchestrationProfile.TopicUnits, document.OrchestrationProfile.TopicUnits[0])
		}, "duplicates title"),
		withSummary("profile-unsupported-content-form", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.OrchestrationProfile.TopicUnits[0].ContentForms = []string{"unsupported"}
		}, "unsupported content form"),
		withSummary("profile-duplicate-learning-outcome", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.OrchestrationProfile.TopicUnits[0].LearningOutcomes = []string{"结果", "结果"}
		}, "duplicates learning outcome"),
		withSummary("profile-unknown-summary-block", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.OrchestrationProfile.TopicUnits[0].SummaryBlockIDs = []string{"unknown-block"}
		}, "unknown summary block"),
		withSummary("profile-evidence-not-owned-by-block", func(document *summarycontract.Document, _ map[string]struct{}) {
			document.OrchestrationProfile.TopicUnits[0].EvidenceChunkIDs = []string{"chunk-2"}
		}, "not referenced by its summary blocks"),
	}
}

func localRegressionClusterMaterial() (PlanCluster, ClusterMaterial) {
	cluster := PlanCluster{
		ClusterKey:      "cluster-1",
		Title:           "主题",
		Summary:         "摘要",
		LearningGoal:    "目标",
		PrimaryTemplate: "concept_cognition",
		Scope:           "范围",
		SourceVideoIDs:  []string{"video-1"},
		ReviewStatus:    PlanAccepted,
		MaterialRequests: []MaterialRequest{{
			VideoID:              "video-1",
			SummaryWikiPageID:    "page-1",
			SummaryVersion:       3,
			TranscriptGeneration: "gen-1",
			SummaryBlockIDs:      []string{"block-1"},
			EvidenceIDs:          []string{"evs:gen-1:one"},
		}},
	}
	material := ClusterMaterial{
		ContractVersion: MaterialContractVersion,
		ClusterKey:      "cluster-1",
		SourceVideoIDs:  []string{"video-1"},
		SummaryBlocks:   []MaterialBlock{{VideoID: "video-1", BlockID: "block-1", Text: "正文"}},
		Evidence: []MaterialEvidence{{
			VideoID:              "video-1",
			TranscriptGeneration: "gen-1",
			EvidenceID:           "evs:gen-1:one",
			StartMs:              100,
			EndMs:                200,
			Text:                 "证据",
		}},
	}
	return cluster, material
}

func localRegressionSummary() (summarycontract.Document, map[string]struct{}) {
	framework, ok := summarycontract.Framework("training")
	if !ok {
		panic("training summary framework missing")
	}
	document := summarycontract.Document{
		SchemaVersion: summarycontract.SchemaVersion,
		VideoType:     "training",
		Classification: &summarycontract.Classification{
			Confidence:       0.9,
			Reason:           "固定契约回归夹具",
			EvidenceChunkIDs: []string{"chunk-1"},
		},
		Sections: make([]summarycontract.Section, 0, len(framework)),
		OrchestrationProfile: &summarycontract.OrchestrationProfile{
			SchemaVersion: summarycontract.OrchestrationProfileSchemaVersion,
			PrimaryTopic:  "固定主题",
			TopicUnits: []summarycontract.OrchestrationTopicUnit{{
				Title:            "固定单元",
				Abstract:         "固定摘要",
				ContentForms:     []string{"concept_cognition"},
				LearningOutcomes: []string{"结果"},
				SummaryBlockIDs:  []string{"contract-block-1"},
				EvidenceChunkIDs: []string{"chunk-1"},
			}},
		},
	}
	for index, section := range framework {
		document.Sections = append(document.Sections, summarycontract.Section{
			ID:    section.ID,
			Title: section.Title,
			Blocks: []summarycontract.Block{{
				ID:               fmt.Sprintf("contract-block-%d", index+1),
				Kind:             summarycontract.BlockKindParagraph,
				Text:             "固定正文",
				EvidenceChunkIDs: []string{"chunk-1"},
			}},
		})
	}
	return document, map[string]struct{}{"chunk-1": {}}
}

func cloneRegressionValue[T any](t *testing.T, value T) T {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var clone T
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return clone
}

func cloneRegressionValueNoT[T any](value T) T {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var clone T
	if err := json.Unmarshal(raw, &clone); err != nil {
		panic(err)
	}
	return clone
}

func mustMarshalRegression(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
