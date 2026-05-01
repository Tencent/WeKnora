package provider

import "strings"

// Model-family detectors live here, not next to per-provider info, because
// they are routing hints used by the chat layer's per-model customizers (see
// internal/models/chat/chat_provider_spec.go). Keeping them in one file
// avoids scattering them across the registry.

// IsQwenThinkingModel reports whether the model belongs to the Qwen family
// that supports the `enable_thinking` parameter. Used by the Aliyun chat
// customizer to toggle Qwen3-style thinking on/off.
func IsQwenThinkingModel(modelName string) bool {
	lower := strings.ToLower(modelName)
	return strings.HasPrefix(lower, "qwen3") ||
		strings.HasPrefix(lower, "qwen-plus") ||
		strings.HasPrefix(lower, "qwen-max") ||
		strings.HasPrefix(lower, "qwen-turbo")
}

// IsQwen3Model reports whether the model belongs to the Qwen3 family
// specifically. Tested directly by provider_test.go, so keep the name stable.
func IsQwen3Model(modelName string) bool {
	return strings.HasPrefix(strings.ToLower(modelName), "qwen3")
}

// IsDeepSeekModel reports whether the model name contains "deepseek". DeepSeek
// chat endpoints don't accept the `tool_choice` parameter, so chat customizers
// use this to suppress it.
func IsDeepSeekModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "deepseek")
}

// IsLKEAPDeepSeekV3Model detects LKEAP's DeepSeek V3.x models. V3 supports a
// thinking toggle; V1/V2 do not.
func IsLKEAPDeepSeekV3Model(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "deepseek-v3")
}

// IsLKEAPDeepSeekR1Model detects LKEAP's DeepSeek R1 models. R1 always runs
// with thinking enabled — no toggle.
func IsLKEAPDeepSeekR1Model(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "deepseek-r1")
}

// IsLKEAPThinkingModel is true when the model is either R1 or V3 family.
func IsLKEAPThinkingModel(modelName string) bool {
	return IsLKEAPDeepSeekR1Model(modelName) || IsLKEAPDeepSeekV3Model(modelName)
}
