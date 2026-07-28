package types

import "testing"

func TestDocumentChunkMetadataQuestionRevision(t *testing.T) {
	oldRevision := 1
	currentRevision := 2
	meta := &DocumentChunkMetadata{
		GeneratedQuestionsRevision: oldRevision,
		GeneratedQuestions: []GeneratedQuestion{
			{ID: "old", Question: "old question"},
			{ID: "current", Question: "current question", ContentRevision: &currentRevision},
		},
	}

	questions := meta.GetQuestionStrings()
	if len(questions) != 2 {
		t.Fatalf("all retrieval questions should remain available, got %v", questions)
	}
	if !meta.IsQuestionCurrent(meta.GeneratedQuestions[0], oldRevision) {
		t.Fatal("legacy question should use the metadata-level revision")
	}
	if meta.IsQuestionCurrent(meta.GeneratedQuestions[0], currentRevision) {
		t.Fatal("legacy question should still be identifiable as based on an older revision")
	}
}
