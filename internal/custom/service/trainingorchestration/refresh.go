package trainingorchestration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"gorm.io/gorm"
)

const (
	RefreshModeReused             = "reused"
	RefreshModeMetadataPatch      = "metadata_patch"
	RefreshModeAvailabilityFilter = "availability_filter"
	RefreshModeIncremental        = "incremental"
	RefreshModeFull               = "full"
)

// RefreshDecision is deliberately based on digests rather than model output.
// It lets the service skip model calls when a source change is provably local.
type RefreshDecision struct {
	Mode                  string
	ChangedVideoCount     int
	ChangedKnowledgeCount int
	ChangedEvidenceCount  int
	ChangedClusterCount   int
	ChangedVideoIDs       []string
	RecoveredVideoIDs     []string
	Recovery              bool
}

func (d RefreshDecision) isLocal() bool {
	return d.Mode == RefreshModeMetadataPatch || d.Mode == RefreshModeAvailabilityFilter
}

func referenceFromVideoProfile(video VideoTopicProfile) inputReference {
	item := inputReference{
		VideoID: video.VideoID, Title: video.Title, DurationSeconds: video.DurationSeconds,
		TranscriptGeneration: video.TranscriptGeneration, TopicSource: string(video.TopicSource),
		KnowledgeWikiPageIDs: []string{}, EvidenceIDs: []string{},
		EvidenceDigests: map[string]string{}, EvidenceMetadataDigests: map[string]string{},
		KnowledgeDigests: map[string]string{}, KnowledgeMetadataDigests: map[string]string{},
	}
	if video.Summary != nil && video.Summary.OrchestrationProfile != nil {
		item.TopicDigest = digestValue(video.Summary.OrchestrationProfile.PrimaryTopic)
	}
	for _, evidence := range video.EvidenceSignals {
		item.EvidenceDigests[evidence.EvidenceID] = digestValue(struct {
			ID, Snippet string
		}{evidence.EvidenceID, evidence.TranscriptSnippet})
		item.EvidenceMetadataDigests[evidence.EvidenceID] = digestValue(struct {
			ID, Generation string
			Start, End     int
		}{evidence.EvidenceID, evidence.TranscriptGeneration, evidence.StartMs, evidence.EndMs})
	}
	for _, signal := range video.KnowledgeSignals {
		item.KnowledgeWikiPageIDs = append(item.KnowledgeWikiPageIDs, signal.WikiPageID)
		item.KnowledgeDigests[signal.KnowledgeObjectID] = digestValue(struct {
			ID, Core    string
			Structure   map[string]string
			EvidenceIDs []string
		}{signal.KnowledgeObjectID, signal.CoreContent, signal.StructureFields, signal.EvidenceIDs})
		item.KnowledgeMetadataDigests[signal.KnowledgeObjectID] = digestValue(struct {
			ID, PageID, Title, Type string
			Version                 int
		}{signal.KnowledgeObjectID, signal.WikiPageID, signal.Title, string(signal.KnowledgeType), signal.WikiPageVersion})
	}
	sort.Strings(item.KnowledgeWikiPageIDs)
	return item
}

func referenceMetadataDigest(ref inputReference) string {
	return digestValue(struct {
		Title, TopicSource, SummaryPage, TranscriptGeneration string
		Duration, SummaryVersion                              int
		KnowledgeDigests, EvidenceDigests                     map[string]string
	}{ref.Title, ref.TopicSource, ref.SummaryWikiPageID, ref.TranscriptGeneration, ref.DurationSeconds, ref.SummaryVersion, ref.KnowledgeMetadataDigests, ref.EvidenceMetadataDigests})
}

func referenceContentDigest(video VideoTopicProfile) string {
	type summarySignal struct {
		BlockID, Section, Text string
		EvidenceIDs            []string
	}
	type transcriptSignal struct {
		Chapter, Text string
		EvidenceIDs   []string
	}
	var summaries []summarySignal
	if video.Summary != nil {
		for _, signal := range video.Summary.Signals {
			item := summarySignal{BlockID: signal.BlockID, Section: signal.Section, Text: signal.Text}
			for _, ref := range signal.EvidenceRefs {
				item.EvidenceIDs = append(item.EvidenceIDs, ref.EvidenceID)
			}
			summaries = append(summaries, item)
		}
	}
	var transcripts []transcriptSignal
	if video.Transcript != nil {
		for _, signal := range video.Transcript.Signals {
			item := transcriptSignal{Chapter: signal.Chapter, Text: signal.Text}
			for _, ref := range signal.EvidenceRefs {
				item.EvidenceIDs = append(item.EvidenceIDs, ref.EvidenceID)
			}
			transcripts = append(transcripts, item)
		}
	}
	return digestValue(struct {
		SummarySignals    []summarySignal
		TranscriptSignals []transcriptSignal
	}{summaries, transcripts})
}

func (video VideoTopicProfile) SummarySignalsForFingerprint() []SummarySignal {
	if video.Summary == nil {
		return nil
	}
	result := make([]SummarySignal, len(video.Summary.Signals))
	for i, signal := range video.Summary.Signals {
		result[i] = signal
		result[i].KnowledgeRefs = nil
		result[i].EvidenceRefs = append([]EvidenceReference(nil), signal.EvidenceRefs...)
	}
	return result
}

func (video VideoTopicProfile) TranscriptSignalsForFingerprint() []TranscriptSignal {
	if video.Transcript == nil {
		return nil
	}
	return append([]TranscriptSignal(nil), video.Transcript.Signals...)
}

func catalogContentDigest(video CatalogVideo) string {
	return digestValue(struct {
		VideoType, TranscriptGeneration string
		Profile                         *summaryFingerprintProfile
		EvidenceContentHashes           map[string]string
	}{video.VideoType, video.TranscriptGeneration, newSummaryFingerprintProfile(video), video.EvidenceContentHashes})
}

type summaryFingerprintProfile struct {
	PrimaryTopic string
	Units        []summaryFingerprintUnit
}

type summaryFingerprintUnit struct {
	Title, Abstract                                                   string
	ContentForms, LearningOutcomes, SummaryBlockIDs, EvidenceChunkIDs []string
	EvidenceRefs                                                      []summaryFingerprintEvidence
}

type summaryFingerprintEvidence struct{ ID string }

func newSummaryFingerprintProfile(video CatalogVideo) *summaryFingerprintProfile {
	profile := video.OrchestrationProfile
	if profile == nil {
		profile = video.CompatibilityProfile
	}
	if profile == nil {
		return nil
	}
	result := &summaryFingerprintProfile{PrimaryTopic: profile.PrimaryTopic, Units: make([]summaryFingerprintUnit, 0, len(profile.TopicUnits))}
	for _, unit := range profile.TopicUnits {
		item := summaryFingerprintUnit{Title: unit.Title, Abstract: unit.Abstract, ContentForms: unit.ContentForms, LearningOutcomes: unit.LearningOutcomes, SummaryBlockIDs: unit.SummaryBlockIDs, EvidenceChunkIDs: unit.EvidenceChunkIDs}
		for _, ref := range unit.EvidenceRefs {
			item.EvidenceRefs = append(item.EvidenceRefs, summaryFingerprintEvidence{ID: ref.EvidenceSentenceID})
		}
		result.Units = append(result.Units, item)
	}
	return result
}

func digestValue(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func classifyRefresh(previousRaw string, current []inputReference) (RefreshDecision, error) {
	decision := RefreshDecision{Mode: RefreshModeFull}
	var previous []inputReference
	if err := json.Unmarshal([]byte(previousRaw), &previous); err != nil || len(previous) == 0 {
		return decision, nil
	}
	oldByID := make(map[string]inputReference, len(previous))
	for _, ref := range previous {
		if strings.TrimSpace(ref.VideoID) == "" || ref.MetadataDigest == "" || ref.ContentDigest == "" {
			return decision, nil
		}
		oldByID[ref.VideoID] = ref
	}
	newByID := make(map[string]inputReference, len(current))
	for _, ref := range current {
		newByID[ref.VideoID] = ref
	}
	for id := range newByID {
		if _, existed := oldByID[id]; !existed {
			decision.ChangedVideoCount++
			decision.ChangedVideoIDs = append(decision.ChangedVideoIDs, id)
			return decision, nil
		}
	}
	hasAvailabilityChange := false
	hasIncrementalChange := false
	hasMetadataChange := false
	for id, old := range oldByID {
		currentRef, ok := newByID[id]
		if !ok {
			if old.Availability == "available" {
				decision.ChangedVideoCount++
				decision.ChangedVideoIDs = appendUniqueString(decision.ChangedVideoIDs, id)
				hasAvailabilityChange = true
			}
			continue
		}
		if old.Availability != currentRef.Availability {
			decision.ChangedVideoCount++
			decision.ChangedVideoIDs = appendUniqueString(decision.ChangedVideoIDs, id)
			hasAvailabilityChange = true
			if old.Availability == "unavailable" && currentRef.Availability == "available" {
				decision.Recovery = true
				decision.RecoveredVideoIDs = appendUniqueString(decision.RecoveredVideoIDs, id)
			}
			continue
		}
		if old.Availability != "available" {
			continue
		}
		if old.TranscriptGeneration != currentRef.TranscriptGeneration || old.TopicDigest != currentRef.TopicDigest {
			decision.ChangedVideoCount++
			decision.ChangedVideoIDs = appendUniqueString(decision.ChangedVideoIDs, id)
			return decision, nil
		}
		evidenceChanges := changedMapCount(old.EvidenceDigests, currentRef.EvidenceDigests)
		knowledgeChanges := changedMapCount(old.KnowledgeDigests, currentRef.KnowledgeDigests)
		decision.ChangedEvidenceCount += evidenceChanges
		decision.ChangedKnowledgeCount += knowledgeChanges
		if old.ContentDigest != currentRef.ContentDigest || evidenceChanges > 0 || knowledgeChanges > 0 {
			hasIncrementalChange = true
			decision.ChangedVideoIDs = appendUniqueString(decision.ChangedVideoIDs, id)
		}
		if old.MetadataDigest != currentRef.MetadataDigest {
			hasMetadataChange = true
			decision.ChangedVideoIDs = appendUniqueString(decision.ChangedVideoIDs, id)
		}
	}
	decision.ChangedVideoCount = len(decision.ChangedVideoIDs)
	if len(decision.ChangedVideoIDs) == 0 {
		decision.Mode = RefreshModeReused
		return decision, nil
	}
	if hasIncrementalChange {
		decision.Mode = RefreshModeIncremental
	} else if hasAvailabilityChange {
		decision.Mode = RefreshModeAvailabilityFilter
	} else if hasMetadataChange {
		decision.Mode = RefreshModeMetadataPatch
	} else {
		decision.Mode = RefreshModeReused
	}
	return decision, nil
}

func changedMapCount(old, current map[string]string) int {
	count := 0
	seen := make(map[string]struct{}, len(old)+len(current))
	for key := range old {
		seen[key] = struct{}{}
	}
	for key := range current {
		seen[key] = struct{}{}
	}
	for key := range seen {
		if old[key] != current[key] {
			count++
		}
	}
	return count
}

func appendUniqueString(values []string, value string) []string {
	for _, item := range values {
		if item == value {
			return values
		}
	}
	return append(values, value)
}

// patchProjection applies only program-owned metadata and availability changes.
// Any content digest change deliberately falls through to full generation.
func patchProjection(projection *Projection, input InputPackage, fingerprint, mode string, now time.Time) error {
	if projection == nil {
		return fmt.Errorf("projection is nil")
	}
	available := make(map[string]VideoTopicProfile, len(input.QualifiedVideos))
	evidence := make(map[string]EvidenceSignal)
	for _, video := range input.QualifiedVideos {
		available[video.VideoID] = video
		for _, signal := range video.EvidenceSignals {
			evidence[evidenceKey(signal.VideoID, signal.TranscriptGeneration, signal.EvidenceID)] = signal
		}
	}
	for clusterIndex := range projection.TopicClusters {
		cluster := &projection.TopicClusters[clusterIndex]
		memberByVideo := make(map[string]MemberTopic, len(cluster.MemberTopics))
		for index, id := range cluster.SourceVideoIDs {
			if index < len(cluster.MemberTopics) {
				memberByVideo[id] = cluster.MemberTopics[index]
			}
		}
		keptVideos := make([]string, 0, len(cluster.SourceVideoIDs))
		keptMembers := make([]MemberTopic, 0, len(cluster.MemberTopics))
		for _, id := range cluster.SourceVideoIDs {
			video, ok := available[id]
			if !ok {
				continue
			}
			keptVideos = append(keptVideos, id)
			member := memberByVideo[id]
			if strings.TrimSpace(video.Title) != "" {
				member.Title = video.Title
			}
			keptMembers = append(keptMembers, member)
		}
		cluster.SourceVideoIDs, cluster.MemberTopics = keptVideos, keptMembers
		for stageIndex := range cluster.Path.Stages {
			stage := &cluster.Path.Stages[stageIndex]
			keptUnits := make([]LearningUnit, 0, len(stage.Units))
			for unitIndex := range stage.Units {
				unit := stage.Units[unitIndex]
				keptEvidence := make([]EvidenceRef, 0, len(unit.EvidenceRefs))
				for _, ref := range unit.EvidenceRefs {
					current, ok := evidence[evidenceKey(ref.VideoID, ref.TranscriptGeneration, ref.EvidenceID)]
					if !ok {
						if mode == RefreshModeAvailabilityFilter {
							continue
						}
						keptEvidence = append(keptEvidence, ref)
						continue
					}
					ref.TranscriptGeneration, ref.StartMs, ref.EndMs = current.TranscriptGeneration, current.StartMs, current.EndMs
					keptEvidence = append(keptEvidence, ref)
				}
				if len(keptEvidence) == 0 {
					continue
				}
				unit.EvidenceRefs = keptEvidence
				unit.Sequence = len(keptUnits) + 1
				keptUnits = append(keptUnits, unit)
			}
			stage.Units = keptUnits
		}
		keptStages := make([]LearningStage, 0, len(cluster.Path.Stages))
		for _, stage := range cluster.Path.Stages {
			if len(stage.Units) == 0 {
				continue
			}
			stage.Sequence = len(keptStages) + 1
			keptStages = append(keptStages, stage)
		}
		cluster.Path.Stages = keptStages
		cluster.EvidenceRefs = allProjectionEvidence([]TopicCluster{*cluster})
	}
	keptClusters := make([]TopicCluster, 0, len(projection.TopicClusters))
	for _, cluster := range projection.TopicClusters {
		unitCount := 0
		for _, stage := range cluster.Path.Stages {
			unitCount += len(stage.Units)
		}
		if len(cluster.SourceVideoIDs) == 0 || len(cluster.Path.Stages) == 0 || unitCount == 0 {
			continue
		}
		keptClusters = append(keptClusters, cluster)
	}
	projection.TopicClusters = keptClusters
	for index := range projection.TopicClusters {
		projection.TopicClusters[index].EvidenceRefs = allProjectionEvidence([]TopicCluster{projection.TopicClusters[index]})
	}
	clusterSet := make(map[string]struct{}, len(keptClusters))
	for _, cluster := range keptClusters {
		clusterSet[cluster.ClusterID] = struct{}{}
	}
	keptRelations := make([]TopicClusterRelation, 0, len(projection.TopicClusterRelations))
	for _, relation := range projection.TopicClusterRelations {
		if _, ok := clusterSet[relation.SourceClusterID]; !ok {
			continue
		}
		if _, ok := clusterSet[relation.TargetClusterID]; !ok {
			continue
		}
		relation.SourceEvidenceRefs = patchEvidenceReferences(relation.SourceEvidenceRefs, evidence, mode)
		relation.TargetEvidenceRefs = patchEvidenceReferences(relation.TargetEvidenceRefs, evidence, mode)
		if len(relation.SourceEvidenceRefs) == 0 || len(relation.TargetEvidenceRefs) == 0 {
			continue
		}
		keptRelations = append(keptRelations, relation)
	}
	projection.TopicClusterRelations = keptRelations
	projection.SourceFingerprint = fingerprint
	projection.GeneratedAt = now.UTC().Format(time.RFC3339Nano)
	bindProjectionKnowledgeFields(projection, input)
	filterKnowledgeBackedProjection(projection)
	bindProjectionKnowledgeFields(projection, input)
	ensureGapAnalyses(projection)
	projection.Statistics = buildStatistics(input, projection.TopicClusters, selectedVideoIDs(projection.TopicClusters), allProjectionEvidence(projection.TopicClusters))
	return ValidateProjection(ProjectionDocument{TrainingPathProjection: *projection}, input)
}

func patchEvidenceReferences(refs []EvidenceRef, current map[string]EvidenceSignal, mode string) []EvidenceRef {
	result := make([]EvidenceRef, 0, len(refs))
	for _, ref := range refs {
		signal, ok := current[evidenceKey(ref.VideoID, ref.TranscriptGeneration, ref.EvidenceID)]
		if !ok {
			if mode == RefreshModeAvailabilityFilter {
				continue
			}
			result = append(result, ref)
			continue
		}
		ref.TranscriptGeneration, ref.StartMs, ref.EndMs = signal.TranscriptGeneration, signal.StartMs, signal.EndMs
		result = append(result, ref)
	}
	return result
}

// catalogInputPackage is sufficient to validate a patched stage-four result.
func catalogInputPackage(snapshot CatalogSnapshot) InputPackage {
	input := InputPackage{SchemaVersion: SchemaVersion, OwnerScopeID: snapshot.OwnerScopeID, ScannedVideos: len(snapshot.Videos) + len(snapshot.SkippedVideos), QualifiedVideos: []VideoTopicProfile{}, SkippedVideos: append([]SkippedVideo(nil), snapshot.SkippedVideos...), SkipReasonCounts: map[SkipReason]int{}, TopicSourceCounts: map[TopicSource]int{TopicSourceFinalSummary: 0, TopicSourceNormalizedTranscript: 0}}
	for _, video := range snapshot.Videos {
		profile := video.OrchestrationProfile
		if profile == nil {
			profile = video.CompatibilityProfile
		}
		item := VideoTopicProfile{VideoID: video.VideoID, Title: video.Title, VideoType: video.VideoType, DurationSeconds: video.DurationSeconds, TranscriptGeneration: video.TranscriptGeneration, TopicSource: TopicSourceNormalizedTranscript, KnowledgeSignals: video.KnowledgeSignals, EvidenceSignals: []EvidenceSignal{}}
		if video.OrchestrationProfile != nil {
			item.TopicSource = TopicSourceFinalSummary
		}
		if profile != nil {
			for _, unit := range profile.TopicUnits {
				for _, ref := range unit.EvidenceRefs {
					item.EvidenceSignals = append(item.EvidenceSignals, EvidenceSignal{VideoID: video.VideoID, TranscriptGeneration: video.TranscriptGeneration, EvidenceID: ref.EvidenceSentenceID, StartMs: ref.StartMs, EndMs: ref.EndMs})
				}
			}
		}
		input.QualifiedVideos = append(input.QualifiedVideos, item)
		input.TopicSourceCounts[item.TopicSource]++
	}
	return input
}

func (s *Service) loadRefreshSource(ctx context.Context) (*ProjectionDocument, *model.TrainingOrchestrationCurrent, string, error) {
	var current model.TrainingOrchestrationCurrent
	if err := s.DB.WithContext(ctx).Where("owner_scope_id = ?", s.OwnerScopeID).First(&current).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, "", nil
		}
		return nil, nil, "", err
	}
	var job model.TrainingOrchestrationJob
	if err := s.DB.WithContext(ctx).Where("id = ?", current.JobID).First(&job).Error; err != nil {
		return nil, &current, "", err
	}
	page, err := s.Wiki.GetPageByID(ctx, s.KnowledgeBaseID, current.ResultWikiPageID)
	if err != nil {
		return nil, &current, job.InputReferences, err
	}
	if page == nil {
		return nil, &current, job.InputReferences, nil
	}
	var doc ProjectionDocument
	if err := json.Unmarshal([]byte(page.Content), &doc); err != nil {
		return nil, &current, job.InputReferences, err
	}
	return &doc, &current, job.InputReferences, nil
}

func (s *Service) refreshDecision(ctx context.Context, refs []inputReference) (RefreshDecision, *ProjectionDocument, error) {
	doc, _, previousRaw, err := s.loadRefreshSource(ctx)
	if err != nil {
		return RefreshDecision{Mode: RefreshModeFull}, nil, err
	}
	decision, err := classifyRefresh(previousRaw, refs)
	if err != nil {
		return RefreshDecision{Mode: RefreshModeFull}, doc, err
	}
	if doc != nil {
		changed := make(map[string]struct{}, len(decision.ChangedVideoIDs))
		for _, id := range decision.ChangedVideoIDs {
			changed[id] = struct{}{}
		}
		for _, cluster := range doc.TrainingPathProjection.TopicClusters {
			for _, id := range cluster.SourceVideoIDs {
				if _, ok := changed[id]; ok {
					decision.ChangedClusterCount++
					break
				}
			}
		}
	}
	if decision.Mode == RefreshModeReused {
		if doc == nil {
			// Reuse requires the previously published projection, not just a
			// matching source fingerprint in the database.
			decision.Mode = RefreshModeFull
		} else if uncovered := uncoveredAvailableVideos(doc, refs); len(uncovered) > 0 {
			// A matching source fingerprint is not sufficient to reuse a result:
			// an earlier run may have published an incomplete plan that left
			// qualified videos outside every visible topic cluster.
			decision.Mode = RefreshModeFull
			decision.ChangedVideoIDs = append(decision.ChangedVideoIDs, uncovered...)
			decision.ChangedVideoCount = len(decision.ChangedVideoIDs)
		}
	}
	if decision.Recovery {
		baseline, baselineRefs, baselineErr := s.loadLatestFullProjection(ctx)
		if baselineErr != nil {
			return RefreshDecision{Mode: RefreshModeFull}, doc, baselineErr
		}
		if baseline != nil {
			doc = mergeRecoveredProjection(doc, baseline, decision.RecoveredVideoIDs)
		}
		decision = reconcileRecoveredSources(decision, baselineRefs, refs)
	}
	return decision, doc, nil
}

func uncoveredAvailableVideos(doc *ProjectionDocument, refs []inputReference) []string {
	if doc == nil {
		return nil
	}
	selected := make(map[string]struct{})
	for _, cluster := range doc.TrainingPathProjection.TopicClusters {
		for _, videoID := range cluster.SourceVideoIDs {
			if id := strings.TrimSpace(videoID); id != "" {
				selected[id] = struct{}{}
			}
		}
	}
	result := make([]string, 0)
	for _, ref := range refs {
		if ref.Availability != "available" {
			continue
		}
		if _, ok := selected[ref.VideoID]; ok {
			continue
		}
		result = append(result, ref.VideoID)
	}
	sort.Strings(result)
	return result
}

// mergeRecoveredProjection restores only clusters and incident relations for
// videos returning from unavailable state. Incremental results for unrelated
// clusters remain the current published baseline.
func mergeRecoveredProjection(current, baseline *ProjectionDocument, recovered []string) *ProjectionDocument {
	if baseline == nil {
		return current
	}
	if current == nil {
		return baseline
	}
	recoveredSet := make(map[string]struct{}, len(recovered))
	for _, id := range recovered {
		recoveredSet[strings.TrimSpace(id)] = struct{}{}
	}
	affected := func(cluster TopicCluster) bool {
		for _, id := range cluster.SourceVideoIDs {
			if _, ok := recoveredSet[strings.TrimSpace(id)]; ok {
				return true
			}
		}
		return false
	}
	result := *current
	clusters := make([]TopicCluster, 0, len(current.TrainingPathProjection.TopicClusters)+len(baseline.TrainingPathProjection.TopicClusters))
	for _, cluster := range current.TrainingPathProjection.TopicClusters {
		if !affected(cluster) {
			clusters = append(clusters, cluster)
		}
	}
	for _, cluster := range baseline.TrainingPathProjection.TopicClusters {
		if affected(cluster) {
			clusters = append(clusters, cluster)
		}
	}
	result.TrainingPathProjection.TopicClusters = clusters
	clusterIDs := make(map[string]struct{}, len(clusters))
	for _, c := range clusters {
		clusterIDs[c.ClusterID] = struct{}{}
	}
	relationIncident := make(map[string]struct{})
	for _, c := range baseline.TrainingPathProjection.TopicClusters {
		if affected(c) {
			relationIncident[c.ClusterID] = struct{}{}
		}
	}
	relations := make([]TopicClusterRelation, 0)
	for _, relation := range current.TrainingPathProjection.TopicClusterRelations {
		if _, ok := relationIncident[relation.SourceClusterID]; ok {
			continue
		}
		if _, ok := relationIncident[relation.TargetClusterID]; ok {
			continue
		}
		relations = append(relations, relation)
	}
	for _, relation := range baseline.TrainingPathProjection.TopicClusterRelations {
		if _, ok := relationIncident[relation.SourceClusterID]; !ok {
			continue
		}
		if _, ok := clusterIDs[relation.TargetClusterID]; !ok {
			continue
		}
		relations = append(relations, relation)
	}
	result.TrainingPathProjection.TopicClusterRelations = relations
	return &result
}

func reconcileRecoveredSources(decision RefreshDecision, baselineRaw string, current []inputReference) RefreshDecision {
	if len(decision.RecoveredVideoIDs) == 0 || decision.Mode == RefreshModeFull {
		return decision
	}
	var baseline []inputReference
	if err := json.Unmarshal([]byte(baselineRaw), &baseline); err != nil || len(baseline) == 0 {
		decision.Mode = RefreshModeFull
		return decision
	}
	baselineByID := make(map[string]inputReference, len(baseline))
	for _, ref := range baseline {
		baselineByID[ref.VideoID] = ref
	}
	currentByID := make(map[string]inputReference, len(current))
	for _, ref := range current {
		currentByID[ref.VideoID] = ref
	}
	for _, id := range decision.RecoveredVideoIDs {
		old, oldOK := baselineByID[id]
		latest, latestOK := currentByID[id]
		if !oldOK || !latestOK || old.Availability != "available" || latest.Availability != "available" || old.ContentDigest == "" {
			decision.Mode = RefreshModeFull
			return decision
		}
		if old.TranscriptGeneration != latest.TranscriptGeneration || old.TopicDigest != latest.TopicDigest {
			decision.Mode = RefreshModeFull
			return decision
		}
		evidenceChanges := changedMapCount(old.EvidenceDigests, latest.EvidenceDigests)
		knowledgeChanges := changedMapCount(old.KnowledgeDigests, latest.KnowledgeDigests)
		decision.ChangedEvidenceCount += evidenceChanges
		decision.ChangedKnowledgeCount += knowledgeChanges
		if old.ContentDigest != latest.ContentDigest || evidenceChanges > 0 || knowledgeChanges > 0 {
			decision.Mode = RefreshModeIncremental
		}
	}
	return decision
}

func (s *Service) recordRefreshDecision(ctx context.Context, jobID string, decision RefreshDecision) {
	_ = s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{
		"refresh_mode": decision.Mode, "changed_video_count": decision.ChangedVideoCount,
		"changed_knowledge_count": decision.ChangedKnowledgeCount, "changed_evidence_count": decision.ChangedEvidenceCount,
		"changed_cluster_count": decision.ChangedClusterCount, "updated_at": time.Now().UTC(),
	}).Error
}

func (s *Service) loadLatestFullProjection(ctx context.Context) (*ProjectionDocument, string, error) {
	var job model.TrainingOrchestrationJob
	err := s.DB.WithContext(ctx).
		Where("owner_scope_id = ? AND status = ? AND result_wiki_page_id <> '' AND (refresh_mode = ? OR refresh_mode = '' OR refresh_mode IS NULL)", s.OwnerScopeID, JobSucceeded, RefreshModeFull).
		Order("created_at DESC").First(&job).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, "", nil
		}
		return nil, "", err
	}
	page, err := s.Wiki.GetPageByID(ctx, s.KnowledgeBaseID, job.ResultWikiPageID)
	if err != nil || page == nil {
		return nil, job.InputReferences, err
	}
	var doc ProjectionDocument
	if err := json.Unmarshal([]byte(page.Content), &doc); err != nil {
		return nil, job.InputReferences, err
	}
	return &doc, job.InputReferences, nil
}

func (s *Service) publishLocalRefresh(ctx context.Context, jobID string, old *ProjectionDocument, input InputPackage, fingerprint string, decision RefreshDecision, overwrite bool) error {
	if old == nil {
		return fmt.Errorf("current training orchestration result is unavailable")
	}
	if err := patchProjection(&old.TrainingPathProjection, input, fingerprint, decision.Mode, time.Now()); err != nil {
		return err
	}
	content, err := json.Marshal(old)
	if err != nil {
		return err
	}
	stable, err := s.sourcesStillMatch(ctx, fingerprint)
	if err != nil {
		return err
	}
	if !stable {
		return &GenerationError{Code: "source_changed", Err: fmt.Errorf("training orchestration sources changed before incremental publication")}
	}
	if err := s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{
		"stage": StagePublishing, "progress": 90, "refresh_mode": decision.Mode,
		"changed_video_count": decision.ChangedVideoCount, "changed_knowledge_count": decision.ChangedKnowledgeCount,
		"changed_evidence_count": decision.ChangedEvidenceCount, "changed_cluster_count": decision.ChangedClusterCount,
		"updated_at": time.Now().UTC(),
	}).Error; err != nil {
		return err
	}
	page, err := s.writeProjectionPage(ctx, overwrite, weknora.WikiPageWrite{
		Slug: projectionSlug(s.OwnerScopeID, fingerprint), Title: "培训学习路径 " + time.Now().Format("2006-01-02 15:04"),
		PageType: "index", Status: "published", Content: string(content), Summary: "由当前视频、正式总结或规范化转写与字幕证据生成的培训学习路径",
		SourceRefs: projectionSourceRefs(input), ChunkRefs: projectionChunkRefs(input),
	})
	if err != nil {
		return err
	}
	if page == nil {
		return fmt.Errorf("training orchestration Wiki writer returned no page")
	}
	var published ProjectionDocument
	if err := json.Unmarshal([]byte(page.Content), &published); err != nil {
		return err
	}
	if err := ValidateProjection(published, input); err != nil {
		return err
	}
	if stable, stableErr := s.sourcesStillMatch(ctx, fingerprint); stableErr != nil || !stable {
		if stableErr == nil {
			stableErr = fmt.Errorf("training orchestration sources changed after Wiki publication")
		}
		return &GenerationError{Code: "source_changed", Err: stableErr}
	}
	return s.publishCurrent(ctx, jobID, page.ID, fingerprint, "", "")
}

func (s *Service) sourcesStillMatch(ctx context.Context, expected string) (bool, error) {
	if s.CatalogCollector != nil {
		snapshot, err := s.CatalogCollector.CollectCatalog(ctx)
		if err != nil {
			return false, err
		}
		actual, err := CatalogFingerprint(snapshot, s.Model, s.PromptVersion)
		return err == nil && actual == expected, err
	}
	input, err := s.Collector.Collect(ctx)
	if err != nil {
		return false, err
	}
	actual, err := SourceFingerprint(input, s.Model, s.PromptVersion)
	return err == nil && actual == expected, err
}
