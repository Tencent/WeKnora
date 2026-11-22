package client

import (
	"encoding/json"
	"testing"
)

func TestAssistantSelectedPage(t *testing.T) {
	req := AgentQARequest{Query: "explain this setting", PageContext: &PageContext{Page: "models", ModelID: "model-1", Selection: &PageSelection{Text: "temperature", Field: "config", StartLine: 2, EndLine: 2}}}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Page map[string]json.RawMessage `json:"page_context"`
	}
	if err = json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	var sel struct {
		Text  string
		Start int `json:"start_line"`
	}
	if err = json.Unmarshal(wire.Page["selection"], &sel); err != nil {
		t.Fatal(err)
	}
	if string(wire.Page["model_id"]) != `"model-1"` || sel.Text != "temperature" || sel.Start != 2 {
		t.Fatalf("lost context: %s", b)
	}
}
