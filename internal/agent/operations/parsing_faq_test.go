package operations

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParserEngineDiscoveryUsesActualWireCaseAndHidesConnectionDetails(t *testing.T) {
	op, ok := Builtin().Get("knowledge_base.list_parser_engines")
	require.True(t, ok)
	body, err := json.Marshal(
		map[string]any{
			"data": []types.ParserEngineInfo{
				{Name: "builtin", Available: true, FileTypes: []string{"pdf"}},
				{Name: "mineru", Available: false, UnavailableReason: "not configured"},
			},
			"docreader_addr": "private:50051",
			"connected":      true,
		},
	)
	require.NoError(t, err)
	out := projectJSON(t, op, string(body))
	assert.Contains(t, out, `"engine":"builtin"`)
	assert.Contains(t, out, `"available":false`)
	assert.Contains(t, out, `"file_types":["pdf"]`)
	assert.NotContains(t, out, "private")
	assert.NotContains(t, out, "docreader_addr")
	for _, route := range []string{"/api/v1/system/parser-engines/check", "/api/v1/system/admin/settings/key"} {
		_, err := NewCatalog(readOp("system.get", route))
		require.Error(t, err, route)
	}
}

func TestKnowledgeBaseParsingParamsAndProjection(t *testing.T) {
	op, _ := Builtin().Get("knowledge_base.update")
	params := map[string]json.RawMessage{
		"knowledge_base_id": raw(
			"kb1",
		), "enable_parent_child": raw(true), "parent_chunk_size": raw(4096), "child_chunk_size": raw(384),
		"parser_file_types": raw(
			[]string{"pdf"},
		), "parser_engine": raw("builtin"), "image_pipeline": raw("default"), "image_ocr_enabled": raw(false),
		"question_generation_enabled": raw(true), "question_count": raw(5),
	}
	call, err := op.Build(params)
	require.NoError(t, err)
	assert.JSONEq(t, `{
  "chunking_config": {
    "enable_parent_child": true,
    "parent_chunk_size": 4096,
    "child_chunk_size": 384
  },
  "image_processing_config": {
    "image_pipeline": "default"
  },
  "parser_file_types": [
    "pdf"
  ],
  "parser_engine": "builtin",
  "image_ocr_enabled": false,
  "question_generation_config": {
    "enabled": true,
    "question_count": 5
  }
}`, string(call.Body))
	params["parser_engine"] = raw("")
	_, err = op.Build(params)
	require.NoError(t, err, "empty engine restores defaults")
	params["question_count"] = raw(11)
	_, err = op.Build(params)
	require.Error(t, err)
	read, _ := Builtin().Get("knowledge_base.get")
	out := projectJSON(t, read, `{
  "data": {
    "id": "kb1",
    "chunking_config": {
      "parser_engine_rules": [
        {
          "file_types": [
            "pdf"
          ],
          "engine": "builtin"
        }
      ]
    },
    "image_processing_config": {
      "image_pipeline": "default",
      "image_pipeline_params": {
        "default.enable_ocr": false,
        "enable_ocr": true,
        "private_token": "secret",
        "default": {
          "enable_ocr": true
        }
      }
    }
  }
}`)
	assert.Contains(t, out, `"engine":"builtin"`)
	assert.Contains(t, out, `"default.enable_ocr":false`, "literal map keys must not resolve as nested paths")
	assert.Contains(t, out, `"enable_ocr":true`)
	assert.NotContains(t, out, "secret")
	assert.NotContains(t, out, `"default":`)
	_, exposed := op.Param("parser_engine_rules")
	assert.False(t, exposed, "only the targeted routing patch is writable")
	_, exposed = op.Param("image_pipeline_params")
	assert.False(t, exposed, "free-form parameters are not writable")
}

func TestFAQBatchUpdateIsBoundedAndTargetsEachEntry(t *testing.T) {
	op, ok := Builtin().Get("faq.batch_update")
	require.True(t, ok)
	assert.Equal(t, RiskWrite, op.Risk)
	assert.Equal(t, http.MethodPatch, op.Method)
	assert.Equal(t, "faq.get_entry", op.Target.Items.Operation)
	assert.Equal(t, "entry_ids", op.Target.Items.Param)
	assert.ElementsMatch(
		t,
		[]string{"knowledge_base_id", "entry_ids", "is_enabled", "is_recommended", "tag_seq_id"},
		paramNames(op),
	)
	call, err := op.Build(
		map[string]json.RawMessage{
			"knowledge_base_id": raw("kb1"),
			"entry_ids":         raw([]int64{1, 2}),
			"is_enabled":        raw(false),
			"is_recommended":    raw(true),
			"tag_seq_id":        raw(0),
		},
	)
	require.NoError(t, err)
	assert.JSONEq(t, `{"entry_ids":[1,2],"is_enabled":false,"is_recommended":true,"tag_id":0}`, string(call.Body))
	assert.Equal(t, "/api/v1/knowledge-bases/kb1/faq/entries", call.URI())
	for _, ids := range [][]int64{{}, make([]int64, 21)} {
		_, err := op.Build(
			map[string]json.RawMessage{
				"knowledge_base_id": raw("kb1"),
				"entry_ids":         raw(ids),
				"is_enabled":        raw(false),
			},
		)
		require.Error(t, err)
	}
}
