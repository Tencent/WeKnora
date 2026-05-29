package cmdutil_test

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

// The confirmation message verb must match the actual operation: an `edit`
// must not be described as `delete`. Regression for the hardcoded-"delete"
// confirmation message that mislabeled kb/agent edits.
func TestConfirmDestructive_VerbMatchesOperation(t *testing.T) {
	iostreams.SetForTest(t) // non-TTY buffers ⇒ the jsonOut/non-TTY branch

	cases := []struct {
		verb, what, id  string
		wantPrefix      string
		wantNotContains string
	}{
		{"edit", "knowledge base", "kb_x", "edit knowledge base kb_x requires", "delete"},
		{"delete", "knowledge base", "kb_x", "delete knowledge base kb_x requires", ""},
		{"remove", "current profile", "prod", "remove current profile prod requires", "delete"},
	}
	for _, tc := range cases {
		err := cmdutil.ConfirmDestructive(&testutil.ConfirmPrompter{}, false, true, tc.verb, tc.what, tc.id, tc.what+"."+tc.verb, "")
		if err == nil {
			t.Fatalf("verb %q: expected confirmation_required error", tc.verb)
		}
		msg := err.Error()
		if !strings.Contains(msg, tc.wantPrefix) {
			t.Errorf("verb %q: message %q does not contain %q", tc.verb, msg, tc.wantPrefix)
		}
		if tc.wantNotContains != "" && strings.Contains(msg, tc.wantNotContains) {
			t.Errorf("verb %q: message %q must not contain %q", tc.verb, msg, tc.wantNotContains)
		}
		if typed := cmdutil.AsError(err); typed == nil || typed.Code != cmdutil.CodeInputConfirmationRequired {
			t.Errorf("verb %q: expected CodeInputConfirmationRequired, got %v", tc.verb, err)
		}
	}
}

// The batch flavor must likewise honor the verb.
func TestConfirmDestructiveBatch_VerbMatchesOperation(t *testing.T) {
	iostreams.SetForTest(t)
	err := cmdutil.ConfirmDestructiveBatch(&testutil.ConfirmPrompter{}, false, true, "delete", "document", 3, "doc.delete", "")
	if err == nil {
		t.Fatal("expected confirmation_required error")
	}
	if !strings.Contains(err.Error(), "delete 3 document(s) requires") {
		t.Errorf("unexpected batch message: %q", err.Error())
	}
}
