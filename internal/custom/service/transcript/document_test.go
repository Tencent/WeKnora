package transcript

import (
	"strings"
	"testing"
)

func validDocumentInput() Input {
	return Input{
		VideoID: "video-1", TranscriptGeneration: "generation-1", Title: "学习 AI", DurationSeconds: 20,
		Chapters: []InputChapter{{Index: 0, Title: "开场", Paragraphs: []InputParagraph{{ParagraphID: "p-1", Index: 0, SpeakerID: "speaker-1", Sentences: []InputSentence{
			{SourceSentenceID: "s-1", EvidenceSentenceID: "evs:v1:one", Text: "Hello", StartMs: 1000, EndMs: 2000},
			{SourceSentenceID: "s-2", EvidenceSentenceID: "evs:v1:two", Text: "world。", StartMs: 2000, EndMs: 3000},
		}}}}},
	}
}

func TestBuildKeepsMappingAndContinuousTextDeterministic(t *testing.T) {
	input := validDocumentInput()
	input.Chapters[0].Paragraphs[0].Sentences[0].StartMs = 0
	doc, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != DocumentSchemaVersion || doc.ContinuousText != "Hello world。" {
		t.Fatalf("unexpected document: %+v", doc)
	}
	paragraph := doc.Chapters[0].Paragraphs[0]
	if paragraph.Text != "Hello world。" || paragraph.SpeakerID != "speaker-1" {
		t.Fatalf("unexpected paragraph: %+v", paragraph)
	}
	if paragraph.StartMs != 0 || paragraph.EndMs != 3000 {
		t.Fatalf("paragraph bounds were not preserved: %d-%d", paragraph.StartMs, paragraph.EndMs)
	}
	if paragraph.TimeMarks[1].EvidenceSentenceID != "evs:v1:two" || paragraph.TimeMarks[1].StartMs != 2000 {
		t.Fatalf("mapping was not retained: %+v", paragraph.TimeMarks)
	}
	if paragraph.TimeMarks[0].Text != "Hello" || paragraph.ParagraphID != "p-1" {
		t.Fatalf("sentence text or paragraph ID was not retained: %+v", paragraph)
	}
	jsonText, err := doc.JSON()
	if err != nil || !strings.Contains(jsonText, `"continuous_text": "Hello world。"`) {
		t.Fatalf("unexpected JSON: %v %s", err, jsonText)
	}
}

func TestBuildRejectsIncompleteMappingAndInvalidOrder(t *testing.T) {
	input := validDocumentInput()
	input.Chapters[0].Paragraphs[0].Sentences[0].EvidenceSentenceID = ""
	if _, err := Build(input); err == nil {
		t.Fatal("expected incomplete mapping error")
	}

	input = validDocumentInput()
	input.Chapters[0].Paragraphs[0].Sentences[0].EndMs = 900
	if _, err := Build(input); err == nil {
		t.Fatal("expected invalid timeline error")
	}
}

func TestValidateRejectsDuplicateEvidence(t *testing.T) {
	doc, err := Build(validDocumentInput())
	if err != nil {
		t.Fatal(err)
	}
	doc.Chapters[0].Paragraphs[0].TimeMarks[1].EvidenceSentenceID = doc.Chapters[0].Paragraphs[0].TimeMarks[0].EvidenceSentenceID
	doc.Chapters[0].Paragraphs[0].EvidenceSentenceIDs[1] = doc.Chapters[0].Paragraphs[0].EvidenceSentenceIDs[0]
	if err := Validate(doc); err == nil || !strings.Contains(err.Error(), "duplicate evidence") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildRejectsSpeakerChangeAndValidateRejectsTamperedText(t *testing.T) {
	input := validDocumentInput()
	input.Chapters[0].Paragraphs[0].Sentences[1].SpeakerID = "speaker-2"
	if _, err := Build(input); err == nil || !strings.Contains(err.Error(), "multiple speakers") {
		t.Fatalf("unexpected speaker error: %v", err)
	}

	doc, err := Build(validDocumentInput())
	if err != nil {
		t.Fatal(err)
	}
	doc.Chapters[0].Paragraphs[0].Text = "被篡改的文本"
	if err := Validate(doc); err == nil || !strings.Contains(err.Error(), "cannot be reconstructed") {
		t.Fatalf("unexpected text error: %v", err)
	}

	doc, err = Build(validDocumentInput())
	if err != nil {
		t.Fatal(err)
	}
	doc.ContinuousText = "被篡改的整篇原文"
	if err := Validate(doc); err == nil || !strings.Contains(err.Error(), "document continuous text") {
		t.Fatalf("unexpected document text error: %v", err)
	}
}

func TestBuildRejectsCrossChapterTimelineAndDurationOverflow(t *testing.T) {
	input := validDocumentInput()
	input.Chapters = append(input.Chapters, InputChapter{Index: 1, Title: "结尾", Paragraphs: []InputParagraph{{Index: 0, SpeakerID: "speaker-1", Sentences: []InputSentence{
		{SourceSentenceID: "s-3", EvidenceSentenceID: "evs:v1:three", Text: "结束", StartMs: 1500, EndMs: 2500},
	}}}})
	if _, err := Build(input); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("unexpected cross-chapter error: %v", err)
	}

	input = validDocumentInput()
	input.DurationSeconds = 2
	if _, err := Build(input); err == nil || !strings.Contains(err.Error(), "exceeds video duration") {
		t.Fatalf("unexpected duration error: %v", err)
	}
}

func TestBuildProducesStableJSON(t *testing.T) {
	first, err := Build(validDocumentInput())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(validDocumentInput())
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := first.JSON()
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := second.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if firstJSON != secondJSON {
		t.Fatal("equivalent inputs produced different JSON")
	}
	if !strings.Contains(firstJSON, `"duration_seconds": 20`) {
		t.Fatalf("duration should remain present in canonical JSON: %s", firstJSON)
	}
}

func TestValidateSourceContentRejectsEmptyOversizeIdentityAndDuration(t *testing.T) {
	doc, err := Build(validDocumentInput())
	if err != nil {
		t.Fatal(err)
	}
	jsonText, err := doc.JSON()
	if err != nil {
		t.Fatal(err)
	}
	content := SourceContent(doc, jsonText, "hash")
	if _, err := ValidateSourceContent("", doc.VideoID, doc.TranscriptGeneration, doc.DurationSeconds); err == nil || !strings.Contains(err.Error(), SourceValidationEmpty) {
		t.Fatalf("expected empty source validation error, got %v", err)
	}
	if _, err := ValidateSourceContent(strings.Repeat("x", MaxSourceDocumentBytes+1), doc.VideoID, doc.TranscriptGeneration, doc.DurationSeconds); err == nil || !strings.Contains(err.Error(), SourceValidationTooLarge) {
		t.Fatalf("expected size validation error, got %v", err)
	}
	if _, err := ValidateSourceContent(content, "other-video", doc.TranscriptGeneration, doc.DurationSeconds); err == nil || !strings.Contains(err.Error(), SourceValidationIdentity) {
		t.Fatalf("expected identity validation error, got %v", err)
	}
	if _, err := ValidateSourceContent(content, doc.VideoID, doc.TranscriptGeneration, doc.DurationSeconds+1); err == nil || !strings.Contains(err.Error(), SourceValidationDuration) {
		t.Fatalf("expected duration validation error, got %v", err)
	}
}
