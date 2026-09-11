package trainingorchestration

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	defaultRelationGenerationTokens = 60000
	defaultRelationBatchTokens      = 30000
	defaultRelationBatchClusters    = 12
	maxGeneratedRelations           = 100
)

type RelationGenerator struct {
	LLM                 CompletionClient
	Gate                *CompletionGate
	MaxInputTokens      int
	BatchMaxInputTokens int
	MaxClustersPerBatch int
	RequestID           func() string
}

//go:embed prompts/training-orchestration-relation-v1.txt
var relationGenerationPrompt string

type relationGenerationModelOutput struct {
	Relations []relationGenerationModelRelation `json:"relations"`
}

type relationGenerationModelRelation struct {
	SourceClusterID    string                  `json:"source_cluster_id"`
	TargetClusterID    string                  `json:"target_cluster_id"`
	RelationType       string                  `json:"relation_type"`
	Summary            string                  `json:"summary"`
	Confidence         float64                 `json:"confidence"`
	SourceEvidenceRefs []relationGenerationRef `json:"source_evidence_refs"`
	TargetEvidenceRefs []relationGenerationRef `json:"target_evidence_refs"`
}

type relationGenerationRef struct {
	VideoID    string `json:"video_id"`
	EvidenceID string `json:"evidence_id"`
}

type relationGenerationEnvelope struct {
	ContractVersion string                   `json:"contract_version"`
	RequestID       string                   `json:"request_id"`
	Input           relationGenerationInput  `json:"input"`
	Limits          relationGenerationLimits `json:"limits"`
}

type relationGenerationInput struct {
	Clusters []relationGenerationCluster `json:"clusters"`
}

type relationGenerationCluster struct {
	ClusterID    string        `json:"cluster_id"`
	Title        string        `json:"title"`
	Summary      string        `json:"summary"`
	LearningGoal string        `json:"learning_goal"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs"`
}

type relationGenerationLimits struct {
	MaxRelations    int `json:"max_relations"`
	MaxOutputTokens int `json:"max_output_tokens"`
}

func (g *RelationGenerator) SetCompletionGate(gate *CompletionGate) {
	if g != nil {
		g.Gate = gate
	}
}

func (g *RelationGenerator) Generate(ctx context.Context, clusters []TopicCluster) ([]TopicClusterRelation, error) {
	if len(clusters) < 2 {
		return []TopicClusterRelation{}, nil
	}
	if g == nil || g.LLM == nil {
		return nil, fmt.Errorf("training orchestration relation generator llm is not configured")
	}
	clusterByID := make(map[string]TopicCluster, len(clusters))
	inputClusters := make([]relationGenerationCluster, 0, len(clusters))
	for index, cluster := range clusters {
		if strings.TrimSpace(cluster.ClusterID) == "" || strings.TrimSpace(cluster.Title) == "" || strings.TrimSpace(cluster.Summary) == "" || strings.TrimSpace(cluster.LearningGoal) == "" {
			return nil, fmt.Errorf("relation input cluster %d is incomplete", index+1)
		}
		if cluster.ReviewStatus != "passed" || len(cluster.EvidenceRefs) == 0 {
			return nil, fmt.Errorf("relation input cluster %s is not publishable", cluster.ClusterID)
		}
		if _, duplicate := clusterByID[cluster.ClusterID]; duplicate {
			return nil, fmt.Errorf("relation input repeats cluster %s", cluster.ClusterID)
		}
		clusterByID[cluster.ClusterID] = cluster
		inputClusters = append(inputClusters, relationGenerationCluster{
			ClusterID: cluster.ClusterID, Title: cluster.Title, Summary: cluster.Summary,
			LearningGoal: cluster.LearningGoal, EvidenceRefs: cluster.EvidenceRefs,
		})
	}
	batches, err := g.packRelationClusters(inputClusters)
	if err != nil {
		return nil, err
	}
	gate := g.Gate
	if gate == nil {
		gate = NewCompletionGate(CompletionGateConfig{})
	}
	pending := append([][]relationGenerationCluster(nil), batches...)
	allRelations := make([]TopicClusterRelation, 0)
	batchNumber := 0
	for len(pending) > 0 {
		batch := pending[0]
		pending = pending[1:]
		batchNumber++
		prompt, promptErr := g.buildPrompt(batch)
		if promptErr != nil {
			return nil, promptErr
		}
		limit := g.batchLimit()
		raw, callErr := gate.Complete(ctx, g.LLM, "relation_generation", fmt.Sprintf("batch-%03d", batchNumber), prompt, limit, nil)
		if callErr != nil {
			classified := classifyCompletionError("generate training orchestration relations", callErr)
			if isShrinkableGenerationError(classified) && len(batch) > 1 {
				middle := len(batch) / 2
				pending = append([][]relationGenerationCluster{batch[:middle], batch[middle:]}, pending...)
				batchNumber--
				continue
			}
			return nil, classified
		}
		if tag, ok := unclosedLeadingReasoning(raw); ok {
			return nil, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("relation generation model output ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
		}
		output, parseErr := parseRelationGenerationOutput(raw)
		if parseErr != nil {
			if isIncompleteJSONError(parseErr) {
				if len(batch) > 1 {
					middle := len(batch) / 2
					pending = append([][]relationGenerationCluster{batch[:middle], batch[middle:]}, pending...)
					batchNumber--
					continue
				}
				return nil, &GenerationError{Code: "model_output_truncated", Err: parseErr}
			}
			return nil, &GenerationError{Code: "model_output_invalid", Err: parseErr}
		}
		batchClusterByID := make(map[string]TopicCluster, len(batch))
		for _, item := range batch {
			batchClusterByID[item.ClusterID] = clusterByID[item.ClusterID]
		}
		relations, resolveErr := resolveRelationOutput(batchClusterByID, output)
		if resolveErr != nil && isRelationWhitelistError(resolveErr) {
			correctionPrompt := prompt + "\n\nCORRECTION:\n" + resolveErr.Error() + "\nReturn one corrected JSON object only. Use evidence references from the corresponding endpoint cluster."
			raw, correctionErr := gate.Complete(ctx, g.LLM, "relation_generation", fmt.Sprintf("batch-%03d-correction", batchNumber), correctionPrompt, limit, nil)
			if correctionErr != nil {
				return nil, classifyCompletionError("correct training orchestration relations", correctionErr)
			}
			if tag, ok := unclosedLeadingReasoning(raw); ok {
				return nil, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("relation correction ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
			}
			output, parseErr = parseRelationGenerationOutput(raw)
			if parseErr != nil && isIncompleteJSONError(parseErr) {
				return nil, &GenerationError{Code: "model_output_truncated", Err: parseErr}
			}
			if parseErr == nil {
				relations, resolveErr = resolveRelationOutput(batchClusterByID, output)
			}
		}
		if parseErr != nil {
			return nil, &GenerationError{Code: "model_output_invalid", Err: parseErr}
		}
		if resolveErr != nil {
			return nil, &GenerationError{Code: "invalid_reference", Err: resolveErr}
		}
		allRelations = append(allRelations, relations...)
	}
	return normalizeStageFourRelations(allRelations)
}

func (g *RelationGenerator) batchLimit() int {
	if g.BatchMaxInputTokens > 0 {
		return g.BatchMaxInputTokens
	}
	if g.MaxInputTokens > 0 {
		return g.MaxInputTokens
	}
	return defaultRelationBatchTokens
}

func (g *RelationGenerator) packRelationClusters(clusters []relationGenerationCluster) ([][]relationGenerationCluster, error) {
	maxClusters := g.MaxClustersPerBatch
	if maxClusters <= 0 {
		maxClusters = defaultRelationBatchClusters
	}
	limit := g.batchLimit()
	result := make([][]relationGenerationCluster, 0)
	for index := 0; index < len(clusters); {
		end := index
		for end < len(clusters) && end-index < maxClusters {
			candidate := append([]relationGenerationCluster(nil), clusters[index:end+1]...)
			prompt, err := g.buildPrompt(candidate)
			if err != nil {
				return nil, err
			}
			tokens, err := estimateTokens(prompt)
			if err != nil {
				return nil, err
			}
			if tokens > limit {
				if end == index {
					return nil, &InputCapacityError{Tokens: tokens, Limit: limit}
				}
				break
			}
			end++
		}
		result = append(result, append([]relationGenerationCluster(nil), clusters[index:end]...))
		index = end
	}
	return result, nil
}

func (g *RelationGenerator) buildPrompt(clusters []relationGenerationCluster) (string, error) {
	requestID := "relations"
	if g.RequestID != nil {
		if custom := strings.TrimSpace(g.RequestID()); custom != "" {
			requestID = custom
		}
	}
	envelope := relationGenerationEnvelope{
		ContractVersion: RelationContractVersion, RequestID: requestID,
		Input:  relationGenerationInput{Clusters: clusters},
		Limits: relationGenerationLimits{MaxRelations: maxGeneratedRelations, MaxOutputTokens: 8000},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode relation generation input: %w", err)
	}
	return strings.TrimSpace(relationGenerationPrompt) + "\n\nINPUT:\n" + string(raw), nil
}

func (g *RelationGenerator) complete(ctx context.Context, prompt string) (string, error) {
	if structured, ok := g.LLM.(structuredCompletionClient); ok {
		return structured.CompleteJSON(ctx, prompt)
	}
	if streaming, ok := g.LLM.(streamingCompletionClient); ok {
		return streaming.Stream(ctx, prompt, nil)
	}
	return g.LLM.Complete(ctx, prompt)
}

func parseRelationGenerationOutput(raw string) (relationGenerationModelOutput, error) {
	decoder := json.NewDecoder(bytes.NewReader(stripJSONFence(raw)))
	decoder.DisallowUnknownFields()
	var output relationGenerationModelOutput
	if err := decoder.Decode(&output); err != nil {
		return relationGenerationModelOutput{}, fmt.Errorf("decode relation generation output: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return relationGenerationModelOutput{}, fmt.Errorf("relation generation output contains trailing content")
	}
	if output.Relations == nil {
		return relationGenerationModelOutput{}, fmt.Errorf("relation generation relations must be a JSON array")
	}
	return output, nil
}

func resolveRelationOutput(clusterByID map[string]TopicCluster, output relationGenerationModelOutput) ([]TopicClusterRelation, error) {
	if len(output.Relations) > maxGeneratedRelations {
		return nil, fmt.Errorf("relation generation exceeds %d relations", maxGeneratedRelations)
	}
	result := make([]TopicClusterRelation, 0, len(output.Relations))
	for index, relation := range output.Relations {
		source, sourceOK := clusterByID[strings.TrimSpace(relation.SourceClusterID)]
		target, targetOK := clusterByID[strings.TrimSpace(relation.TargetClusterID)]
		if !sourceOK || !targetOK {
			return nil, fmt.Errorf("relation %d references an unknown cluster", index+1)
		}
		if source.ClusterID == target.ClusterID {
			return nil, fmt.Errorf("relation %d connects a cluster to itself", index+1)
		}
		if _, ok := allowedRelationTypes[relation.RelationType]; !ok {
			return nil, fmt.Errorf("relation %d has unsupported relation type %q", index+1, relation.RelationType)
		}
		if strings.TrimSpace(relation.Summary) == "" || relation.Confidence < 0 || relation.Confidence > 1 {
			return nil, fmt.Errorf("relation %d has invalid summary or confidence", index+1)
		}
		sourceRefs, err := resolveRelationRefs(source, relation.SourceEvidenceRefs)
		if err != nil {
			return nil, fmt.Errorf("relation %d source evidence: %w", index+1, err)
		}
		targetRefs, err := resolveRelationRefs(target, relation.TargetEvidenceRefs)
		if err != nil {
			return nil, fmt.Errorf("relation %d target evidence: %w", index+1, err)
		}
		sourceID, targetID := source.ClusterID, target.ClusterID
		if relation.RelationType == "complementary" || relation.RelationType == "contrast" {
			if sourceID > targetID {
				sourceID, targetID = targetID, sourceID
				sourceRefs, targetRefs = targetRefs, sourceRefs
			}
		}
		result = append(result, TopicClusterRelation{
			SourceClusterID: sourceID, TargetClusterID: targetID, RelationType: relation.RelationType,
			Summary: strings.TrimSpace(relation.Summary), SourceEvidenceRefs: sourceRefs,
			TargetEvidenceRefs: targetRefs, Confidence: relation.Confidence, ReviewStatus: "passed",
		})
	}
	return result, nil
}

func resolveRelationRefs(cluster TopicCluster, refs []relationGenerationRef) ([]EvidenceRef, error) {
	if len(refs) == 0 {
		return nil, fmt.Errorf("relation endpoint requires evidence")
	}
	allowed := make(map[string]EvidenceRef, len(cluster.EvidenceRefs))
	for _, ref := range cluster.EvidenceRefs {
		allowed[ref.VideoID+"\x00"+ref.EvidenceID] = ref
	}
	result := make([]EvidenceRef, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		key := strings.TrimSpace(ref.VideoID) + "\x00" + strings.TrimSpace(ref.EvidenceID)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("relation endpoint repeats evidence %s", ref.EvidenceID)
		}
		allowedRef, ok := allowed[key]
		if !ok {
			return nil, fmt.Errorf("evidence is outside its endpoint cluster")
		}
		seen[key] = struct{}{}
		result = append(result, allowedRef)
	}
	return result, nil
}

func isRelationWhitelistError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "outside its endpoint cluster")
}
