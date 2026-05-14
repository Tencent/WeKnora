package cmdutil_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

var testFields = []string{"id", "name", "kb_id", "updated_at"}

func newTestCmd(t *testing.T, captured **cmdutil.JSONOptions) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{
		Use:           "test",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			opts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			*captured = opts
			return nil
		},
	}
	cmdutil.AddJSONFlags(cmd, testFields)
	return cmd
}

func TestAddJSONFlags_BareYieldsEnabledOptsWithNoFields(t *testing.T) {
	// `--json` bare → Enabled() with empty Fields → caller emits full payload.
	var captured *cmdutil.JSONOptions
	cmd := newTestCmd(t, &captured)
	cmd.SetArgs([]string{"--json"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if !captured.Enabled() {
		t.Fatalf("expected Enabled, got nil")
	}
	if len(captured.Fields) != 0 {
		t.Errorf("bare --json must produce empty Fields, got %v", captured.Fields)
	}
}

func TestAddJSONFlags_FieldsFlagParsing(t *testing.T) {
	// NoOptDefVal sentinel means the `=` form is required for value passing.
	// Space form `--json id,name` parses as bare + positional, which is a
	// documented divergence from gh CLI: weknora keeps bare `--json` as a
	// shortcut for "full payload".
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"--json=id,name"}, []string{"id", "name"}},
		{[]string{"--json=id,name,kb_id"}, []string{"id", "name", "kb_id"}},
		{[]string{"--json=id"}, []string{"id"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var captured *cmdutil.JSONOptions
			cmd := newTestCmd(t, &captured)
			cmd.SetArgs(tc.args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() err = %v", err)
			}
			if captured == nil {
				t.Fatalf("expected JSONOptions captured, got nil")
			}
			if !equalSlice(captured.Fields, tc.want) {
				t.Errorf("Fields = %v, want %v", captured.Fields, tc.want)
			}
			if captured.JQ != "" {
				t.Errorf("JQ should be empty, got %q", captured.JQ)
			}
		})
	}
}

func TestAddJSONFlags_NoFlagsSet(t *testing.T) {
	var captured *cmdutil.JSONOptions
	cmd := newTestCmd(t, &captured)
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if captured != nil {
		t.Errorf("expected nil JSONOptions for human mode, got %+v", captured)
	}
}

func TestAddJSONFlags_JQWithoutJSON(t *testing.T) {
	var captured *cmdutil.JSONOptions
	cmd := newTestCmd(t, &captured)
	cmd.SetArgs([]string{"--jq", ".data.items[]"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected mutual-dep error, got nil")
	}
	want := "cannot use `--jq` without specifying `--json`"
	if err.Error() != want {
		t.Errorf("wrong message.\nwant: %q\ngot:  %q", want, err.Error())
	}
	// Must NOT be a FlagError - gh emits this as a plain error so exit
	// code stays 1, not 2.
	var fe *cmdutil.FlagError
	if errors.As(err, &fe) {
		t.Errorf("error should not be FlagError; gh treats this as exit 1")
	}
}

func TestAddJSONFlags_JQWithJSON(t *testing.T) {
	var captured *cmdutil.JSONOptions
	cmd := newTestCmd(t, &captured)
	cmd.SetArgs([]string{"--json=id,name", "--jq", ".data.items[0].id"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if captured == nil {
		t.Fatalf("expected JSONOptions, got nil")
	}
	if !equalSlice(captured.Fields, []string{"id", "name"}) {
		t.Errorf("Fields = %v, want [id name]", captured.Fields)
	}
	if captured.JQ != ".data.items[0].id" {
		t.Errorf("JQ = %q, want %q", captured.JQ, ".data.items[0].id")
	}
}

func TestAddJSONFlags_JQShortFlag(t *testing.T) {
	// gh uses -q as shorthand for --jq.
	var captured *cmdutil.JSONOptions
	cmd := newTestCmd(t, &captured)
	cmd.SetArgs([]string{"--json=id", "-q", ".data"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if captured == nil || captured.JQ != ".data" {
		t.Errorf("expected JQ=.data, got %+v", captured)
	}
}

func TestAddJSONFlags_HelpListsFields(t *testing.T) {
	cmd := &cobra.Command{Use: "test", Short: "A test command"}
	cmdutil.AddJSONFlags(cmd, []string{"name", "id", "updated_at"})
	if !strings.Contains(cmd.Long, "JSON fields available") {
		t.Errorf("expected help to include 'JSON fields available'; got Long=%q", cmd.Long)
	}
	// Fields should appear alphabetically sorted.
	idAt := strings.Index(cmd.Long, "id")
	nameAt := strings.Index(cmd.Long, "name")
	updatedAt := strings.Index(cmd.Long, "updated_at")
	if !(idAt < nameAt && nameAt < updatedAt) {
		t.Errorf("fields not sorted in Long: idAt=%d nameAt=%d updatedAt=%d", idAt, nameAt, updatedAt)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
