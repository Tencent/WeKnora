package cmdutil_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// newModelUpdateLikeCmd mirrors the flag registration of cli/cmd/model/update.go,
// including the persistent -y that the root command contributes.
func newModelUpdateLikeCmd() *cobra.Command {
	c := &cobra.Command{Use: "update", Args: cobra.ExactArgs(1)}
	var isDefault, yes bool
	var format string
	c.Flags().BoolVar(&isDefault, "default", false, "Mark this the default model for its type")
	c.Flags().StringVar(&format, "format", "", "Output format")
	c.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation")
	return c
}

// runRetryArgv replays everything after `weknora model update` into a fresh
// copy of the same command, the way an agent or a human would run the argv it
// was just handed. It returns the resulting --default value and the parse error.
func runRetryArgv(t *testing.T, argv []string) (bool, error) {
	t.Helper()
	c := newModelUpdateLikeCmd()
	c.SilenceUsage = true
	c.SilenceErrors = true
	c.SetArgs(argv[3:]) // drop the `weknora model update` prefix
	c.RunE = func(*cobra.Command, []string) error { return nil }
	err := c.Execute()
	got, _ := c.Flags().GetBool("default")
	return got, err
}

// `retry_argv` is documented as "directly executable", and on exit-10 agents are
// told to surface it verbatim for the human to approve. pflag gives every bool
// flag a NoOptDefVal, so the two-token `--name value` form left that value as a
// stray positional and the command refused the argv outright. For `--name=false`
// it was worse: the flag fell back to its NoOptDefVal "true", so the retry argv
// set the very flag the caller had asked to clear.
func TestBuildRetryArgv_BoolFlagsRoundTrip(t *testing.T) {
	tests := []struct {
		name        string
		typed       []string
		wantDefault bool
	}{
		{
			name:        "bare bool",
			typed:       []string{"--default", "--format", "json"},
			wantDefault: true,
		},
		{
			name:        "explicit false must stay false",
			typed:       []string{"--default=false", "--format", "json"},
			wantDefault: false,
		},
		{
			name:        "explicit true",
			typed:       []string{"--default=true", "--format", "json"},
			wantDefault: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := newModelUpdateLikeCmd()
			require.NoError(t, src.Flags().Parse(tt.typed))

			argv := cmdutil.BuildRetryArgv(src,
				[]string{"weknora", "model", "update", "mdl_abc"},
				"default", "format")

			gotDefault, err := runRetryArgv(t, argv)
			require.NoError(t, err, "retry_argv must be executable as emitted: %v", argv)
			assert.Equal(t, tt.wantDefault, gotDefault,
				"replayed argv changed the flag value: %v", argv)
		})
	}
}

// The single-token form is load-bearing, not cosmetic: pflag ignores the token
// after `--name` for a bool flag. Assert the argv elements themselves so a
// future refactor cannot quietly reintroduce the two-token form while the
// round-trip test still passes for the bare-flag case. (testify's Contains on
// a slice compares whole elements, not substrings, so "--default=false" is not
// matched by the "--default" element check below.)
func TestBuildRetryArgv_BoolFlagUsesSingleToken(t *testing.T) {
	src := newModelUpdateLikeCmd()
	require.NoError(t, src.Flags().Parse([]string{"--default=false", "--format", "json"}))

	got := cmdutil.BuildRetryArgv(src,
		[]string{"weknora", "model", "update", "mdl_abc"},
		"default", "format")

	assert.Contains(t, got, "--default=false")
	assert.NotContains(t, got, "--default")
	assert.NotContains(t, got, "false")
}

// String and slice flags keep their existing two-token form.
func TestBuildRetryArgv_NonBoolFlagsUnchanged(t *testing.T) {
	src := newModelUpdateLikeCmd()
	var name string
	src.Flags().StringVar(&name, "name", "", "")
	require.NoError(t, src.Flags().Parse([]string{"--name", "Renamed", "--format", "json"}))

	got := cmdutil.BuildRetryArgv(src,
		[]string{"weknora", "model", "update", "mdl_abc"},
		"default", "format", "name")

	assert.Contains(t, got, "--name")
	assert.NotContains(t, got, "--name=Renamed")
	assert.Contains(t, got, "Renamed")
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
