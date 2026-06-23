package embedding

import "testing"

func TestEmbeddingDimensionsParamRequiresExplicitOverride(t *testing.T) {
	tests := []struct {
		name     string
		supports func(bool) bool
	}{
		{
			name: "aliyun",
			supports: func(enabled bool) bool {
				e := &AliyunEmbedder{dimensions: 1024}
				e.SetSupportsDimensionOverride(enabled)
				return e.supportsDimensionsParam()
			},
		},
		{
			name: "nvidia",
			supports: func(enabled bool) bool {
				e := &NvidiaEmbedder{dimensions: 1024}
				e.SetSupportsDimensionOverride(enabled)
				return e.supportsDimensionsParam()
			},
		},
		{
			name: "volcengine",
			supports: func(enabled bool) bool {
				e := &VolcengineEmbedder{dimensions: 1024}
				e.SetSupportsDimensionOverride(enabled)
				return e.supportsDimensionsParam()
			},
		},
		{
			name: "zhipu",
			supports: func(enabled bool) bool {
				e := &ZhipuEmbedder{dimensions: 1024}
				e.SetSupportsDimensionOverride(enabled)
				return e.supportsDimensionsParam()
			},
		},
		{
			name: "azure_openai",
			supports: func(enabled bool) bool {
				e := &AzureOpenAIEmbedder{dimensions: 1024}
				e.SetSupportsDimensionOverride(enabled)
				return e.supportsDimensionsParam()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.supports(false); got {
				t.Fatalf("supportsDimensionsParam() = true with override disabled")
			}
			if got := tt.supports(true); !got {
				t.Fatalf("supportsDimensionsParam() = false with override enabled")
			}
		})
	}
}

func TestEmbeddingDimensionsParamRequiresPositiveDimension(t *testing.T) {
	e := &OpenAIEmbedder{dimensions: 0}
	e.SetSupportsDimensionOverride(true)
	if e.supportsDimensionsParam() {
		t.Fatal("expected dimensions param to be omitted when configured dimension is zero")
	}
}
