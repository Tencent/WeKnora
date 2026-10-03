package chat

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// The agent composes multimodal turns as MultiContent parts — the shape every
// remote protocol prefers — while older callers fill Images. Ollama read only
// the latter, so such a turn reached the model with neither its question nor
// its picture and came back answering about nothing.
func TestOllamaConvertMessagesReadsMultiContent(t *testing.T) {
	c := &OllamaChat{}
	original := []string{"data:image/png;base64,aGVsbG8="}
	messages := []Message{
		{
			Role:   "user",
			Images: original,
			MultiContent: []api.MessageContentPart{
				{Type: "text", Text: "what is this?"},
				{Type: "image_url", ImageURL: &api.ImageURL{URL: "data:image/png;base64,d29ybGQ="}},
			},
		},
	}

	got := c.convertMessages(messages)
	if len(got) != 1 {
		t.Fatalf("expected one message, got %d", len(got))
	}
	if got[0].Content != "what is this?" {
		t.Errorf("text parts should become the content, got %q", got[0].Content)
	}
	if len(got[0].Images) != 2 {
		t.Errorf("both the legacy image and the MultiContent one should survive, got %d", len(got[0].Images))
	}
	if len(original) != 1 || original[0] != "data:image/png;base64,aGVsbG8=" {
		t.Errorf("the caller's slice must not be written through, got %v", original)
	}
}

func TestOllamaConvertMessagesKeepsExplicitContent(t *testing.T) {
	c := &OllamaChat{}
	got := c.convertMessages([]Message{{
		Role:    "user",
		Content: "already set",
		MultiContent: []api.MessageContentPart{
			{Type: "text", Text: "ignored"},
		},
	}})
	if got[0].Content != "already set" {
		t.Errorf("an explicit Content wins over the parts, got %q", got[0].Content)
	}
}
