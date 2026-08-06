package types

import (
	"strings"
	"testing"
)

func TestGeneratedQuestionSourceID(t *testing.T) {
	chunkID := "135bf11d-c20e-419a-9f3c-5288d6a0516b"
	shortQuestionID := "q1722147984000000000"
	if got, want := GeneratedQuestionSourceID(chunkID, shortQuestionID), chunkID+"-"+shortQuestionID; got != want {
		t.Fatalf("short source ID changed: got %q, want %q", got, want)
	}

	longQuestionID := "a4fa83a2-0fde-4b85-99bc-4572fa92d51c"
	got := GeneratedQuestionSourceID(chunkID, longQuestionID)
	if len(got) > maxGeneratedQuestionSourceIDLength {
		t.Fatalf("source ID length = %d, want <= %d: %q", len(got), maxGeneratedQuestionSourceIDLength, got)
	}
	if !strings.HasPrefix(got, chunkID+"-q") {
		t.Fatalf("hashed source ID has unexpected format: %q", got)
	}
	if got != GeneratedQuestionSourceID(chunkID, longQuestionID) {
		t.Fatal("source ID hashing is not deterministic")
	}
}
