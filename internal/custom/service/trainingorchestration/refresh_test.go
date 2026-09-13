package trainingorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func refreshTestReference(id, title string) inputReference {
	ref := inputReference{
		VideoID: id, Availability: "available", Title: title, DurationSeconds: 60,
		TranscriptGeneration: "generation-1", TopicSource: string(TopicSourceFinalSummary),
		SummaryWikiPageID: "summary-" + id, SummaryVersion: 1,
		EvidenceIDs: []string{"e-" + id}, EvidenceDigests: map[string]string{"e-" + id: "evidence-1"},
		KnowledgeDigests: map[string]string{"k-" + id: "knowledge-1"}, ContentDigest: "content-1",
	}
	ref.MetadataDigest = referenceMetadataDigest(ref)
	return ref
}

func TestClassifyRefreshModes(t *testing.T) {
	base := refreshTestReference("video-1", "视频一")
	raw, err := json.Marshal([]inputReference{base})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(inputReference) inputReference
		want   string
	}{
		{name: "unchanged", mutate: func(value inputReference) inputReference { return value }, want: RefreshModeReused},
		{name: "title metadata", mutate: func(value inputReference) inputReference {
			value.Title = "新标题"
			value.MetadataDigest = referenceMetadataDigest(value)
			return value
		}, want: RefreshModeMetadataPatch},
		{name: "availability", mutate: func(value inputReference) inputReference {
			value.Availability = "unavailable"
			value.SkipReason = string(SkipInaccessible)
			return value
		}, want: RefreshModeAvailabilityFilter},
		{name: "content", mutate: func(value inputReference) inputReference { value.ContentDigest = "content-2"; return value }, want: RefreshModeIncremental},
		{name: "transcript generation", mutate: func(value inputReference) inputReference { value.TranscriptGeneration = "generation-2"; return value }, want: RefreshModeFull},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := test.mutate(base)
			decision, err := classifyRefresh(string(raw), []inputReference{current})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Mode != test.want {
				t.Fatalf("mode=%q, want %q (%#v)", decision.Mode, test.want, decision)
			}
		})
	}
}

func TestReconcileRecoveredSourcesEscalatesChangedContent(t *testing.T) {
	baseline := refreshTestReference("video-1", "视频一")
	baselineRaw, err := json.Marshal([]inputReference{baseline})
	if err != nil {
		t.Fatal(err)
	}
	previous := baseline
	previous.Availability = "unavailable"
	previousRaw, err := json.Marshal([]inputReference{previous})
	if err != nil {
		t.Fatal(err)
	}

	decision, err := classifyRefresh(string(previousRaw), []inputReference{baseline})
	if err != nil {
		t.Fatal(err)
	}
	if got := reconcileRecoveredSources(decision, string(baselineRaw), []inputReference{baseline}).Mode; got != RefreshModeAvailabilityFilter {
		t.Fatalf("unchanged recovery mode=%q, want %q", got, RefreshModeAvailabilityFilter)
	}

	contentChanged := baseline
	contentChanged.ContentDigest = "content-2"
	if got := reconcileRecoveredSources(decision, string(baselineRaw), []inputReference{contentChanged}).Mode; got != RefreshModeIncremental {
		t.Fatalf("content-changed recovery mode=%q, want %q", got, RefreshModeIncremental)
	}

	transcriptChanged := baseline
	transcriptChanged.TranscriptGeneration = "generation-2"
	if got := reconcileRecoveredSources(decision, string(baselineRaw), []inputReference{transcriptChanged}).Mode; got != RefreshModeFull {
		t.Fatalf("transcript-changed recovery mode=%q, want %q", got, RefreshModeFull)
	}
}

func TestPatchProjectionFiltersUnavailableVideoAndUpdatesEvidence(t *testing.T) {
	input := InputPackage{
		SchemaVersion: SchemaVersion, OwnerScopeID: "scope-1", ScannedVideos: 2,
		QualifiedVideos: []VideoTopicProfile{{
			VideoID: "video-2", Title: "新视频标题", TranscriptGeneration: "generation-2", TopicSource: TopicSourceFinalSummary,
			EvidenceSignals:  []EvidenceSignal{{VideoID: "video-2", TranscriptGeneration: "generation-2", EvidenceID: "e-2", StartMs: 2200, EndMs: 4200}},
			KnowledgeSignals: []KnowledgeSignal{{SourceVideoID: "video-2", KnowledgeObjectID: "k-2", WikiPageID: "page-2", KnowledgeType: knowledge.TypeConcept, Title: "知识二", EvidenceIDs: []string{"e-2"}}},
		}},
		SkippedVideos:     []SkippedVideo{{VideoID: "video-1", Reason: SkipInaccessible}},
		SkipReasonCounts:  map[SkipReason]int{SkipInaccessible: 1},
		TopicSourceCounts: map[TopicSource]int{TopicSourceFinalSummary: 1},
	}
	projection := Projection{
		SchemaVersion: SchemaVersion, OwnerScopeID: "scope-1", SourceFingerprint: "old", GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		TopicClusters: []TopicCluster{{
			ClusterID: "cluster-1", Title: "课程", Summary: "摘要", LearningGoal: "目标", LearningContentType: "concept_cognition",
			MemberTopics:   []MemberTopic{{TopicID: "topic-1", Title: "旧视频"}, {TopicID: "topic-2", Title: "旧标题"}},
			SourceVideoIDs: []string{"video-1", "video-2"}, EvidenceRefs: []EvidenceRef{{VideoID: "video-2", TranscriptGeneration: "generation-2", EvidenceID: "e-2", StartMs: 2200, EndMs: 4200}},
			Confidence: .9, ReviewStatus: "passed", Path: LearningPath{PathID: "path-1", PrimaryTemplate: "concept_cognition", Stages: []LearningStage{{
				StageID: "stage-1", Title: "阶段", Summary: "阶段摘要", Sequence: 1, Units: []LearningUnit{{
					UnitID: "unit-1", LearningTitle: "任务", LearnerQuestion: "问题", LearningOutcome: "结果", Sequence: 1,
					KnowledgeRefs: []KnowledgeRef{{KnowledgeObjectID: "k-2", WikiPageID: "page-2", KnowledgeType: knowledge.TypeConcept, Title: "知识二", LocatorEvidence: EvidenceRef{VideoID: "video-2", TranscriptGeneration: "generation-2", EvidenceID: "e-2", StartMs: 2200, EndMs: 4200}}},
					EvidenceRefs:  []EvidenceRef{{VideoID: "video-2", TranscriptGeneration: "generation-2", EvidenceID: "e-2", StartMs: 2200, EndMs: 4200}}, Confidence: .9, ReviewStatus: "passed",
				}},
			}}},
		}},
		TopicClusterRelations: []TopicClusterRelation{},
	}
	if err := patchProjection(&projection, input, "new", RefreshModeAvailabilityFilter, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(projection.TopicClusters) != 1 {
		t.Fatalf("valid single-video cluster should remain visible, got %#v", projection.TopicClusters)
	}
	if projection.SourceFingerprint != "new" {
		t.Fatalf("fingerprint=%q, want new", projection.SourceFingerprint)
	}
}

func TestUncoveredAvailableVideosForceRefresh(t *testing.T) {
	doc := &ProjectionDocument{TrainingPathProjection: Projection{
		TopicClusters: []TopicCluster{{SourceVideoIDs: []string{"video-1"}}},
	}}
	refs := []inputReference{
		{VideoID: "video-1", Availability: "available"},
		{VideoID: "video-2", Availability: "available"},
		{VideoID: "video-3", Availability: "unavailable"},
	}
	got := uncoveredAvailableVideos(doc, refs)
	if len(got) != 1 || got[0] != "video-2" {
		t.Fatalf("uncovered videos=%v, want [video-2]", got)
	}
}

type countingProjectionGenerator struct {
	doc   ProjectionDocument
	calls int
}

func (g *countingProjectionGenerator) Generate(context.Context, InputPackage) (ProjectionDocument, error) {
	g.calls++
	return g.doc, nil
}

func TestServiceAvailabilityRefreshSkipsModelAndPublishesFilteredCopy(t *testing.T) {
	input := refreshTwoVideoInput()
	fingerprint, err := SourceFingerprint(input, "refresh-model", "refresh-v1")
	if err != nil {
		t.Fatal(err)
	}
	doc := refreshTwoVideoProjection(input, fingerprint)
	generator := &countingProjectionGenerator{doc: doc}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "refresh.db")+"?_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.TrainingOrchestrationJob{}, &model.TrainingOrchestrationCurrent{}); err != nil {
		t.Fatal(err)
	}
	collector := &sequenceInputCollector{inputs: []InputPackage{input, input}}
	wiki := &memoryProjectionWiki{}
	service := &Service{DB: db, Collector: collector, Generator: generator, Wiki: wiki, KnowledgeBaseID: "kb", OwnerScopeID: input.OwnerScopeID, Model: "refresh-model", PromptVersion: "refresh-v1", RunTimeout: time.Second}
	first, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first = waitForTrainingJob(t, service, first.ID)
	if first.Status != JobSucceeded || generator.calls != 1 {
		t.Fatalf("initial generation failed: %#v calls=%d", first, generator.calls)
	}
	changed := input
	changed.QualifiedVideos = []VideoTopicProfile{input.QualifiedVideos[1]}
	changed.SkippedVideos = []SkippedVideo{{VideoID: "video-1", Reason: SkipInaccessible}}
	changed.ScannedVideos = 2
	changed.SkipReasonCounts = map[SkipReason]int{SkipInaccessible: 1}
	changed.TopicSourceCounts = map[TopicSource]int{TopicSourceFinalSummary: 1}
	collector.inputs = append(collector.inputs, changed, changed, changed)
	second, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second = waitForTrainingJob(t, service, second.ID)
	if second.Status != JobSucceeded || second.RefreshMode != RefreshModeAvailabilityFilter || generator.calls != 1 {
		t.Fatalf("availability refresh did not bypass model: %#v calls=%d wiki=%s", second, generator.calls, wiki.page.Content)
	}
	current, _, err := service.GetCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || len(current.TrainingPathProjection.TopicClusters) != 1 {
		t.Fatalf("filtered current projection should retain the available single-video topic: %#v", current)
	}
}

func TestServiceMetadataRefreshPatchesTitlesAndEvidenceWithoutModel(t *testing.T) {
	input := refreshTwoVideoInput()
	fingerprint, err := SourceFingerprint(input, "refresh-model", "refresh-v1")
	if err != nil {
		t.Fatal(err)
	}
	generator := &countingProjectionGenerator{doc: refreshTwoVideoProjection(input, fingerprint)}
	collector := &sequenceInputCollector{inputs: []InputPackage{input, input}}
	wiki := &memoryProjectionWiki{}
	service := &Service{DB: newTrainingServiceDB(t), Collector: collector, Generator: generator, Wiki: wiki, KnowledgeBaseID: "kb", OwnerScopeID: input.OwnerScopeID, Model: "refresh-model", PromptVersion: "refresh-v1", RunTimeout: time.Second}

	first, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first = waitForTrainingJob(t, service, first.ID)
	if first.Status != JobSucceeded {
		t.Fatalf("initial generation failed: %#v", first)
	}

	changed := input
	changed.QualifiedVideos = append([]VideoTopicProfile(nil), input.QualifiedVideos...)
	changed.QualifiedVideos[0].Title = "视频一（更新）"
	changed.QualifiedVideos[0].EvidenceSignals = append([]EvidenceSignal(nil), input.QualifiedVideos[0].EvidenceSignals...)
	changed.QualifiedVideos[0].EvidenceSignals[0].StartMs = 12_000
	changed.QualifiedVideos[0].EvidenceSignals[0].EndMs = 14_000
	changed.QualifiedVideos[0].KnowledgeSignals = append([]KnowledgeSignal(nil), input.QualifiedVideos[0].KnowledgeSignals...)
	changed.QualifiedVideos[0].KnowledgeSignals[0].Title = "知识一（更新）"
	collector.inputs = append(collector.inputs, changed, changed, changed)

	second, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second = waitForTrainingJob(t, service, second.ID)
	if second.Status != JobSucceeded || second.RefreshMode != RefreshModeMetadataPatch || generator.calls != 1 {
		t.Fatalf("metadata refresh did not stay local: job=%#v calls=%d", second, generator.calls)
	}
	current, _, err := service.GetCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cluster := current.TrainingPathProjection.TopicClusters[0]
	unit := cluster.Path.Stages[0].Units[0]
	if cluster.MemberTopics[0].Title != "视频一（更新）" || unit.EvidenceRefs[0].StartMs != 12_000 || unit.EvidenceRefs[0].EndMs != 14_000 || unit.KnowledgeRefs[0].Title != "知识一（更新）" {
		t.Fatalf("metadata patch did not update published fields: cluster=%#v unit=%#v", cluster, unit)
	}
}

type failingIncrementalStageFourRunner struct {
	doc              ProjectionDocument
	fullCalls        int
	incrementalCalls int
}

func (r *failingIncrementalStageFourRunner) Run(context.Context, CatalogSnapshot) (ProjectionDocument, error) {
	r.fullCalls++
	return r.doc, nil
}

func (r *failingIncrementalStageFourRunner) RunIncremental(context.Context, CatalogSnapshot, ProjectionDocument, []string) (ProjectionDocument, error) {
	r.incrementalCalls++
	return ProjectionDocument{}, errors.New("incremental generation failed")
}

func TestServiceIncrementalFailureKeepsCurrentPublishedResult(t *testing.T) {
	videoOne := validCatalogVideo("video-1")
	videoOne.Title = "视频一"
	videoOne.KnowledgeSignals = []KnowledgeSignal{{SourceVideoID: "video-1", KnowledgeObjectID: "k-1", WikiPageID: "page-k-1", KnowledgeType: knowledge.TypeConcept, Title: "知识一", EvidenceIDs: []string{"evs:gen-1:one"}}}
	videoTwo := validCatalogVideo("video-2")
	videoTwo.Title = "视频二"
	videoTwo.KnowledgeSignals = []KnowledgeSignal{{SourceVideoID: "video-2", KnowledgeObjectID: "k-2", WikiPageID: "page-k-2", KnowledgeType: knowledge.TypeConcept, Title: "知识二", EvidenceIDs: []string{"evs:gen-1:one"}}}
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, OwnerScopeID: "refresh-scope", Videos: []CatalogVideo{videoOne, videoTwo}}
	fingerprint, err := CatalogFingerprint(snapshot, "refresh-model", "refresh-v1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.SourceFingerprint = fingerprint
	runner := &failingIncrementalStageFourRunner{doc: refreshTwoVideoProjection(catalogInputPackage(snapshot), fingerprint)}
	collector := &staticCatalogCollector{snapshot: snapshot}
	wiki := &memoryProjectionWiki{}
	service := &Service{DB: newTrainingServiceDB(t), CatalogCollector: collector, StageFour: runner, Wiki: wiki, KnowledgeBaseID: "kb", OwnerScopeID: snapshot.OwnerScopeID, Model: "refresh-model", PromptVersion: "refresh-v1", RunTimeout: time.Second}

	first, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first = waitForTrainingJob(t, service, first.ID)
	if first.Status != JobSucceeded {
		t.Fatalf("initial generation failed: %#v", first)
	}
	var before model.TrainingOrchestrationCurrent
	if err := service.DB.Where("owner_scope_id = ?", snapshot.OwnerScopeID).First(&before).Error; err != nil {
		t.Fatal(err)
	}

	changed := snapshot
	changed.Videos = append([]CatalogVideo(nil), snapshot.Videos...)
	profile := *changed.Videos[0].OrchestrationProfile
	profile.TopicUnits = append([]summary.OrchestrationTopicUnit(nil), profile.TopicUnits...)
	profile.TopicUnits[0].Abstract = "发生局部变化的摘要"
	changed.Videos[0].OrchestrationProfile = &profile
	changed.SourceFingerprint = ""
	collector.snapshot = changed

	second, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second = waitForTrainingJob(t, service, second.ID)
	if second.Status != JobFailed || second.RefreshMode != RefreshModeIncremental || runner.incrementalCalls != 1 || runner.fullCalls != 1 {
		t.Fatalf("unexpected incremental failure result: job=%#v full=%d incremental=%d", second, runner.fullCalls, runner.incrementalCalls)
	}
	var after model.TrainingOrchestrationCurrent
	if err := service.DB.Where("owner_scope_id = ?", snapshot.OwnerScopeID).First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.JobID != before.JobID || after.ResultWikiPageID != before.ResultWikiPageID || wiki.writes != 1 {
		t.Fatalf("failed incremental refresh changed current result: before=%#v after=%#v writes=%d", before, after, wiki.writes)
	}
}

func refreshTwoVideoInput() InputPackage {
	input := InputPackage{SchemaVersion: SchemaVersion, OwnerScopeID: "refresh-scope", ScannedVideos: 2, QualifiedVideos: []VideoTopicProfile{}, SkipReasonCounts: map[SkipReason]int{}, TopicSourceCounts: map[TopicSource]int{TopicSourceFinalSummary: 2}}
	for index := 1; index <= 2; index++ {
		id := "video-" + string(rune('0'+index))
		input.QualifiedVideos = append(input.QualifiedVideos, VideoTopicProfile{VideoID: id, Title: "视频" + string(rune('0'+index)), TranscriptGeneration: "generation-1", TopicSource: TopicSourceFinalSummary,
			EvidenceSignals:  []EvidenceSignal{{VideoID: id, TranscriptGeneration: "generation-1", EvidenceID: "e-" + string(rune('0'+index)), StartMs: index * 1000, EndMs: index*1000 + 900}},
			KnowledgeSignals: []KnowledgeSignal{{SourceVideoID: id, KnowledgeObjectID: "k-" + string(rune('0'+index)), WikiPageID: "page-" + string(rune('0'+index)), KnowledgeType: knowledge.TypeConcept, Title: "知识" + string(rune('0'+index)), EvidenceIDs: []string{"e-" + string(rune('0'+index))}}},
		})
	}
	return input
}

func refreshTwoVideoProjection(input InputPackage, fingerprint string) ProjectionDocument {
	units := make([]LearningUnit, 0, len(input.QualifiedVideos))
	for index, video := range input.QualifiedVideos {
		evidence := video.EvidenceSignals[0]
		units = append(units, LearningUnit{UnitID: "unit-" + string(rune('0'+index+1)), LearningTitle: "任务" + string(rune('0'+index+1)), LearnerQuestion: "问题", LearningOutcome: "结果", Sequence: index + 1, EvidenceRefs: []EvidenceRef{{VideoID: video.VideoID, TranscriptGeneration: evidence.TranscriptGeneration, EvidenceID: evidence.EvidenceID, StartMs: evidence.StartMs, EndMs: evidence.EndMs}}, Confidence: .9, ReviewStatus: "passed"})
	}
	cluster := TopicCluster{ClusterID: "cluster-1", Title: "课程", Summary: "摘要", LearningGoal: "目标", LearningContentType: "concept_cognition", MemberTopics: []MemberTopic{{TopicID: "topic-1", Title: "视频1"}, {TopicID: "topic-2", Title: "视频2"}}, SourceVideoIDs: []string{"video-1", "video-2"}, EvidenceRefs: []EvidenceRef{}, Confidence: .9, ReviewStatus: "passed", Path: LearningPath{PathID: "path-1", PrimaryTemplate: "concept_cognition", Stages: []LearningStage{{StageID: "stage-1", Title: "阶段", Summary: "摘要", Sequence: 1, Units: units}}}}
	for _, unit := range units {
		cluster.EvidenceRefs = append(cluster.EvidenceRefs, unit.EvidenceRefs...)
	}
	projection := Projection{SchemaVersion: SchemaVersion, OwnerScopeID: input.OwnerScopeID, SourceFingerprint: fingerprint, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), TopicClusters: []TopicCluster{cluster}, TopicClusterRelations: []TopicClusterRelation{}}
	bindProjectionKnowledgeFields(&projection, input)
	ensureGapAnalyses(&projection)
	projection.Statistics = buildStatistics(input, projection.TopicClusters, selectedVideoIDs(projection.TopicClusters), allProjectionEvidence(projection.TopicClusters))
	return ProjectionDocument{TrainingPathProjection: projection}
}
