package trainingorchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
	"github.com/google/uuid"
)

//go:embed prompts/training-orchestration-planning-v1.txt
var stageOnePlanningPrompt string

//go:embed prompts/training-orchestration-planning-merge-v1.txt
var stageOneMergePrompt string

const (
	defaultPlanningMaxInputTokens = 450000
	defaultPlanningBatchTokens    = 35000
	defaultPlanningMergeTokens    = 35000
	defaultPlanningBatchVideos    = 5
	defaultPlanningMergeCalls     = 32
	defaultPlanningMergeItems     = 20
	defaultPlanningMergeDepth     = 8
	minAcceptedPlanConfidence     = 0.60
)

type Planner interface {
	Plan(context.Context, CatalogSnapshot) (PlanDraft, error)
}

type PlannerConfig struct {
	MaxInputTokens      int
	BatchMaxInputTokens int
	MergeMaxInputTokens int
	MaxVideosPerBatch   int
	MaxMergeCalls       int
	MaxMergeItems       int
	MaxMergeDepth       int
}

type StageOnePlanner struct {
	LLM           CompletionClient
	Gate          *CompletionGate
	Config        PlannerConfig
	PromptVersion string
	RequestID     func() string
}

type planningModelOutput struct {
	TopicClusters    []PlanCluster     `json:"topic_clusters"`
	UnselectedVideos []UnselectedVideo `json:"unselected_videos"`
}

type planningEnvelope struct {
	ContractVersion   string          `json:"contract_version"`
	RequestID         string          `json:"request_id"`
	SourceFingerprint string          `json:"source_fingerprint"`
	Input             planningCatalog `json:"input"`
	Limits            planningLimits  `json:"limits"`
}

type planningLimits struct {
	MaxOutputItems  int `json:"max_items"`
	MaxOutputTokens int `json:"max_output_tokens"`
}

type planningCatalog struct {
	ContractVersion   string                 `json:"contract_version"`
	OwnerScopeID      string                 `json:"owner_scope_id"`
	SourceFingerprint string                 `json:"source_fingerprint"`
	Videos            []planningCatalogVideo `json:"videos"`
	SkippedVideos     []SkippedVideo         `json:"skipped_videos"`
}

type planningCatalogVideo struct {
	VideoID              string                  `json:"video_id"`
	Title                string                  `json:"title"`
	VideoType            string                  `json:"video_type"`
	DurationSeconds      int                     `json:"duration_seconds"`
	TranscriptGeneration string                  `json:"transcript_generation"`
	SummaryWikiPageID    string                  `json:"summary_wiki_page_id,omitempty"`
	SummaryVersion       int                     `json:"summary_version,omitempty"`
	OrchestrationProfile *planningProfile        `json:"orchestration_profile,omitempty"`
	CompatibilityProfile *planningProfile        `json:"compatibility_profile,omitempty"`
	AllowedReferences    planningAllowedRefsHint `json:"allowed_references"`
}

type planningProfile struct {
	SchemaVersion int                 `json:"schemaVersion"`
	PrimaryTopic  string              `json:"primaryTopic"`
	TopicUnits    []planningTopicUnit `json:"topicUnits"`
}

type planningTopicUnit struct {
	Title            string   `json:"title"`
	Abstract         string   `json:"abstract"`
	ContentForms     []string `json:"contentForms"`
	LearningOutcomes []string `json:"learningOutcomes"`
	SummaryBlockIDs  []string `json:"summaryBlockIds"`
	EvidenceIDs      []string `json:"evidence_ids,omitempty"`
}

type planningAllowedRefsHint struct {
	SummaryBlockIDs []string `json:"summary_block_ids"`
	EvidenceIDs     []string `json:"evidence_ids"`
}

func (p *StageOnePlanner) SetCompletionGate(gate *CompletionGate) {
	if p != nil {
		p.Gate = gate
	}
}

func (p *StageOnePlanner) Plan(ctx context.Context, snapshot CatalogSnapshot) (PlanDraft, error) {
	if p == nil || p.LLM == nil {
		return PlanDraft{}, fmt.Errorf("training orchestration planner llm is not configured")
	}
	if strings.TrimSpace(snapshot.ContractVersion) != PlanningContractVersion {
		return PlanDraft{}, fmt.Errorf("unsupported catalog contract version %q", snapshot.ContractVersion)
	}
	if strings.TrimSpace(snapshot.OwnerScopeID) == "" {
		return PlanDraft{}, fmt.Errorf("catalog owner scope is required")
	}
	if err := validateCatalogVideos(snapshot); err != nil {
		return PlanDraft{}, err
	}
	snapshot = normalizeCatalog(snapshot)
	fingerprint, err := CatalogFingerprint(snapshot, p.LLM.Model(), p.promptVersion())
	if err != nil {
		return PlanDraft{}, err
	}
	snapshot.SourceFingerprint = fingerprint
	if len(snapshot.Videos) == 0 {
		return PlanDraft{ContractVersion: PlanningContractVersion, SourceFingerprint: fingerprint, TopicClusters: []PlanCluster{}, UnselectedVideos: []UnselectedVideo{}}, nil
	}
	config, err := p.config()
	if err != nil {
		return PlanDraft{}, err
	}
	batches, totalTokens, err := p.pack(snapshot, config)
	if err != nil {
		return PlanDraft{}, err
	}
	if totalTokens > config.MaxInputTokens {
		return PlanDraft{}, &InputCapacityError{Tokens: totalTokens, Limit: config.MaxInputTokens}
	}
	if p.Gate == nil {
		p.Gate = NewCompletionGate(CompletionGateConfig{})
	}
	remainingBudget := config.MaxInputTokens

	clusters := make([]PlanCluster, 0)
	unselected := make([]UnselectedVideo, 0)
	pending := append([]CatalogSnapshot(nil), batches...)
	batchNumber := 0
	for len(pending) > 0 {
		batch := pending[0]
		pending = pending[1:]
		batchNumber++
		prompt, promptErr := p.buildPrompt(batch, fingerprint, p.requestID("batch"), config)
		if promptErr != nil {
			return PlanDraft{}, promptErr
		}
		output, callErr := p.completeWithCorrection(ctx, prompt, batch, config, &remainingBudget, fmt.Sprintf("batch-%03d", batchNumber))
		if callErr != nil {
			if isShrinkableGenerationError(callErr) && len(batch.Videos) > 1 {
				middle := len(batch.Videos) / 2
				left, right := splitCatalogSnapshot(batch, middle)
				pending = append([]CatalogSnapshot{left, right}, pending...)
				batchNumber--
				continue
			}
			return PlanDraft{}, callErr
		}
		clusters = append(clusters, output.TopicClusters...)
		unselected = append(unselected, output.UnselectedVideos...)
	}

	clusters, err = p.merge(ctx, snapshot, clusters, fingerprint, config, &remainingBudget)
	if err != nil {
		return PlanDraft{}, err
	}
	draft := PlanDraft{ContractVersion: PlanningContractVersion, SourceFingerprint: fingerprint, TopicClusters: clusters, UnselectedVideos: unselected}
	draft = normalizePlan(draft, snapshot)
	if err := draft.ValidateAgainst(snapshot); err != nil {
		return PlanDraft{}, &GenerationError{Code: "planning_output_invalid", Err: err}
	}
	return draft, nil
}

func (p *StageOnePlanner) config() (PlannerConfig, error) {
	c := p.Config
	if c.MaxInputTokens == 0 {
		c.MaxInputTokens = defaultPlanningMaxInputTokens
	}
	if c.MaxInputTokens < 0 {
		return PlannerConfig{}, fmt.Errorf("training orchestration planning input limit must be non-negative")
	}
	if c.BatchMaxInputTokens <= 0 {
		c.BatchMaxInputTokens = defaultPlanningBatchTokens
	}
	if c.BatchMaxInputTokens > defaultPlanningBatchTokens {
		return PlannerConfig{}, fmt.Errorf("planning batch input limit cannot exceed %d", defaultPlanningBatchTokens)
	}
	if c.MergeMaxInputTokens <= 0 {
		c.MergeMaxInputTokens = defaultPlanningMergeTokens
	}
	if c.MergeMaxInputTokens > defaultPlanningMergeTokens {
		return PlannerConfig{}, fmt.Errorf("planning merge input limit cannot exceed %d", defaultPlanningMergeTokens)
	}
	if c.MaxVideosPerBatch <= 0 {
		c.MaxVideosPerBatch = defaultPlanningBatchVideos
	}
	if c.MaxVideosPerBatch > defaultPlanningBatchVideos {
		return PlannerConfig{}, fmt.Errorf("planning batch video limit cannot exceed %d", defaultPlanningBatchVideos)
	}
	if c.MaxMergeCalls <= 0 {
		c.MaxMergeCalls = defaultPlanningMergeCalls
	}
	if c.MaxMergeItems <= 0 {
		c.MaxMergeItems = defaultPlanningMergeItems
	}
	if c.MaxMergeItems > defaultPlanningMergeItems {
		return PlannerConfig{}, fmt.Errorf("planning merge item limit cannot exceed %d", defaultPlanningMergeItems)
	}
	if c.MaxMergeDepth <= 0 {
		c.MaxMergeDepth = defaultPlanningMergeDepth
	}
	return c, nil
}

func (p *StageOnePlanner) pack(snapshot CatalogSnapshot, config PlannerConfig) ([]CatalogSnapshot, int, error) {
	result := make([]CatalogSnapshot, 0, (len(snapshot.Videos)+config.MaxVideosPerBatch-1)/config.MaxVideosPerBatch)
	total := 0
	for index := 0; index < len(snapshot.Videos); {
		end := index
		for end < len(snapshot.Videos) && end-index < config.MaxVideosPerBatch {
			candidate := snapshot
			candidate.Videos = append([]CatalogVideo(nil), snapshot.Videos[index:end+1]...)
			prompt, err := p.buildPrompt(candidate, snapshot.SourceFingerprint, fmt.Sprintf("estimate-%03d", len(result)+1), config)
			if err != nil {
				return nil, 0, err
			}
			count, err := estimateTokens(prompt)
			if err != nil {
				return nil, 0, err
			}
			if count > config.BatchMaxInputTokens {
				if end == index {
					return nil, 0, &InputCapacityError{Tokens: count, Limit: config.BatchMaxInputTokens}
				}
				break
			}
			end++
		}
		batch := snapshot
		batch.Videos = append([]CatalogVideo(nil), snapshot.Videos[index:end]...)
		prompt, err := p.buildPrompt(batch, snapshot.SourceFingerprint, fmt.Sprintf("estimate-%03d", len(result)+1), config)
		if err != nil {
			return nil, 0, err
		}
		count, err := estimateTokens(prompt)
		if err != nil {
			return nil, 0, err
		}
		total += count
		result = append(result, batch)
		index = end
	}
	return result, total, nil
}

func (p *StageOnePlanner) merge(ctx context.Context, snapshot CatalogSnapshot, clusters []PlanCluster, fingerprint string, config PlannerConfig, budget *int) ([]PlanCluster, error) {
	if len(clusters) <= 1 {
		return normalizeClusters(clusters), nil
	}
	mergeCalls := 0
	mergeDepth := 0
	for len(clusters) > 1 {
		mergeDepth++
		if mergeDepth > config.MaxMergeDepth {
			return nil, &GenerationError{Code: "planning_merge_depth_exceeded", Err: fmt.Errorf("planning merge depth exceeds %d", config.MaxMergeDepth)}
		}
		if mergeCalls >= config.MaxMergeCalls {
			return nil, &GenerationError{Code: "planning_merge_limit", Err: fmt.Errorf("planning merge calls exceed %d", config.MaxMergeCalls)}
		}
		groups := make([][]PlanCluster, 0, (len(clusters)+config.MaxMergeItems-1)/config.MaxMergeItems)
		for index := 0; index < len(clusters); index += config.MaxMergeItems {
			end := index + config.MaxMergeItems
			if end > len(clusters) {
				end = len(clusters)
			}
			groups = append(groups, clusters[index:end])
		}
		next := make([]PlanCluster, 0, len(clusters))
		pendingGroups := append([][]PlanCluster(nil), groups...)
		for len(pendingGroups) > 0 {
			group := pendingGroups[0]
			pendingGroups = pendingGroups[1:]
			if len(group) == 1 {
				next = append(next, group[0])
				continue
			}
			if mergeCalls >= config.MaxMergeCalls {
				return nil, &GenerationError{Code: "planning_merge_limit", Err: fmt.Errorf("planning merge calls exceed %d", config.MaxMergeCalls)}
			}
			input := planningMergeInput{ContractVersion: PlanningContractVersion, SourceFingerprint: fingerprint, Clusters: group}
			prompt, err := p.buildMergePrompt(input, p.requestID("merge"), config)
			if err != nil {
				return nil, err
			}
			mergeCalls++
			output, err := p.completeMergeWithCorrection(ctx, prompt, input, snapshot, config, budget, fmt.Sprintf("merge-%03d", mergeCalls))
			if err != nil {
				if isShrinkableGenerationError(err) && len(group) > 1 {
					middle := len(group) / 2
					pendingGroups = append([][]PlanCluster{group[:middle], group[middle:]}, pendingGroups...)
					continue
				}
				return nil, err
			}
			next = append(next, output.TopicClusters...)
		}
		if len(next) == 0 {
			return nil, &GenerationError{Code: "planning_output_invalid", Err: fmt.Errorf("planning merge returned no clusters")}
		}
		clusters = normalizeClusters(next)
		if len(clusters) >= len(groupedClusters(groups)) {
			break
		}
	}
	return clusters, nil
}

func splitCatalogSnapshot(snapshot CatalogSnapshot, middle int) (CatalogSnapshot, CatalogSnapshot) {
	left := snapshot
	right := snapshot
	left.Videos = append([]CatalogVideo(nil), snapshot.Videos[:middle]...)
	right.Videos = append([]CatalogVideo(nil), snapshot.Videos[middle:]...)
	return left, right
}

func groupedClusters(groups [][]PlanCluster) []PlanCluster {
	result := make([]PlanCluster, 0)
	for _, group := range groups {
		result = append(result, group...)
	}
	return result
}

type planningMergeInput struct {
	ContractVersion   string        `json:"contract_version"`
	SourceFingerprint string        `json:"source_fingerprint"`
	Clusters          []PlanCluster `json:"clusters"`
}

func (p *StageOnePlanner) buildPrompt(snapshot CatalogSnapshot, fingerprint, requestID string, config PlannerConfig) (string, error) {
	envelope := planningEnvelope{ContractVersion: PlanningContractVersion, RequestID: requestID, SourceFingerprint: fingerprint, Input: planningCatalogFromSnapshot(snapshot), Limits: planningLimits{MaxOutputItems: 20, MaxOutputTokens: 8192}}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode planning prompt: %w", err)
	}
	return strings.TrimSpace(stageOnePlanningPrompt) + "\n\nINPUT:\n" + string(raw), nil
}

func planningCatalogFromSnapshot(snapshot CatalogSnapshot) planningCatalog {
	result := planningCatalog{
		ContractVersion:   snapshot.ContractVersion,
		OwnerScopeID:      snapshot.OwnerScopeID,
		SourceFingerprint: snapshot.SourceFingerprint,
		SkippedVideos:     append([]SkippedVideo(nil), snapshot.SkippedVideos...),
		Videos:            make([]planningCatalogVideo, 0, len(snapshot.Videos)),
	}
	for _, video := range snapshot.Videos {
		view := planningCatalogVideo{
			VideoID:              video.VideoID,
			Title:                video.Title,
			VideoType:            video.VideoType,
			DurationSeconds:      video.DurationSeconds,
			TranscriptGeneration: video.TranscriptGeneration,
			SummaryWikiPageID:    video.SummaryWikiPageID,
			SummaryVersion:       video.SummaryVersion,
			OrchestrationProfile: planningProfileFromSummary(video.OrchestrationProfile),
			CompatibilityProfile: planningProfileFromSummary(video.CompatibilityProfile),
		}
		profile, err := video.routingProfile()
		if err == nil {
			blocks, evidence := orchestrationProfileReferences(profile)
			view.AllowedReferences = planningAllowedRefsHint{
				SummaryBlockIDs: sortedMapKeys(blocks),
				EvidenceIDs:     sortedMapKeys(evidence),
			}
		}
		result.Videos = append(result.Videos, view)
	}
	return result
}

func planningProfileFromSummary(profile *summary.OrchestrationProfile) *planningProfile {
	if profile == nil {
		return nil
	}
	view := &planningProfile{
		SchemaVersion: profile.SchemaVersion,
		PrimaryTopic:  profile.PrimaryTopic,
		TopicUnits:    make([]planningTopicUnit, 0, len(profile.TopicUnits)),
	}
	for _, unit := range profile.TopicUnits {
		topicUnit := planningTopicUnit{
			Title:            unit.Title,
			Abstract:         unit.Abstract,
			ContentForms:     append([]string(nil), unit.ContentForms...),
			LearningOutcomes: append([]string(nil), unit.LearningOutcomes...),
			SummaryBlockIDs:  append([]string(nil), unit.SummaryBlockIDs...),
			EvidenceIDs:      make([]string, 0, len(unit.EvidenceRefs)),
		}
		for _, ref := range unit.EvidenceRefs {
			if id := strings.TrimSpace(ref.EvidenceSentenceID); id != "" {
				topicUnit.EvidenceIDs = append(topicUnit.EvidenceIDs, id)
			}
		}
		view.TopicUnits = append(view.TopicUnits, topicUnit)
	}
	return view
}

func buildPlanningCorrectionPrompt(prompt string, validationErr error, snapshot CatalogSnapshot) string {
	return prompt + "\n\nCORRECTION:\n" + validationErr.Error() + "\n" + planningAllowedReferencesPrompt(snapshot) + "\nReturn one corrected JSON object only."
}

func planningAllowedReferencesPrompt(snapshot CatalogSnapshot) string {
	raw, err := json.Marshal(struct {
		AllowedReferences []struct {
			VideoID              string   `json:"video_id"`
			SummaryBlockIDs      []string `json:"summary_block_ids"`
			EvidenceIDs          []string `json:"evidence_ids"`
			TranscriptGeneration string   `json:"transcript_generation"`
			SummaryWikiPageID    string   `json:"summary_wiki_page_id,omitempty"`
			SummaryVersion       int      `json:"summary_version,omitempty"`
		} `json:"allowed_references"`
	}{AllowedReferences: planningAllowedReferences(snapshot)})
	if err != nil {
		return "Use only evidence_ids from INPUT videos[].allowed_references.evidence_ids."
	}
	return "Use only these allowed IDs. material_requests[].evidence_ids must copy the matching video's allowed_references.evidence_ids exactly; never use chunk IDs: " + string(raw)
}

func planningAllowedReferences(snapshot CatalogSnapshot) []struct {
	VideoID              string   `json:"video_id"`
	SummaryBlockIDs      []string `json:"summary_block_ids"`
	EvidenceIDs          []string `json:"evidence_ids"`
	TranscriptGeneration string   `json:"transcript_generation"`
	SummaryWikiPageID    string   `json:"summary_wiki_page_id,omitempty"`
	SummaryVersion       int      `json:"summary_version,omitempty"`
} {
	result := make([]struct {
		VideoID              string   `json:"video_id"`
		SummaryBlockIDs      []string `json:"summary_block_ids"`
		EvidenceIDs          []string `json:"evidence_ids"`
		TranscriptGeneration string   `json:"transcript_generation"`
		SummaryWikiPageID    string   `json:"summary_wiki_page_id,omitempty"`
		SummaryVersion       int      `json:"summary_version,omitempty"`
	}, 0, len(snapshot.Videos))
	for _, video := range snapshot.Videos {
		profile, err := video.routingProfile()
		if err != nil {
			continue
		}
		blocks, evidence := orchestrationProfileReferences(profile)
		result = append(result, struct {
			VideoID              string   `json:"video_id"`
			SummaryBlockIDs      []string `json:"summary_block_ids"`
			EvidenceIDs          []string `json:"evidence_ids"`
			TranscriptGeneration string   `json:"transcript_generation"`
			SummaryWikiPageID    string   `json:"summary_wiki_page_id,omitempty"`
			SummaryVersion       int      `json:"summary_version,omitempty"`
		}{
			VideoID:              video.VideoID,
			SummaryBlockIDs:      sortedMapKeys(blocks),
			EvidenceIDs:          sortedMapKeys(evidence),
			TranscriptGeneration: video.TranscriptGeneration,
			SummaryWikiPageID:    video.SummaryWikiPageID,
			SummaryVersion:       video.SummaryVersion,
		})
	}
	return result
}

func sortedMapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (p *StageOnePlanner) buildMergePrompt(input planningMergeInput, requestID string, config PlannerConfig) (string, error) {
	envelope := struct {
		ContractVersion   string             `json:"contract_version"`
		RequestID         string             `json:"request_id"`
		SourceFingerprint string             `json:"source_fingerprint"`
		Input             planningMergeInput `json:"input"`
		Limits            planningLimits     `json:"limits"`
	}{ContractVersion: PlanningContractVersion, RequestID: requestID, SourceFingerprint: input.SourceFingerprint, Input: input, Limits: planningLimits{MaxOutputItems: 20, MaxOutputTokens: 8192}}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode planning merge prompt: %w", err)
	}
	return strings.TrimSpace(stageOneMergePrompt) + "\n\nINPUT:\n" + string(raw), nil
}

func (p *StageOnePlanner) completeWithCorrection(ctx context.Context, prompt string, snapshot CatalogSnapshot, config PlannerConfig, budget *int, batch string) (planningModelOutput, error) {
	correctionUsed := false
	raw, err := p.completeBudgeted(ctx, prompt, budget, config.BatchMaxInputTokens, "planning", batch)
	if err != nil {
		if isInvalidStructuredOutput(err) {
			correctionUsed = true
			raw, err = p.completeBudgeted(
				ctx,
				buildPlanningCorrectionPrompt(prompt, err, snapshot),
				budget,
				config.BatchMaxInputTokens,
				"planning",
				batch+"-correction",
			)
			if err != nil {
				return planningModelOutput{}, classifyPlanningError(err)
			}
		} else {
			return planningModelOutput{}, classifyPlanningError(err)
		}
	}
	if err != nil {
		return planningModelOutput{}, classifyPlanningError(err)
	}
	if tag, ok := unclosedLeadingReasoning(raw); ok {
		return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("planning model output ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
	}
	output, parseErr := parsePlanningOutput(raw)
	if parseErr != nil && isIncompleteJSONError(parseErr) {
		return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: parseErr}
	}
	if parseErr == nil {
		output = normalizePlanningIdentity(output, snapshot)
		output = completePlanningCoverage(output, snapshot)
		candidate := PlanDraft{ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint, TopicClusters: output.TopicClusters, UnselectedVideos: output.UnselectedVideos}
		if validateErr := validateCandidate(candidate, snapshot); validateErr == nil {
			return output, nil
		} else {
			parseErr = validateErr
		}
	}
	if parseErr == nil {
		return planningModelOutput{}, fmt.Errorf("planning model returned invalid output")
	}
	if correctionUsed {
		if isPlanningWhitelistError(parseErr) {
			return planningModelOutput{}, &GenerationError{Code: "invalid_reference", Err: parseErr}
		}
		if isIncompleteJSONError(parseErr) {
			return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: parseErr}
		}
		return planningModelOutput{}, &GenerationError{Code: "planning_output_invalid", Err: parseErr}
	}
	correctionPrompt := buildPlanningCorrectionPrompt(prompt, parseErr, snapshot)
	raw, retryErr := p.completeBudgeted(ctx, correctionPrompt, budget, config.BatchMaxInputTokens, "planning", batch+"-correction")
	if retryErr != nil {
		return planningModelOutput{}, classifyPlanningError(retryErr)
	}
	if tag, ok := unclosedLeadingReasoning(raw); ok {
		return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("planning correction ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
	}
	output, retryErr = parsePlanningOutput(raw)
	if retryErr != nil {
		if isIncompleteJSONError(retryErr) {
			return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: retryErr}
		}
		return planningModelOutput{}, &GenerationError{Code: "planning_output_invalid", Err: retryErr}
	}
	output = normalizePlanningIdentity(output, snapshot)
	output = completePlanningCoverage(output, snapshot)
	candidate := PlanDraft{ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint, TopicClusters: output.TopicClusters, UnselectedVideos: output.UnselectedVideos}
	if retryErr = validateCandidate(candidate, snapshot); retryErr != nil {
		if isPlanningWhitelistError(retryErr) {
			return planningModelOutput{}, &GenerationError{Code: "invalid_reference", Err: retryErr}
		}
		return planningModelOutput{}, &GenerationError{Code: "planning_output_invalid", Err: retryErr}
	}
	return output, nil
}

func (p *StageOnePlanner) completeMergeWithCorrection(ctx context.Context, prompt string, input planningMergeInput, snapshot CatalogSnapshot, config PlannerConfig, budget *int, batch string) (planningModelOutput, error) {
	mergeSnapshot := snapshotForClusters(snapshot, input.Clusters)
	correctionUsed := false
	raw, err := p.completeBudgeted(ctx, prompt, budget, config.MergeMaxInputTokens, "planning_merge", batch)
	if err != nil {
		if isInvalidStructuredOutput(err) {
			correctionUsed = true
			raw, err = p.completeBudgeted(
				ctx,
				buildPlanningCorrectionPrompt(prompt, err, mergeSnapshot),
				budget,
				config.MergeMaxInputTokens,
				"planning_merge",
				batch+"-correction",
			)
			if err != nil {
				return planningModelOutput{}, classifyPlanningError(err)
			}
		} else {
			return planningModelOutput{}, classifyPlanningError(err)
		}
	}
	if tag, ok := unclosedLeadingReasoning(raw); ok {
		return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("planning merge output ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
	}
	output, parseErr := parsePlanningOutput(raw)
	if parseErr != nil && isIncompleteJSONError(parseErr) {
		return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: parseErr}
	}
	if parseErr == nil {
		output = normalizePlanningIdentity(output, snapshot)
		candidate := PlanDraft{ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint, TopicClusters: output.TopicClusters, UnselectedVideos: output.UnselectedVideos}
		if validateErr := validateMergeCandidate(candidate, mergeSnapshot); validateErr == nil {
			return output, nil
		} else {
			parseErr = validateErr
		}
	}
	if parseErr == nil {
		return planningModelOutput{}, &GenerationError{Code: "planning_output_invalid", Err: parseErr}
	}
	if correctionUsed {
		if isPlanningWhitelistError(parseErr) {
			return planningModelOutput{}, &GenerationError{Code: "invalid_reference", Err: parseErr}
		}
		if isIncompleteJSONError(parseErr) {
			return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: parseErr}
		}
		// A provider-side structured-output failure can be followed by a
		// correction response that is still syntactically JSON but contains an
		// unknown field or trailing content. Keep the strict parser (unknown
		// fields are never silently discarded), but spend one bounded extra
		// correction on this specific format failure before terminating.
		if isPlanningFormatError(parseErr) {
			secondCorrectionPrompt := buildPlanningCorrectionPrompt(prompt, parseErr, mergeSnapshot) +
				"\nThe previous correction still violated the exact output shape. Return only the two allowed root fields: topic_clusters and unselected_videos."
			raw, retryErr := p.completeBudgeted(ctx, secondCorrectionPrompt, budget, config.MergeMaxInputTokens, "planning_merge", batch+"-correction-2")
			if retryErr != nil {
				return planningModelOutput{}, classifyPlanningError(retryErr)
			}
			if tag, ok := unclosedLeadingReasoning(raw); ok {
				return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("planning merge second correction ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
			}
			output, retryErr := parsePlanningOutput(raw)
			if retryErr != nil {
				if isIncompleteJSONError(retryErr) {
					return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: retryErr}
				}
				return planningModelOutput{}, &GenerationError{Code: "planning_output_invalid", Err: retryErr}
			}
			output = normalizePlanningIdentity(output, snapshot)
			candidate := PlanDraft{ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint, TopicClusters: output.TopicClusters, UnselectedVideos: output.UnselectedVideos}
			if retryErr = validateMergeCandidate(candidate, mergeSnapshot); retryErr != nil {
				if isPlanningWhitelistError(retryErr) {
					return planningModelOutput{}, &GenerationError{Code: "invalid_reference", Err: retryErr}
				}
				return planningModelOutput{}, &GenerationError{Code: "planning_output_invalid", Err: retryErr}
			}
			return output, nil
		}
		return planningModelOutput{}, &GenerationError{Code: "planning_output_invalid", Err: parseErr}
	}
	correctionPrompt := buildPlanningCorrectionPrompt(prompt, parseErr, mergeSnapshot)
	raw, err = p.completeBudgeted(ctx, correctionPrompt, budget, config.MergeMaxInputTokens, "planning_merge", batch+"-correction")
	if err != nil {
		return planningModelOutput{}, classifyPlanningError(err)
	}
	if tag, ok := unclosedLeadingReasoning(raw); ok {
		return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("planning merge correction ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
	}
	output, err = parsePlanningOutput(raw)
	if err != nil {
		if isIncompleteJSONError(err) {
			return planningModelOutput{}, &GenerationError{Code: "model_output_truncated", Err: err}
		}
		return planningModelOutput{}, &GenerationError{Code: "planning_output_invalid", Err: err}
	}
	output = normalizePlanningIdentity(output, snapshot)
	candidate := PlanDraft{ContractVersion: PlanningContractVersion, SourceFingerprint: snapshot.SourceFingerprint, TopicClusters: output.TopicClusters, UnselectedVideos: output.UnselectedVideos}
	if err := validateMergeCandidate(candidate, mergeSnapshot); err != nil {
		if isPlanningWhitelistError(err) {
			return planningModelOutput{}, &GenerationError{Code: "invalid_reference", Err: err}
		}
		return planningModelOutput{}, &GenerationError{Code: "planning_output_invalid", Err: err}
	}
	return output, nil
}

func validateCandidate(candidate PlanDraft, snapshot CatalogSnapshot) error {
	for _, cluster := range candidate.TopicClusters {
		if cluster.ReviewStatus != "" && cluster.ReviewStatus != PlanCandidate {
			return fmt.Errorf("model must return candidate review status")
		}
	}
	return candidate.ValidateAgainst(snapshot)
}

// normalizePlanningIdentity fills fields that are immutable facts of the
// catalog. The model only needs to select a video, summary blocks and
// evidence; it should not have to copy page versions or transcript identity
// strings that are easy to mistype.
func normalizePlanningIdentity(output planningModelOutput, snapshot CatalogSnapshot) planningModelOutput {
	videoByID := make(map[string]CatalogVideo, len(snapshot.Videos))
	for _, video := range snapshot.Videos {
		videoByID[strings.TrimSpace(video.VideoID)] = video
	}
	for clusterIndex := range output.TopicClusters {
		// review_status is a program-owned decision. The model may copy a
		// downstream status such as accepted/passed; normalize it before
		// candidate validation so enum drift does not abort planning.
		output.TopicClusters[clusterIndex].ReviewStatus = PlanCandidate
		for requestIndex := range output.TopicClusters[clusterIndex].MaterialRequests {
			request := &output.TopicClusters[clusterIndex].MaterialRequests[requestIndex]
			video, ok := videoByID[strings.TrimSpace(request.VideoID)]
			if !ok {
				continue
			}
			request.SummaryWikiPageID = video.SummaryWikiPageID
			request.SummaryVersion = video.SummaryVersion
			request.TranscriptGeneration = video.TranscriptGeneration
		}
	}
	return output
}

func completePlanningCoverage(output planningModelOutput, snapshot CatalogSnapshot) planningModelOutput {
	selected := make(map[string]struct{})
	for _, cluster := range output.TopicClusters {
		for _, videoID := range cluster.SourceVideoIDs {
			if id := strings.TrimSpace(videoID); id != "" {
				selected[id] = struct{}{}
			}
		}
	}
	unselected := make(map[string]struct{}, len(output.UnselectedVideos))
	for _, item := range output.UnselectedVideos {
		if id := strings.TrimSpace(item.VideoID); id != "" {
			unselected[id] = struct{}{}
		}
	}
	for _, video := range snapshot.Videos {
		videoID := strings.TrimSpace(video.VideoID)
		if videoID == "" {
			continue
		}
		if _, ok := selected[videoID]; ok {
			continue
		}
		if _, ok := unselected[videoID]; ok {
			continue
		}
		output.UnselectedVideos = append(output.UnselectedVideos, UnselectedVideo{VideoID: videoID, Reason: "未形成可靠主题"})
		unselected[videoID] = struct{}{}
	}
	return output
}

func validateMergeCandidate(candidate PlanDraft, snapshot CatalogSnapshot) error {
	for _, cluster := range candidate.TopicClusters {
		if cluster.ReviewStatus != "" && cluster.ReviewStatus != PlanCandidate {
			return fmt.Errorf("model must return candidate review status")
		}
	}
	return candidate.validateAgainst(snapshot, true)
}

func snapshotForClusters(snapshot CatalogSnapshot, clusters []PlanCluster) CatalogSnapshot {
	ids := make(map[string]struct{})
	for _, cluster := range clusters {
		for _, id := range cluster.SourceVideoIDs {
			ids[id] = struct{}{}
		}
		for _, request := range cluster.MaterialRequests {
			ids[request.VideoID] = struct{}{}
		}
	}
	result := snapshot
	result.Videos = make([]CatalogVideo, 0, len(ids))
	for _, video := range snapshot.Videos {
		if _, ok := ids[video.VideoID]; ok {
			result.Videos = append(result.Videos, video)
		}
	}
	return result
}

func (p *StageOnePlanner) complete(ctx context.Context, prompt string) (string, error) {
	if structured, ok := p.LLM.(structuredCompletionClient); ok {
		return structured.CompleteJSON(ctx, prompt)
	}
	if streaming, ok := p.LLM.(streamingCompletionClient); ok {
		return streaming.Stream(ctx, prompt, nil)
	}
	return p.LLM.Complete(ctx, prompt)
}

func (p *StageOnePlanner) completeBudgeted(ctx context.Context, prompt string, remaining *int, limit int, stage, batch string) (string, error) {
	return p.Gate.Complete(ctx, p.LLM, stage, batch, prompt, limit, remaining)
}

func (p *StageOnePlanner) requestID(prefix string) string {
	if p.RequestID != nil {
		if id := strings.TrimSpace(p.RequestID()); id != "" {
			return prefix + "-" + id
		}
	}
	return prefix + "-" + uuid.NewString()
}

func parsePlanningOutput(raw string) (planningModelOutput, error) {
	normalized := stripJSONFence(raw)
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	var output planningModelOutput
	if err := decoder.Decode(&output); err != nil {
		return planningModelOutput{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return planningModelOutput{}, fmt.Errorf("planning output contains trailing content")
	}
	if output.TopicClusters == nil || output.UnselectedVideos == nil {
		return planningModelOutput{}, fmt.Errorf("planning output arrays must be JSON arrays")
	}
	return output, nil
}

func classifyPlanningError(err error) error {
	var capacity *InputCapacityError
	if errors.As(err, &capacity) {
		return err
	}
	classified := classifyCompletionError("planning model", err)
	var generationErr *GenerationError
	if errors.As(classified, &generationErr) {
		return classified
	}
	return &GenerationError{Code: "planning_model_failed", Err: err}
}

func isPlanningWhitelistError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"outside catalog",
		"outside cluster members",
		"outside orchestration profile",
		"outside the material whitelist",
		"outside its endpoint cluster",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isPlanningFormatError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"unknown field",
		"trailing content",
		"arrays must be json arrays",
		"invalid character",
		"cannot unmarshal",
		"decode planning",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func normalizeCatalog(snapshot CatalogSnapshot) CatalogSnapshot {
	copySnapshot := snapshot
	copySnapshot.Videos = append([]CatalogVideo(nil), snapshot.Videos...)
	copySnapshot.SkippedVideos = append([]SkippedVideo(nil), snapshot.SkippedVideos...)
	sort.Slice(copySnapshot.Videos, func(i, j int) bool { return copySnapshot.Videos[i].VideoID < copySnapshot.Videos[j].VideoID })
	sort.Slice(copySnapshot.SkippedVideos, func(i, j int) bool {
		return copySnapshot.SkippedVideos[i].VideoID < copySnapshot.SkippedVideos[j].VideoID
	})
	return copySnapshot
}

func validateCatalogVideos(snapshot CatalogSnapshot) error {
	seen := make(map[string]struct{}, len(snapshot.Videos))
	for _, video := range snapshot.Videos {
		if strings.TrimSpace(video.VideoID) == "" || strings.TrimSpace(video.TranscriptGeneration) == "" {
			return fmt.Errorf("catalog video identity is incomplete")
		}
		if _, exists := seen[video.VideoID]; exists {
			return fmt.Errorf("catalog contains duplicate video ID %q", video.VideoID)
		}
		seen[video.VideoID] = struct{}{}
		profile, err := video.routingProfile()
		if err != nil {
			return err
		}
		if profile == nil || len(profile.TopicUnits) == 0 {
			return fmt.Errorf("catalog video %s has no bounded routing profile", video.VideoID)
		}
	}
	return nil
}

func normalizePlan(draft PlanDraft, snapshot CatalogSnapshot) PlanDraft {
	draft.TopicClusters = normalizeClusters(draft.TopicClusters)
	selected := make(map[string]struct{})
	seenClusterKeys := make(map[string]int, len(draft.TopicClusters))
	for index := range draft.TopicClusters {
		cluster := &draft.TopicClusters[index]
		cluster.ClusterKey = stableClusterKey(*cluster)
		seenClusterKeys[cluster.ClusterKey]++
		if seenClusterKeys[cluster.ClusterKey] > 1 {
			cluster.ClusterKey = fmt.Sprintf("%s-%d", cluster.ClusterKey, seenClusterKeys[cluster.ClusterKey])
		}
		// The model may only propose candidates; publication status is a
		// deterministic decision owned by the program.
		cluster.ReviewStatus = PlanCandidate
		if cluster.ReviewStatus == PlanCandidate {
			if cluster.Confidence < minAcceptedPlanConfidence || len(cluster.UncertainVideoIDs) > 0 {
				cluster.ReviewStatus = PlanAbstained
				cluster.SourceVideoIDs = nil
				cluster.MaterialRequests = nil
			} else {
				cluster.ReviewStatus = PlanAccepted
			}
		}
		if cluster.ReviewStatus == PlanAccepted {
			for _, id := range cluster.SourceVideoIDs {
				selected[id] = struct{}{}
			}
		}
	}
	seenUnselected := make(map[string]struct{}, len(draft.UnselectedVideos))
	for _, item := range draft.UnselectedVideos {
		seenUnselected[item.VideoID] = struct{}{}
	}
	for _, video := range snapshot.Videos {
		if _, ok := selected[video.VideoID]; ok {
			continue
		}
		if _, ok := seenUnselected[video.VideoID]; !ok {
			draft.UnselectedVideos = append(draft.UnselectedVideos, UnselectedVideo{VideoID: video.VideoID, Reason: "未形成可靠主题"})
		}
	}
	sort.Slice(draft.UnselectedVideos, func(i, j int) bool { return draft.UnselectedVideos[i].VideoID < draft.UnselectedVideos[j].VideoID })
	return draft
}

func normalizeClusters(clusters []PlanCluster) []PlanCluster {
	result := append([]PlanCluster(nil), clusters...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Title == result[j].Title {
			return strings.Join(result[i].SourceVideoIDs, "\x00") < strings.Join(result[j].SourceVideoIDs, "\x00")
		}
		return result[i].Title < result[j].Title
	})
	for index := range result {
		sort.Strings(result[index].SourceVideoIDs)
		sort.Strings(result[index].UncertainVideoIDs)
		for requestIndex := range result[index].MaterialRequests {
			sort.Strings(result[index].MaterialRequests[requestIndex].SummaryBlockIDs)
			sort.Strings(result[index].MaterialRequests[requestIndex].EvidenceIDs)
		}
		sort.Slice(result[index].MaterialRequests, func(i, j int) bool {
			return result[index].MaterialRequests[i].VideoID < result[index].MaterialRequests[j].VideoID
		})
	}
	return result
}

func stableClusterKey(cluster PlanCluster) string {
	ids := append([]string(nil), cluster.SourceVideoIDs...)
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.TrimSpace(cluster.Title) + "\x00" + strings.TrimSpace(cluster.PrimaryTemplate) + "\x00" + strings.Join(ids, "\x00")))
	return "cluster-" + hex.EncodeToString(sum[:])[:16]
}

func estimateTokens(prompt string) (int, error) {
	estimator, err := token.NewEstimator()
	if err != nil {
		return 0, fmt.Errorf("initialize planning tokenizer: %w", err)
	}
	return estimator.EstimateString(prompt), nil
}

func (p *StageOnePlanner) promptVersion() string {
	if strings.TrimSpace(p.PromptVersion) != "" {
		return strings.TrimSpace(p.PromptVersion)
	}
	return p.LLM.PromptVersion()
}
