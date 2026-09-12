package types

import (
	"net/url"
	"strings"
	"unicode"
)

// EvaluationModelBehaviorConfig is the sanitized behavior configuration that
// participates in the model config fingerprint. It deliberately excludes API
// keys, app secrets, app IDs, custom headers, and URL-embedded credentials:
// the fingerprint explains output behavior, never authentication. The model
// write paths maintain a non-secret behavior revision when effective custom
// headers change.
type EvaluationModelBehaviorConfig struct {
	ContextWindow       int                 `json:"context_window"`
	MaxOutputTokens     int                 `json:"max_output_tokens"`
	BaseURL             string              `json:"base_url"`
	InterfaceType       string              `json:"interface_type"`
	Provider            string              `json:"provider"`
	ParameterSize       string              `json:"parameter_size"`
	EmbeddingParameters EmbeddingParameters `json:"embedding_parameters"`
	ExtraConfig         map[string]string   `json:"extra_config"`
	SupportsVision      bool                `json:"supports_vision"`
	MaxConcurrency      int                 `json:"max_concurrency"`
}

// EvaluationModelSanitizeBaseURL strips credentials and the client-only
// fragment from an upstream base URL. Non-sensitive query parameters remain
// part of the endpoint identity because values such as api-version can change
// provider behavior.
func EvaluationModelSanitizeBaseURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		// Unparseable values carry no structured credentials; keep the raw
		// string so fingerprint drift remains visible.
		return rawURL
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		if evaluationModelConfigKeyIsSensitive(key) {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	if parsed.RawQuery == "" {
		parsed.ForceQuery = false
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

// EvaluationModelBehaviorConfigFrom extracts the sanitized behavior
// configuration of one model record.
func EvaluationModelBehaviorConfigFrom(model *Model) *EvaluationModelBehaviorConfig {
	if model == nil {
		return &EvaluationModelBehaviorConfig{ExtraConfig: map[string]string{}}
	}
	parameters := model.Parameters
	extra := make(map[string]string, len(parameters.ExtraConfig))
	for key, value := range parameters.ExtraConfig {
		if evaluationModelConfigKeyIsSensitive(key) {
			continue
		}
		extra[key] = value
	}
	return &EvaluationModelBehaviorConfig{
		BaseURL:             EvaluationModelSanitizeBaseURL(parameters.BaseURL),
		InterfaceType:       parameters.InterfaceType,
		Provider:            parameters.Provider,
		ParameterSize:       parameters.ParameterSize,
		EmbeddingParameters: parameters.EmbeddingParameters,
		ExtraConfig:         extra,
		SupportsVision:      parameters.SupportsVision,
		MaxConcurrency:      parameters.MaxConcurrency,
		ContextWindow:       parameters.ContextWindow, MaxOutputTokens: parameters.MaxOutputTokens,
	}
}

// EvaluationModelConfigSHA256 computes the canonical fingerprint of one
// model's sanitized behavior configuration.
func EvaluationModelConfigSHA256(model *Model) string {
	return EvaluationModelConfigSHA256ForVersion(model, 2)
}

// EvaluationModelConfigSHA256ForVersion fingerprints sanitized model behavior for the specified contract version.
func EvaluationModelConfigSHA256ForVersion(model *Model, version int) string {
	config := EvaluationModelBehaviorConfigFrom(model)
	payload := map[string]any{
		"base_url":       config.BaseURL,
		"interface_type": config.InterfaceType,
		"provider":       config.Provider,
		"parameter_size": config.ParameterSize,
		"embedding_parameters": map[string]any{
			"dimension":                   config.EmbeddingParameters.Dimension,
			"truncate_prompt_tokens":      config.EmbeddingParameters.TruncatePromptTokens,
			"supports_dimension_override": config.EmbeddingParameters.SupportsDimensionOverride,
		},
		"extra_config":    evaluationStringMapToAny(config.ExtraConfig),
		"supports_vision": config.SupportsVision,
		"max_concurrency": config.MaxConcurrency,
	}
	if version >= 2 {
		if model != nil {
			payload["model_name"] = model.Name
			payload["model_type"] = string(model.Type)
			payload["model_source"] = string(model.Source)
		}
		payload["context_window"] = config.ContextWindow
		payload["max_output_tokens"] = config.MaxOutputTokens
		payload["behavior_version"] = 2
		payload["prompt_cache_policy"] = "explicit-v1"
		payload["output_budget_policy"] = "model-ceiling-v1"
	}
	encoded := canonicalEvaluationJSONBytes(payload)
	return "sha256:" + hashEvaluationCanonicalJSON(encoded)
}

// EvaluationModelSnapshotFrom builds the snapshot entry for one model role.
func EvaluationModelSnapshotFrom(model *Model) *EvaluationModelSnapshot {
	if model == nil {
		return nil
	}
	return &EvaluationModelSnapshot{
		ConfigVersion: 2,
		ID:            model.ID,
		UpstreamName:  model.Name,
		Type:          string(model.Type),
		Source:        string(model.Source),
		Provider:      model.Parameters.Provider,
		InterfaceType: model.Parameters.InterfaceType,
		ConfigSHA256:  EvaluationModelConfigSHA256(model),
		UpdatedAt:     model.UpdatedAt.UTC(),
	}
}

func evaluationModelConfigKeyIsSensitive(key string) bool {
	parts := splitEvaluationConfigKey(key)
	for _, part := range parts {
		switch part {
		case "secret", "password", "passwd", "authorization", "credential", "credentials":
			return true
		}
	}
	compact := strings.Join(parts, "")
	for _, suffix := range []string{
		"apikey", "appid", "appsecret", "clientid", "clientsecret", "privatekey",
		"secretkey", "accesskey", "accesskeyid", "subscriptionkey", "sessiontoken",
		"accesstoken", "refreshtoken", "idtoken", "authtoken", "bearertoken",
		"password", "credential", "token", "secret",
	} {
		if strings.HasSuffix(compact, suffix) {
			return true
		}
	}
	return false
}

func splitEvaluationConfigKey(key string) []string {
	return strings.FieldsFunc(strings.ToLower(strings.TrimSpace(key)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func evaluationStringMapToAny(input map[string]string) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
