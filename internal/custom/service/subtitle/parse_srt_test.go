package subtitle

import (
	"strings"
	"testing"
)

func TestParseSRTPreservesTimelineAndSpeaker(t *testing.T) {
	paragraphs, err := ParseSRT(strings.NewReader("\ufeff1\n00:00:01,200 --> 00:00:02,300\n[说话人 2] 你好\n\n2\n00:00:02.400 --> 00:00:04.000\n继续\n"))
	if err != nil {
		t.Fatalf("ParseSRT() error = %v", err)
	}
	if len(paragraphs) != 2 {
		t.Fatalf("paragraph count = %d, want 2", len(paragraphs))
	}
	if paragraphs[0].SpeakerID != "2" || paragraphs[0].Sentences[0].Text != "你好" {
		t.Fatalf("first cue = %#v", paragraphs[0])
	}
	if paragraphs[1].Sentences[0].StartMs != 2400 || paragraphs[1].Sentences[0].EndMs != 4000 {
		t.Fatalf("second timeline = %#v", paragraphs[1].Sentences[0])
	}
}

func TestParseSRTRejectsInvalidCue(t *testing.T) {
	if _, err := ParseSRT(strings.NewReader("1\n00:00:02,000 --> 00:00:01,000\ninvalid\n")); err == nil {
		t.Fatal("ParseSRT() error = nil")
	}
}
