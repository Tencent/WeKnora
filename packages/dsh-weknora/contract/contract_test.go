// Package contract asserts that the WeKnora API surface the dsh-weknora plugin
// speaks still exists on this server: the request bodies it sends must decode
// into the real handler request structs with no unknown fields, and the response
// fields it reads must still be produced by the real response types.
//
// The fixture is shared with the plugin's own JavaScript tests
// (packages/dsh-weknora/test/contract.test.mjs), which assert that the plugin
// still emits exactly these calls. Together the two sides catch a rename on
// either side at CI time instead of inside a user's agent.
package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/types"
)

type contractCall struct {
	Tool          string            `json:"tool"`
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Query         map[string]string `json:"query"`
	Body          json.RawMessage   `json:"body"`
	GoRequestType *string           `json:"goRequestType"`
}

type contractFixture struct {
	Calls               []contractCall      `json:"calls"`
	ResponseFieldsRead  map[string][]string `json:"responseFieldsRead"`
	StreamResponseTypes []string            `json:"streamResponseTypes"`
}

func loadFixture(t *testing.T) contractFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "test", "fixtures", "api-contract.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture contractFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(fixture.Calls) == 0 {
		t.Fatal("fixture declares no calls")
	}
	return fixture
}

// newRequestTarget returns an empty instance of the handler request struct the
// named call decodes into.
func newRequestTarget(t *testing.T, name string) any {
	t.Helper()
	switch name {
	case "session.SearchKnowledgeRequest":
		return &session.SearchKnowledgeRequest{}
	case "session.CreateSessionRequest":
		return &session.CreateSessionRequest{}
	case "session.CreateKnowledgeQARequest":
		return &session.CreateKnowledgeQARequest{}
	case "types.Pagination":
		return &types.Pagination{}
	default:
		t.Fatalf("fixture names an unknown Go request type %q", name)
		return nil
	}
}

// TestPluginRequestBodiesDecodeIntoHandlerTypes: every body the plugin sends is
// accepted by the struct its handler binds, with no field the server would drop.
func TestPluginRequestBodiesDecodeIntoHandlerTypes(t *testing.T) {
	fixture := loadFixture(t)
	decoded := 0
	for _, call := range fixture.Calls {
		if call.GoRequestType == nil || len(call.Body) == 0 || string(call.Body) == "null" {
			continue
		}
		target := newRequestTarget(t, *call.GoRequestType)
		decoder := json.NewDecoder(bytes.NewReader(call.Body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			t.Errorf("%s %s (%s) body rejected by %s: %v",
				call.Method, call.Path, call.Tool, *call.GoRequestType, err)
			continue
		}
		decoded++
	}
	if decoded == 0 {
		t.Fatal("no request body was checked; the fixture lost its bodies")
	}
}

// TestRetrievalRequestCarriesRequiredFields: the search body binds the fields
// the handler validates, so the plugin cannot send a request the handler rejects
// with 400 at runtime.
func TestRetrievalRequestCarriesRequiredFields(t *testing.T) {
	fixture := loadFixture(t)
	for _, call := range fixture.Calls {
		if call.Path != "/api/v1/knowledge-search" {
			continue
		}
		var request session.SearchKnowledgeRequest
		if err := json.Unmarshal(call.Body, &request); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		if request.Query == "" {
			t.Error("the plugin must always send a non-empty query; the handler rejects an empty one")
		}
		if len(request.KnowledgeBaseIDs) == 0 && len(request.KnowledgeIDs) == 0 {
			t.Error("the plugin must scope retrieval; the handler rejects a request with no target")
		}
		return
	}
	t.Fatal("the fixture no longer covers /knowledge-search")
}

// TestChatRequestSelectsThePipeline: the agent route must carry both the agent id
// and the agent-enabled flag, since the RAG and ReAct pipelines are selected by
// the same struct.
func TestChatRequestSelectsThePipeline(t *testing.T) {
	fixture := loadFixture(t)
	var sawRAG, sawAgent bool
	for _, call := range fixture.Calls {
		if call.GoRequestType == nil || *call.GoRequestType != "session.CreateKnowledgeQARequest" {
			continue
		}
		var request session.CreateKnowledgeQARequest
		if err := json.Unmarshal(call.Body, &request); err != nil {
			t.Fatalf("decode chat body: %v", err)
		}
		if request.Query == "" {
			t.Errorf("%s must send a query", call.Path)
		}
		if request.Channel != "api" {
			t.Errorf("%s must declare channel \"api\", got %q", call.Path, request.Channel)
		}
		switch {
		case request.AgentID != "":
			sawAgent = true
			if !request.AgentEnabled {
				t.Errorf("%s names an agent but leaves agent_enabled false", call.Path)
			}
		default:
			sawRAG = true
		}
	}
	if !sawRAG || !sawAgent {
		t.Fatal("the fixture must cover both the RAG and the agent chat route")
	}
}

// TestResponseFieldsThePluginReadsStillExist: marshal the real response types and
// confirm every JSON key the plugin depends on is present, so a renamed or
// dropped json tag fails here.
func TestResponseFieldsThePluginReadsStillExist(t *testing.T) {
	fixture := loadFixture(t)
	samples := map[string]any{
		"types.SearchResult": types.SearchResult{
			ID:                "chunk-1",
			Content:           "默认的 vector_threshold 是 0.5",
			KnowledgeID:       "doc-1",
			ChunkIndex:        3,
			KnowledgeTitle:    "检索流程.md",
			KnowledgeFilename: "检索流程.md",
			ImageInfo:         `[{"url":"https://cdn.example.com/diagram.png","caption":"检索流程图"}]`,
			Score:             0.83,
		},
		"types.Knowledge": types.Knowledge{
			ID:                "doc-1",
			Title:             "检索流程.md",
			FileName:          "检索流程.md",
			Description:       "讲解 WeKnora 混合检索的召回与阈值。",
			KnowledgeBaseID:   "kb-product",
			KnowledgeBaseName: "Product docs",
		},
		"types.Chunk": types.Chunk{
			ID:          "chunk-1",
			Content:     "默认的 vector_threshold 是 0.5",
			ChunkIndex:  3,
			KnowledgeID: "doc-1",
			ImageInfo:   `[{"url":"https://cdn.example.com/diagram.png","caption":"检索流程图"}]`,
		},
		"types.StreamResponse": types.StreamResponse{
			ID:                  "event-1",
			ResponseType:        types.ResponseTypeAnswer,
			Content:             "答案",
			SessionID:           "session-1",
			KnowledgeReferences: types.References{{ID: "chunk-1"}},
			ToolCalls:           []types.LLMToolCall{{ID: "call-1", Type: "function"}},
		},
		"types.LLMToolCall": types.LLMToolCall{ID: "call-1", Type: "function"},
		"types.FunctionCall": types.FunctionCall{
			Name:      "knowledge_search",
			Arguments: "{}",
		},
	}

	for typeName, fields := range fixture.ResponseFieldsRead {
		sample, ok := samples[typeName]
		if !ok {
			t.Errorf("fixture names %s but this test has no sample for it", typeName)
			continue
		}
		raw, err := json.Marshal(sample)
		if err != nil {
			t.Errorf("marshal %s: %v", typeName, err)
			continue
		}
		var asMap map[string]json.RawMessage
		if err := json.Unmarshal(raw, &asMap); err != nil {
			t.Errorf("re-decode %s: %v", typeName, err)
			continue
		}
		for _, field := range fields {
			if _, ok := asMap[field]; !ok {
				t.Errorf("%s no longer serializes %q, which dsh-weknora reads", typeName, field)
			}
		}
	}
}

// TestStreamResponseTypesThePluginHandlesStillExist: the SSE event names the
// plugin switches on must still be the server's constants.
func TestStreamResponseTypesThePluginHandlesStillExist(t *testing.T) {
	fixture := loadFixture(t)
	known := map[string]types.ResponseType{
		"answer":     types.ResponseTypeAnswer,
		"references": types.ResponseTypeReferences,
		"tool_call":  types.ResponseTypeToolCall,
		"error":      types.ResponseTypeError,
		"complete":   types.ResponseTypeComplete,
	}
	for _, name := range fixture.StreamResponseTypes {
		constant, ok := known[name]
		if !ok {
			t.Errorf("fixture handles stream event %q, which this test does not map to a server constant", name)
			continue
		}
		if string(constant) != name {
			t.Errorf("stream event %q is now spelled %q on the server", name, string(constant))
		}
	}
}
