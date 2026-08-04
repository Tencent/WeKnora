package types

import (
	"reflect"
	"strings"
	"testing"
)

func TestChunkFeedbackUserIDMatchesSessionPrincipalWidth(t *testing.T) {
	feedbackField, ok := reflect.TypeOf(ChunkFeedback{}).FieldByName("UserID")
	if !ok {
		t.Fatal("ChunkFeedback.UserID field not found")
	}
	sessionField, ok := reflect.TypeOf(Session{}).FieldByName("UserID")
	if !ok {
		t.Fatal("Session.UserID field not found")
	}

	if got := feedbackField.Tag.Get("gorm"); !strings.Contains(got, "type:varchar(512)") {
		t.Fatalf("ChunkFeedback.UserID gorm tag = %q, want varchar(512)", got)
	}
	if got := sessionField.Tag.Get("gorm"); !strings.Contains(got, "type:varchar(512)") {
		t.Fatalf("Session.UserID gorm tag = %q, want varchar(512)", got)
	}
}
