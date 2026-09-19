package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// recordingTool decodes its arguments into a typed struct, as the real
// retrieval tools do, and records what reached Execute.
type recordingTool struct {
	BaseTool
	got   json.RawMessage
	calls int
}

func (r *recordingTool) Execute(_ context.Context, args json.RawMessage) (*types.ToolResult, error) {
	r.calls++
	r.got = args
	var input SearchKnowledgeInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, err
	}
	return &types.ToolResult{Success: true}, nil
}

func newRecordingSearchTool() *recordingTool {
	return &recordingTool{BaseTool: BaseTool{name: ToolSearchKnowledge, schema: searchKnowledgeTool.schema}}
}

// Models sometimes wrap string arguments as {"text": "..."}; the pre-merge
// knowledge_search failed with "cannot unmarshal object into Go struct field
// KnowledgeSearchInput.queries of type string". The registry now unwraps
// such values before the tool decodes them.
func TestRegistryUnwrapsSingleStringObjects(t *testing.T) {
	tool := newRecordingSearchTool()
	r := NewToolRegistry()
	r.RegisterTool(tool)

	res, err := r.ExecuteTool(context.Background(), ToolSearchKnowledge, json.RawMessage(
		`{"query":{"text":"LARP 收入基础设施 创始人"},"knowledge_base_ids":[{"id":"kb-1"},"kb-2"]}`,
	))
	if err != nil || !res.Success {
		t.Fatalf("wrapped arguments must be repaired: res=%+v err=%v", res, err)
	}
	var input SearchKnowledgeInput
	if err := json.Unmarshal(tool.got, &input); err != nil {
		t.Fatalf("args reaching the tool must decode: %v (%s)", err, tool.got)
	}
	if input.Query != "LARP 收入基础设施 创始人" {
		t.Fatalf("query = %q", input.Query)
	}
	if strings.Join(input.KnowledgeBaseIDs, ",") != "kb-1,kb-2" {
		t.Fatalf("knowledge_base_ids = %v", input.KnowledgeBaseIDs)
	}
}

// Element types that cannot be repaired are rejected by validation with a
// message naming the element, instead of a Go decode error from the tool.
func TestRegistryRejectsWrongArrayElementTypesBeforeExecute(t *testing.T) {
	tool := newRecordingSearchTool()
	r := NewToolRegistry()
	r.RegisterTool(tool)

	res, err := r.ExecuteTool(context.Background(), ToolSearchKnowledge, json.RawMessage(
		`{"query":"x","knowledge_base_ids":[{"id":"kb-1","name":"Docs"}]}`,
	))
	if err != nil {
		t.Fatalf("validation failures are tool results, not Go errors: %v", err)
	}
	if res.Success || !strings.Contains(res.Error, "knowledge_base_ids[0]") ||
		!strings.Contains(res.Error, "should be type 'string'") {
		t.Fatalf("expected an element-level validation error, got %+v", res)
	}
	if strings.Contains(res.Error, "unmarshal") {
		t.Fatalf("the Go decode error must not reach the model: %q", res.Error)
	}
	if tool.calls != 0 {
		t.Fatal("the tool must not run with invalid arguments")
	}
}

func TestCastParamsLeavesAmbiguousObjectsAlone(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	for _, raw := range []string{
		`{"query":{"text":"a","lang":"en"}}`, // two fields: intent unclear
		`{"query":{"n":1}}`,                  // single non-string field
	} {
		if got := CastParams(json.RawMessage(raw), schema); string(got) != raw {
			t.Errorf("CastParams(%s) = %s, want unchanged", raw, got)
		}
	}
}
