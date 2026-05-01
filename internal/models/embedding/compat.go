package embedding

// Legacy constructors kept purely to satisfy existing tests in this package
// that pre-date the spec-based refactor. New code should go through
// newRemoteEmbedder (see embedder.go).
//
// These are deliberately unexported for the public API surface but keep the
// test-visible name. Each wires its Config-based counterpart to the
// corresponding spec.

// NewAzureOpenAIEmbedder is a compatibility shim for azure_openai_test.go.
// It constructs an httpEmbedder configured against azureOpenAISpec, exposing
// the .httpClient field that the test overrides to inject a fake transport.
func NewAzureOpenAIEmbedder(
	apiKey, baseURL, modelName string,
	truncatePromptTokens int, dimensions int, modelID string,
	apiVersion string, pooler EmbedderPooler,
) (*httpEmbedder, error) {
	cfg := Config{
		APIKey:               apiKey,
		BaseURL:              baseURL,
		ModelName:            modelName,
		TruncatePromptTokens: truncatePromptTokens,
		Dimensions:           dimensions,
		ModelID:              modelID,
	}
	if apiVersion != "" {
		cfg.ExtraConfig = map[string]string{"api_version": apiVersion}
	}
	return newHTTPEmbedder(cfg, pooler, azureOpenAISpec)
}
