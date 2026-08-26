package subtitle

import "testing"

func TestValidateParagraphsAcceptsTimedText(t *testing.T) {
	paragraphs := []TranscriptParagraph{{
		ParagraphID: "p1",
		Sentences:   []TranscriptSentence{{SentenceID: "s1", Text: "你好", StartMs: 100, EndMs: 900}},
	}}

	if err := ValidateParagraphs(paragraphs); err != nil {
		t.Fatalf("ValidateParagraphs() error = %v", err)
	}
	got := ParagraphsToSRT(paragraphs)
	want := "1\n00:00:00,100 --> 00:00:00,900\n[说话人 0] 你好\n\n"
	if got != want {
		t.Fatalf("ParagraphsToSRT() = %q, want %q", got, want)
	}
}

func TestValidateParagraphsRejectsEmptyText(t *testing.T) {
	err := ValidateParagraphs([]TranscriptParagraph{{
		Sentences: []TranscriptSentence{{Text: "   ", StartMs: 0, EndMs: 100}},
	}})
	if err == nil {
		t.Fatal("ValidateParagraphs() error = nil, want empty transcript error")
	}
}

func TestValidateParagraphsRejectsInvalidTimeline(t *testing.T) {
	err := ValidateParagraphs([]TranscriptParagraph{{
		Sentences: []TranscriptSentence{{Text: "内容", StartMs: 900, EndMs: 100}},
	}})
	if err == nil {
		t.Fatal("ValidateParagraphs() error = nil, want invalid timeline error")
	}
}
