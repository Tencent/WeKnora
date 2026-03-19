package types

import (
	"testing"
)

func TestCalculateFAQContentHash_NormalizeIsApplied(t *testing.T) {
	// The core bug: CalculateFAQContentHash must normalize the input so that
	// sanitized-only data and pre-normalized data produce the same hash.
	meta := &FAQChunkMetadata{
		StandardQuestion: "  你好，World？ ",
		SimilarQuestions: []string{"Hello World", "hello world"},
		Answers:          []string{"answer1"},
		AnswerStrategy:   AnswerStrategyAll,
		Version:          1,
	}

	// Path 1: what SetFAQMetadata does (normalize first, then hash)
	normalized := meta.Normalize()
	hashFromNormalized := CalculateFAQContentHash(normalized)

	// Path 2: what calculateReplaceOperations does (hash directly from sanitized data)
	sanitized := &FAQChunkMetadata{
		StandardQuestion: "  你好，World？ ",
		SimilarQuestions: []string{"Hello World", "hello world"},
		Answers:          []string{"answer1"},
		AnswerStrategy:   AnswerStrategyAll,
		Version:          1,
	}
	sanitized.Sanitize()
	hashFromSanitized := CalculateFAQContentHash(sanitized)

	if hashFromNormalized != hashFromSanitized {
		t.Errorf("Hash mismatch between write and read paths:\n  write (normalized first): %s\n  read  (sanitized only):   %s",
			hashFromNormalized, hashFromSanitized)
	}
}

func TestCalculateFAQContentHash_ConsistentViaSetFAQMetadata(t *testing.T) {
	// Simulate the full write path then read-path comparison
	meta := &FAQChunkMetadata{
		StandardQuestion: "如何退款？",
		SimilarQuestions: []string{"怎么退款", "退款流程"},
		Answers:          []string{"请联系客服"},
		AnswerStrategy:   AnswerStrategyAll,
		Version:          1,
		Source:           "faq",
	}

	// Write path: SetFAQMetadata stores ContentHash
	chunk := &Chunk{}
	if err := chunk.SetFAQMetadata(meta); err != nil {
		t.Fatalf("SetFAQMetadata failed: %v", err)
	}
	if chunk.ContentHash == "" {
		t.Fatal("SetFAQMetadata did not set ContentHash")
	}

	// Read path: calculateReplaceOperations calls sanitize + CalculateFAQContentHash
	readMeta := &FAQChunkMetadata{
		StandardQuestion: "如何退款？",
		SimilarQuestions: []string{"怎么退款", "退款流程"},
		Answers:          []string{"请联系客服"},
		AnswerStrategy:   AnswerStrategyAll,
		Version:          1,
		Source:           "faq",
	}
	readMeta.Sanitize()
	readHash := CalculateFAQContentHash(readMeta)

	if chunk.ContentHash != readHash {
		t.Errorf("Hash mismatch between SetFAQMetadata and direct CalculateFAQContentHash:\n  SetFAQMetadata:           %s\n  CalculateFAQContentHash:  %s",
			chunk.ContentHash, readHash)
	}
}

func TestCalculateFAQContentHash_CaseAndPunctuationInvariant(t *testing.T) {
	meta1 := &FAQChunkMetadata{
		StandardQuestion: "Hello World?",
		Answers:          []string{"answer"},
	}
	meta2 := &FAQChunkMetadata{
		StandardQuestion: "hello world？",
		Answers:          []string{"answer"},
	}

	hash1 := CalculateFAQContentHash(meta1)
	hash2 := CalculateFAQContentHash(meta2)

	if hash1 != hash2 {
		t.Errorf("Hash should be case/punctuation invariant after normalization:\n  %q -> %s\n  %q -> %s",
			meta1.StandardQuestion, hash1, meta2.StandardQuestion, hash2)
	}
}

func TestCalculateFAQContentHash_TraditionalSimplifiedInvariant(t *testing.T) {
	meta1 := &FAQChunkMetadata{
		StandardQuestion: "如何退款",
		Answers:          []string{"请联系客服"},
	}
	meta2 := &FAQChunkMetadata{
		StandardQuestion: "如何退款", // simplified
		Answers:          []string{"請聯繫客服"}, // traditional in answers — answers only sanitize, not normalize
	}

	// Questions should normalize, but answers only sanitize.
	// So answers in traditional vs simplified WILL produce different hashes (by design).
	// But standard questions with t2s should match.
	metaTraditionalQ := &FAQChunkMetadata{
		StandardQuestion: "開發環境",
		Answers:          []string{"answer"},
	}
	metaSimplifiedQ := &FAQChunkMetadata{
		StandardQuestion: "开发环境",
		Answers:          []string{"answer"},
	}

	hashTrad := CalculateFAQContentHash(metaTraditionalQ)
	hashSimp := CalculateFAQContentHash(metaSimplifiedQ)

	if hashTrad != hashSimp {
		t.Errorf("Hash should be traditional/simplified invariant for questions:\n  traditional: %s\n  simplified:  %s",
			hashTrad, hashSimp)
	}

	_ = meta1
	_ = meta2
}

func TestCalculateFAQContentHash_SortInvariant(t *testing.T) {
	meta1 := &FAQChunkMetadata{
		StandardQuestion: "问题",
		SimilarQuestions: []string{"a", "b", "c"},
		Answers:          []string{"x", "y", "z"},
	}
	meta2 := &FAQChunkMetadata{
		StandardQuestion: "问题",
		SimilarQuestions: []string{"c", "a", "b"},
		Answers:          []string{"z", "x", "y"},
	}

	hash1 := CalculateFAQContentHash(meta1)
	hash2 := CalculateFAQContentHash(meta2)

	if hash1 != hash2 {
		t.Errorf("Hash should be order-invariant for similar questions and answers:\n  order1: %s\n  order2: %s",
			hash1, hash2)
	}
}

func TestCalculateFAQContentHash_NilAndEmpty(t *testing.T) {
	if h := CalculateFAQContentHash(nil); h != "" {
		t.Errorf("Expected empty hash for nil, got %s", h)
	}

	meta := &FAQChunkMetadata{}
	h := CalculateFAQContentHash(meta)
	if h == "" {
		t.Error("Expected non-empty hash for empty metadata (still has delimiters)")
	}
}

func TestCalculateFAQContentHash_FullWidthHalfWidthInvariant(t *testing.T) {
	metaFull := &FAQChunkMetadata{
		StandardQuestion: "Ｈｅｌｌｏ　Ｗｏｒｌｄ",
		Answers:          []string{"answer"},
	}
	metaHalf := &FAQChunkMetadata{
		StandardQuestion: "hello world",
		Answers:          []string{"answer"},
	}

	hashFull := CalculateFAQContentHash(metaFull)
	hashHalf := CalculateFAQContentHash(metaHalf)

	if hashFull != hashHalf {
		t.Errorf("Hash should be fullwidth/halfwidth invariant:\n  fullwidth: %s\n  halfwidth: %s",
			hashFull, hashHalf)
	}
}
