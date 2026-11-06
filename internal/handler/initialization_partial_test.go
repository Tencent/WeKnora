package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpdateKBConfigPartial(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		check        func(*testing.T, *types.KnowledgeBase)
	}{
		{"models only", ``, func(t *testing.T, kb *types.KnowledgeBase) {
			require.True(t, kb.VLMConfig.Enabled)
			require.Equal(t, "vision", kb.VLMConfig.ModelID)
			require.Equal(t, "en", kb.VLMConfig.DescriptionLanguage)
			require.True(t, kb.ASRConfig.Enabled)
			require.Equal(t, "audio", kb.ASRConfig.ModelID)
			require.Equal(t, "en", kb.ASRConfig.Language)
			require.Equal(t, 80, kb.ChunkingConfig.ChunkOverlap)
			require.True(t, kb.ChunkingConfig.EnableParentChild)
			require.Len(t, kb.ChunkingConfig.ParserEngineRules, 1)
			require.True(t, kb.ExtractConfig.Enabled)
			require.True(t, kb.QuestionGenerationConfig.Enabled)
			require.Equal(t, 7, kb.QuestionGenerationConfig.QuestionCount)
		}},
		{"nested partial", `,"documentSplitting":{"chunkSize":1024},"vlm_config":{"description_language":"fr"}` +
			`,"asr_config":{"language":"fr"},"nodeExtract":{"customInstructions":" updated "}` +
			`,"questionGeneration":{"customInstructions":" updated "}`, func(t *testing.T, kb *types.KnowledgeBase) {
			require.Equal(t, 1024, kb.ChunkingConfig.ChunkSize)
			require.Equal(t, 80, kb.ChunkingConfig.ChunkOverlap)
			require.True(t, kb.ChunkingConfig.EnableParentChild)
			require.Len(t, kb.ChunkingConfig.ParserEngineRules, 1)
			require.True(t, kb.VLMConfig.IsEnabled())
			require.Equal(t, "fr", kb.VLMConfig.DescriptionLanguage)
			require.True(t, kb.ASRConfig.IsASREnabled())
			require.Equal(t, "fr", kb.ASRConfig.Language)
			require.True(t, kb.ExtractConfig.Enabled)
			require.Equal(t, "updated", kb.ExtractConfig.CustomInstructions)
			require.True(t, kb.QuestionGenerationConfig.Enabled)
			require.Equal(t, 7, kb.QuestionGenerationConfig.QuestionCount)
			require.Equal(t, "updated", kb.QuestionGenerationConfig.CustomInstructions)
		}},
		{"explicit clear", `,"documentSplitting":{"chunkOverlap":0,"parserEngineRules":[]` +
			`,"enableParentChild":false,"strategy":"","tokenLimit":0,"languages":[]}` +
			`,"multimodal":{"enabled":false},"asr_config":{"enabled":false}` +
			`,"nodeExtract":{"enabled":false},"questionGeneration":{"enabled":false` +
			`,"questionCount":0,"customInstructions":""}`, func(t *testing.T, kb *types.KnowledgeBase) {
			require.Zero(t, kb.ChunkingConfig.ChunkOverlap)
			require.Empty(t, kb.ChunkingConfig.ParserEngineRules)
			require.False(t, kb.ChunkingConfig.EnableParentChild)
			require.Empty(t, kb.ChunkingConfig.Strategy)
			require.Zero(t, kb.ChunkingConfig.TokenLimit)
			require.Empty(t, kb.ChunkingConfig.Languages)
			require.False(t, kb.VLMConfig.IsEnabled(), "legacy VLM must also be disabled")
			require.False(t, kb.ASRConfig.IsASREnabled())
			require.False(t, kb.ExtractConfig.Enabled)
			require.Equal(t, "extract", kb.ExtractConfig.Text)
			require.False(t, kb.QuestionGenerationConfig.Enabled)
			require.Zero(t, kb.QuestionGenerationConfig.QuestionCount)
			require.Empty(t, kb.QuestionGenerationConfig.CustomInstructions)
		}},
		{
			"enable preserves count", `,"questionGeneration":{"enabled":true}`,
			func(t *testing.T, kb *types.KnowledgeBase) {
				require.Equal(t, 7, kb.QuestionGenerationConfig.QuestionCount)
			},
		},
		{"first enable default", `,"questionGeneration":{"enabled":true}`, func(t *testing.T, kb *types.KnowledgeBase) {
			require.True(t, kb.QuestionGenerationConfig.Enabled)
			require.Equal(t, 3, kb.QuestionGenerationConfig.QuestionCount)
		}},
		{"empty sections", `,"documentSplitting":{},"vlm_config":{},"asr_config":{},"nodeExtract":{}` +
			`,"questionGeneration":{}`, func(t *testing.T, kb *types.KnowledgeBase) {
			require.True(t, kb.VLMConfig.IsEnabled())
			require.True(t, kb.ASRConfig.IsASREnabled())
			require.Equal(t, 80, kb.ChunkingConfig.ChunkOverlap)
			require.True(t, kb.ExtractConfig.Enabled)
			require.True(t, kb.QuestionGenerationConfig.Enabled)
			require.Equal(t, 7, kb.QuestionGenerationConfig.QuestionCount)
		}},
		{
			"zero count stays zero", `,"questionGeneration":{"customInstructions":"updated"}`,
			func(t *testing.T, kb *types.KnowledgeBase) {
				require.Zero(t, kb.QuestionGenerationConfig.QuestionCount)
			},
		},
		{"VLM explicit disable", `,"vlm_config":{"enabled":false}`, func(t *testing.T, kb *types.KnowledgeBase) {
			require.False(t, kb.VLMConfig.IsEnabled())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kb := &types.KnowledgeBase{
				ID: "kb", TenantID: 7, EmbeddingModelID: "embedding",
				VLMConfig: types.VLMConfig{
					Enabled: true, ModelID: "vision", DescriptionLanguage: "en",
					ModelName: "legacy", BaseURL: "http://legacy",
				},
				ASRConfig: types.ASRConfig{Enabled: true, ModelID: "audio", Language: "en"},
				ChunkingConfig: types.ChunkingConfig{
					ChunkSize: 512, ChunkOverlap: 80, EnableParentChild: true,
					ParserEngineRules: []types.ParserEngineRule{{}},
					Strategy:          "auto", TokenLimit: 200, Languages: []string{"en"},
				},
				ExtractConfig: &types.ExtractConfig{
					Enabled: true, Text: "extract", Tags: []string{"entity"},
					Nodes:     []*types.GraphNode{{Name: "entity"}},
					Relations: []*types.GraphRelation{{Node1: "entity", Node2: "entity", Type: "related"}},
				},
				QuestionGenerationConfig: &types.QuestionGenerationConfig{
					Enabled: true, QuestionCount: 7, CustomInstructions: "questions",
				},
			}
			if tc.name == "zero count stays zero" {
				kb.QuestionGenerationConfig.QuestionCount = 0
			}
			if tc.name == "first enable default" {
				kb.QuestionGenerationConfig = nil
			}
			repo := &stubInitializationKBRepo{}
			h := &InitializationHandler{
				kbService: &stubInitializationKBService{kb: kb}, kbRepository: repo,
				modelService: &stubTenantStampModelService{
					getModelByID: func(context.Context, string) (*types.Model, error) {
						return &types.Model{ID: "model"}, nil
					},
				},
			}
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Params = gin.Params{{Key: "kbId", Value: "kb"}}
			body := `{"llmModelId":"chat","embeddingModelId":"embedding"` + tc.fields + `}`
			c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			// A shared admin can update settings while leaving owner storage alone.
			caller := types.Caller{TenantID: 42, UserID: "admin", Role: types.TenantRoleAdmin}
			ctx := types.WithExecutionTenant(types.WithCaller(c.Request.Context(), caller), 7)
			c.Request = c.Request.WithContext(ctx)
			c.Set(middleware.KBAccessContextKey, &access.KBAccess{
				KnowledgeBase: kb, Caller: caller, EffectiveTenantID: 7, Permission: types.OrgRoleAdmin,
			})
			h.UpdateKBConfig(c)
			require.Empty(t, c.Errors)
			require.NotNil(t, repo.updated)
			tc.check(t, repo.updated)
		})
	}
}
