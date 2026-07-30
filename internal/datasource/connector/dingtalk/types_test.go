package dingtalk

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseDingTalkConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg, err := parseDingTalkConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"app_key":     "key1",
				"app_secret":  "secret1",
				"operator_id": "union-abc",
				"base_url":    "https://api.dingtalk.com",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AppKey != "key1" || cfg.AppSecret != "secret1" || cfg.OperatorID != "union-abc" {
			t.Errorf("unexpected cfg: %+v", cfg)
		}
		if cfg.GetBaseURL() != "https://api.dingtalk.com" {
			t.Errorf("GetBaseURL = %q", cfg.GetBaseURL())
		}
	})

	t.Run("default base url", func(t *testing.T) {
		cfg, err := parseDingTalkConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"app_key": "k", "app_secret": "s", "operator_id": "u",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.GetBaseURL() != DefaultBaseURL {
			t.Errorf("GetBaseURL = %q, want %q", cfg.GetBaseURL(), DefaultBaseURL)
		}
	})

	t.Run("missing app_key", func(t *testing.T) {
		_, err := parseDingTalkConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{"app_secret": "s", "operator_id": "u"},
		})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("missing operator_id", func(t *testing.T) {
		_, err := parseDingTalkConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{"app_key": "k", "app_secret": "s"},
		})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("nil config", func(t *testing.T) {
		_, err := parseDingTalkConfig(nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestEncodeParseResourceID(t *testing.T) {
	if got := encodeResourceID("ws1", ""); got != "ws1" {
		t.Errorf("encode empty node = %q", got)
	}
	if got := encodeResourceID("ws1", "n1"); got != "ws1:n1" {
		t.Errorf("encode = %q", got)
	}
	ws, n := parseResourceID("ws1:n1")
	if ws != "ws1" || n != "n1" {
		t.Errorf("parse = %q, %q", ws, n)
	}
	ws, n = parseResourceID("ws1")
	if ws != "ws1" || n != "" {
		t.Errorf("parse bare = %q, %q", ws, n)
	}
}

func TestBlocksToMarkdown(t *testing.T) {
	md := blocksToMarkdown([]docBlock{
		{Index: 1, BlockType: "paragraph", Paragraph: &struct {
			Text string `json:"text"`
		}{Text: "hello"}},
		{Index: 0, BlockType: "heading", Heading: &struct {
			Level string `json:"level"`
			Text  string `json:"text"`
		}{Level: "heading-1", Text: "Title"}},
		{Index: 2, BlockType: "unorderedList", UnorderedList: &struct {
			Text string `json:"text"`
		}{Text: "item"}},
	})
	want := "# Title\n\nhello\n\n- item\n"
	if md != want {
		t.Errorf("blocksToMarkdown =\n%q\nwant\n%q", md, want)
	}
}

func TestSanitizeFileName(t *testing.T) {
	if got := sanitizeFileName("a/b:c"); got != "a_b_c" {
		t.Errorf("sanitize = %q", got)
	}
	if got := sanitizeFileName(""); got != "untitled" {
		t.Errorf("empty = %q", got)
	}
}

func TestIsSyncableFile(t *testing.T) {
	if !isSyncableFile(wikiNode{Type: "FILE", Category: "ALIDOC"}) {
		t.Error("ALIDOC should be syncable")
	}
	if isSyncableFile(wikiNode{Type: "FOLDER"}) {
		t.Error("FOLDER should not be syncable")
	}
	if isSyncableFile(wikiNode{Type: "FILE", Category: "FOLDER"}) {
		t.Error("FILE+FOLDER category should not be syncable")
	}
}
