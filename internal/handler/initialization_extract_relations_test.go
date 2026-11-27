package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type proseReplyChat struct{}

func (proseReplyChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: "抱歉，这段文本没有可抽取的实体和关系。", FinishReason: "stop"}, nil
}

func (proseReplyChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented")
}
func (proseReplyChat) GetModelName() string { return "prose" }
func (proseReplyChat) GetModelID() string   { return "prose" }

// The graph-settings "try extract" button must report a prose refusal as a
// failure. Returning an empty graph would make the UI clear the user's
// few-shot nodes/relations and show "success" (#3606 review).
func TestExtractRelationsFromTextReportsModelRefusal(t *testing.T) {
	h := &InitializationHandler{config: &config.Config{
		ExtractManager: &config.ExtractManagerConfig{
			ExtractGraph: &types.PromptTemplateStructured{Description: "extract"},
		},
	}}
	res, err := h.extractRelationsFromText(context.Background(), "目录", nil, proseReplyChat{})
	if err == nil {
		t.Fatalf("expected an error for a prose refusal, got %+v", res)
	}
}

func TestNodeExtractConfigGraphBackend(t *testing.T) {
	for _, backend := range []string{"neo4j", "hologres", "none"} {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("GRAPH_BACKEND", backend)
			t.Setenv("NEO4J_ENABLE", "false")
			req := &InitializationRequest{}
			req.NodeExtract.Enabled = true
			req.NodeExtract.Text = "Acme is located in Shanghai."
			req.NodeExtract.Tags = []string{"company", "city"}
			req.NodeExtract.Nodes = []struct {
				Name       string   `json:"name"`
				Attributes []string `json:"attributes"`
			}{{Name: "Acme"}, {Name: "Shanghai"}}
			req.NodeExtract.Relations = []struct {
				Node1 string `json:"node1"`
				Node2 string `json:"node2"`
				Type  string `json:"type"`
			}{{Node1: "Acme", Node2: "Shanghai", Type: "LOCATED_IN"}}
			err := validateNodeExtractConfig(context.Background(), req)
			if (err == nil) != (backend != "none") {
				t.Fatalf("backend %s validation: %v", backend, err)
			}
			// Backend selection must not bypass extraction data validation.
			req.NodeExtract.Relations = nil
			if err := validateNodeExtractConfig(context.Background(), req); err == nil {
				t.Fatal("expected incomplete extraction data to be rejected")
			}
			req.NodeExtract.Enabled = false
			if err := validateNodeExtractConfig(context.Background(), req); err != nil {
				t.Fatalf("disabled extraction: %v", err)
			}
		})
	}
}
