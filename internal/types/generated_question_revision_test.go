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

	questions := meta.GetCurrentQuestionStrings(currentRevision)
	if len(questions) != 1 || questions[0] != "current question" {
		t.Fatalf("current questions = %v", questions)
	}
	if !meta.IsQuestionCurrent(meta.GeneratedQuestions[0], oldRevision) {
		t.Fatal("legacy question should use the metadata-level revision")
	}
}
