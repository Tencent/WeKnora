package embedding

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestConfigFromModel(t *testing.T) {
	m := &types.Model{
		ID:     "emb-1",
		Name:   "text-embedding-3-small",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:  "https://api.example.com/v1",
			APIKey:   "sk-xxx",
			Provider: "openai",
			EmbeddingParameters: types.EmbeddingParameters{
				Dimension:            1536,
				TruncatePromptTokens: 512,
			},
			ExtraConfig:   map[string]string{"region": "us-east"},
			CustomHeaders: map[string]string{"X-Gateway": "g1"},
		},
	}

	cfg := ConfigFromModel(m, "app", "secret")
	if cfg.ModelID != "emb-1" || cfg.ModelName != "text-embedding-3-small" {
		t.Errorf("identity mismatch: %+v", cfg)
	}
	if cfg.Dimensions != 1536 || cfg.TruncatePromptTokens != 512 {
		t.Errorf("embedding params mismatch: %+v", cfg)
	}
	if cfg.CustomHeaders["X-Gateway"] != "g1" {
		t.Errorf("CustomHeaders not propagated: %+v", cfg.CustomHeaders)
	}
	if cfg.ExtraConfig["region"] != "us-east" {
		t.Errorf("ExtraConfig not propagated: %+v", cfg.ExtraConfig)
	}
	if cfg.AppID != "app" || cfg.AppSecret != "secret" {
		t.Errorf("cloud creds mismatch: %+v", cfg)
	}
}

func TestNewEmbedderAppliesAsymmetricExtraConfig(t *testing.T) {
	openAIEmbedder, err := NewEmbedder(Config{
		Source:    types.ModelSourceRemote,
		BaseURL:   "https://api.example.com/v1",
		ModelName: "text-embedding",
		Provider:  "generic",
		ExtraConfig: map[string]string{
			"query_input_type":    "query",
			"document_input_type": "passage",
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewEmbedder returned error: %v", err)
	}
	openAI, ok := openAIEmbedder.(*OpenAIEmbedder)
	if !ok {
		t.Fatalf("embedder type = %T, want *OpenAIEmbedder", openAIEmbedder)
	}
	if openAI.queryInputType != "query" || openAI.documentInputType != "passage" {
		t.Fatalf("OpenAI asymmetric config = %q/%q", openAI.queryInputType, openAI.documentInputType)
	}

	jinaEmbedder, err := NewEmbedder(Config{
		Source:    types.ModelSourceRemote,
		BaseURL:   "https://api.jina.ai/v1",
		ModelName: "jina-embeddings-v5-text-small-retrieval",
		Provider:  "jina",
		ExtraConfig: map[string]string{
			"query_task":    "retrieval.query",
			"document_task": "retrieval.passage",
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewEmbedder returned error: %v", err)
	}
	jina, ok := jinaEmbedder.(*JinaEmbedder)
	if !ok {
		t.Fatalf("embedder type = %T, want *JinaEmbedder", jinaEmbedder)
	}
	if jina.queryTask != "retrieval.query" || jina.documentTask != "retrieval.passage" {
		t.Fatalf("Jina asymmetric config = %q/%q", jina.queryTask, jina.documentTask)
	}
}
