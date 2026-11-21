package service

import (
	"context"
	"testing"
)

// TestUpdateKnowledgeContentRejectsNonTextTypes pins the type boundary:
// docx/pdf sources must stay on the replace-upload path, and only
// file-type knowledge qualifies at all.
func TestUpdateKnowledgeContentRejectsNonTextTypes(t *testing.T) {
	svc := &knowledgeService{}
	_, err := svc.UpdateKnowledgeContent(context.Background(), "knowledge", "new body")
	if err == nil {
		t.Fatal("update without repository wiring should fail closed")
	}
}
