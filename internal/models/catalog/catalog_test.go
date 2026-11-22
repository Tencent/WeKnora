package catalog

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models"
	"github.com/Tencent/WeKnora/internal/types"
)

func catalogFixture() []models.ModelSpec {
	return []models.ModelSpec{
		{ID: "gpt-5", Type: types.ModelTypeKnowledgeQA, Aliases: []string{"gpt5"}, Match: "gpt-5*"},
		{ID: "gpt-5-embed", Type: types.ModelTypeEmbedding, Match: "gpt-5*"},
		{ID: "qwen3-family", Type: types.ModelTypeKnowledgeQA, Match: "qwen3*"},
		{ID: "qwen3-235b", Type: types.ModelTypeKnowledgeQA, Match: "qwen3-235b*"},
		{ID: "qwen3.8-text-embedding", Type: types.ModelTypeEmbedding, Match: "qwen3*"},
		{ID: "deepseek-r1", Type: types.ModelTypeKnowledgeQA, Match: "*deepseek-r1*"},
		{ID: "vision-1", Type: types.ModelTypeKnowledgeQA, Input: []string{"text", "image"}},
		{ID: "text-only", Type: types.ModelTypeKnowledgeQA, Input: []string{"text"}},
		{ID: "typeless", Input: []string{"text"}},
		{ID: "old-model", Type: types.ModelTypeKnowledgeQA, Deprecated: true},
		{ID: "", Type: types.ModelTypeKnowledgeQA, Name: "nameless"},
	}
}

func TestNewAndModelsReturnIndependentCopies(t *testing.T) {
	entries := catalogFixture()
	c := New(entries)

	got := c.Models()
	if len(got) != len(entries) {
		t.Fatalf("Models returned %d entries, want %d", len(got), len(entries))
	}
	// Mutating a returned copy must not affect the catalog or the input.
	got[0].Aliases[0] = "changed"
	entries[0].Aliases[0] = "input-changed"

	again := c.Models()
	if again[0].Aliases[0] != "gpt5" {
		t.Fatalf("Models shares storage: alias is %q", again[0].Aliases[0])
	}
}

func TestModelsByType(t *testing.T) {
	c := New(catalogFixture())

	chat := c.ModelsByType(types.ModelTypeKnowledgeQA)
	ids := map[string]bool{}
	for _, m := range chat {
		ids[m.ID] = true
	}
	// Deprecated and empty-ID entries are hidden; typeless entries fold to chat.
	for _, want := range []string{"gpt-5", "qwen3-family", "qwen3-235b", "deepseek-r1", "vision-1", "text-only", "typeless"} {
		if !ids[want] {
			t.Errorf("ModelsByType(chat) is missing %q: %v", want, ids)
		}
	}
	for _, hidden := range []string{"old-model", "", "gpt-5-embed", "qwen3.8-text-embedding"} {
		if ids[hidden] {
			t.Errorf("ModelsByType(chat) should hide %q", hidden)
		}
	}

	vlm := c.ModelsByType(types.ModelTypeVLLM)
	if len(vlm) != 1 || vlm[0].ID != "vision-1" {
		t.Fatalf("ModelsByType(VLLM) = %v, want only vision-1", vlm)
	}

	emb := c.ModelsByType(types.ModelTypeEmbedding)
	if len(emb) != 2 {
		t.Fatalf("ModelsByType(Embedding) = %d entries, want 2", len(emb))
	}
}

func TestFindModelExactAliasAndGlob(t *testing.T) {
	c := New(catalogFixture())

	if m, ok := c.FindModel("GPT-5", types.ModelTypeKnowledgeQA); !ok || m.ID != "gpt-5" {
		t.Fatalf("FindModel exact id (case-insensitive) = %+v, %v", m, ok)
	}
	if m, ok := c.FindModel("gpt5", types.ModelTypeKnowledgeQA); !ok || m.ID != "gpt-5" {
		t.Fatalf("FindModel alias = %+v, %v", m, ok)
	}
	// Exact id wins over the glob pattern on the same row.
	if m, ok := c.FindModel("gpt-5", types.ModelTypeKnowledgeQA); !ok || m.ID != "gpt-5" {
		t.Fatalf("FindModel exact id lost to glob: %+v, %v", m, ok)
	}
	// Longest literal prefix wins among globs.
	if m, ok := c.FindModel("qwen3-235b-a22b", types.ModelTypeKnowledgeQA); !ok || m.ID != "qwen3-235b" {
		t.Fatalf("FindModel longest-prefix glob = %+v, %v", m, ok)
	}
	if m, ok := c.FindModel("xxdeepseek-r1yy", types.ModelTypeKnowledgeQA); !ok || m.ID != "deepseek-r1" {
		t.Fatalf("FindModel leading-star glob = %+v, %v", m, ok)
	}
	if _, ok := c.FindModel("no-such-model", types.ModelTypeKnowledgeQA); ok {
		t.Fatalf("FindModel matched an unknown name")
	}
	if _, ok := c.FindModel("   ", types.ModelTypeKnowledgeQA); ok {
		t.Fatalf("FindModel matched a blank name")
	}
}

func TestFindModelKeepsTypesSeparate(t *testing.T) {
	c := New(catalogFixture())

	// The chat glob gpt-5* also matches "gpt-5-embed" by prefix, so a chat
	// lookup returns the chat family row — but never the embedding row.
	// That is the point of the type filter: an untyped lookup could hand
	// the embedding row the chat family's compat, which cannot be built.
	if m, ok := c.FindModel("gpt-5-embed", types.ModelTypeKnowledgeQA); !ok || m.ID != "gpt-5" {
		t.Fatalf("FindModel(chat, gpt-5-embed) = %+v, %v; want the chat row gpt-5, never the embedding row", m, ok)
	}
	if m, ok := c.FindModel("qwen3.8-text-embedding", types.ModelTypeKnowledgeQA); !ok || m.ID != "qwen3-family" {
		t.Fatalf("FindModel(chat, qwen3.8-text-embedding) = %+v, %v; want the chat row qwen3-family, never the embedding row", m, ok)
	}
	if m, ok := c.FindModel("gpt-5-embed", types.ModelTypeEmbedding); !ok || m.ID != "gpt-5-embed" {
		t.Fatalf("FindModel(embedding) = %+v, %v", m, ok)
	}
	// A VLM lookup searches among the chat entries.
	if m, ok := c.FindModel("vision-1", types.ModelTypeVLLM); !ok || m.ID != "vision-1" {
		t.Fatalf("FindModel(VLLM) = %+v, %v", m, ok)
	}
}

func TestEntryType(t *testing.T) {
	if got := EntryType(""); got != types.ModelTypeKnowledgeQA {
		t.Errorf("EntryType(\"\") = %q, want KnowledgeQA", got)
	}
	if got := EntryType(types.ModelTypeVLLM); got != types.ModelTypeKnowledgeQA {
		t.Errorf("EntryType(VLLM) = %q, want KnowledgeQA", got)
	}
	if got := EntryType(types.ModelTypeEmbedding); got != types.ModelTypeEmbedding {
		t.Errorf("EntryType(Embedding) = %q, want Embedding", got)
	}
	if got := EntryType(types.ModelTypeRerank); got != types.ModelTypeRerank {
		t.Errorf("EntryType(Rerank) = %q, want Rerank", got)
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		s       string
		want    bool
	}{
		{"gpt-5*", "gpt-5", true},
		{"gpt-5*", "gpt-5-chat-latest", true},
		{"gpt-5*", "gpt-4", false},
		{"*deepseek-r1*", "xxdeepseek-r1yy", true},
		{"*deepseek-r1*", "deepseek-v3", false},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "aXc", false},
		{"plain", "plain", true},
		{"plain", "plainx", false},
		{"*", "anything", true},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.s); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.s, got, tc.want)
		}
	}
}
