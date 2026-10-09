package types

import "testing"

func TestIsGeneratedQuestionSource(t *testing.T) {
	const chunkID = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	cases := []struct {
		name       string
		chunkID    string
		sourceID   string
		sourceType SourceType
		want       bool
	}{
		{name: "body row", chunkID: chunkID, sourceID: chunkID, sourceType: ChunkSourceType},
		{
			name: "question row", chunkID: chunkID, sourceID: chunkID + "-q123",
			sourceType: ChunkSourceType, want: true,
		},
		{
			name: "hashed question row", chunkID: chunkID, sourceID: chunkID + "-q0123456789abcdef01234567",
			sourceType: ChunkSourceType, want: true,
		},
		{
			name: "image row with a suffix", chunkID: chunkID, sourceID: chunkID + "-image",
			sourceType: ImageSourceType,
		},
		{name: "empty chunk", sourceID: "-q1", sourceType: ChunkSourceType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsGeneratedQuestionSource(tc.chunkID, tc.sourceID, tc.sourceType); got != tc.want {
				t.Fatalf("IsGeneratedQuestionSource() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestQuestionGenerationDisabledLeavesFAQAlone(t *testing.T) {
	faq := &KnowledgeBase{Type: KnowledgeBaseTypeFAQ}
	if QuestionGenerationDisabled(faq) {
		t.Fatal("FAQ similar-question rows must not be treated as generated questions")
	}
	off := &KnowledgeBase{Type: KnowledgeBaseTypeDocument}
	if !QuestionGenerationDisabled(off) {
		t.Fatal("a document knowledge base with no question config is off")
	}
	on := &KnowledgeBase{
		Type:                     KnowledgeBaseTypeDocument,
		QuestionGenerationConfig: &QuestionGenerationConfig{Enabled: true},
	}
	if QuestionGenerationDisabled(on) || !QuestionGenerationActive(on) {
		t.Fatal("an enabled document knowledge base should keep generated questions")
	}
}
