package wecom

import (
	"testing"
)

func TestStripAtMentionBasic(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"no mention", "hello world", "hello world"},
		{"empty string", "", ""},
		{"mention only no content", "@Bot", "@Bot"},
		{"single-word bot double-space", "@Bot  /stop", "/stop"},
		{"single-word bot single-space", "@Bot /stop", "/stop"},
		{"single-word bot double-space chinese", "@Bot  你好世界", "你好世界"},
		{"multi-word bot double-space", "@WeKnora Bot  /stop", "/stop"},
		{"multi-word bot double-space chinese", "@WeKnora Bot  什么是上下文工程", "什么是上下文工程"},
		{"multi-word bot single-space command", "@WeKnora Bot /stop", "/stop"},
		{"multi-word bot single-space chinese", "@WeKnora Bot 什么是老登", "什么是老登"},
		{"leading whitespace", "  @Bot  hello  ", "hello"},
		{"double-space in user content", "@Bot  hello  world", "hello  world"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripAtMentionBasic(tt.content)
			if got != tt.want {
				t.Errorf("stripAtMentionBasic(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestLongConnClient_StripAtMention(t *testing.T) {
	t.Run("learns bot name from double-space then handles single-space", func(t *testing.T) {
		c := &LongConnClient{}

		// First message: double-space → learn "WeKnora Bot"
		got := c.stripAtMention("@WeKnora Bot  什么是上下文工程")
		if got != "什么是上下文工程" {
			t.Errorf("first message: got %q, want %q", got, "什么是上下文工程")
		}

		// Verify bot name was cached
		if name, _ := c.botDisplayName.Load().(string); name != "WeKnora Bot" {
			t.Errorf("cached bot name = %q, want %q", name, "WeKnora Bot")
		}

		// Second message: single-space → should use cached name
		got = c.stripAtMention("@WeKnora Bot /stop")
		if got != "/stop" {
			t.Errorf("second message: got %q, want %q", got, "/stop")
		}

		// Single-space with chinese content
		got = c.stripAtMention("@WeKnora Bot 什么是B+树")
		if got != "什么是B+树" {
			t.Errorf("chinese single-space: got %q, want %q", got, "什么是B+树")
		}
	})

	t.Run("preconfigured bot name", func(t *testing.T) {
		c := &LongConnClient{}
		c.botDisplayName.Store("WeKnora Bot")

		// Single-space should work immediately without learning
		got := c.stripAtMention("@WeKnora Bot /stop")
		if got != "/stop" {
			t.Errorf("got %q, want %q", got, "/stop")
		}
	})

	t.Run("cached name must not partial-match a longer bot name", func(t *testing.T) {
		c := &LongConnClient{}
		c.botDisplayName.Store("Bot")

		// "@BotX /stop" should NOT match cached "Bot" — "BotX" is a different name.
		// Falls through to Strategy 3 (strip first @word).
		got := c.stripAtMention("@BotX /stop")
		if got != "/stop" {
			t.Errorf("got %q, want %q", got, "/stop")
		}
	})

	t.Run("NewLongConnClient with bot name", func(t *testing.T) {
		c := NewLongConnClient("id", "secret", "My Bot", nil)

		got := c.stripAtMention("@My Bot /help")
		if got != "/help" {
			t.Errorf("got %q, want %q", got, "/help")
		}
	})

	t.Run("does not overwrite cached name from user double-spaces", func(t *testing.T) {
		c := &LongConnClient{}
		c.botDisplayName.Store("Bot")

		// User content has double space — should NOT overwrite "Bot" with "Bot hello"
		got := c.stripAtMention("@Bot  hello  world")
		if got != "hello  world" {
			t.Errorf("got %q, want %q", got, "hello  world")
		}

		// Cached name should still be "Bot"
		if name, _ := c.botDisplayName.Load().(string); name != "Bot" {
			t.Errorf("cached bot name = %q, want %q", name, "Bot")
		}
	})

	t.Run("no mention passthrough", func(t *testing.T) {
		c := &LongConnClient{}
		got := c.stripAtMention("hello world")
		if got != "hello world" {
			t.Errorf("got %q, want %q", got, "hello world")
		}
	})

	t.Run("empty string", func(t *testing.T) {
		c := &LongConnClient{}
		got := c.stripAtMention("")
		if got != "" {
			t.Errorf("got %q, want %q", got, "")
		}
	})

	t.Run("cold start single-space command", func(t *testing.T) {
		c := &LongConnClient{}
		// No cached name, single space → heuristic detects " /" boundary
		got := c.stripAtMention("@WeKnora Bot /stop")
		if got != "/stop" {
			t.Errorf("got %q, want %q", got, "/stop")
		}
	})

	t.Run("cold start single-space chinese", func(t *testing.T) {
		c := &LongConnClient{}
		// No cached name, single space → heuristic detects non-ASCII boundary
		got := c.stripAtMention("@WeKnora Bot 什么是老登")
		if got != "什么是老登" {
			t.Errorf("got %q, want %q", got, "什么是老登")
		}
	})

	t.Run("cold start all-ascii content falls back to first word", func(t *testing.T) {
		c := &LongConnClient{}
		// No cached name, all ASCII → heuristic can't find boundary, strips first @word
		got := c.stripAtMention("@WeKnora Bot hello")
		if got != "Bot hello" {
			t.Errorf("got %q, want %q", got, "Bot hello")
		}
	})
}
