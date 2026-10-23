package handler

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parsingPatch(t *testing.T, kb *types.KnowledgeBase, body string) (knowledgeBaseUpdate, error) {
	t.Helper()
	var p PatchKnowledgeBaseRequest
	var sections map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(body), &p))
	require.NoError(t, json.Unmarshal([]byte(body), &sections))
	return applyKnowledgeBasePatch(kb, p, sections)
}

func TestParserRoutingPatchPreservesOtherRulesAndChunking(t *testing.T) {
	header := true
	kb := &types.KnowledgeBase{
		ChunkingConfig: types.ChunkingConfig{
			ChunkSize:    512,
			ChunkOverlap: 64,
			ParserEngineRules: []types.ParserEngineRule{
				{FileTypes: []string{"pdf", "docx"}, Engine: "builtin"},
				{FileTypes: []string{"xlsx", "csv"}, Engine: "simple", XLSXFirstRowAsHeader: &header},
			},
		},
	}
	result, err := parsingPatch(t, kb, `{"parser_file_types":[".PDF","pdf","xlsx"],"parser_engine":"new-engine"}`)
	require.NoError(t, err)
	cfg := result.Config.ChunkingConfig
	assert.Equal(t, 512, cfg.ChunkSize)
	assert.Equal(t, 64, cfg.ChunkOverlap)
	assert.Equal(t, "new-engine", cfg.ResolveParserEngine("pdf"))
	assert.Equal(t, "builtin", cfg.ResolveParserEngine("docx"))
	assert.Equal(t, "simple", cfg.ResolveParserEngine("csv"))
	require.NotNil(t, cfg.ResolveParserEngineRule("xlsx").XLSXFirstRowAsHeader)
	assert.True(t, *cfg.ResolveParserEngineRule("xlsx").XLSXFirstRowAsHeader)
	assert.Equal(
		t,
		[]string{"pdf", "docx"},
		kb.ChunkingConfig.ParserEngineRules[0].FileTypes,
		"must not mutate the loaded entity",
	)
	result, err = parsingPatch(t, kb, `{"parser_file_types":["pdf"],"parser_engine":""}`)
	require.NoError(t, err)
	assert.Nil(t, result.Config.ChunkingConfig.ResolveParserEngineRule("pdf"))
	assert.Equal(t, "builtin", result.Config.ChunkingConfig.ResolveParserEngine("docx"))
}

func TestParserRoutingPatchRejectsAmbiguousOrInvalidChanges(t *testing.T) {
	for _, body := range []string{
		`{"parser_engine":"builtin"}`, `{"parser_file_types":["pdf"]}`,
		`{"parser_file_types":[],"parser_engine":"builtin"}`,
		`{"parser_file_types":["*.pdf"],"parser_engine":"builtin"}`,
		`{"parser_file_types":["file.pdf"],"parser_engine":"builtin"}`,
		`{"parser_file_types":["pdf"],"parser_engine":"https://example.org"}`,
		`{
  "parser_file_types": [
    "pdf"
  ],
  "parser_engine": "builtin",
  "parser_xlsx_first_row_as_header": true
}`,
		`{
  "parser_file_types": [
    "pdf"
  ],
  "parser_engine": "builtin",
  "chunking_config": {
    "parser_engine_rules": []
  }
}`,
	} {
		_, err := parsingPatch(t, &types.KnowledgeBase{}, body)
		require.Error(t, err, body)
	}
}

func TestImageControlsPreserveOtherParamsAndRespectNamespace(t *testing.T) {
	kb := &types.KnowledgeBase{ImageProcessingConfig: types.ImageProcessingConfig{
		ImagePipelineID: "default", ImagePipelineParams: map[string]any{
			"default.ocr_enabled":              true,
			"caption_enabled":                  true,
			"default.caption.thinking_enabled": true,
		},
	}}
	result, err := parsingPatch(t, kb, `{"image_ocr_enabled":false}`)
	require.NoError(t, err)
	image := result.Config.ImageProcessingConfig
	assert.Equal(t, false, image.ImagePipelineParams["default.ocr_enabled"])
	assert.Equal(t, true, image.ImagePipelineParams["default.caption.thinking_enabled"])
	assert.Equal(t, true, image.ImagePipelineParams["caption_enabled"])
	// One home per setting: the switch lands in the pipeline's slot and is not
	// also written under its bare name, which nothing reads.
	_, bare := image.ImagePipelineParams["ocr_enabled"]
	assert.False(t, bare, "the switch must be stored under its slot only")
	assert.Equal(t, true, kb.ImageProcessingConfig.ImagePipelineParams["default.ocr_enabled"])
	require.NoError(t, service.ValidateImagePipelineParams(types.ImagePipelineDefault, image.ImagePipelineParams))
	result, err = parsingPatch(t, kb, `{"image_ocr_enabled":false,"image_caption_enabled":false}`)
	require.NoError(t, err)
	require.Error(
		t,
		service.ValidateImagePipelineParams(
			types.ImagePipelineDefault,
			result.Config.ImageProcessingConfig.ImagePipelineParams,
		),
	)
	kb.ImageProcessingConfig.ImagePipelineID = "smartocr"
	_, err = parsingPatch(t, kb, `{"image_ocr_enabled":false}`)
	require.Error(t, err, "manual toggles must not silently do nothing on smartocr")
	result, err = parsingPatch(
		t,
		kb,
		`{"image_processing_config":{"image_pipeline":"default"},"image_ocr_enabled":false}`,
	)
	require.NoError(t, err)
	assert.Equal(t, "default", result.Config.ImageProcessingConfig.ImagePipelineID)
}

func TestImageControlsPatchProjectsEffectiveSwitches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		caption bool
		ocr     bool
	}{
		{name: "caption only", caption: true, ocr: false},
		{name: "OCR only", caption: false, ocr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kb := &types.KnowledgeBase{ID: "kb1", ImageProcessingConfig: types.ImageProcessingConfig{
				ImagePipelineID: "default", ImagePipelineParams: map[string]any{
					"enable_caption":         !tc.caption,
					"enable_ocr":             !tc.ocr,
					"default.enable_caption": !tc.caption,
					"default.enable_ocr":     !tc.ocr,
					"private_token":          "secret",
				},
			}}
			patch, err := json.Marshal(map[string]bool{
				"image_caption_enabled": tc.caption,
				"image_ocr_enabled":     tc.ocr,
			})
			require.NoError(t, err)
			result, err := parsingPatch(t, kb, string(patch))
			require.NoError(t, err)
			kb.ImageProcessingConfig = *result.Config.ImageProcessingConfig
			body, err := json.Marshal(map[string]any{"data": kb})
			require.NoError(t, err)
			for _, operationID := range []string{"knowledge_base.get", "knowledge_base.update"} {
				t.Run(operationID, func(t *testing.T) {
					op, ok := operations.Builtin().Get(operationID)
					require.True(t, ok)
					projected, err := op.Project(body)
					require.NoError(t, err)
					fields, ok := projected.Data.(map[string]any)
					require.True(t, ok)
					assert.Equal(t, tc.caption, fields["default.caption_enabled"])
					assert.Equal(t, tc.ocr, fields["default.ocr_enabled"])
					for _, key := range []string{
						"enable_caption", "enable_ocr", "default.enable_caption", "default.enable_ocr", "private_token",
					} {
						assert.NotContains(t, fields, key)
					}
				})
			}
		})
	}
}

func TestParsingPatchKeepsVLMAndQuestionOptions(t *testing.T) {
	kb := &types.KnowledgeBase{
		VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vision"},
		QuestionGenerationConfig: &types.QuestionGenerationConfig{
			Enabled:            true,
			QuestionCount:      3,
			CustomInstructions: "keep me",
		},
		ChunkingConfig: types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: 64},
	}
	result, err := parsingPatch(t, kb, `{
  "chunking_config": {
    "enable_parent_child": true,
    "parent_chunk_size": 4096,
    "child_chunk_size": 384
  },
  "question_generation_config": {
    "question_count": 5
  }
}`)
	require.NoError(t, err)
	assert.True(t, result.Config.ChunkingConfig.EnableParentChild)
	assert.Equal(t, 512, result.Config.ChunkingConfig.ChunkSize)
	assert.True(t, result.Config.QuestionGenerationConfig.Enabled)
	assert.Equal(t, "keep me", result.Config.QuestionGenerationConfig.CustomInstructions)
	assert.Nil(t, result.VLM)
}
