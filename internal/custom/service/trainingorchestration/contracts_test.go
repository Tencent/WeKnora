package trainingorchestration

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/service/summary"
)

func TestPlanDraftValidatesCatalogClosure(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: "sha256:test",
		Videos:            []CatalogVideo{validCatalogVideo("video-1")},
	}
	plan := PlanDraft{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: snapshot.SourceFingerprint,
		TopicClusters: []PlanCluster{{
			ClusterKey: "cluster-key-1", Title: "基础认知", LearningGoal: "理解核心概念", PrimaryTemplate: "concept_cognition", ReviewStatus: PlanAccepted,
			SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
				VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
				SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
			}},
		}},
	}
	if err := plan.ValidateAgainst(snapshot); err != nil {
		t.Fatalf("ValidateAgainst returned error: %v", err)
	}

	plan.TopicClusters[0].MaterialRequests[0].VideoID = "video-2"
	if err := plan.ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted a material request outside the catalog")
	}

	plan.TopicClusters[0].MaterialRequests[0].VideoID = "video-1"
	snapshot.ContractVersion = "training-orchestration/planning/v2"
	if err := plan.ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted an unsupported catalog contract version")
	}
}

func validCatalogVideo(videoID string) CatalogVideo {
	return CatalogVideo{
		VideoID: videoID, SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
		OrchestrationProfile: &summary.OrchestrationProfile{
			SchemaVersion: 1, PrimaryTopic: "主题", TopicUnits: []summary.OrchestrationTopicUnit{{
				Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, LearningOutcomes: []string{"结果"},
				SummaryBlockIDs: []string{"block-1"}, EvidenceChunkIDs: []string{"chunk-knowledge-1"},
				EvidenceRefs: []summary.EvidenceRef{{
					ChunkID:            "chunk-knowledge-1",
					EvidenceSentenceID: "evs:gen-1:one",
					StartMs:            100,
					EndMs:              200,
				}},
			}},
		},
	}
}

func TestPlanDraftRejectsUnsupportedStatusAndTemplate(t *testing.T) {
	snapshot := CatalogSnapshot{SourceFingerprint: "sha256:test", Videos: []CatalogVideo{{VideoID: "video-1"}}}
	plan := PlanDraft{ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint, TopicClusters: []PlanCluster{{
		ClusterKey: "cluster-1", Title: "主题", LearningGoal: "目标", PrimaryTemplate: "unknown", ReviewStatus: "needs_review", SourceVideoIDs: []string{"video-1"},
	}}}
	if err := plan.ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted unsupported template or status")
	}
}

func TestPlanDraftRejectsDuplicateCatalogAndUnselectedEntries(t *testing.T) {
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{validCatalogVideo("video-1"), validCatalogVideo("video-1")}}
	plan := validAcceptedPlan(snapshot)
	if err := plan.ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted duplicate catalog video")
	}

	snapshot.Videos = []CatalogVideo{validCatalogVideo("video-1"), validCatalogVideo("video-2")}
	plan = validAcceptedPlan(snapshot)
	plan.UnselectedVideos = []UnselectedVideo{{VideoID: "video-2", Reason: "证据不足"}, {VideoID: "video-2", Reason: "重复"}}
	if err := plan.ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted duplicate unselected video")
	}
}

func TestCatalogFingerprintCanonicalizesEmptyCollections(t *testing.T) {
	video := validCatalogVideo("video-1")
	withEmptySlice := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope",
		Videos:          []CatalogVideo{video},
		SkippedVideos:   []SkippedVideo{},
	}
	withNilSlice := withEmptySlice
	withNilSlice.SkippedVideos = nil

	left, err := CatalogFingerprint(withEmptySlice, "model", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	right, err := CatalogFingerprint(withNilSlice, "model", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("catalog fingerprint changed with empty-vs-nil skipped videos: empty=%s nil=%s", left, right)
	}
}

func TestCatalogFingerprintCanonicalizesVideoOrder(t *testing.T) {
	first := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope",
		Videos:          []CatalogVideo{validCatalogVideo("video-2"), validCatalogVideo("video-1")},
	}
	second := first
	second.Videos = []CatalogVideo{first.Videos[1], first.Videos[0]}

	left, err := CatalogFingerprint(first, "model", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	right, err := CatalogFingerprint(second, "model", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("catalog fingerprint changed with video order: first=%s second=%s", left, right)
	}
}

func TestPlanDraftRejectsDuplicatePersistentClusterKeys(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion:   PlanningContractVersion,
		SourceFingerprint: "sha256:test",
		Videos:            []CatalogVideo{validCatalogVideo("video-1"), validCatalogVideo("video-2")},
	}
	cluster := func(videoID string) PlanCluster {
		return PlanCluster{
			ClusterKey: "cluster-001", Title: "主题 " + videoID, LearningGoal: "目标",
			PrimaryTemplate: "concept_cognition", ReviewStatus: PlanCandidate,
			SourceVideoIDs: []string{videoID},
			MaterialRequests: []MaterialRequest{{
				VideoID:              videoID,
				SummaryWikiPageID:    "page-1",
				SummaryVersion:       3,
				TranscriptGeneration: "gen-1",
				SummaryBlockIDs:      []string{"block-1"},
				EvidenceIDs:          []string{"evs:gen-1:one"},
			}},
		}
	}
	plan := PlanDraft{
		ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint,
		TopicClusters: []PlanCluster{cluster("video-1"), cluster("video-2")},
	}
	if err := plan.ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted duplicate persistent cluster keys")
	}
}

func TestPlanDraftRequiresProfileClosedMaterialRequests(t *testing.T) {
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{validCatalogVideo("video-1")}}
	plan := validAcceptedPlan(snapshot)
	plan.TopicClusters[0].MaterialRequests[0].SummaryBlockIDs = []string{"outside-block"}
	if err := plan.ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted summary block outside profile")
	}

	plan = validAcceptedPlan(snapshot)
	plan.TopicClusters[0].MaterialRequests = nil
	if err := plan.ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted missing material request")
	}
}

func TestPlanDraftRejectsChunkIDsWhenEvidenceRefsExist(t *testing.T) {
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{validCatalogVideo("video-1")}}
	plan := validAcceptedPlan(snapshot)
	plan.TopicClusters[0].MaterialRequests[0].EvidenceIDs = []string{"chunk-knowledge-1"}

	if err := plan.ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted chunk knowledge ID instead of evidence sentence ID")
	}

	plan.TopicClusters[0].MaterialRequests[0].EvidenceIDs = []string{"evs:gen-1:one"}
	if err := plan.ValidateAgainst(snapshot); err != nil {
		t.Fatalf("ValidateAgainst rejected evidence sentence ID: %v", err)
	}
}

func TestPlanDraftRejectsProfilesWithoutEvidenceSentenceRefs(t *testing.T) {
	video := validCatalogVideo("video-1")
	video.OrchestrationProfile.TopicUnits[0].EvidenceRefs = nil
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{video}}

	if err := validAcceptedPlan(snapshot).ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted evidence IDs without evidence sentence refs")
	}
}

func TestPlanDraftAcceptsExplicitCompatibilityProfile(t *testing.T) {
	video := validCatalogVideo("video-1")
	video.CompatibilityProfile = video.OrchestrationProfile
	video.OrchestrationProfile = nil
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{video}}
	if err := validAcceptedPlan(snapshot).ValidateAgainst(snapshot); err != nil {
		t.Fatalf("ValidateAgainst rejected compatibility profile: %v", err)
	}
}

func TestPlanDraftRejectsAbstainedOrRejectedMaterials(t *testing.T) {
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{validCatalogVideo("video-1")}}
	plan := validAcceptedPlan(snapshot)
	plan.TopicClusters[0].ReviewStatus = PlanAbstained
	if err := plan.ValidateAgainst(snapshot); err == nil {
		t.Fatal("ValidateAgainst accepted abstained cluster with materials")
	}
}

func TestClusterMaterialValidatesAgainstAcceptedPlan(t *testing.T) {
	plan := validAcceptedPlan(CatalogSnapshot{ContractVersion: PlanningContractVersion, SourceFingerprint: "sha256:test", Videos: []CatalogVideo{validCatalogVideo("video-1")}})
	cluster := plan.TopicClusters[0]
	material := ClusterMaterial{
		ContractVersion: MaterialContractVersion, ClusterKey: cluster.ClusterKey, SourceVideoIDs: []string{"video-1"},
		SummaryBlocks: []MaterialBlock{{VideoID: "video-1", BlockID: "block-1", Text: "正文"}},
		Evidence:      []MaterialEvidence{{VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "evs:gen-1:one", StartMs: 100, EndMs: 200, Text: "证据"}},
	}
	if err := material.ValidateAgainst(cluster); err != nil {
		t.Fatalf("ValidateAgainst returned error: %v", err)
	}
	material.Evidence[0].EvidenceID = "outside-evidence"
	if err := material.ValidateAgainst(cluster); err == nil {
		t.Fatal("ClusterMaterial accepted evidence outside plan")
	}
	material = ClusterMaterial{ContractVersion: MaterialContractVersion, ClusterKey: cluster.ClusterKey, SourceVideoIDs: []string{"video-1"}}
	if err := material.ValidateAgainst(cluster); err == nil {
		t.Fatal("ClusterMaterial accepted empty material")
	}
	material = ClusterMaterial{
		ContractVersion: MaterialContractVersion, ClusterKey: cluster.ClusterKey, SourceVideoIDs: []string{"video-1", "video-1"},
		SummaryBlocks: []MaterialBlock{{VideoID: "video-1", BlockID: "block-1", Text: "正文"}},
		Evidence:      []MaterialEvidence{{VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "evs:gen-1:one", StartMs: 100, EndMs: 200, Text: "证据"}},
	}
	if err := material.ValidateAgainst(cluster); err == nil {
		t.Fatal("ClusterMaterial accepted duplicate source video")
	}
}

func validAcceptedPlan(snapshot CatalogSnapshot) PlanDraft {
	return PlanDraft{
		ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint,
		TopicClusters: []PlanCluster{{
			ClusterKey: "cluster-1", Title: "主题", LearningGoal: "目标", PrimaryTemplate: "concept_cognition", ReviewStatus: PlanAccepted,
			SourceVideoIDs: []string{"video-1"}, MaterialRequests: []MaterialRequest{{
				VideoID: "video-1", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
				SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
			}},
		}},
	}
}
