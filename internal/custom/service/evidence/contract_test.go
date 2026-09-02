package evidence

import "testing"

func TestBuildSentenceIsDeterministicAndGenerationScoped(t *testing.T) {
	input := Input{
		VideoID: "video-1", TranscriptGeneration: "generation-1", Ordinal: 3,
		SourceSentenceID: "provider-sentence-9", Text: "真实原文", SpeakerID: "speaker-2",
		StartMs: 1200, EndMs: 3400,
	}
	one, err := BuildSentence(input)
	if err != nil {
		t.Fatalf("BuildSentence returned error: %v", err)
	}
	two, err := BuildSentence(input)
	if err != nil {
		t.Fatalf("BuildSentence returned error: %v", err)
	}
	if one.ID == "" || one.ID != two.ID {
		t.Fatalf("sentence ID is not deterministic: %q / %q", one.ID, two.ID)
	}
	if one.ID != "evs:v1:"+one.Digest {
		t.Fatalf("sentence ID does not expose the versioned digest contract: %q", one.ID)
	}

	changedGeneration := input
	changedGeneration.TranscriptGeneration = "generation-2"
	other, err := BuildSentence(changedGeneration)
	if err != nil {
		t.Fatalf("BuildSentence returned error: %v", err)
	}
	if one.ID == other.ID {
		t.Fatal("a new transcript generation reused the old evidence sentence ID")
	}
}

func TestValidateManifestRequiresOneToOneCurrentGenerationMapping(t *testing.T) {
	inputs := []Input{
		{VideoID: "video-1", TranscriptGeneration: "generation-1", Ordinal: 0, SourceSentenceID: "s1", Text: "第一句", SpeakerID: "a", StartMs: 0, EndMs: 1000},
		{VideoID: "video-1", TranscriptGeneration: "generation-1", Ordinal: 1, SourceSentenceID: "s2", Text: "第二句", SpeakerID: "b", StartMs: 1000, EndMs: 2200},
	}
	sentences := make([]Sentence, 0, len(inputs))
	for _, input := range inputs {
		sentence, err := BuildSentence(input)
		if err != nil {
			t.Fatalf("BuildSentence returned error: %v", err)
		}
		sentences = append(sentences, sentence)
	}
	if err := ValidateManifest(sentences, "video-1", "generation-1"); err != nil {
		t.Fatalf("ValidateManifest returned error: %v", err)
	}

	tests := []struct {
		name   string
		mutate func([]Sentence) []Sentence
	}{
		{"generation mismatch", func(items []Sentence) []Sentence {
			items[1].TranscriptGeneration = "generation-2"
			return items
		}},
		{"duplicate evidence ID", func(items []Sentence) []Sentence {
			items[1].ID = items[0].ID
			return items
		}},
		{"changed immutable timing", func(items []Sentence) []Sentence {
			items[1].StartMs = 900
			return items
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items := append([]Sentence(nil), sentences...)
			if err := ValidateManifest(test.mutate(items), "video-1", "generation-1"); err == nil {
				t.Fatal("ValidateManifest accepted an invalid immutable mapping")
			}
		})
	}
}

func TestBuildSentenceRejectsInvalidEvidence(t *testing.T) {
	base := Input{VideoID: "video-1", TranscriptGeneration: "generation-1", Ordinal: 0, SourceSentenceID: "s1", Text: "原文", StartMs: 100, EndMs: 200}
	for _, mutate := range []func(*Input){
		func(input *Input) { input.VideoID = "" },
		func(input *Input) { input.TranscriptGeneration = "" },
		func(input *Input) { input.Text = " " },
		func(input *Input) { input.StartMs = -1 },
		func(input *Input) { input.EndMs = input.StartMs },
		func(input *Input) { input.Ordinal = -1 },
	} {
		input := base
		mutate(&input)
		if _, err := BuildSentence(input); err == nil {
			t.Fatalf("BuildSentence accepted invalid input: %+v", input)
		}
	}
}
