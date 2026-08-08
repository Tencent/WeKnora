package cmdutil_test

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
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

func TestBuildRetryArgv_PreservesScalarBoolAndRepeatableFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "update"}
	cmd.Flags().String("description", "", "")
	cmd.Flags().Bool("default", true, "")
	cmd.Flags().StringSlice("add-kb", nil, "")
	cmd.Flags().StringArray("param", nil, "")

	require.NoError(t, cmd.Flags().Set("description", "value with spaces;$(noop)"))
	require.NoError(t, cmd.Flags().Set("default", "false"))
	require.NoError(t, cmd.Flags().Set("add-kb", "kb_a"))
	require.NoError(t, cmd.Flags().Set("add-kb", "kb_b"))
	require.NoError(t, cmd.Flags().Set("param", "temperature=0.2"))
	require.NoError(t, cmd.Flags().Set("param", "label=a,b"))

	got := cmdutil.BuildRetryArgv(cmd, []string{"weknora", "probe", "update"},
		"description", "default", "add-kb", "param")
	require.Equal(t, []string{
		"weknora", "probe", "update",
		"--add-kb", "kb_a",
		"--add-kb", "kb_b",
		"--default=false",
		"--description", "value with spaces;$(noop)",
		"--param", "temperature=0.2",
		"--param", "label=a,b",
		"-y",
	}, got)
}

func TestBuildRetryArgv_StringSliceValueContainingCommaRoundTrips(t *testing.T) {
	cmd := &cobra.Command{Use: "update"}
	cmd.Flags().StringSlice("add-kb", nil, "")
	require.NoError(t, cmd.Flags().Set("add-kb", "\"kb,blue\""))

	got := cmdutil.BuildRetryArgv(cmd, []string{"weknora", "agent", "update", "ag_abc"}, "add-kb")
	require.Equal(t, []string{
		"weknora", "agent", "update", "ag_abc",
		"--add-kb", "\"kb,blue\"",
		"-y",
	}, got)

	replayed := &cobra.Command{Use: "update"}
	var values []string
	replayed.Flags().StringSliceVar(&values, "add-kb", nil, "")
	replayed.Flags().BoolP("yes", "y", false, "")
	require.NoError(t, replayed.ParseFlags(got[4:]))
	require.Equal(t, []string{"kb,blue"}, values)
}

func TestBuildRetryArgv_DoesNotMutateHead(t *testing.T) {
	cmd := &cobra.Command{Use: "update"}
	cmd.Flags().String("name", "", "")
	require.NoError(t, cmd.Flags().Set("name", "new"))
	head := []string{"weknora", "agent", "update", "ag_abc"}
	wantHead := append([]string(nil), head...)

	_ = cmdutil.BuildRetryArgv(cmd, head, "name")
	require.Equal(t, wantHead, head)
}
