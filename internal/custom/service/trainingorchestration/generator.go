package trainingorchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type CompletionClient interface {
	Complete(context.Context, string) (string, error)
	Model() string
	PromptVersion() string
}

type streamingCompletionClient interface {
	Stream(context.Context, string, func(string) error) (string, error)
}

type structuredCompletionClient interface {
	CompleteJSON(context.Context, string) (string, error)
}

type Generator struct {
	LLM            CompletionClient
	Gate           *CompletionGate
	MaxInputTokens int
	PromptVersion  string
	Now            func() time.Time
}

type modelProjection struct {
	TopicClusters         []TopicCluster         `json:"topic_clusters"`
	TopicClusterRelations []TopicClusterRelation `json:"topic_cluster_relations"`
}

//go:embed prompts/training-orchestration-v2.txt
var trainingOrchestrationPromptV2 string

type InputCapacityError struct{ Tokens, Limit int }

func (e *InputCapacityError) Error() string {
	return fmt.Sprintf("training orchestration input exceeds configured token limit %d: got %d", e.Limit, e.Tokens)
}

type GenerationError struct {
	Code string
	Err  error
}

func (e *GenerationError) Error() string { return e.Err.Error() }
func (e *GenerationError) Unwrap() error { return e.Err }

func (g *Generator) Generate(ctx context.Context, input InputPackage) (ProjectionDocument, error) {
	if g == nil || g.LLM == nil {
		return ProjectionDocument{}, fmt.Errorf("training orchestration llm is not configured")
	}
	input = withoutKnowledgeObjects(input)
	fingerprint, err := SourceFingerprint(input, g.LLM.Model(), g.promptVersion())
	if err != nil {
		return ProjectionDocument{}, err
	}
	now := time.Now().UTC()
	if g.Now != nil {
		now = g.Now().UTC()
	}
	projection := Projection{SchemaVersion: SchemaVersion, OwnerScopeID: input.OwnerScopeID, SourceFingerprint: fingerprint, GeneratedAt: now.Format(time.RFC3339Nano), TopicClusters: []TopicCluster{}, TopicClusterRelations: []TopicClusterRelation{}}
	if len(input.QualifiedVideos) == 0 {
		projection.Statistics = buildStatistics(input, nil, map[string]struct{}{}, nil)
		doc := ProjectionDocument{TrainingPathProjection: projection}
		if err := ValidateProjection(doc, input); err != nil {
			return ProjectionDocument{}, err
		}
		return doc, nil
	}
	prompt, err := buildPrompt(input)
	if err != nil {
		return ProjectionDocument{}, err
	}
	gate := g.Gate
	if gate == nil {
		gate = NewCompletionGate(CompletionGateConfig{})
	}
	raw, err := gate.Complete(ctx, g.LLM, "legacy_generation", "all", prompt, g.MaxInputTokens, nil)
	if err != nil {
		return ProjectionDocument{}, classifyCompletionError("generate training orchestration", err)
	}
	if tag, ok := unclosedLeadingReasoning(raw); ok {
		return ProjectionDocument{}, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("training orchestration model output ended before closing <%s> (output_bytes=%d)", tag, len(raw))}
	}
	normalized := stripJSONFence(raw)
	var generated modelProjection
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&generated); err != nil {
		if isIncompleteJSONError(err) {
			return ProjectionDocument{}, &GenerationError{Code: "model_output_truncated", Err: fmt.Errorf("decode training orchestration model output: %w", err)}
		}
		return ProjectionDocument{}, &GenerationError{Code: "model_output_invalid", Err: fmt.Errorf("decode training orchestration model output: %w", err)}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ProjectionDocument{}, &GenerationError{Code: "model_output_invalid", Err: fmt.Errorf("decode training orchestration model output: trailing content")}
	}
	if generated.TopicClusters == nil || generated.TopicClusterRelations == nil {
		return ProjectionDocument{}, &GenerationError{Code: "model_output_invalid", Err: fmt.Errorf("decode training orchestration model output: topic arrays must be JSON arrays")}
	}
	projection.TopicClusters = generated.TopicClusters
	projection.TopicClusterRelations = generated.TopicClusterRelations
	normalizeProjectionKnowledgeFields(&projection)
	selectedVideos := map[string]struct{}{}
	unitEvidence := make([]EvidenceRef, 0)
	for _, cluster := range projection.TopicClusters {
		for _, id := range cluster.SourceVideoIDs {
			selectedVideos[id] = struct{}{}
		}
		for _, stage := range cluster.Path.Stages {
			for _, unit := range stage.Units {
				unitEvidence = append(unitEvidence, unit.EvidenceRefs...)
			}
		}
	}
	projection.Statistics = buildStatistics(input, projection.TopicClusters, selectedVideos, unitEvidence)
	doc := ProjectionDocument{TrainingPathProjection: projection}
	if err := ValidateProjection(doc, input); err != nil {
		return ProjectionDocument{}, &GenerationError{Code: "model_output_invalid", Err: fmt.Errorf("reject training orchestration model output: %w", err)}
	}
	return doc, nil
}

func unclosedLeadingReasoning(raw string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	for _, tag := range []string{"think", "analysis"} {
		if strings.HasPrefix(lower, "<"+tag+">") && !strings.Contains(lower, "</"+tag+">") {
			return tag, true
		}
	}
	return "", false
}

func (g *Generator) promptVersion() string {
	if strings.TrimSpace(g.PromptVersion) != "" {
		return strings.TrimSpace(g.PromptVersion)
	}
	return g.LLM.PromptVersion()
}

func SourceFingerprint(input InputPackage, model, promptVersion string) (string, error) {
	input = withoutKnowledgeObjects(input)
	payload := struct {
		Input         InputPackage `json:"input"`
		Model         string       `json:"model"`
		PromptVersion string       `json:"prompt_version"`
		SchemaVersion string       `json:"schema_version"`
	}{input, strings.TrimSpace(model), strings.TrimSpace(promptVersion), SchemaVersion}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode training orchestration fingerprint: %w", err)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:]), nil
}

func buildPrompt(input InputPackage) (string, error) {
	input = withoutKnowledgeObjects(input)
	raw, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode training orchestration input: %w", err)
	}
	return strings.TrimSpace(trainingOrchestrationPromptV2) + "\n\nINPUT:\n" + string(raw), nil
}

func stripJSONFence(raw string) []byte {
	trimmed := strings.TrimSpace(raw)
	for {
		before := trimmed
		for _, tag := range []string{"think", "analysis"} {
			if remainder, ok := stripLeadingTagBlock(trimmed, tag); ok {
				trimmed = remainder
				break
			}
		}
		for _, tag := range []string{"final", "answer"} {
			if inner, ok := unwrapTag(trimmed, tag); ok {
				trimmed = inner
				break
			}
		}
		if strings.HasPrefix(trimmed, "```") {
			lines := strings.Split(trimmed, "\n")
			if len(lines) >= 3 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
				trimmed = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
			}
		}
		if trimmed == before {
			break
		}
	}
	return []byte(trimmed)
}

func stripLeadingTagBlock(value, tag string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	opening := "<" + tag + ">"
	closing := "</" + tag + ">"
	if !strings.HasPrefix(lower, opening) {
		return value, false
	}
	end := strings.Index(lower, closing)
	if end < 0 {
		return value, false
	}
	return strings.TrimSpace(trimmed[end+len(closing):]), true
}

func unwrapTag(value, tag string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	opening := "<" + tag + ">"
	closing := "</" + tag + ">"
	if !strings.HasPrefix(lower, opening) || !strings.HasSuffix(lower, closing) {
		return value, false
	}
	return strings.TrimSpace(trimmed[len(opening) : len(trimmed)-len(closing)]), true
}
