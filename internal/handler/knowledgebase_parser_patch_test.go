package handler

import (
	"encoding/json"
	"testing"

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
			"default.enable_ocr": true,
			"enable_caption":     true,
			"caption.thinking":   true,
		},
	}}
	result, err := parsingPatch(t, kb, `{"image_ocr_enabled":false}`)
	require.NoError(t, err)
	image := result.Config.ImageProcessingConfig
	assert.Equal(t, false, image.ImagePipelineParams["enable_ocr"])
	assert.Equal(t, false, image.ImagePipelineParams["default.enable_ocr"])
	assert.Equal(t, true, image.ImagePipelineParams["caption.thinking"])
	assert.Equal(t, true, image.ImagePipelineParams["enable_caption"])
	assert.Equal(t, true, kb.ImageProcessingConfig.ImagePipelineParams["default.enable_ocr"])
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
