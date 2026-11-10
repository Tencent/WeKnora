package notion

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseNotionConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg, err := parseNotionConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"api_key": "ntn_test123",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.APIKey != "ntn_test123" {
			t.Errorf("APIKey = %q, want %q", cfg.APIKey, "ntn_test123")
		}
	})

	t.Run("nil config", func(t *testing.T) {
		_, err := parseNotionConfig(nil)
		if err == nil {
			t.Fatal("expected error for nil config")
		}
	})

	t.Run("missing api_key", func(t *testing.T) {
		_, err := parseNotionConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{},
		})
		if err == nil {
			t.Fatal("expected error for missing api_key")
		}
	})
}

func TestNotionBlockUnmarshalJSON(t *testing.T) {
	t.Run("paragraph block", func(t *testing.T) {
		data := []byte(`{
			"id": "block-1",
			"type": "paragraph",
			"has_children": false,
			"paragraph": {"rich_text": [{"type": "text", "plain_text": "hello"}]}
		}`)
		var block notionBlock
		if err := json.Unmarshal(data, &block); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if block.ID != "block-1" {
			t.Errorf("ID = %q, want %q", block.ID, "block-1")
		}
		if block.Type != "paragraph" {
			t.Errorf("Type = %q, want %q", block.Type, "paragraph")
		}
		if block.HasChildren != false {
			t.Error("HasChildren should be false")
		}
		if block.RawContent == nil {
			t.Fatal("RawContent should not be nil")
		}
		// Verify RawContent contains the paragraph object
		var content struct {
			RichText []notionRichText `json:"rich_text"`
		}
		if err := json.Unmarshal(block.RawContent, &content); err != nil {
			t.Fatalf("unmarshal RawContent: %v", err)
		}
		if len(content.RichText) != 1 || content.RichText[0].PlainText != "hello" {
			t.Errorf("unexpected rich_text content: %+v", content.RichText)
		}
	})

	t.Run("block with children flag", func(t *testing.T) {
		data := []byte(`{
			"id": "block-2",
			"type": "toggle",
			"has_children": true,
			"toggle": {"rich_text": [{"type": "text", "plain_text": "Details"}]}
		}`)
		var block notionBlock
		if err := json.Unmarshal(data, &block); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if !block.HasChildren {
			t.Error("HasChildren should be true")
		}
		// Children is populated by client, not by unmarshal
		if block.Children != nil {
			t.Error("Children should be nil after unmarshal")
		}
	})

	t.Run("unknown block type", func(t *testing.T) {
		data := []byte(`{"id": "block-3", "type": "future_type", "has_children": false}`)
		var block notionBlock
		if err := json.Unmarshal(data, &block); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if block.Type != "future_type" {
			t.Errorf("Type = %q", block.Type)
		}
		// RawContent may be nil for unknown types without matching field
	})
}

func TestNotionFileGetURL(t *testing.T) {
	t.Run("hosted file", func(t *testing.T) {
		f := &notionFile{
			Type: "file",
			File: &struct {
				URL        string    `json:"url"`
				ExpiryTime time.Time `json:"expiry_time"`
			}{URL: "https://s3.example.com/file.pdf"},
		}
		if got := f.GetURL(); got != "https://s3.example.com/file.pdf" {
			t.Errorf("GetURL() = %q", got)
		}
	})

	t.Run("external file", func(t *testing.T) {
		f := &notionFile{
			Type: "external",
			External: &struct {
				URL string `json:"url"`
			}{URL: "https://example.com/image.png"},
		}
		if got := f.GetURL(); got != "https://example.com/image.png" {
			t.Errorf("GetURL() = %q", got)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		f := &notionFile{Type: "file"}
		if got := f.GetURL(); got != "" {
			t.Errorf("GetURL() = %q, want empty", got)
		}
	})
}
