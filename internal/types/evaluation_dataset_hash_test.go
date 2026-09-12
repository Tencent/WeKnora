package types

import (
	"testing"
)

func evaluationDatasetHashFixture() *EvaluationDatasetVersionInput {
	return &EvaluationDatasetVersionInput{
		Passages: []EvaluationDatasetPassageInput{
			{PID: "p2", Content: "second passage", Metadata: JSON(`{"lang":"zh"}`)},
			{PID: "p1", Content: "first passage"},
			{PID: "p10", Content: "tenth passage"},
		},
		Questions: []EvaluationDatasetQuestionInput{
			{QID: "q2", Question: "第二个问题？", Answer: "答案二"},
			{QID: "q1", Question: "first question?", Answer: "answer one"},
		},
		Relevance: []EvaluationDatasetRelevanceInput{
			{QID: "q2", PID: "p2", Grade: 1},
			{QID: "q1", PID: "p1", Grade: 2},
			{QID: "q1", PID: "p2", Grade: 1},
		},
	}
}

func TestCanonicalEvaluationDatasetContentHashIsOrderIndependent(t *testing.T) {
	base := evaluationDatasetHashFixture()
	shuffled := &EvaluationDatasetVersionInput{
		Passages: []EvaluationDatasetPassageInput{
			base.Passages[2], base.Passages[0], base.Passages[1],
		},
		Questions: []EvaluationDatasetQuestionInput{
			base.Questions[1], base.Questions[0],
		},
		Relevance: []EvaluationDatasetRelevanceInput{
			base.Relevance[1], base.Relevance[2], base.Relevance[0],
		},
	}

	got := CanonicalEvaluationDatasetContentSHA256(base)
	want := CanonicalEvaluationDatasetContentSHA256(shuffled)
	if got != want {
		t.Fatalf("content hash changed with input order: %s != %s", got, want)
	}
	if len(got) != 64 {
		t.Fatalf("content hash length = %d, want 64 hex chars", len(got))
	}
}

func TestCanonicalEvaluationDatasetContentHashChangesOnAnyContentChange(t *testing.T) {
	base := CanonicalEvaluationDatasetContentSHA256(evaluationDatasetHashFixture())

	cases := []struct {
		name   string
		mutate func(*EvaluationDatasetVersionInput)
	}{
		{"passage id", func(in *EvaluationDatasetVersionInput) { in.Passages[0].PID = "p2x" }},
		{"passage content", func(in *EvaluationDatasetVersionInput) { in.Passages[0].Content += " " }},
		{"passage metadata", func(in *EvaluationDatasetVersionInput) {
			in.Passages[0].Metadata = JSON(`{"lang":"en"}`)
		}},
		{"question id", func(in *EvaluationDatasetVersionInput) { in.Questions[0].QID = "q2x" }},
		{"question text", func(in *EvaluationDatasetVersionInput) { in.Questions[0].Question += "!" }},
		{"reference answer", func(in *EvaluationDatasetVersionInput) { in.Questions[0].Answer = "答案三" }},
		{"relevance grade", func(in *EvaluationDatasetVersionInput) { in.Relevance[0].Grade = 3 }},
		{"relevance edge", func(in *EvaluationDatasetVersionInput) { in.Relevance[0].PID = "p10" }},
		{"removed passage", func(in *EvaluationDatasetVersionInput) {
			in.Passages = in.Passages[:2]
		}},
		{"added question", func(in *EvaluationDatasetVersionInput) {
			in.Questions = append(in.Questions, EvaluationDatasetQuestionInput{QID: "q3", Question: "q3"})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := evaluationDatasetHashFixture()
			tc.mutate(input)
			if got := CanonicalEvaluationDatasetContentSHA256(input); got == base {
				t.Fatalf("content hash did not change after %s mutation", tc.name)
			}
		})
	}
}

func TestCanonicalEvaluationDatasetContentHashPreservesRawUTF8(t *testing.T) {
	// Trailing/leading whitespace and distinct Unicode forms must not be
	// normalized away: each spelling is a different logical content.
	padded := evaluationDatasetHashFixture()
	padded.Passages[1].Content = " first passage "
	if CanonicalEvaluationDatasetContentSHA256(padded) ==
		CanonicalEvaluationDatasetContentSHA256(evaluationDatasetHashFixture()) {
		t.Fatal("content hash must not trim whitespace")
	}

	decomposed := evaluationDatasetHashFixture()
	// "é" as e + combining acute (U+0065 U+0301) must differ from precomposed U+00E9.
	decomposed.Questions[0].Question = "café"
	composed := evaluationDatasetHashFixture()
	composed.Questions[0].Question = "caf\u00e9"
	if CanonicalEvaluationDatasetContentSHA256(decomposed) ==
		CanonicalEvaluationDatasetContentSHA256(composed) {
		t.Fatal("content hash must not apply Unicode normalization")
	}
}

func TestCanonicalEvaluationDatasetContentHashCoversSchemaAndRecordType(t *testing.T) {
	empty := CanonicalEvaluationDatasetContentSHA256(&EvaluationDatasetVersionInput{})
	if len(empty) != 64 {
		t.Fatalf("empty input hash length = %d, want 64", len(empty))
	}
	// A single passage line must differ from a single question line even when
	// the visible text overlaps, because the record type is part of the line.
	passageOnly := CanonicalEvaluationDatasetContentSHA256(&EvaluationDatasetVersionInput{
		Passages: []EvaluationDatasetPassageInput{{PID: "x", Content: "same"}},
	})
	questionOnly := CanonicalEvaluationDatasetContentSHA256(&EvaluationDatasetVersionInput{
		Questions: []EvaluationDatasetQuestionInput{{QID: "x", Question: "same"}},
	})
	if passageOnly == questionOnly {
		t.Fatal("record type must participate in the canonical hash")
	}
}

func TestEvaluationDatasetArtifactSHA256(t *testing.T) {
	payload := []byte(`{"passages":[],"questions":[],"relevance":[]}`)
	got := EvaluationDatasetArtifactSHA256(payload)
	if len(got) != 64 {
		t.Fatalf("artifact hash length = %d, want 64", len(got))
	}
	if got == EvaluationDatasetArtifactSHA256(append(payload, ' ')) {
		t.Fatal("artifact hash must change when the payload bytes change")
	}
}
