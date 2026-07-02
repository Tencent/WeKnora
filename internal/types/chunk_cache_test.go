package types

import "testing"

func TestNormalizeForHash(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool // whether a and b normalize equal
	}{
		{"crlf vs lf", "line1\r\nline2", "line1\nline2", true},
		{"cr vs lf", "line1\rline2", "line1\nline2", true},
		{"trailing spaces", "hello   \nworld\t", "hello\nworld", true},
		{"outer whitespace", "\n  body  \n", "body", true},
		{"distinct content stays distinct", "alpha", "beta", false},
		{"internal spacing preserved", "a  b", "a b", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeForHash(c.a) == NormalizeForHash(c.b)
			if got != c.want {
				t.Fatalf("NormalizeForHash equality (%q vs %q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestContentHashStableAndSized(t *testing.T) {
	h1 := ContentHash("Some content\r\n")
	h2 := ContentHash("Some content")
	if h1 != h2 {
		t.Fatalf("content hash not stable across line-ending diff: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("content hash length = %d, want 64 (fits varchar(64))", len(h1))
	}
	if ContentHash("a") == ContentHash("b") {
		t.Fatalf("distinct content produced identical hash")
	}
}

func TestStableChunkID(t *testing.T) {
	const kb = "kb-123"
	id := StableChunkID(kb, ChunkTypeText, "hello world", 0)

	if len(id) != stableChunkIDLen {
		t.Fatalf("id length = %d, want %d (must fit varchar(36))", len(id), stableChunkIDLen)
	}
	if got := StableChunkID(kb, ChunkTypeText, "hello world", 0); got != id {
		t.Fatalf("StableChunkID not deterministic: %s vs %s", id, got)
	}
	// content-equivalent input (line endings/trailing ws) maps to same ID
	if got := StableChunkID(kb, ChunkTypeText, "hello world  \n", 0); got != id {
		t.Fatalf("normalization not applied to ID: %s vs %s", id, got)
	}
	// occurrence, knowledge, type and content each change the ID
	if StableChunkID(kb, ChunkTypeText, "hello world", 1) == id {
		t.Fatal("different occurrence produced same ID")
	}
	if StableChunkID("other-kb", ChunkTypeText, "hello world", 0) == id {
		t.Fatal("different knowledge produced same ID")
	}
	if StableChunkID(kb, ChunkTypeParentText, "hello world", 0) == id {
		t.Fatal("different chunk type produced same ID")
	}
	if StableChunkID(kb, ChunkTypeText, "different", 0) == id {
		t.Fatal("different content produced same ID")
	}
}

func TestChunkIDAllocator(t *testing.T) {
	a := NewChunkIDAllocator("kb-1")
	id1, hash1 := a.Allocate(ChunkTypeText, "repeated")
	id2, hash2 := a.Allocate(ChunkTypeText, "repeated")

	if id1 == id2 {
		t.Fatal("duplicate content within a doc must get distinct IDs (occurrence counter)")
	}
	if hash1 != hash2 {
		t.Fatal("content hash must match for identical content regardless of occurrence")
	}

	// A fresh allocator (a later re-parse of the same document) reproduces the
	// same IDs in the same order — this is what makes embeddings reusable.
	b := NewChunkIDAllocator("kb-1")
	rid1, _ := b.Allocate(ChunkTypeText, "repeated")
	rid2, _ := b.Allocate(ChunkTypeText, "repeated")
	if rid1 != id1 || rid2 != id2 {
		t.Fatalf("re-parse produced different IDs: (%s,%s) vs (%s,%s)", rid1, rid2, id1, id2)
	}
}

func TestPlanChunkReuse(t *testing.T) {
	// Existing store: A (unchanged), B (will change), C (will be removed).
	existing := []*Chunk{
		{ID: "A", ContentHash: "ha"},
		{ID: "B", ContentHash: "hb"},
		{ID: "C", ContentHash: "hc"},
	}
	// Fresh parse: A unchanged, B' changed (new ID since content-addressed),
	// D brand new. C is gone.
	incoming := []*Chunk{
		{ID: "A", ContentHash: "ha"},
		{ID: "Bnew", ContentHash: "hb2"},
		{ID: "D", ContentHash: "hd"},
	}

	plan := PlanChunkReuse(existing, incoming)

	if _, ok := plan.ReuseIDs["A"]; !ok || plan.ReuseCount() != 1 {
		t.Fatalf("expected only A reused, got %v", plan.ReuseIDs)
	}
	if !containsChunkID(plan.ToEmbed, "Bnew") || !containsChunkID(plan.ToEmbed, "D") || len(plan.ToEmbed) != 2 {
		t.Fatalf("expected Bnew and D to embed, got %v", chunkIDs(plan.ToEmbed))
	}
	// B (old) and C are no longer present -> delete.
	if !containsString(plan.ToDeleteIDs, "B") || !containsString(plan.ToDeleteIDs, "C") || len(plan.ToDeleteIDs) != 2 {
		t.Fatalf("expected B and C to delete, got %v", plan.ToDeleteIDs)
	}
}

func TestPlanChunkReuseLegacyEmptyHashTreatedAsChanged(t *testing.T) {
	// A legacy row with the same ID but no ContentHash (pre-#1679 UUID data)
	// must not be treated as a cache hit.
	existing := []*Chunk{{ID: "X", ContentHash: ""}}
	incoming := []*Chunk{{ID: "X", ContentHash: "hx"}}

	plan := PlanChunkReuse(existing, incoming)
	if plan.ReuseCount() != 0 {
		t.Fatalf("legacy empty-hash row must not be reused, got %v", plan.ReuseIDs)
	}
	if !containsChunkID(plan.ToEmbed, "X") {
		t.Fatal("expected X to be (re)embedded")
	}
}

func containsChunkID(chunks []*Chunk, id string) bool {
	for _, c := range chunks {
		if c.ID == id {
			return true
		}
	}
	return false
}

func chunkIDs(chunks []*Chunk) []string {
	ids := make([]string, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	return ids
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
