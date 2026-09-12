package metricregistry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// SemanticJudgeRequest is the content and frozen rubric supplied to an external judge.
type SemanticJudgeRequest struct {
	GeneratedText      string
	ReferenceText      string
	Rubric             string
	PromptVersion      string
	PromptSHA256       string
	JudgeModelSnapshot json.RawMessage
	Temperature        float64
	Seed               int64
	RetryLimit         int
}

// SemanticJudge evaluates one generated answer and returns a finite score in [0,1].
type SemanticJudge interface {
	Judge(context.Context, SemanticJudgeRequest) (float64, error)
}

type semanticJudgeMetric struct{ judge SemanticJudge }

type semanticJudgeConfig struct {
	Rubric             string          `json:"rubric"`
	PromptVersion      string          `json:"prompt_version"`
	PromptSHA256       string          `json:"prompt_sha256"`
	JudgeModelSnapshot json.RawMessage `json:"judge_model_snapshot"`
	Temperature        float64         `json:"temperature"`
	Seed               int64           `json:"seed"`
	RetryLimit         int             `json:"retry_limit"`
}

// NewSemanticJudgeMetric creates an opt-in registry plugin. The default registry does not include it.
func NewSemanticJudgeMetric(judge SemanticJudge) (Metric, error) {
	if judge == nil {
		return nil, errors.New("semantic judge metric: judge is required")
	}
	return semanticJudgeMetric{judge: judge}, nil
}

func (semanticJudgeMetric) Definition() Definition {
	return Definition{
		Key: "generation.semantic_judge", Version: "1.0.0", Kind: KindGeneration,
		Description: "External semantic judge score using a frozen rubric.",
		DefaultConfig: json.RawMessage(
			`{"rubric":"Score factual agreement with the reference answer from 0 to 1.",` +
				`"prompt_version":"1.0.0",` +
				`"prompt_sha256":"93d91f65a4e4e60ca6b5e5884a53a0add8693d3f40cb5155f3d63d11683fbea9",` +
				`"judge_model_snapshot":{"id":"semantic-judge","fingerprint":"injected"},` +
				`"temperature":0,"seed":0,"retry_limit":0}`,
		),
		ConfigSchema: json.RawMessage(
			`{"type":"object",` +
				`"required":["rubric","prompt_version","prompt_sha256","judge_model_snapshot",` +
				`"temperature","seed","retry_limit"],` +
				`"properties":{"rubric":{"type":"string","minLength":1,"maxLength":4000},` +
				`"prompt_version":{"type":"string","minLength":1,"maxLength":64},` +
				`"prompt_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"},` +
				`"judge_model_snapshot":{"type":"object"},` +
				`"temperature":{"type":"number","minimum":0,"maximum":2},` +
				`"seed":{"type":"integer"},` +
				`"retry_limit":{"type":"integer","minimum":0,"maximum":10}},` +
				`"additionalProperties":false}`,
		),
	}
}

func (semanticJudgeMetric) Validate(config json.RawMessage) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(config, &raw); err != nil {
		return err
	}
	for key := range raw {
		if key != "rubric" && key != "prompt_version" && key != "prompt_sha256" &&
			key != "judge_model_snapshot" && key != "temperature" && key != "seed" && key != "retry_limit" {
			return errors.New("semantic judge metric: configuration contains unknown fields")
		}
	}
	var parsed semanticJudgeConfig
	if err := json.Unmarshal(config, &parsed); err != nil {
		return err
	}
	parsed.Rubric = strings.TrimSpace(parsed.Rubric)
	if parsed.Rubric == "" || len([]byte(parsed.Rubric)) > 4000 || strings.TrimSpace(parsed.PromptVersion) == "" ||
		len([]byte(parsed.PromptVersion)) > 64 || parsed.Temperature < 0 || parsed.Temperature > 2 ||
		parsed.RetryLimit < 0 || parsed.RetryLimit > 10 {
		return errors.New("semantic judge metric: frozen prompt and execution settings are invalid")
	}
	var modelSnapshot map[string]json.RawMessage
	if json.Unmarshal(parsed.JudgeModelSnapshot, &modelSnapshot) != nil || len(modelSnapshot) == 0 {
		return errors.New("semantic judge metric: judge_model_snapshot must be a non-empty object")
	}
	digest := sha256.Sum256([]byte(parsed.Rubric))
	if parsed.PromptSHA256 != fmt.Sprintf("%x", digest) {
		return errors.New("semantic judge metric: prompt_sha256 does not match rubric UTF-8 bytes")
	}
	return nil
}

func (m semanticJudgeMetric) Compute(
	ctx context.Context,
	input *types.MetricInput,
	config json.RawMessage,
) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{Status: types.EvaluationMetricObservationFailed, ErrorCode: "context_canceled"}, err
	}
	if input == nil {
		return Observation{Status: types.EvaluationMetricObservationFailed, ErrorCode: "invalid_metric_input"},
			errors.New("semantic judge metric: input is required")
	}
	if err := m.Validate(config); err != nil {
		return Observation{Status: types.EvaluationMetricObservationFailed, ErrorCode: "invalid_metric_config"}, err
	}
	var parsed semanticJudgeConfig
	_ = json.Unmarshal(config, &parsed)
	value, err := m.judge.Judge(ctx, SemanticJudgeRequest{
		GeneratedText: input.GeneratedTexts, ReferenceText: input.GeneratedGT,
		Rubric: strings.TrimSpace(parsed.Rubric), PromptVersion: strings.TrimSpace(parsed.PromptVersion),
		PromptSHA256:       parsed.PromptSHA256,
		JudgeModelSnapshot: append(json.RawMessage(nil), parsed.JudgeModelSnapshot...),
		Temperature:        parsed.Temperature, Seed: parsed.Seed, RetryLimit: parsed.RetryLimit,
	})
	if err != nil {
		return Observation{Status: types.EvaluationMetricObservationFailed, ErrorCode: "semantic_judge_failed"}, err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return Observation{Status: types.EvaluationMetricObservationFailed, ErrorCode: "semantic_judge_invalid_score"},
			errors.New("semantic judge metric: judge score must be finite and between zero and one")
	}
	return Observation{Value: &value, Status: types.EvaluationMetricObservationValid}, nil
}
