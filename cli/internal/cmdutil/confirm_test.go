package cmdutil_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

// The confirmation message verb must match the actual operation: an `edit`
// must not be described as `delete`. Regression for the hardcoded-"delete"
// confirmation message that mislabeled kb/agent updates.
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
		err := cmdutil.ConfirmDestructive(&testutil.ConfirmPrompter{}, false, true, tc.verb, tc.what, tc.id, tc.what+"."+tc.verb, nil)
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
	err := cmdutil.ConfirmDestructiveBatch(&testutil.ConfirmPrompter{}, false, true, "delete", "document", 3, "doc.delete", nil)
	if err == nil {
		t.Fatal("expected confirmation_required error")
	}
	if !strings.Contains(err.Error(), "delete 3 document(s) requires") {
		t.Errorf("unexpected batch message: %q", err.Error())
	}
}

func TestBuildRetryArgv_ScalarsAndSlices(t *testing.T) {
	cmd := &cobra.Command{Use: "update"}
	var name string
	var addKBs []string
	var format string
	cmd.Flags().StringVar(&name, "name", "", "")
	cmd.Flags().StringSliceVar(&addKBs, "add-kb", nil, "")
	cmd.Flags().StringVar(&format, "format", "", "")
	if err := cmd.Flags().Parse([]string{
		"--add-kb", "kb_new",
		"--add-kb", "kb_old",
		"--name", "Renamed",
		"--format", "json",
	}); err != nil {
		t.Fatal(err)
	}

	got := cmdutil.BuildRetryArgv(cmd, []string{"weknora", "agent", "update", "ag_abc"},
		"name", "add-kb", "format")
	// Visit order is lexicographical among changed flags.
	want := []string{
		"weknora", "agent", "update", "ag_abc",
		"--add-kb", "kb_new",
		"--add-kb", "kb_old",
		"--format", "json",
		"--name", "Renamed",
		"-y",
	}
	if len(got) != len(want) {
		t.Fatalf("len got=%d want=%d\ngot=%v\nwant=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx %d: got %q want %q\nfull got=%v", i, got[i], want[i], got)
		}
	}
}

func TestBuildRetryArgv_SkipsFlagsNotInAllow(t *testing.T) {
	cmd := &cobra.Command{Use: "update"}
	var name, secret string
	cmd.Flags().StringVar(&name, "name", "", "")
	cmd.Flags().StringVar(&secret, "api-key-stdin", "", "")
	if err := cmd.Flags().Parse([]string{"--name", "x", "--api-key-stdin", "-"}); err != nil {
		t.Fatal(err)
	}

	got := cmdutil.BuildRetryArgv(cmd, []string{"weknora", "model", "update", "m1"}, "name", "format")
	want := []string{"weknora", "model", "update", "m1", "--name", "x", "-y"}
	if len(got) != len(want) {
		t.Fatalf("len got=%d want=%d\ngot=%v\nwant=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx %d: got %q want %q\nfull got=%v", i, got[i], want[i], got)
		}
	}
}
