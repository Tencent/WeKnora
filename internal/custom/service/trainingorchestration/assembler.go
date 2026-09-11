package trainingorchestration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const defaultStageFourClusterRetries = 2

// StageFourAssemblyInput is the immutable handoff presented to the final
// stage-four assembler. The assembler is the only module allowed to turn
// cluster drafts and relation candidates into a publishable projection.
type StageFourAssemblyInput struct {
	InitialCatalog CatalogSnapshot
	LatestCatalog  CatalogSnapshot
	Plan           PlanDraft
	Materials      []ClusterMaterial
	Drafts         []ClusterGenerationDraft
	Relations      []TopicClusterRelation
}

// StageFourProjectionAssembler is the narrow seam used by the internal
// orchestration pipeline. It returns a complete, validated document and has
// no publishing side effects.
type StageFourProjectionAssembler interface {
	Assemble(StageFourAssemblyInput) (ProjectionDocument, error)
}

// ProjectionAssembler assigns the final document metadata and validates the
// aggregate. Fingerprint is injected so the planner's model and prompt
// identity remain outside this module's interface.
type ProjectionAssembler struct {
	Fingerprint func(CatalogSnapshot) (string, error)
	Now         func() time.Time
}

// Assemble verifies the source twice: once against the catalog used for
// planning and once against the freshly collected catalog. A changed
// directory entry, summary page/version, or transcript generation therefore
// fails before a caller can publish the returned document.
func (a *ProjectionAssembler) Assemble(input StageFourAssemblyInput) (ProjectionDocument, error) {
	if a == nil || a.Fingerprint == nil {
		return ProjectionDocument{}, fmt.Errorf("training orchestration projection assembler fingerprint verifier is not configured")
	}
	if err := validateAssemblyCatalogs(input.InitialCatalog, input.LatestCatalog); err != nil {
		return ProjectionDocument{}, err
	}
	if strings.TrimSpace(input.Plan.SourceFingerprint) == "" {
		return ProjectionDocument{}, fmt.Errorf("stage-four plan source fingerprint is required")
	}
	if err := a.verifySourceFingerprint(input.InitialCatalog, input.Plan.SourceFingerprint, "initial"); err != nil {
		return ProjectionDocument{}, err
	}
	if err := a.verifySourceFingerprint(input.LatestCatalog, input.Plan.SourceFingerprint, "latest"); err != nil {
		return ProjectionDocument{}, err
	}

	plannedCatalog := input.InitialCatalog
	plannedCatalog.SourceFingerprint = input.Plan.SourceFingerprint
	if err := input.Plan.ValidateAgainst(plannedCatalog); err != nil {
		return ProjectionDocument{}, fmt.Errorf("validate stage-four plan: %w", err)
	}

	clusterByKey, err := assembleStageFourClusters(plannedCatalog, input.Plan, input.Materials, input.Drafts)
	if err != nil {
		return ProjectionDocument{}, err
	}
	clusters := make([]TopicCluster, 0, len(clusterByKey))
	for _, cluster := range clusterByKey {
		clusters = append(clusters, cluster)
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].ClusterID < clusters[j].ClusterID })

	relations, err := normalizeStageFourRelations(input.Relations)
	if err != nil {
		return ProjectionDocument{}, err
	}
	validationInput := stageFourValidationInput(plannedCatalog, input.Materials)
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	projection := Projection{
		SchemaVersion:              SchemaVersion,
		OwnerScopeID:               plannedCatalog.OwnerScopeID,
		SourceFingerprint:          input.Plan.SourceFingerprint,
		GeneratedAt:                now.Format(time.RFC3339Nano),
		RetrievalDegraded:          hasRetrievalDegradation(input.Materials),
		RetrievalDegradationReason: retrievalDegradationSummary(input.Materials),
		TopicClusters:              clusters,
		TopicClusterRelations:      relations,
	}
	selectedVideos := make(map[string]struct{})
	unitEvidence := make([]EvidenceRef, 0)
	for _, cluster := range clusters {
		for _, videoID := range cluster.SourceVideoIDs {
			selectedVideos[videoID] = struct{}{}
		}
		for _, stage := range cluster.Path.Stages {
			for _, unit := range stage.Units {
				unitEvidence = append(unitEvidence, unit.EvidenceRefs...)
			}
		}
	}
	projection.Statistics = buildStatistics(validationInput, clusters, selectedVideos, unitEvidence)
	normalizeProjectionKnowledgeFields(&projection)
	doc := ProjectionDocument{TrainingPathProjection: projection}
	if err := ValidateProjection(doc, validationInput); err != nil {
		return ProjectionDocument{}, fmt.Errorf("validate assembled stage-four projection: %w", err)
	}
	return doc, nil
}

func hasRetrievalDegradation(materials []ClusterMaterial) bool {
	for _, material := range materials {
		if material.RetrievalDegraded {
			return true
		}
	}
	return false
}

func retrievalDegradationSummary(materials []ClusterMaterial) string {
	seen := make(map[string]struct{})
	reasons := make([]string, 0)
	for _, material := range materials {
		if !material.RetrievalDegraded {
			continue
		}
		reason := strings.TrimSpace(material.RetrievalDegradationReason)
		if reason == "" {
			reason = "search_unavailable"
		}
		if _, ok := seen[reason]; ok {
			continue
		}
		seen[reason] = struct{}{}
		reasons = append(reasons, reason)
	}
	return strings.Join(reasons, ",")
}

func (a *ProjectionAssembler) verifySourceFingerprint(snapshot CatalogSnapshot, expected, label string) error {
	actual, err := a.Fingerprint(snapshot)
	if err != nil {
		return &GenerationError{Code: "fingerprint_failed", Err: fmt.Errorf("calculate %s catalog fingerprint: %w", label, err)}
	}
	if strings.TrimSpace(actual) == "" || actual != expected {
		return &GenerationError{Code: "source_changed", Err: fmt.Errorf("%s catalog fingerprint does not match the planned source", label)}
	}
	return nil
}

func validateAssemblyCatalogs(initial, latest CatalogSnapshot) error {
	if strings.TrimSpace(initial.ContractVersion) != PlanningContractVersion || strings.TrimSpace(latest.ContractVersion) != PlanningContractVersion {
		return fmt.Errorf("stage-four catalogs must use the planning contract")
	}
	if strings.TrimSpace(initial.OwnerScopeID) == "" || initial.OwnerScopeID != latest.OwnerScopeID {
		return fmt.Errorf("stage-four catalog owner scope changed")
	}
	return nil
}

func assembleStageFourClusters(snapshot CatalogSnapshot, plan PlanDraft, materials []ClusterMaterial, drafts []ClusterGenerationDraft) (map[string]TopicCluster, error) {
	materialsByKey := make(map[string]ClusterMaterial, len(materials))
	for index, material := range materials {
		key := strings.TrimSpace(material.ClusterKey)
		if key == "" {
			return nil, fmt.Errorf("stage-four material %d has an empty cluster key", index+1)
		}
		if _, duplicate := materialsByKey[key]; duplicate {
			return nil, fmt.Errorf("stage-four materials repeat cluster %s", key)
		}
		materialsByKey[key] = material
	}
	draftsByKey := make(map[string]ClusterGenerationDraft, len(drafts))
	for index, draft := range drafts {
		key := strings.TrimSpace(draft.ClusterKey)
		if key == "" {
			return nil, fmt.Errorf("stage-four draft %d has an empty cluster key", index+1)
		}
		if _, duplicate := draftsByKey[key]; duplicate {
			return nil, fmt.Errorf("stage-four drafts repeat cluster %s", key)
		}
		draftsByKey[key] = draft
	}

	result := make(map[string]TopicCluster)
	accepted := make(map[string]struct{})
	for _, cluster := range plan.TopicClusters {
		if cluster.ReviewStatus != PlanAccepted {
			continue
		}
		accepted[cluster.ClusterKey] = struct{}{}
		material, ok := materialsByKey[cluster.ClusterKey]
		if !ok {
			return nil, fmt.Errorf("accepted cluster %s has no material", cluster.ClusterKey)
		}
		draft, ok := draftsByKey[cluster.ClusterKey]
		if !ok {
			return nil, fmt.Errorf("accepted cluster %s has no generation draft", cluster.ClusterKey)
		}
		assembled, err := AssembleClusterGenerationDraft(snapshot, cluster, draft, material)
		if err != nil {
			return nil, fmt.Errorf("assemble cluster %s: %w", cluster.ClusterKey, err)
		}
		result[cluster.ClusterKey] = assembled
	}
	for key := range materialsByKey {
		if _, ok := accepted[key]; !ok {
			return nil, fmt.Errorf("stage-four material %s does not belong to an accepted cluster", key)
		}
	}
	for key := range draftsByKey {
		if _, ok := accepted[key]; !ok {
			return nil, fmt.Errorf("stage-four draft %s does not belong to an accepted cluster", key)
		}
	}
	return result, nil
}

func normalizeStageFourRelations(relations []TopicClusterRelation) ([]TopicClusterRelation, error) {
	byKey := make(map[string]TopicClusterRelation, len(relations))
	for _, relation := range relations {
		current := relation
		if current.RelationType == "complementary" || current.RelationType == "contrast" {
			if current.SourceClusterID > current.TargetClusterID {
				current.SourceClusterID, current.TargetClusterID = current.TargetClusterID, current.SourceClusterID
				current.SourceEvidenceRefs, current.TargetEvidenceRefs = current.TargetEvidenceRefs, current.SourceEvidenceRefs
			}
		}
		key := current.RelationType + "\x00" + current.SourceClusterID + "\x00" + current.TargetClusterID
		if existing, duplicate := byKey[key]; duplicate {
			if relationPreferred(current, existing) {
				byKey[key] = current
			}
			continue
		}
		byKey[key] = current
	}
	result := make([]TopicClusterRelation, 0, len(byKey))
	for _, relation := range byKey {
		result = append(result, relation)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SourceClusterID != result[j].SourceClusterID {
			return result[i].SourceClusterID < result[j].SourceClusterID
		}
		if result[i].TargetClusterID != result[j].TargetClusterID {
			return result[i].TargetClusterID < result[j].TargetClusterID
		}
		return result[i].RelationType < result[j].RelationType
	})
	for index := range result {
		result[index].RelationID = fmt.Sprintf("cluster-relation-%03d", index+1)
	}
	if hasRequiredBeforeCycle(result) {
		return nil, fmt.Errorf("required_before relations contain a cycle")
	}
	return result, nil
}

func relationPreferred(candidate, existing TopicClusterRelation) bool {
	if candidate.Confidence != existing.Confidence {
		return candidate.Confidence > existing.Confidence
	}
	if candidate.Summary != existing.Summary {
		return candidate.Summary < existing.Summary
	}
	return relationEvidenceSortKey(candidate) < relationEvidenceSortKey(existing)
}

func relationEvidenceSortKey(relation TopicClusterRelation) string {
	parts := make([]string, 0, len(relation.SourceEvidenceRefs)+len(relation.TargetEvidenceRefs))
	for _, ref := range append(append([]EvidenceRef(nil), relation.SourceEvidenceRefs...), relation.TargetEvidenceRefs...) {
		parts = append(parts, ref.VideoID+"\x00"+ref.TranscriptGeneration+"\x00"+ref.EvidenceID)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x00")
}

func stageFourValidationInput(snapshot CatalogSnapshot, materials []ClusterMaterial) InputPackage {
	input := InputPackage{
		SchemaVersion:     SchemaVersion,
		OwnerScopeID:      snapshot.OwnerScopeID,
		ScannedVideos:     len(snapshot.Videos) + len(snapshot.SkippedVideos),
		QualifiedVideos:   make([]VideoTopicProfile, 0, len(snapshot.Videos)),
		SkippedVideos:     append([]SkippedVideo(nil), snapshot.SkippedVideos...),
		SkipReasonCounts:  make(map[SkipReason]int),
		TopicSourceCounts: map[TopicSource]int{TopicSourceFinalSummary: 0, TopicSourceNormalizedTranscript: 0},
	}
	for _, reason := range []SkipReason{SkipProcessing, SkipProcessingFailed, SkipFormalContentNotReady, SkipFormalContentValidationFailed, SkipEvidenceMissing, SkipInaccessible} {
		input.SkipReasonCounts[reason] = 0
	}
	for _, skipped := range snapshot.SkippedVideos {
		input.SkipReasonCounts[skipped.Reason]++
	}
	evidenceByVideo := make(map[string][]EvidenceSignal)
	for _, material := range materials {
		for _, evidence := range material.Evidence {
			evidenceByVideo[evidence.VideoID] = append(evidenceByVideo[evidence.VideoID], EvidenceSignal{
				VideoID: evidence.VideoID, TranscriptGeneration: evidence.TranscriptGeneration,
				EvidenceID: evidence.EvidenceID, StartMs: evidence.StartMs, EndMs: evidence.EndMs,
			})
		}
	}
	for _, video := range snapshot.Videos {
		source := TopicSourceNormalizedTranscript
		if video.OrchestrationProfile != nil {
			source = TopicSourceFinalSummary
		}
		input.TopicSourceCounts[source]++
		input.QualifiedVideos = append(input.QualifiedVideos, VideoTopicProfile{
			VideoID: video.VideoID, Title: video.Title, VideoType: video.VideoType,
			DurationSeconds: video.DurationSeconds, TranscriptGeneration: video.TranscriptGeneration,
			TopicSource: source, EvidenceSignals: dedupeEvidenceSignals(evidenceByVideo[video.VideoID]),
		})
	}
	return input
}

func dedupeEvidenceSignals(signals []EvidenceSignal) []EvidenceSignal {
	result := make([]EvidenceSignal, 0, len(signals))
	seen := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		key := evidenceKey(signal.VideoID, signal.TranscriptGeneration, signal.EvidenceID)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, signal)
	}
	return result
}

// StageFourSnapshotReader is the source recheck adapter used after all model
// calls complete and immediately before assembly.
type StageFourSnapshotReader interface {
	CollectCatalog(context.Context) (CatalogSnapshot, error)
}

type StageFourMaterializer interface {
	Materialize(context.Context, CatalogSnapshot, PlanDraft) ([]ClusterMaterial, error)
}

type StageFourMaterialRetriever interface {
	Retrieve(context.Context, PlanCluster, ClusterMaterial) (ClusterMaterial, error)
}

type StageFourClusterGenerator interface {
	Generate(context.Context, PlanCluster, ClusterMaterial) (ClusterGenerationDraft, error)
	SplitMaterial(PlanCluster, ClusterMaterial) ([]ClusterGenerationPart, error)
}

type StageFourRelationGenerator interface {
	Generate(context.Context, []TopicCluster) ([]TopicClusterRelation, error)
}

// StageFourOrchestrationConfig is the internal runtime configuration for the
// two-stage pipeline. It is applied only when the corresponding concrete
// module is supplied; Service owns the HTTP job and publication contract.
type StageFourOrchestrationConfig struct {
	PlanningMaxInputTokens          int
	PlanningMergeMaxInputTokens     int
	PlanningMaxVideosPerBatch       int
	PlanningMaxMergeItems           int
	ClusterGenerationMaxInputTokens int
	UnitMergeMaxInputTokens         int // reserved: unit merge is programmatic and makes no model call
	RelationMaxInputTokens          int
	RelationBatchMaxInputTokens     int
	RequestTimeout                  time.Duration
	TaskTimeout                     time.Duration
	MaxConcurrentCalls              int
	MaxRecursionDepth               int
	MaxTotalCalls                   int
	MaxTotalInputTokens             int
}

// StageFourOrchestrator is an internal, non-publishing pipeline. Service wires
// it into the existing HTTP task entry point and owns publication side effects.
type StageFourOrchestrator struct {
	Planner           Planner
	Materializer      StageFourMaterializer
	MaterialRetriever StageFourMaterialRetriever
	ClusterGenerator  StageFourClusterGenerator
	RelationGenerator StageFourRelationGenerator
	SnapshotReader    StageFourSnapshotReader
	Assembler         StageFourProjectionAssembler
	Gate              *CompletionGate
	MaxClusterParts   int
	MaxClusterRetries int
	MaxRecursionDepth int
	TaskTimeout       time.Duration
	Config            StageFourOrchestrationConfig
}

func (o *StageFourOrchestrator) Run(ctx context.Context, snapshot CatalogSnapshot) (ProjectionDocument, error) {
	if o == nil || o.Planner == nil || o.Materializer == nil || o.ClusterGenerator == nil || o.RelationGenerator == nil || o.SnapshotReader == nil || o.Assembler == nil {
		return ProjectionDocument{}, fmt.Errorf("stage-four orchestration dependencies are not configured")
	}
	o.applyConfig()
	if o.TaskTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.TaskTimeout)
		defer cancel()
	}
	if o.Gate != nil {
		runGate := o.Gate.NewRun()
		installCompletionGate(o.Planner, runGate)
		installCompletionGate(o.ClusterGenerator, runGate)
		installCompletionGate(o.RelationGenerator, runGate)
	}
	plan, err := o.Planner.Plan(ctx, snapshot)
	if err != nil {
		return ProjectionDocument{}, stageFourError("planning", err)
	}
	plannedCatalog := snapshot
	plannedCatalog.SourceFingerprint = plan.SourceFingerprint
	materials, err := o.Materializer.Materialize(ctx, plannedCatalog, plan)
	if err != nil {
		return ProjectionDocument{}, stageFourError("materialization", err)
	}
	materialByKey := make(map[string]ClusterMaterial, len(materials))
	materialIndexByKey := make(map[string]int, len(materials))
	for index, material := range materials {
		materialByKey[material.ClusterKey] = material
		materialIndexByKey[material.ClusterKey] = index
	}
	drafts := make([]ClusterGenerationDraft, 0)
	clusters := make([]TopicCluster, 0)
	for _, cluster := range plan.TopicClusters {
		if cluster.ReviewStatus != PlanAccepted {
			continue
		}
		material, ok := materialByKey[cluster.ClusterKey]
		if !ok {
			return ProjectionDocument{}, fmt.Errorf("stage-four accepted cluster %s has no material", cluster.ClusterKey)
		}
		if o.MaterialRetriever != nil {
			material, err = o.MaterialRetriever.Retrieve(ctx, cluster, material)
			if err != nil {
				return ProjectionDocument{}, stageFourError(fmt.Sprintf("cluster %s retrieval", cluster.ClusterKey), err)
			}
			materialByKey[cluster.ClusterKey] = material
			materials[materialIndexByKey[cluster.ClusterKey]] = material
		}
		parts, err := o.clusterParts(cluster, material)
		if err != nil {
			return ProjectionDocument{}, stageFourError(fmt.Sprintf("cluster %s split", cluster.ClusterKey), err)
		}
		generatedDrafts := make([]ClusterGenerationDraft, 0, len(parts))
		for _, part := range parts {
			partDrafts, err := o.generateClusterPartsWithRetry(ctx, cluster, part)
			if err != nil {
				return ProjectionDocument{}, stageFourError(fmt.Sprintf("cluster %s generation", cluster.ClusterKey), err)
			}
			generatedDrafts = append(generatedDrafts, partDrafts...)
		}
		draft, err := mergeClusterGenerationDrafts(cluster, generatedDrafts)
		if err != nil {
			return ProjectionDocument{}, stageFourError(fmt.Sprintf("cluster %s draft merge", cluster.ClusterKey), err)
		}
		drafts = append(drafts, draft)
		assembled, err := AssembleClusterGenerationDraft(plannedCatalog, cluster, draft, material)
		if err != nil {
			return ProjectionDocument{}, stageFourError(fmt.Sprintf("cluster %s assembly", cluster.ClusterKey), err)
		}
		clusters = append(clusters, assembled)
	}
	relations, err := o.RelationGenerator.Generate(ctx, clusters)
	if err != nil {
		return ProjectionDocument{}, stageFourError("relation generation", err)
	}
	latest, err := o.SnapshotReader.CollectCatalog(ctx)
	if err != nil {
		return ProjectionDocument{}, stageFourError("source recheck", err)
	}
	return o.Assembler.Assemble(StageFourAssemblyInput{
		InitialCatalog: plannedCatalog, LatestCatalog: latest, Plan: plan,
		Materials: materials, Drafts: drafts, Relations: relations,
	})
}

func (o *StageFourOrchestrator) generateClusterPartsWithRetry(ctx context.Context, cluster PlanCluster, part ClusterGenerationPart) ([]ClusterGenerationDraft, error) {
	maxRetries := o.MaxClusterRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	for attempt := 0; ; attempt++ {
		drafts, err := o.generateClusterParts(ctx, cluster, part, 0)
		if err == nil {
			return drafts, nil
		}
		if attempt >= maxRetries || !retryableClusterGenerationError(err) {
			return nil, err
		}
	}
}

func retryableClusterGenerationError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var capacityErr *InputCapacityError
	if errors.As(err, &capacityErr) {
		return false
	}
	var generationErr *GenerationError
	if !errors.As(err, &generationErr) {
		return false
	}
	switch generationErr.Code {
	case "model_output_truncated", "model_output_invalid", "invalid_reference", "model_transport_failed":
		return true
	default:
		return false
	}
}

func stageFourError(stage string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &GenerationError{Code: "timeout", Err: fmt.Errorf("stage-four %s: %w", stage, err)}
	}
	return fmt.Errorf("stage-four %s: %w", stage, err)
}

func (o *StageFourOrchestrator) applyConfig() {
	config := o.Config
	if planner, ok := o.Planner.(*StageOnePlanner); ok {
		if config.PlanningMaxInputTokens > 0 {
			planner.Config.MaxInputTokens = config.PlanningMaxInputTokens
		}
		if config.PlanningMergeMaxInputTokens > 0 {
			planner.Config.MergeMaxInputTokens = config.PlanningMergeMaxInputTokens
		}
		if config.PlanningMaxVideosPerBatch > 0 {
			planner.Config.MaxVideosPerBatch = config.PlanningMaxVideosPerBatch
		}
		if config.PlanningMaxMergeItems > 0 {
			planner.Config.MaxMergeItems = config.PlanningMaxMergeItems
		}
	}
	if generator, ok := o.ClusterGenerator.(*ClusterGenerator); ok && config.ClusterGenerationMaxInputTokens > 0 {
		generator.MaxInputTokens = config.ClusterGenerationMaxInputTokens
	}
	if generator, ok := o.RelationGenerator.(*RelationGenerator); ok {
		if config.RelationMaxInputTokens > 0 {
			generator.MaxInputTokens = config.RelationMaxInputTokens
		}
		if config.RelationBatchMaxInputTokens > 0 {
			generator.BatchMaxInputTokens = config.RelationBatchMaxInputTokens
		}
	}
	if o.MaxRecursionDepth <= 0 && config.MaxRecursionDepth > 0 {
		o.MaxRecursionDepth = config.MaxRecursionDepth
	}
	if o.MaxClusterRetries == 0 {
		o.MaxClusterRetries = defaultStageFourClusterRetries
	} else if o.MaxClusterRetries < 0 {
		o.MaxClusterRetries = 0
	}
	if o.TaskTimeout <= 0 && config.TaskTimeout > 0 {
		o.TaskTimeout = config.TaskTimeout
	}
	if o.Gate == nil {
		o.Gate = NewCompletionGate(CompletionGateConfig{
			RequestTimeout:      config.RequestTimeout,
			MaxConcurrent:       config.MaxConcurrentCalls,
			MaxTotalCalls:       config.MaxTotalCalls,
			MaxTotalInputTokens: config.MaxTotalInputTokens,
		})
	}
}

type completionGateSetter interface {
	SetCompletionGate(*CompletionGate)
}

func installCompletionGate(value any, gate *CompletionGate) {
	if setter, ok := value.(completionGateSetter); ok {
		setter.SetCompletionGate(gate)
	}
}

func (o *StageFourOrchestrator) clusterParts(cluster PlanCluster, material ClusterMaterial) ([]ClusterGenerationPart, error) {
	parts, err := o.ClusterGenerator.SplitMaterial(cluster, material)
	if err != nil {
		return nil, err
	}
	maxParts := o.MaxClusterParts
	if maxParts <= 0 {
		maxParts = 32
	}
	if len(parts) > maxParts {
		return nil, fmt.Errorf("cluster material split exceeds %d parts", maxParts)
	}
	return parts, nil
}

func (o *StageFourOrchestrator) generateClusterParts(ctx context.Context, original PlanCluster, part ClusterGenerationPart, depth int) ([]ClusterGenerationDraft, error) {
	draft, err := o.ClusterGenerator.Generate(ctx, part.Cluster, part.Material)
	if err == nil {
		return []ClusterGenerationDraft{draft}, nil
	}
	if !isShrinkableGenerationError(err) {
		return nil, err
	}
	maxDepth := o.MaxRecursionDepth
	if maxDepth <= 0 {
		maxDepth = 8
	}
	if depth >= maxDepth {
		return nil, &GenerationError{Code: "material_split_depth_exceeded", Err: fmt.Errorf("cluster %s material split depth exceeds %d", original.ClusterKey, maxDepth)}
	}
	parts := []ClusterGenerationPart(nil)
	var splitErr error
	if retrySplitter, ok := o.ClusterGenerator.(interface {
		SplitMaterialForRetry(PlanCluster, ClusterMaterial) ([]ClusterGenerationPart, error)
	}); ok {
		parts, splitErr = retrySplitter.SplitMaterialForRetry(part.Cluster, part.Material)
	} else {
		parts, splitErr = o.ClusterGenerator.SplitMaterial(part.Cluster, part.Material)
	}
	if splitErr != nil {
		return nil, splitErr
	}
	if len(parts) <= 1 {
		return nil, err
	}
	result := make([]ClusterGenerationDraft, 0, len(parts))
	for _, child := range parts {
		childDrafts, childErr := o.generateClusterParts(ctx, original, child, depth+1)
		if childErr != nil {
			return nil, childErr
		}
		result = append(result, childDrafts...)
	}
	return result, nil
}
