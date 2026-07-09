package types

import (
	"strings"
	"testing"
)

func TestNormalizeContent_BasicCollapse(t *testing.T) {
	in := "  Hello\t\tWorld  \n\nNext   line  "
	want := "Hello World\nNext line"
	if got := NormalizeContent(in); got != want {
		t.Errorf("NormalizeContent mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestNormalizeContent_NFKCAndWidthFold(t *testing.T) {
	// Full-width Latin "Ａｂｃ" and NBSP between words should normalize.
	in := "\uFF21\uFF42\uFF43\u00A0hello\r\n\r\nworld"
	out := NormalizeContent(in)
	if !strings.HasPrefix(out, "Abc") {
		t.Errorf("expected width-folded ABC prefix, got %q", out)
	}
	if strings.Contains(out, "\u00A0") {
		t.Errorf("NBSP survived normalization: %q", out)
	}
	if strings.Contains(out, "\r") {
		t.Errorf("CR survived normalization: %q", out)
	}
	if strings.Contains(out, "\n\n\n") {
		t.Errorf("3+ newlines survived normalization")
	}
}

func TestNormalizeContent_EmptyAndZeroWidth(t *testing.T) {
	if got := NormalizeContent(""); got != "" {
		t.Errorf("empty input should stay empty, got %q", got)
	}
	if got := NormalizeContent("\u200B\uFEFF\u00A0"); got != "" {
		t.Errorf("zero-width/BOM/NBSP-only input should collapse to empty, got %q", got)
	}
}

func TestStableContentHash_VersionedAndStable(t *testing.T) {
	a := StableContentHash("hello")
	b := StableContentHash("hello")
	if a != b {
		t.Fatalf("identical content produced different hashes: %s != %s", a, b)
	}
	if !strings.HasPrefix(a, ContentNormalizationVersion+":") {
		t.Errorf("hash missing normalization-version prefix: %s", a)
	}
	// Trailing space is trimmed during normalization so these MUST match a.
	if c := StableContentHash("hello "); a != c {
		t.Errorf("trailing space should be trimmed so hashes match: %s != %s", a, c)
	}
	if d := StableContentHash("hello\n"); a != d {
		t.Errorf("trailing newline should be trimmed so hashes match: %s != %s", a, d)
	}
	// A genuine content difference MUST change the hash.
	if e := StableContentHash("hello x"); a == e {
		t.Errorf("real content difference did not change the hash")
	}
}
func TestStableChunkID_Deterministic(t *testing.T) {
	id1 := StableChunkID("doc-1", "same content", 0)
	id2 := StableChunkID("doc-1", "same content", 0)
	if id1 != id2 {
		t.Fatalf("stable IDs not deterministic: %s != %s", id1, id2)
	}
	if len(id1) != 36 {
		t.Fatalf("stable ID not 36 chars (uuid-shaped): len=%d id=%s", len(id1), id1)
	}
	// Dashes at canonical UUID offsets 8/13/18/23.
	for _, i := range []int{8, 13, 18, 23} {
		if id1[i] != '-' {
			t.Errorf("expected '-' at offset %d in %s", i, id1)
		}
	}
	// Version nibble = 5 at offset 14.
	if id1[14] != '5' {
		t.Errorf("expected version nibble '5' at offset 14, got %q in %s", byte(id1[14]), id1)
	}
}

func TestStableChunkID_DifferentSeqDiffers(t *testing.T) {
	a := StableChunkID("doc-1", "same content", 0)
	b := StableChunkID("doc-1", "same content", 1)
	if a == b {
		t.Fatalf("stable_seq failed to disambiguate identical content: %s == %s", a, b)
	}
}

func TestStableChunkID_DifferentDocIDDiffers(t *testing.T) {
	a := StableChunkID("doc-1", "same content", 0)
	b := StableChunkID("doc-2", "same content", 0)
	if a == b {
		t.Fatalf("doc_id failed to differentiate cross-doc identical content: %s == %s", a, b)
	}
}

func TestStableChunkID_DifferentContentDiffers(t *testing.T) {
	a := StableChunkID("doc-1", "content a", 0)
	b := StableChunkID("doc-1", "content b", 0)
	if a == b {
		t.Fatalf("different content produced same ID: %s", a)
	}
}

func TestStableChunkID_NormalizationStability(t *testing.T) {
	// The same logical content with only whitespace differences MUST produce
	// the same stable ID — that's the whole point of the normalize→hash
	// pipeline.
	a := StableChunkID("doc-1", "Hello\tworld", 0)
	b := StableChunkID("doc-1", " Hello    world ", 0)
	if a != b {
		t.Fatalf("whitespace-only differences produced different IDs: %s != %s", a, b)
	}
}

func TestStableChunkID_AmbiguityResistance(t *testing.T) {
	// ("ab|","c",0) must NOT collide with ("a","b|c",0) — the NUL separators
	// in the pre-hash input prevent this.
	a := StableChunkID("ab|", "c", 0)
	b := StableChunkID("a", "b|c", 0)
	if a == b {
		t.Fatalf("length-ambiguity collision: %s == %s", a, b)
	}
}