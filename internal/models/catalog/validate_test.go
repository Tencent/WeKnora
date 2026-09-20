package catalog_test

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/catalog"
	_ "github.com/Tencent/WeKnora/internal/models/vendors"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateRow(t *testing.T) {
	cases := []struct {
		name      string
		modelName string
		modelType types.ModelType
		params    *types.ModelParameters
		wantErr   bool
	}{
		{
			name: "nil parameters", modelName: "x", modelType: types.ModelTypeKnowledgeQA,
		},
		{
			name: "embedding rows carry no catalog parameters", modelName: "text-embedding-v4",
			modelType: types.ModelTypeEmbedding,
			params:    &types.ModelParameters{Provider: "aliyun", Spec: &types.ModelSpecOverride{API: "nope"}},
		},
		{
			name: "a catalogued chat row resolves", modelName: "deepseek-v4-pro",
			modelType: types.ModelTypeKnowledgeQA,
			params:    &types.ModelParameters{Provider: "deepseek"},
		},
		{
			name: "unknown protocol is rejected", modelName: "deepseek-v4-pro",
			modelType: types.ModelTypeKnowledgeQA,
			params: &types.ModelParameters{
				Provider: "deepseek", Spec: &types.ModelSpecOverride{API: "openai-chat-v9"},
			},
			wantErr: true,
		},
		{
			name: "misspelled thinking level is rejected", modelName: "deepseek-v4-pro",
			modelType: types.ModelTypeKnowledgeQA,
			params: &types.ModelParameters{
				Provider: "deepseek",
				Spec:     &types.ModelSpecOverride{ThinkingLevels: map[string]*string{"hgih": nil}},
			},
			wantErr: true,
		},
		{
			name: "unknown compat key is rejected", modelName: "deepseek-v4-pro",
			modelType: types.ModelTypeKnowledgeQA,
			params: &types.ModelParameters{
				Provider: "deepseek",
				Spec:     &types.ModelSpecOverride{Compat: map[string]any{"max_tokens_fields": "max_tokens"}},
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := catalog.ValidateRow(tc.modelName, tc.modelType, tc.params)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
