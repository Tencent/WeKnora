package chunk

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/service/subtitle"
)

func TestSplitAssignsDeterministicSourceSentenceIDWhenProviderOmitsOne(t *testing.T) {
	results := NewSplitter().Split(SplitInputs{
		VideoID: "video-1",
		Paragraphs: []subtitle.TranscriptParagraph{{
			Sentences: []subtitle.TranscriptSentence{
				{Text: "第一句", StartMs: 0, EndMs: 1000},
				{Text: "第二句", StartMs: 1000, EndMs: 2000},
			},
		}},
	})
	if len(results) != 2 {
		t.Fatalf("split returned %d results, want 2", len(results))
	}
	if results[0].Metadata.SentenceID != "sentence:000000" || results[1].Metadata.SentenceID != "sentence:000001" {
		t.Fatalf("unexpected fallback sentence IDs: %q, %q", results[0].Metadata.SentenceID, results[1].Metadata.SentenceID)
	}
}

func TestSplitNormalizesUnknownSpeakerID(t *testing.T) {
	results := NewSplitter().Split(SplitInputs{
		VideoID: "video-1",
		Paragraphs: []subtitle.TranscriptParagraph{{
			SpeakerID: " \t ",
			Sentences: []subtitle.TranscriptSentence{{
				SentenceID: "sentence-1", Text: "未知说话人的一句话", StartMs: 100, EndMs: 900,
			}},
		}},
	})
	if len(results) != 1 {
		t.Fatalf("split returned %d results, want 1", len(results))
	}
	if results[0].Metadata.SpeakerID != "0" {
		t.Fatalf("speaker ID = %q, want unknown-speaker fallback", results[0].Metadata.SpeakerID)
	}
}
