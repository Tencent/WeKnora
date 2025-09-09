package rerank

import (
	"testing"
)

// TestAliyunRerankerCreation tests creating an Aliyun reranker
func TestAliyunRerankerCreation(t *testing.T) {
	config := &RerankerConfig{
		APIKey:    "test-api-key",
		BaseURL:   "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank",
		ModelName: "text-rerank",
		Source:    "aliyun",
		ModelID:   "aliyun-text-rerank",
	}

	reranker, err := NewAliyunReranker(config)
	if err != nil {
		t.Fatalf("Failed to create Aliyun reranker: %v", err)
	}

	if reranker.GetModelName() != config.ModelName {
		t.Errorf("Expected model name %s, got %s", config.ModelName, reranker.GetModelName())
	}

	if reranker.GetModelID() != config.ModelID {
		t.Errorf("Expected model ID %s, got %s", config.ModelID, reranker.GetModelID())
	}
}

// TestAliyunRerankerViaNewReranker tests creating an Aliyun reranker through NewReranker
func TestAliyunRerankerViaNewReranker(t *testing.T) {
	config := &RerankerConfig{
		APIKey:    "test-api-key",
		BaseURL:   "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank",
		ModelName: "text-rerank",
		Source:    "aliyun",
		ModelID:   "aliyun-text-rerank",
	}

	reranker, err := NewReranker(config)
	if err != nil {
		t.Fatalf("Failed to create reranker: %v", err)
	}

	// Verify it's an AliyunReranker
	_, ok := reranker.(*AliyunReranker)
	if !ok {
		t.Errorf("Expected AliyunReranker, got %T", reranker)
	}

	if reranker.GetModelName() != config.ModelName {
		t.Errorf("Expected model name %s, got %s", config.ModelName, reranker.GetModelName())
	}

	if reranker.GetModelID() != config.ModelID {
		t.Errorf("Expected model ID %s, got %s", config.ModelID, reranker.GetModelID())
	}
}

// TestAliyunRerankerUnsupportedSource tests error handling for unsupported sources
func TestAliyunRerankerUnsupportedSource(t *testing.T) {
	config := &RerankerConfig{
		APIKey:    "test-api-key",
		BaseURL:   "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank",
		ModelName: "text-rerank",
		Source:    "unsupported",
		ModelID:   "aliyun-text-rerank",
	}

	_, err := NewReranker(config)
	if err == nil {
		t.Error("Expected error for unsupported source, got nil")
	}

	expectedError := "unsupported rerank model source: unsupported"
	if err.Error() != expectedError {
		t.Errorf("Expected error %s, got %s", expectedError, err.Error())
	}
}
