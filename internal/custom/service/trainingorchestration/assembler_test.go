package trainingorchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

func TestProjectionAssemblerBuildsValidatedDocumentAndStatistics(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope-1",
		Videos: []CatalogVideo{
			func() CatalogVideo {
				video := validCatalogVideo("video-1")
				video.Title = "视频一"
				return video
			}(),
			func() CatalogVideo {
				video := validCatalogVideo("video-2")
				video.Title = "视频二"
				return video
			}(),
		},
		SkippedVideos: []SkippedVideo{{VideoID: "video-3", Reason: SkipInaccessible}},
	}
	plan := validAcceptedPlan(snapshot)
	plan.TopicClusters[0].Summary = "主题摘要"
	plan.UnselectedVideos = []UnselectedVideo{{VideoID: "video-2", Reason: "重复证据"}}
	plan.SourceFingerprint = testStageFourFingerprint(t, snapshot)
	material := validStageFourMaterial(plan.TopicClusters[0].ClusterKey)
	draft := validStageFourDraft(plan.TopicClusters[0].ClusterKey, material.Evidence[0])

	assembler := &ProjectionAssembler{
		Fingerprint: stageFourFingerprint("planner-model", "planning-v1"),
		Now:         func() time.Time { return time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC) },
	}
	doc, err := assembler.Assemble(StageFourAssemblyInput{
		InitialCatalog: snapshot,
		LatestCatalog:  snapshot,
		Plan:           plan,
		Materials:      []ClusterMaterial{material},
		Drafts:         []ClusterGenerationDraft{draft},
	})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}

	projection := doc.TrainingPathProjection
	if projection.GeneratedAt != "2026-09-10T01:02:03Z" || projection.OwnerScopeID != "scope-1" {
		t.Fatalf("unexpected projection identity: %#v", projection)
	}
	if len(projection.TopicClusters) != 1 || len(projection.TopicClusters[0].Path.Stages[0].Units) != 1 {
		t.Fatalf("unexpected assembled clusters: %#v", projection.TopicClusters)
	}
	stats := projection.Statistics
	if stats.SelectedVideos != 1 || stats.NotSelectedVideos != 1 || stats.SkippedVideos != 1 {
		t.Fatalf("selected/not-selected statistics are incorrect: %#v", stats)
	}
	if stats.LearningUnitCount != 1 || stats.LearningDurationSecs != 2 {
		t.Fatalf("learning statistics are incorrect: %#v", stats)
	}
	if stats.TopicClusterCount != 1 || stats.SkippedReasonCounts[SkipInaccessible] != 1 {
		t.Fatalf("aggregate statistics are incorrect: %#v", stats)
	}
}

func TestProjectionAssemblerCarriesRetrievalDegradationWarning(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope-1",
		Videos: []CatalogVideo{func() CatalogVideo {
			video := validCatalogVideo("video-1")
			video.Title = "视频一"
			return video
		}()},
	}
	plan := validAcceptedPlan(snapshot)
	plan.TopicClusters[0].Summary = "主题摘要"
	plan.SourceFingerprint = testStageFourFingerprint(t, snapshot)
	material := validStageFourMaterial(plan.TopicClusters[0].ClusterKey)
	material.RetrievalDegraded = true
	material.RetrievalDegradationReason = "search_unavailable"
	draft := validStageFourDraft(plan.TopicClusters[0].ClusterKey, material.Evidence[0])

	doc, err := (&ProjectionAssembler{
		Fingerprint: stageFourFingerprint("planner-model", "planning-v1"),
	}).Assemble(StageFourAssemblyInput{
		InitialCatalog: snapshot, LatestCatalog: snapshot, Plan: plan,
		Materials: []ClusterMaterial{material}, Drafts: []ClusterGenerationDraft{draft},
	})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}
	if !doc.TrainingPathProjection.RetrievalDegraded || doc.TrainingPathProjection.RetrievalDegradationReason != "search_unavailable" {
		t.Fatalf("retrieval degradation warning was not carried to projection: %#v", doc.TrainingPathProjection)
	}
}

func TestProjectionAssemblerRejectsSourceChangesBeforePublication(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope-1",
		Videos: []CatalogVideo{func() CatalogVideo {
			video := validCatalogVideo("video-1")
			video.Title = "视频一"
			return video
		}()},
	}
	plan := validAcceptedPlan(snapshot)
	plan.TopicClusters[0].Summary = "主题摘要"
	plan.SourceFingerprint = testStageFourFingerprint(t, snapshot)
	material := validStageFourMaterial(plan.TopicClusters[0].ClusterKey)
	draft := validStageFourDraft(plan.TopicClusters[0].ClusterKey, material.Evidence[0])
	latest := snapshot
	latest.Videos = append([]CatalogVideo(nil), snapshot.Videos...)
	latest.Videos[0].SummaryVersion++

	_, err := (&ProjectionAssembler{Fingerprint: stageFourFingerprint("planner-model", "planning-v1")}).Assemble(StageFourAssemblyInput{
		InitialCatalog: snapshot, LatestCatalog: latest, Plan: plan,
		Materials: []ClusterMaterial{material}, Drafts: []ClusterGenerationDraft{draft},
	})
	var generationErr *GenerationError
	if !errors.As(err, &generationErr) || generationErr.Code != "source_changed" {
		t.Fatalf("expected source_changed, got %v", err)
	}
}

func TestProjectionAssemblerRejectsRelationEvidenceOutsideEndpoint(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope-1",
		Videos: []CatalogVideo{
			func() CatalogVideo {
				video := validCatalogVideo("video-1")
				video.Title = "视频一"
				return video
			}(),
			func() CatalogVideo {
				video := validCatalogVideo("video-2")
				video.Title = "视频二"
				return video
			}(),
		},
	}
	plan := validAcceptedPlan(snapshot)
	plan.TopicClusters[0].ClusterKey = "cluster-1"
	plan.TopicClusters[0].MaterialRequests[0].VideoID = "video-1"
	plan.TopicClusters = append(plan.TopicClusters, PlanCluster{
		ClusterKey: "cluster-2", Title: "主题二", LearningGoal: "目标二", PrimaryTemplate: "skill_method",
		ReviewStatus: PlanAccepted, SourceVideoIDs: []string{"video-2"},
		MaterialRequests: []MaterialRequest{{
			VideoID: "video-2", SummaryWikiPageID: "page-1", SummaryVersion: 3, TranscriptGeneration: "gen-1",
			SummaryBlockIDs: []string{"block-1"}, EvidenceIDs: []string{"evs:gen-1:one"},
		}},
	})
	plan.UnselectedVideos = nil
	plan.TopicClusters[0].Summary = "主题摘要"
	plan.TopicClusters[1].Summary = "主题二摘要"
	plan.SourceFingerprint = testStageFourFingerprint(t, snapshot)
	materialOne := validStageFourMaterial("cluster-1")
	materialTwo := validStageFourMaterial("cluster-2")
	materialTwo.SourceVideoIDs = []string{"video-2"}
	materialTwo.SummaryBlocks[0].VideoID = "video-2"
	materialTwo.Evidence[0].VideoID = "video-2"
	materialTwo.Evidence[0].Text = "证据二"
	draftOne := validStageFourDraft("cluster-1", materialOne.Evidence[0])
	draftTwo := validStageFourDraft("cluster-2", materialTwo.Evidence[0])
	draftTwo.PrimaryTemplate = "skill_method"

	_, err := (&ProjectionAssembler{Fingerprint: stageFourFingerprint("planner-model", "planning-v1")}).Assemble(StageFourAssemblyInput{
		InitialCatalog: snapshot, LatestCatalog: snapshot, Plan: plan,
		Materials: []ClusterMaterial{materialOne, materialTwo},
		Drafts:    []ClusterGenerationDraft{draftOne, draftTwo},
		Relations: []TopicClusterRelation{{
			SourceClusterID: "cluster-1", TargetClusterID: "cluster-2", RelationType: "application",
			Summary: "应用", Confidence: 0.8, ReviewStatus: "passed",
			SourceEvidenceRefs: []EvidenceRef{materialTwoEvidence(materialTwo)},
			TargetEvidenceRefs: []EvidenceRef{materialTwoEvidence(materialTwo)},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "outside its endpoint cluster") {
		t.Fatalf("expected relation evidence closure rejection, got %v", err)
	}
}

func TestStageFourOrchestratorKeepsPlannerAndAssemblerOnInternalSeam(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope-1",
		Videos: []CatalogVideo{func() CatalogVideo {
			video := validCatalogVideo("video-1")
			video.Title = "视频一"
			return video
		}()},
	}
	plan := validAcceptedPlan(snapshot)
	plan.TopicClusters[0].Summary = "主题摘要"
	plan.SourceFingerprint = testStageFourFingerprint(t, snapshot)
	material := validStageFourMaterial(plan.TopicClusters[0].ClusterKey)
	draft := validStageFourDraft(plan.TopicClusters[0].ClusterKey, material.Evidence[0])
	assembler := &recordingStageFourAssembler{}
	orchestrator := &StageFourOrchestrator{
		Planner:           stageFourPlannerStub{plan: plan},
		Materializer:      stageFourMaterializerStub{materials: []ClusterMaterial{material}},
		ClusterGenerator:  stageFourClusterGeneratorStub{draft: draft},
		RelationGenerator: stageFourRelationGeneratorStub{},
		SnapshotReader:    stageFourSnapshotReaderStub{snapshot: snapshot},
		Assembler:         assembler,
	}
	if _, err := orchestrator.Run(context.Background(), snapshot); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if assembler.calls != 1 || len(assembler.input.Drafts) != 1 || len(assembler.input.Relations) != 0 {
		t.Fatalf("internal seam did not receive the complete handoff: %#v", assembler.input)
	}
	if assembler.input.InitialCatalog.SourceFingerprint != plan.SourceFingerprint {
		t.Fatalf("assembler initial catalog did not use the planned fingerprint: got %q want %q", assembler.input.InitialCatalog.SourceFingerprint, plan.SourceFingerprint)
	}
}

func TestStageFourOrchestratorPassesRetrievedMaterialToGenerationAndAssembly(t *testing.T) {
	snapshot := CatalogSnapshot{
		ContractVersion: PlanningContractVersion,
		OwnerScopeID:    "scope-1",
		Videos: []CatalogVideo{func() CatalogVideo {
			video := validCatalogVideo("video-1")
			video.Title = "视频一"
			return video
		}()},
	}
	plan := validAcceptedPlan(snapshot)
	plan.TopicClusters[0].Summary = "主题摘要"
	plan.TopicClusters[0].MaterialRequests[0].EvidenceIDs = []string{"evs:gen-1:one", "evs:gen-1:two"}
	plan.SourceFingerprint = testStageFourFingerprint(t, snapshot)
	material := validStageFourMaterial(plan.TopicClusters[0].ClusterKey)
	material.Evidence = append(material.Evidence, MaterialEvidence{
		VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "evs:gen-1:two",
		StartMs: 1300, EndMs: 1600, Text: "第二条证据材料",
	})
	reader := &fakeEvidenceQueryReader{results: [][]transcript.Chunk{{{EvidenceSentenceID: "evs:gen-1:two"}}}}
	generator := &recordingStageFourClusterGenerator{draft: validStageFourDraft(plan.TopicClusters[0].ClusterKey, material.Evidence[1])}
	assembler := &recordingStageFourAssembler{}
	orchestrator := &StageFourOrchestrator{
		Planner:           stageFourPlannerStub{plan: plan},
		Materializer:      stageFourMaterializerStub{materials: []ClusterMaterial{material}},
		MaterialRetriever: &QueryMaterialRetriever{Evidence: reader, MaxEvidencePerVideo: 1},
		ClusterGenerator:  generator,
		RelationGenerator: stageFourRelationGeneratorStub{},
		SnapshotReader:    stageFourSnapshotReaderStub{snapshot: snapshot},
		Assembler:         assembler,
	}
	if _, err := orchestrator.Run(context.Background(), snapshot); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(generator.materials) != 1 || len(generator.materials[0].Evidence) != 1 || generator.materials[0].Evidence[0].EvidenceID != "evs:gen-1:two" {
		t.Fatalf("cluster generator did not receive retrieved evidence: %#v", generator.materials)
	}
	if len(assembler.input.Materials) != 1 || len(assembler.input.Materials[0].Evidence) != 1 || assembler.input.Materials[0].Evidence[0].EvidenceID != "evs:gen-1:two" {
		t.Fatalf("assembler did not receive retrieved evidence: %#v", assembler.input.Materials)
	}
}

type blockingStageFourPlanner struct{}

func (blockingStageFourPlanner) Plan(ctx context.Context, _ CatalogSnapshot) (PlanDraft, error) {
	<-ctx.Done()
	return PlanDraft{}, ctx.Err()
}

func TestStageFourOrchestratorAppliesTaskTimeout(t *testing.T) {
	orchestrator := &StageFourOrchestrator{
		Planner:           blockingStageFourPlanner{},
		Materializer:      stageFourMaterializerStub{},
		ClusterGenerator:  stageFourClusterGeneratorStub{},
		RelationGenerator: stageFourRelationGeneratorStub{},
		SnapshotReader:    stageFourSnapshotReaderStub{},
		Assembler:         &recordingStageFourAssembler{},
		TaskTimeout:       10 * time.Millisecond,
	}
	_, err := orchestrator.Run(context.Background(), CatalogSnapshot{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected orchestrator task deadline, got %v", err)
	}
}

func TestStageFourOrchestratorRetriesOneFailedClusterGeneration(t *testing.T) {
	cluster := PlanCluster{ClusterKey: "cluster-1", PrimaryTemplate: "concept_cognition"}
	material := validStageFourMaterial(cluster.ClusterKey)
	generator := &retryingStageFourClusterGenerator{draft: validStageFourDraft(cluster.ClusterKey, material.Evidence[0])}
	orchestrator := &StageFourOrchestrator{ClusterGenerator: generator, MaxClusterRetries: 1}

	drafts, err := orchestrator.generateClusterPartsWithRetry(context.Background(), cluster, ClusterGenerationPart{Cluster: cluster, Material: material})
	if err != nil {
		t.Fatalf("retrying cluster generation returned error: %v", err)
	}
	if generator.calls != 2 || len(drafts) != 1 {
		t.Fatalf("expected one bounded retry, calls=%d drafts=%d", generator.calls, len(drafts))
	}
}

func TestStageFourOrchestratorDefaultsToTwoClusterRetries(t *testing.T) {
	cluster := PlanCluster{ClusterKey: "cluster-1", PrimaryTemplate: "concept_cognition"}
	material := validStageFourMaterial(cluster.ClusterKey)
	generator := &retryingStageFourClusterGenerator{draft: validStageFourDraft(cluster.ClusterKey, material.Evidence[0]), failures: 2}
	orchestrator := &StageFourOrchestrator{ClusterGenerator: generator}
	orchestrator.applyConfig()

	drafts, err := orchestrator.generateClusterPartsWithRetry(context.Background(), cluster, ClusterGenerationPart{Cluster: cluster, Material: material})
	if err != nil {
		t.Fatalf("default retries did not recover: %v", err)
	}
	if generator.calls != 3 || len(drafts) != 1 {
		t.Fatalf("expected two bounded retries, calls=%d drafts=%d", generator.calls, len(drafts))
	}
}

type retryingStageFourClusterGenerator struct {
	draft    ClusterGenerationDraft
	calls    int
	failures int
}

func (g *retryingStageFourClusterGenerator) Generate(context.Context, PlanCluster, ClusterMaterial) (ClusterGenerationDraft, error) {
	g.calls++
	failures := g.failures
	if failures == 0 {
		failures = 1
	}
	if g.calls <= failures {
		return ClusterGenerationDraft{}, &GenerationError{Code: "model_output_invalid", Err: errors.New("invalid model output")}
	}
	return g.draft, nil
}

func (*retryingStageFourClusterGenerator) SplitMaterial(cluster PlanCluster, material ClusterMaterial) ([]ClusterGenerationPart, error) {
	return []ClusterGenerationPart{{Cluster: cluster, Material: material}}, nil
}

func stageFourFingerprint(model, prompt string) func(CatalogSnapshot) (string, error) {
	return func(snapshot CatalogSnapshot) (string, error) {
		return CatalogFingerprint(snapshot, model, prompt)
	}
}

func testStageFourFingerprint(t *testing.T, snapshot CatalogSnapshot) string {
	t.Helper()
	fingerprint, err := CatalogFingerprint(snapshot, "planner-model", "planning-v1")
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func validStageFourMaterial(clusterKey string) ClusterMaterial {
	return ClusterMaterial{
		ContractVersion: MaterialContractVersion, ClusterKey: clusterKey,
		SourceVideoIDs: []string{"video-1"},
		SummaryBlocks:  []MaterialBlock{{VideoID: "video-1", BlockID: "block-1", Text: "总结材料"}},
		Evidence: []MaterialEvidence{{
			VideoID: "video-1", TranscriptGeneration: "gen-1", EvidenceID: "evs:gen-1:one",
			StartMs: 100, EndMs: 1200, Text: "证据材料",
		}},
	}
}

func validStageFourDraft(clusterKey string, evidence MaterialEvidence) ClusterGenerationDraft {
	return ClusterGenerationDraft{
		ContractVersion: ClusterGenerationContractVersion, ClusterKey: clusterKey, PrimaryTemplate: "concept_cognition",
		Stages: []ClusterGenerationStage{
			{
				Title: "基础", Summary: "建立基础认知",
				Units: []ClusterGenerationUnit{
					{
						LearningTitle: "核心问题", LearnerQuestion: "核心问题是什么？", LearningOutcome: "能够说明核心问题。",
						EvidenceRefs: []EvidenceRef{{VideoID: evidence.VideoID, TranscriptGeneration: evidence.TranscriptGeneration, EvidenceID: evidence.EvidenceID, StartMs: evidence.StartMs, EndMs: evidence.EndMs}},
						Confidence:   0.9,
					},
				},
			},
		},
	}
}

func materialTwoEvidence(material ClusterMaterial) EvidenceRef {
	evidence := material.Evidence[0]
	return EvidenceRef{VideoID: evidence.VideoID, TranscriptGeneration: evidence.TranscriptGeneration, EvidenceID: evidence.EvidenceID, StartMs: evidence.StartMs, EndMs: evidence.EndMs}
}

type stageFourPlannerStub struct{ plan PlanDraft }

func (s stageFourPlannerStub) Plan(context.Context, CatalogSnapshot) (PlanDraft, error) {
	return s.plan, nil
}

type stageFourMaterializerStub struct{ materials []ClusterMaterial }

func (s stageFourMaterializerStub) Materialize(context.Context, CatalogSnapshot, PlanDraft) ([]ClusterMaterial, error) {
	return s.materials, nil
}

type stageFourClusterGeneratorStub struct{ draft ClusterGenerationDraft }

func (s stageFourClusterGeneratorStub) Generate(context.Context, PlanCluster, ClusterMaterial) (ClusterGenerationDraft, error) {
	return s.draft, nil
}

func (s stageFourClusterGeneratorStub) SplitMaterial(cluster PlanCluster, material ClusterMaterial) ([]ClusterGenerationPart, error) {
	return []ClusterGenerationPart{{Cluster: cluster, Material: material}}, nil
}

type recordingStageFourClusterGenerator struct {
	draft     ClusterGenerationDraft
	materials []ClusterMaterial
}

func (g *recordingStageFourClusterGenerator) Generate(_ context.Context, _ PlanCluster, material ClusterMaterial) (ClusterGenerationDraft, error) {
	g.materials = append(g.materials, material)
	return g.draft, nil
}

func (*recordingStageFourClusterGenerator) SplitMaterial(cluster PlanCluster, material ClusterMaterial) ([]ClusterGenerationPart, error) {
	return []ClusterGenerationPart{{Cluster: cluster, Material: material}}, nil
}

type stageFourRelationGeneratorStub struct{}

func (stageFourRelationGeneratorStub) Generate(context.Context, []TopicCluster) ([]TopicClusterRelation, error) {
	return []TopicClusterRelation{}, nil
}

type stageFourSnapshotReaderStub struct{ snapshot CatalogSnapshot }

func (s stageFourSnapshotReaderStub) CollectCatalog(context.Context) (CatalogSnapshot, error) {
	return s.snapshot, nil
}

type recordingStageFourAssembler struct {
	calls int
	input StageFourAssemblyInput
}

func (a *recordingStageFourAssembler) Assemble(input StageFourAssemblyInput) (ProjectionDocument, error) {
	a.calls++
	a.input = input
	return ProjectionDocument{}, nil
}
