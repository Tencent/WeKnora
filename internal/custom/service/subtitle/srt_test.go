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

func TestParagraphsToSRTSupportsCrossHourTimeline(t *testing.T) {
	paragraphs := []TranscriptParagraph{{
		SpeakerID: "1",
		Sentences: []TranscriptSentence{{Text: "跨小时内容", StartMs: 3_599_900, EndMs: 3_600_500}},
	}}
	if err := ValidateParagraphs(paragraphs); err != nil {
		t.Fatalf("ValidateParagraphs() error = %v", err)
	}
	want := "1\n00:59:59,900 --> 01:00:00,500\n[说话人 1] 跨小时内容\n\n"
	if got := ParagraphsToSRT(paragraphs); got != want {
		t.Fatalf("ParagraphsToSRT() = %q, want %q", got, want)
	}
}

func TestValidateTranscriptQualityRejectsNonMonotonicTimestamps(t *testing.T) {
	paragraphs := []TranscriptParagraph{{Sentences: []TranscriptSentence{
		{Text: "第一句", StartMs: 100, EndMs: 200},
		{Text: "第二句", StartMs: 50, EndMs: 150},
	}}}
	if err := ValidateTranscriptQuality(paragraphs, 1); err == nil {
		t.Fatal("ValidateTranscriptQuality() error = nil, want non-monotonic timeline error")
	}
}

func TestValidateTranscriptQualityAcceptsCompleteTimeline(t *testing.T) {
	paragraphs := []TranscriptParagraph{{Sentences: []TranscriptSentence{
		{Text: "第一句", StartMs: 100, EndMs: 200},
		{Text: "第二句", StartMs: 300, EndMs: 900},
	}}}
	if err := ValidateTranscriptQuality(paragraphs, 1); err != nil {
		t.Fatalf("ValidateTranscriptQuality() error = %v", err)
	}
}
