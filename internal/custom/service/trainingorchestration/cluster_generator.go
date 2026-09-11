package trainingorchestration

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	ClusterGenerationContractVersion = "training-orchestration/cluster-generation/v1"
	defaultClusterGenerationTokens   = 120000
	maxClusterGenerationStages       = 6
	maxClusterGenerationUnits        = 16
	maxClusterGenerationOutputTokens = 8000
	maxClusterGenerationTextRunes    = 500
)

// ClusterGenerationDraft is the program-owned result of one accepted topic
// cluster. IDs, sequences, review status and evidence coordinates are resolved
// outside the model response.
type ClusterGenerationDraft struct {
	ContractVersion string                   `json:"contract_version"`
	ClusterKey      string                   `json:"cluster_key"`
	PrimaryTemplate string                   `json:"primary_template"`
	Stages          []ClusterGenerationStage `json:"stages"`
}

type ClusterGenerationStage struct {
	Title   string                  `json:"title"`
	Summary string                  `json:"summary"`
	Units   []ClusterGenerationUnit `json:"units"`
}

type ClusterGenerationUnit struct {
	LearningTitle   string        `json:"learning_title"`
	LearnerQuestion string        `json:"learner_question"`
	LearningOutcome string        `json:"learning_outcome"`
	EvidenceRefs    []EvidenceRef `json:"evidence_refs"`
	Confidence      float64       `json:"confidence"`
}

type ClusterGenerator struct {
	LLM            CompletionClient
	Gate           *CompletionGate
	MaxInputTokens int
	PromptVersion  string
	RequestID      func() string
}

type ClusterGenerationPart struct {
	Cluster  PlanCluster
	Material ClusterMaterial
}

type clusterMaterialFragment struct {
	videoID      string
	request      MaterialRequest
	blockKeys    []string
	evidenceKeys []string
}

//go:embed prompts/training-orchestration-cluster-generation-v1.txt
var clusterGenerationPrompt string

type clusterGenerationModelOutput struct {
	Stages []clusterGenerationModelStage `json:"stages"`
}

type clusterGenerationModelStage struct {
	Title   string                       `json:"title"`
	Summary string                       `json:"summary"`
	Units   []clusterGenerationModelUnit `json:"units"`
}

type clusterGenerationModelUnit struct {
	LearningTitle   string                      `json:"learning_title"`
	LearnerQuestion string                      `json:"learner_question"`
	LearningOutcome string                      `json:"learning_outcome"`
	EvidenceRefs    []clusterGenerationModelRef `json:"evidence_refs"`
	Confidence      float64                     `json:"confidence"`
}

type clusterGenerationModelRef struct {
	VideoID    string `json:"video_id"`
	EvidenceID string `json:"evidence_id"`
}

type clusterGenerationEnvelope struct {
	ContractVersion string                  `json:"contract_version"`
	RequestID       string                  `json:"request_id"`
	Input           clusterGenerationInput  `json:"input"`
	Limits          clusterGenerationLimits `json:"limits"`
}

type clusterGenerationInput struct {
	Title           string          `json:"title"`
	LearningGoal    string          `json:"learning_goal"`
	PrimaryTemplate string          `json:"primary_template"`
	Scope           string          `json:"scope"`
	Material        ClusterMaterial `json:"material"`
}

type clusterGenerationLimits struct {
	MaxStages       int `json:"max_stages"`
	MaxUnits        int `json:"max_units"`
	MaxOutputTokens int `json:"max_output_tokens"`
}

func (g *ClusterGenerator) SetCompletionGate(gate *CompletionGate) {
	if g != nil {
		g.Gate = gate
	}
}

func (g *ClusterGenerator) Generate(ctx context.Context, cluster PlanCluster, material ClusterMaterial) (ClusterGenerationDraft, error) {
	if g == nil || g.LLM == nil {
		return ClusterGenerationDraft{}, fmt.Errorf("training orchestration cluster generator llm is not configured")
	}
	if cluster.ReviewStatus != PlanAccepted {
		return ClusterGenerationDraft{}, fmt.Errorf("cluster generation requires an accepted plan cluster")
	}
	if err := material.ValidateRetrievedAgainst(cluster); err != nil {
		return ClusterGenerationDraft{}, fmt.Errorf("validate cluster material: %w", err)
	}
	prompt, err := g.buildPrompt(cluster, material)
	if err != nil {
		return ClusterGenerationDraft{}, err
	}
	limit := g.MaxInputTokens
	if limit == 0 {
		limit = defaultClusterGenerationTokens
	}
	if limit < 0 {
		return ClusterGenerationDraft{}, fmt.Errorf("cluster generation input limit must be non-negative")
	}
	gate := g.Gate
	if gate == nil {
		gate = NewCompletionGate(CompletionGateConfig{})
	}
	correctionUsed := false
	raw, err := gate.Complete(ctx, g.LLM, "cluster_generation", cluster.ClusterKey, prompt, limit, nil)
	if err != nil {
		if !isInvalidStructuredOutput(err) {
			return ClusterGenerationDraft{}, classifyCompletionError("generate cluster training path", err)
		}
		correctionUsed = true
		raw, err = gate.Complete(
			ctx, g.LLM, "cluster_generation", cluster.ClusterKey+"-correction",
			clusterGenerationCorrectionPrompt(prompt, err), limit, nil,
		)
		if err != nil {
			return ClusterGenerationDraft{}, classifyCompletionError("correct cluster training path", err)
		}
	}
	if tag, ok := unclosedLeadingReasoning(raw); ok {
		return ClusterGenerationDraft{}, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("cluster generation model output ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
	}
	output, err := parseClusterGenerationOutput(raw)
	if err != nil {
		if isIncompleteJSONError(err) {
			return ClusterGenerationDraft{}, &GenerationError{Code: "model_output_truncated", Err: err}
		}
		if correctionUsed {
			return ClusterGenerationDraft{}, &GenerationError{Code: "model_output_invalid", Err: err}
		}
		correctionUsed = true
		raw, correctionErr := gate.Complete(
			ctx, g.LLM, "cluster_generation", cluster.ClusterKey+"-correction",
			clusterGenerationCorrectionPrompt(prompt, err), limit, nil,
		)
		if correctionErr != nil {
			return ClusterGenerationDraft{}, classifyCompletionError("correct cluster training path", correctionErr)
		}
		if tag, ok := unclosedLeadingReasoning(raw); ok {
			return ClusterGenerationDraft{}, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("cluster generation correction ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
		}
		output, err = parseClusterGenerationOutput(raw)
		if err != nil {
			if isIncompleteJSONError(err) {
				return ClusterGenerationDraft{}, &GenerationError{Code: "model_output_truncated", Err: err}
			}
			return ClusterGenerationDraft{}, &GenerationError{Code: "model_output_invalid", Err: fmt.Errorf("cluster generation correction: %w", err)}
		}
	}
	draft, err := resolveClusterGenerationOutput(cluster, material, output)
	if err != nil {
		if !isClusterCorrectionError(err) {
			return ClusterGenerationDraft{}, &GenerationError{Code: "invalid_reference", Err: err}
		}
		if correctionUsed {
			return ClusterGenerationDraft{}, &GenerationError{Code: "invalid_reference", Err: err}
		}
		correctionUsed = true
		raw, correctionErr := gate.Complete(
			ctx, g.LLM, "cluster_generation", cluster.ClusterKey+"-correction",
			clusterGenerationCorrectionPrompt(prompt, err), limit, nil,
		)
		if correctionErr != nil {
			return ClusterGenerationDraft{}, classifyCompletionError("correct cluster training path", correctionErr)
		}
		if tag, ok := unclosedLeadingReasoning(raw); ok {
			return ClusterGenerationDraft{}, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("cluster generation correction ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
		}
		output, err = parseClusterGenerationOutput(raw)
		if err != nil && isIncompleteJSONError(err) {
			return ClusterGenerationDraft{}, &GenerationError{Code: "model_output_truncated", Err: err}
		}
		if err == nil {
			draft, err = resolveClusterGenerationOutput(cluster, material, output)
		}
		if err != nil {
			if !isClusterCorrectionError(err) {
				return ClusterGenerationDraft{}, &GenerationError{Code: "model_output_invalid", Err: fmt.Errorf("cluster generation correction: %w", err)}
			}
			return ClusterGenerationDraft{}, &GenerationError{Code: "invalid_reference", Err: fmt.Errorf("cluster generation correction: %w", err)}
		}
	}
	return draft, nil
}

func clusterGenerationCorrectionPrompt(prompt string, reason error) string {
	return prompt + "\n\nCORRECTION:\n" + reason.Error() + "\nReturn one corrected JSON object only. Preserve the output contract, use no Markdown or extra text, and use only evidence references from INPUT.material.evidence."
}

func (g *ClusterGenerator) buildPrompt(cluster PlanCluster, material ClusterMaterial) (string, error) {
	requestID := "cluster-" + uuid.NewString()
	if g.RequestID != nil {
		if custom := strings.TrimSpace(g.RequestID()); custom != "" {
			requestID = custom
		}
	}
	envelope := clusterGenerationEnvelope{
		ContractVersion: ClusterGenerationContractVersion,
		RequestID:       requestID,
		Input: clusterGenerationInput{
			Title: cluster.Title, LearningGoal: cluster.LearningGoal,
			PrimaryTemplate: cluster.PrimaryTemplate, Scope: cluster.Scope, Material: material,
		},
		Limits: clusterGenerationLimits{
			MaxStages: maxClusterGenerationStages, MaxUnits: maxClusterGenerationUnits,
			MaxOutputTokens: maxClusterGenerationOutputTokens,
		},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode cluster generation input: %w", err)
	}
	return strings.TrimSpace(clusterGenerationPrompt) + "\n\nINPUT:\n" + string(raw), nil
}

// SplitMaterial creates complete material packages when one cluster cannot fit
// in a single request. It only splits at request, summary-block, and evidence
// boundaries; it never slices source text.
func (g *ClusterGenerator) SplitMaterial(cluster PlanCluster, material ClusterMaterial) ([]ClusterGenerationPart, error) {
	return g.splitMaterial(cluster, material, false)
}

// SplitMaterialForRetry forces independent complete fragments after a provider
// reports a context limit even though the local estimate accepted the package.
// This is only used after a failed request; normal planning still packs as
// much complete material as the configured local limit allows.
func (g *ClusterGenerator) SplitMaterialForRetry(cluster PlanCluster, material ClusterMaterial) ([]ClusterGenerationPart, error) {
	return g.splitMaterial(cluster, material, true)
}

func (g *ClusterGenerator) splitMaterial(cluster PlanCluster, material ClusterMaterial, forceIndependent bool) ([]ClusterGenerationPart, error) {
	if err := material.ValidateRetrievedAgainst(cluster); err != nil {
		return nil, fmt.Errorf("validate cluster material before split: %w", err)
	}
	limit := g.MaxInputTokens
	if limit <= 0 {
		limit = defaultClusterGenerationTokens
	}
	prompt, err := g.buildPrompt(cluster, material)
	if err != nil {
		return nil, err
	}
	tokens, err := estimateTokens(prompt)
	if err != nil {
		return nil, err
	}
	if tokens <= limit && !forceIndependent {
		return []ClusterGenerationPart{{Cluster: cluster, Material: material}}, nil
	}

	blocks := make(map[string]MaterialBlock, len(material.SummaryBlocks))
	for _, block := range material.SummaryBlocks {
		blocks[block.VideoID+"\x00"+block.BlockID] = block
	}
	evidence := make(map[string]MaterialEvidence, len(material.Evidence))
	evidenceIDsByVideo := make(map[string][]string)
	for _, item := range material.Evidence {
		evidence[item.VideoID+"\x00"+item.EvidenceID] = item
		evidenceIDsByVideo[item.VideoID] = append(evidenceIDsByVideo[item.VideoID], item.EvidenceID)
	}
	fragments := make([]clusterMaterialFragment, 0)
	for _, request := range cluster.MaterialRequests {
		availableEvidenceIDs := evidenceIDsByVideo[request.VideoID]
		if len(availableEvidenceIDs) == 0 {
			return nil, fmt.Errorf("cluster material request %s has no evidence to split", request.VideoID)
		}
		requestFragments := make([]clusterMaterialFragment, len(availableEvidenceIDs))
		for index, evidenceID := range availableEvidenceIDs {
			requestFragments[index] = clusterMaterialFragment{
				videoID: request.VideoID, request: request,
				evidenceKeys: []string{request.VideoID + "\x00" + evidenceID},
			}
		}
		for index, blockID := range request.SummaryBlockIDs {
			requestFragments[index%len(requestFragments)].blockKeys = append(
				requestFragments[index%len(requestFragments)].blockKeys,
				request.VideoID+"\x00"+blockID,
			)
		}
		fragments = append(fragments, requestFragments...)
	}

	parts := make([]ClusterGenerationPart, 0)
	for _, item := range fragments {
		if len(item.blockKeys) != 0 {
			for _, key := range item.blockKeys {
				if _, ok := blocks[key]; !ok {
					return nil, fmt.Errorf("cluster material split is missing summary block %s", key)
				}
			}
		}
		for _, key := range item.evidenceKeys {
			if _, ok := evidence[key]; !ok {
				return nil, fmt.Errorf("cluster material split is missing evidence %s", key)
			}
		}
		placed := false
		if forceIndependent {
			part := newClusterPart(cluster, item, blocks, evidence)
			partPrompt, promptErr := g.buildPrompt(part.Cluster, part.Material)
			if promptErr != nil {
				return nil, promptErr
			}
			partTokens, estimateErr := estimateTokens(partPrompt)
			if estimateErr != nil {
				return nil, estimateErr
			}
			if partTokens > limit {
				return nil, &InputCapacityError{Tokens: partTokens, Limit: limit}
			}
			parts = append(parts, part)
			continue
		}
		for index := range parts {
			if contains(parts[index].Cluster.SourceVideoIDs, item.videoID) {
				continue
			}
			candidate := appendClusterPart(parts[index], item, blocks, evidence)
			prompt, promptErr := g.buildPrompt(candidate.Cluster, candidate.Material)
			if promptErr != nil {
				return nil, promptErr
			}
			candidateTokens, estimateErr := estimateTokens(prompt)
			if estimateErr != nil {
				return nil, estimateErr
			}
			if candidateTokens <= limit {
				parts[index] = candidate
				placed = true
				break
			}
		}
		if placed {
			continue
		}
		part := newClusterPart(cluster, item, blocks, evidence)
		prompt, promptErr := g.buildPrompt(part.Cluster, part.Material)
		if promptErr != nil {
			return nil, promptErr
		}
		partTokens, estimateErr := estimateTokens(prompt)
		if estimateErr != nil {
			return nil, estimateErr
		}
		if partTokens > limit {
			return nil, &InputCapacityError{Tokens: partTokens, Limit: limit}
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("cluster material split produced no parts")
	}
	return parts, nil
}

func newClusterPart(cluster PlanCluster, item clusterMaterialFragment, blocks map[string]MaterialBlock, evidence map[string]MaterialEvidence) ClusterGenerationPart {
	partCluster := cluster
	partCluster.SourceVideoIDs = []string{item.videoID}
	partCluster.MaterialRequests = []MaterialRequest{item.request}
	partCluster.MaterialRequests[0].SummaryBlockIDs = blockIDsForKeys(item.blockKeys)
	partCluster.MaterialRequests[0].EvidenceIDs = evidenceIDsForKeys(item.evidenceKeys)
	partMaterial := ClusterMaterial{
		ContractVersion: MaterialContractVersion,
		ClusterKey:      cluster.ClusterKey,
		SourceVideoIDs:  []string{item.videoID},
		SummaryBlocks:   blocksForKeys(item.blockKeys, blocks),
		Evidence:        evidenceForKeys(item.evidenceKeys, evidence),
	}
	return ClusterGenerationPart{Cluster: partCluster, Material: partMaterial}
}

func appendClusterPart(part ClusterGenerationPart, item clusterMaterialFragment, blocks map[string]MaterialBlock, evidence map[string]MaterialEvidence) ClusterGenerationPart {
	result := part
	result.Cluster.SourceVideoIDs = append([]string(nil), part.Cluster.SourceVideoIDs...)
	result.Cluster.MaterialRequests = append([]MaterialRequest(nil), part.Cluster.MaterialRequests...)
	for index := range result.Cluster.MaterialRequests {
		result.Cluster.MaterialRequests[index].SummaryBlockIDs = append([]string(nil), part.Cluster.MaterialRequests[index].SummaryBlockIDs...)
		result.Cluster.MaterialRequests[index].EvidenceIDs = append([]string(nil), part.Cluster.MaterialRequests[index].EvidenceIDs...)
	}
	result.Material.SummaryBlocks = append([]MaterialBlock(nil), part.Material.SummaryBlocks...)
	result.Material.Evidence = append([]MaterialEvidence(nil), part.Material.Evidence...)
	request := item.request
	request.SummaryBlockIDs = blockIDsForKeys(item.blockKeys)
	request.EvidenceIDs = evidenceIDsForKeys(item.evidenceKeys)
	result.Cluster.SourceVideoIDs = append(result.Cluster.SourceVideoIDs, item.videoID)
	result.Cluster.MaterialRequests = append(result.Cluster.MaterialRequests, request)
	result.Material.SummaryBlocks = append(result.Material.SummaryBlocks, blocksForKeys(item.blockKeys, blocks)...)
	result.Material.Evidence = append(result.Material.Evidence, evidenceForKeys(item.evidenceKeys, evidence)...)
	return result
}

func blockIDsForKeys(keys []string) []string {
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) == 2 {
			result = append(result, parts[1])
		}
	}
	return result
}

func evidenceIDsForKeys(keys []string) []string {
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) == 2 {
			result = append(result, parts[1])
		}
	}
	return result
}

func blocksForKeys(keys []string, values map[string]MaterialBlock) []MaterialBlock {
	result := make([]MaterialBlock, 0, len(keys))
	for _, key := range keys {
		if value, ok := values[key]; ok {
			result = append(result, value)
		}
	}
	return result
}

func evidenceForKeys(keys []string, values map[string]MaterialEvidence) []MaterialEvidence {
	result := make([]MaterialEvidence, 0, len(keys))
	for _, key := range keys {
		if value, ok := values[key]; ok {
			result = append(result, value)
		}
	}
	return result
}

func mergeClusterGenerationDrafts(cluster PlanCluster, drafts []ClusterGenerationDraft) (ClusterGenerationDraft, error) {
	if len(drafts) == 0 {
		return ClusterGenerationDraft{}, fmt.Errorf("cluster generation produced no drafts")
	}
	merged := ClusterGenerationDraft{
		ContractVersion: ClusterGenerationContractVersion,
		ClusterKey:      cluster.ClusterKey,
		PrimaryTemplate: cluster.PrimaryTemplate,
		Stages:          []ClusterGenerationStage{},
	}
	for _, draft := range drafts {
		if draft.ContractVersion != merged.ContractVersion || draft.ClusterKey != merged.ClusterKey || draft.PrimaryTemplate != merged.PrimaryTemplate {
			return ClusterGenerationDraft{}, fmt.Errorf("cluster generation draft identity does not match split cluster")
		}
		for stageIndex, stage := range draft.Stages {
			if stageIndex >= len(merged.Stages) {
				merged.Stages = append(merged.Stages, ClusterGenerationStage{Title: stage.Title, Summary: stage.Summary, Units: []ClusterGenerationUnit{}})
			}
			target := &merged.Stages[stageIndex]
			if strings.TrimSpace(target.Title) == "" {
				target.Title = stage.Title
			}
			if strings.TrimSpace(target.Summary) == "" {
				target.Summary = stage.Summary
			}
			target.Units = append(target.Units, stage.Units...)
		}
	}
	if len(merged.Stages) == 0 || len(merged.Stages) > maxClusterGenerationStages {
		return ClusterGenerationDraft{}, fmt.Errorf("merged cluster generation exceeds %d stages", maxClusterGenerationStages)
	}
	units := 0
	for _, stage := range merged.Stages {
		units += len(stage.Units)
	}
	if units == 0 || units > maxClusterGenerationUnits {
		return ClusterGenerationDraft{}, fmt.Errorf("merged cluster generation exceeds %d learning units", maxClusterGenerationUnits)
	}
	return merged, nil
}

func (g *ClusterGenerator) complete(ctx context.Context, prompt string) (string, error) {
	if structured, ok := g.LLM.(structuredCompletionClient); ok {
		return structured.CompleteJSON(ctx, prompt)
	}
	if streaming, ok := g.LLM.(streamingCompletionClient); ok {
		return streaming.Stream(ctx, prompt, nil)
	}
	return g.LLM.Complete(ctx, prompt)
}

func parseClusterGenerationOutput(raw string) (clusterGenerationModelOutput, error) {
	normalized := stripJSONFence(raw)
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	var output clusterGenerationModelOutput
	if err := decoder.Decode(&output); err != nil {
		return clusterGenerationModelOutput{}, fmt.Errorf("decode cluster generation output: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return clusterGenerationModelOutput{}, fmt.Errorf("cluster generation output contains trailing content")
	}
	if output.Stages == nil {
		return clusterGenerationModelOutput{}, fmt.Errorf("cluster generation stages must be a JSON array")
	}
	return output, nil
}

func resolveClusterGenerationOutput(cluster PlanCluster, material ClusterMaterial, output clusterGenerationModelOutput) (ClusterGenerationDraft, error) {
	if len(output.Stages) == 0 {
		return ClusterGenerationDraft{}, fmt.Errorf("cluster generation must contain at least one stage")
	}
	if len(output.Stages) > maxClusterGenerationStages {
		return ClusterGenerationDraft{}, fmt.Errorf("cluster generation exceeds %d stages", maxClusterGenerationStages)
	}
	evidenceByKey := make(map[string]MaterialEvidence, len(material.Evidence))
	for _, evidence := range material.Evidence {
		key := evidence.VideoID + "\x00" + evidence.EvidenceID
		evidenceByKey[key] = evidence
	}
	draft := ClusterGenerationDraft{
		ContractVersion: ClusterGenerationContractVersion,
		ClusterKey:      cluster.ClusterKey,
		PrimaryTemplate: cluster.PrimaryTemplate,
		Stages:          make([]ClusterGenerationStage, 0, len(output.Stages)),
	}
	totalUnits := 0
	for stageIndex, stage := range output.Stages {
		if err := validateGenerationText(stage.Title, "stage title"); err != nil {
			return ClusterGenerationDraft{}, err
		}
		if err := validateGenerationText(stage.Summary, "stage summary"); err != nil {
			return ClusterGenerationDraft{}, err
		}
		if stage.Units == nil || len(stage.Units) == 0 {
			return ClusterGenerationDraft{}, fmt.Errorf("stage %d must contain learning units", stageIndex+1)
		}
		stageDraft := ClusterGenerationStage{Title: strings.TrimSpace(stage.Title), Summary: strings.TrimSpace(stage.Summary), Units: make([]ClusterGenerationUnit, 0, len(stage.Units))}
		for unitIndex, unit := range stage.Units {
			if err := validateGenerationText(unit.LearningTitle, "learning title"); err != nil {
				return ClusterGenerationDraft{}, fmt.Errorf("stage %d unit %d: %w", stageIndex+1, unitIndex+1, err)
			}
			if err := validateGenerationText(unit.LearnerQuestion, "learner question"); err != nil {
				return ClusterGenerationDraft{}, fmt.Errorf("stage %d unit %d: %w", stageIndex+1, unitIndex+1, err)
			}
			if err := validateGenerationText(unit.LearningOutcome, "learning outcome"); err != nil {
				return ClusterGenerationDraft{}, fmt.Errorf("stage %d unit %d: %w", stageIndex+1, unitIndex+1, err)
			}
			if unit.EvidenceRefs == nil || len(unit.EvidenceRefs) == 0 {
				return ClusterGenerationDraft{}, fmt.Errorf("stage %d unit %d must contain evidence references", stageIndex+1, unitIndex+1)
			}
			if unit.Confidence < 0 || unit.Confidence > 1 {
				return ClusterGenerationDraft{}, fmt.Errorf("stage %d unit %d confidence is outside [0,1]", stageIndex+1, unitIndex+1)
			}
			seenRefs := make(map[string]struct{}, len(unit.EvidenceRefs))
			resolvedRefs := make([]EvidenceRef, 0, len(unit.EvidenceRefs))
			for refIndex, ref := range unit.EvidenceRefs {
				videoID, evidenceID := strings.TrimSpace(ref.VideoID), strings.TrimSpace(ref.EvidenceID)
				key := videoID + "\x00" + evidenceID
				if videoID == "" || evidenceID == "" {
					return ClusterGenerationDraft{}, fmt.Errorf("stage %d unit %d evidence reference %d is incomplete", stageIndex+1, unitIndex+1, refIndex+1)
				}
				if _, duplicate := seenRefs[key]; duplicate {
					return ClusterGenerationDraft{}, fmt.Errorf("stage %d unit %d repeats evidence %s", stageIndex+1, unitIndex+1, evidenceID)
				}
				seenRefs[key] = struct{}{}
				evidence, ok := evidenceByKey[key]
				if !ok {
					return ClusterGenerationDraft{}, fmt.Errorf("evidence %s/%s is outside the material whitelist", videoID, evidenceID)
				}
				resolvedRefs = append(resolvedRefs, EvidenceRef{
					VideoID: evidence.VideoID, TranscriptGeneration: evidence.TranscriptGeneration,
					EvidenceID: evidence.EvidenceID, StartMs: evidence.StartMs, EndMs: evidence.EndMs,
				})
			}
			stageDraft.Units = append(stageDraft.Units, ClusterGenerationUnit{
				LearningTitle: strings.TrimSpace(unit.LearningTitle), LearnerQuestion: strings.TrimSpace(unit.LearnerQuestion),
				LearningOutcome: strings.TrimSpace(unit.LearningOutcome), EvidenceRefs: resolvedRefs, Confidence: unit.Confidence,
			})
			totalUnits++
			if totalUnits > maxClusterGenerationUnits {
				return ClusterGenerationDraft{}, fmt.Errorf("cluster generation exceeds %d learning units", maxClusterGenerationUnits)
			}
		}
		draft.Stages = append(draft.Stages, stageDraft)
	}
	return draft, nil
}

// AssembleClusterGenerationDraft assigns all persistent identifiers and
// sequences after model validation. The model never owns these fields.
func AssembleClusterGenerationDraft(snapshot CatalogSnapshot, cluster PlanCluster, draft ClusterGenerationDraft, material ClusterMaterial) (TopicCluster, error) {
	if draft.ContractVersion != ClusterGenerationContractVersion || draft.ClusterKey != cluster.ClusterKey || draft.PrimaryTemplate != cluster.PrimaryTemplate {
		return TopicCluster{}, fmt.Errorf("cluster generation draft identity does not match plan")
	}
	if err := material.ValidateRetrievedAgainst(cluster); err != nil {
		return TopicCluster{}, fmt.Errorf("validate cluster material: %w", err)
	}
	videoTitles := make(map[string]string, len(snapshot.Videos))
	for _, video := range snapshot.Videos {
		videoTitles[video.VideoID] = strings.TrimSpace(video.Title)
	}
	memberTopics := make([]MemberTopic, 0, len(cluster.SourceVideoIDs))
	for index, videoID := range cluster.SourceVideoIDs {
		title := videoTitles[videoID]
		if title == "" {
			return TopicCluster{}, fmt.Errorf("cluster source video %s is outside catalog", videoID)
		}
		memberTopics = append(memberTopics, MemberTopic{
			TopicID: fmt.Sprintf("topic-%s-%03d", cluster.ClusterKey, index+1),
			Title:   title,
		})
	}
	clusterEvidence := make([]EvidenceRef, 0, len(material.Evidence))
	seenEvidence := make(map[string]struct{}, len(material.Evidence))
	for _, evidence := range material.Evidence {
		key := evidence.VideoID + "\x00" + evidence.EvidenceID
		if _, duplicate := seenEvidence[key]; duplicate {
			continue
		}
		seenEvidence[key] = struct{}{}
		clusterEvidence = append(clusterEvidence, EvidenceRef{
			VideoID: evidence.VideoID, TranscriptGeneration: evidence.TranscriptGeneration,
			EvidenceID: evidence.EvidenceID, StartMs: evidence.StartMs, EndMs: evidence.EndMs,
		})
	}
	if len(clusterEvidence) == 0 {
		return TopicCluster{}, fmt.Errorf("cluster generation material has no evidence")
	}
	clusterID := cluster.ClusterKey
	result := TopicCluster{
		ClusterID: clusterID, Title: cluster.Title, Summary: cluster.Summary, LearningGoal: cluster.LearningGoal,
		LearningContentType: cluster.PrimaryTemplate, MemberTopics: memberTopics,
		SourceVideoIDs: append([]string(nil), cluster.SourceVideoIDs...), EvidenceRefs: clusterEvidence,
		Confidence: cluster.Confidence, ReviewStatus: "passed",
		Path: LearningPath{PathID: "path-" + clusterID, PrimaryTemplate: cluster.PrimaryTemplate, Stages: []LearningStage{}},
	}
	for stageIndex, stage := range draft.Stages {
		learningStage := LearningStage{
			StageID: fmt.Sprintf("stage-%s-%03d", clusterID, stageIndex+1),
			Title:   stage.Title, Summary: stage.Summary, Sequence: stageIndex + 1, Units: []LearningUnit{},
		}
		for unitIndex, unit := range stage.Units {
			learningStage.Units = append(learningStage.Units, LearningUnit{
				UnitID:        fmt.Sprintf("unit-%s-%03d-%03d", clusterID, stageIndex+1, unitIndex+1),
				LearningTitle: unit.LearningTitle, LearnerQuestion: unit.LearnerQuestion,
				LearningOutcome: unit.LearningOutcome, Sequence: unitIndex + 1,
				KnowledgeRefs: []KnowledgeRef{}, EvidenceRefs: unit.EvidenceRefs,
				Confidence: unit.Confidence, ReviewStatus: "passed",
			})
		}
		result.Path.Stages = append(result.Path.Stages, learningStage)
	}
	return result, nil
}

func validateGenerationText(value, field string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > maxClusterGenerationTextRunes {
		return fmt.Errorf("%s exceeds %d characters", field, maxClusterGenerationTextRunes)
	}
	return nil
}

func classifyClusterGenerationError(err error) error {
	return classifyCompletionError("generate cluster training path", err)
}

func isClusterCorrectionError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "outside the material whitelist") ||
		strings.Contains(message, "outside its endpoint cluster") ||
		strings.Contains(message, "repeats evidence")
}
