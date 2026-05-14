package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

func TestRoot_Help(t *testing.T) {
	var out bytes.Buffer
	root := NewRootCmd(cmdutil.New())
	root.SetArgs([]string{"--help"})
	root.SetOut(&out)
	require.NoError(t, root.Execute())
	got := out.String()
	assert.Contains(t, got, "weknora")
	assert.Contains(t, got, "version")
}

func TestVersion_JSON(t *testing.T) {
	var out bytes.Buffer
	root := NewRootCmd(cmdutil.New())
	root.SetArgs([]string{"version", "--json"})
	root.SetOut(&out)
	require.NoError(t, root.Execute())
	got := out.String()
	assert.True(t, strings.HasPrefix(strings.TrimSpace(got), `{`), "expected bare JSON object, got: %q", got)
	assert.Contains(t, got, `"version":"`)
	assert.NotContains(t, got, `"ok":`)
	assert.NotContains(t, got, `"data":`)
}

// Smoke test for cmdutil.ExitCode wiring; full coverage lives in
// cli/internal/cmdutil/exit_test.go.
func TestExecute_ExitCodeSurface(t *testing.T) {
	assert.Equal(t, 0, cmdutil.ExitCode(nil))
	assert.Equal(t, 1, cmdutil.ExitCode(assert.AnError))
}

// TestMapCobraError_PinnedPrefixes guards against silent breakage if cobra
// changes the message format of unknown-command / required-flag / arg-count
// errors. Cobra v1.10 emits these via fmt.Errorf in args.go and command.go;
// if a future bump alters the wording, this test fails loudly so we update
// cobraFlagErrorPrefixes (or migrate to typed sentinels if cobra ever
// provides them).
func TestMapCobraError_PinnedPrefixes(t *testing.T) {
	t.Run("unknown command", func(t *testing.T) {
		root := NewRootCmd(cmdutil.New())
		root.SetArgs([]string{"bogus"})
		root.SetErr(&bytes.Buffer{})
		root.SetOut(&bytes.Buffer{})
		err := root.Execute()
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "unknown command "),
			"cobra unknown-command prefix changed; update cobraFlagErrorPrefixes. got: %q", err.Error())
	})

	t.Run("required flag(s)", func(t *testing.T) {
		// Self-contained probe — the pin must hold even before resource commands
		// register their own required flags. RunE is required: without it cobra
		// treats the command as a parent and skips ValidateRequiredFlags.
		probe := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }}
		probe.Flags().String("host", "", "")
		require.NoError(t, probe.MarkFlagRequired("host"))
		probe.SetErr(&bytes.Buffer{})
		probe.SetOut(&bytes.Buffer{})
		err := probe.Execute()
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "required flag(s)"),
			"cobra required-flag prefix changed; update cobraFlagErrorPrefixes. got: %q", err.Error())
	})

	t.Run("accepts N arg(s) — ExactArgs", func(t *testing.T) {
		probe := &cobra.Command{
			Use:  "probe",
			Args: cobra.ExactArgs(1),
			RunE: func(*cobra.Command, []string) error { return nil },
		}
		probe.SetArgs([]string{}) // no args, but ExactArgs(1) wants 1
		probe.SetErr(&bytes.Buffer{})
		probe.SetOut(&bytes.Buffer{})
		err := probe.Execute()
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "accepts "),
			"cobra ExactArgs prefix changed; update cobraFlagErrorPrefixes. got: %q", err.Error())
	})
}

func TestMapCobraError(t *testing.T) {
	t.Run("nil passes through", func(t *testing.T) {
		assert.Nil(t, MapCobraError(nil))
	})
	t.Run("non-matching error passes through", func(t *testing.T) {
		err := MapCobraError(assert.AnError)
		assert.Equal(t, assert.AnError, err)
	})
	t.Run("unknown command wraps as FlagError", func(t *testing.T) {
		err := MapCobraError(errors.New(`unknown command "bogus" for "weknora"`))
		var fe *cmdutil.FlagError
		assert.True(t, errors.As(err, &fe))
	})
	t.Run("required flag wraps as FlagError", func(t *testing.T) {
		err := MapCobraError(errors.New(`required flag(s) "host" not set`))
		var fe *cmdutil.FlagError
		assert.True(t, errors.As(err, &fe))
	})
}

// TestRoot_ContextFlagPropagation guards the cobra → Factory wiring of the
// global --context flag. Without this, a future refactor that disconnects
// PersistentPreRun from f.ContextOverride would only fail e2e — the
// per-package TestFactory_ContextOverride only proves the Factory side.
func TestRoot_ContextFlagPropagation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no flag", []string{"version"}, ""},
		{"global before subcmd", []string{"--context", "staging", "version"}, "staging"},
		{"--context=value form", []string{"--context=prod", "version"}, "prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := cmdutil.New()
			root := NewRootCmd(f)
			root.SetArgs(tc.args)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			require.NoError(t, root.Execute())
			assert.Equal(t, tc.want, f.ContextOverride)
		})
	}
}

func TestArgsRequestJSON(t *testing.T) {
	// v0.4 R-1: --json is now StringSlice (gh-style field filter). Any
	// `--json` token in args means the user wants JSON output, regardless
	// of value (boolean string parsing dropped).
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, false},
		{"--json bare", []string{"version", "--json"}, true},
		{"--json=id,name", []string{"kb", "list", "--json=id,name"}, true},
		{"--json=anything", []string{"version", "--json=foo"}, true},
		{"unrelated", []string{"bogus", "--kb", "x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, argsRequestJSON(tc.args))
		})
	}
}

func TestWantsJSONOutput(t *testing.T) {
	// Build a minimal *cobra.Command with the json flag directly so we test
	// the helper without going through cobra's parse pipeline. WantsJSONOutput
	// reads cmd.Flags() which on a fresh command equals LocalFlags().
	c := &cobra.Command{Use: "x"}
	c.Flags().Bool("json", false, "")
	assert.False(t, WantsJSONOutput(c), "default: --json unset")

	require.NoError(t, c.Flags().Set("json", "true"))
	assert.True(t, WantsJSONOutput(c), "--json=true honored")
}
