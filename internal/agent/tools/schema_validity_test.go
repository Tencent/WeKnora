package tools

import (
	"encoding/json"
	"testing"
)

// Static tool schemas are hand-written JSON spliced with Go string
// constants; a stray quote would only surface as a provider-side 400.
func TestBuiltInToolSchemasAreValidJSON(t *testing.T) {
	static := map[string]json.RawMessage{
		ToolSearchKnowledge:     searchKnowledgeTool.schema,
		ToolReadDocument:        readDocumentTool.schema,
		ToolListDocuments:       listDocumentsTool.schema,
		ToolQueryKnowledgeGraph: queryKnowledgeGraphTool.schema,
		ToolThinking:            sequentialThinkingTool.schema,
	}
	wikiSearch := NewWikiSearchTool(nil, nil, NewWikiScopesFromKBIDs([]string{"kb"}), nil)
	wikiRead := NewWikiReadPageTool(nil, nil, NewWikiScopesFromKBIDs([]string{"kb"}), nil)
	static[wikiSearch.Name()] = wikiSearch.Parameters()
	static[wikiRead.Name()] = wikiRead.Parameters()

	for name, schema := range static {
		var parsed map[string]interface{}
		if err := json.Unmarshal(schema, &parsed); err != nil {
			t.Errorf("%s schema is not valid JSON: %v", name, err)
			continue
		}
		if parsed["type"] != "object" {
			t.Errorf("%s schema must describe an object, got %v", name, parsed["type"])
		}
		props, _ := parsed["properties"].(map[string]interface{})
		if len(props) == 0 {
			t.Errorf("%s schema declares no properties", name)
		}
	}
	if desc, _ := func() (string, error) {
		var parsed struct {
			Properties struct {
				Thought struct {
					Description string `json:"description"`
				} `json:"thought"`
			} `json:"properties"`
		}
		err := json.Unmarshal(sequentialThinkingTool.schema, &parsed)
		return parsed.Properties.Thought.Description, err
	}(); desc == "" || !json.Valid(sequentialThinkingTool.schema) {
		t.Fatalf("thinking schema lost its thought description: %q", desc)
	}
}
