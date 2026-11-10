package cmd

import (
	"bytes"
	"encoding/json"
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
	root.SetArgs([]string{"version", "--format", "json"})
	root.SetOut(&out)
	require.NoError(t, root.Execute())
	got := out.String()
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "expected valid JSON envelope, got: %q", got)
	assert.True(t, env.OK, "envelope.ok must be true")
	assert.NotNil(t, env.Data, "envelope.data must be present")
	assert.Contains(t, got, `"version":`)
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
		// With installUnknownSubcommandGuard in place, unknown root-level
		// subcommands now return a typed *cmdutil.Error (CodeInputUnknownSubcommand)
		// rather than cobra's legacy "unknown command" text. The cobraFlagErrorPrefixes
		// fallback remains for any path that bypasses the guard.
		root := NewRootCmd(cmdutil.New())
		root.SetArgs([]string{"bogus"})
		root.SetErr(&bytes.Buffer{})
		root.SetOut(&bytes.Buffer{})
		err := root.Execute()
		require.Error(t, err)
		typed := cmdutil.AsError(err)
		require.NotNil(t, typed, "expected typed *cmdutil.Error; got %T: %v", err, err)
		assert.Equal(t, cmdutil.CodeInputUnknownSubcommand, typed.Code)
	})

	t.Run("required flag(s)", func(t *testing.T) {
		// Self-contained probe - the pin must hold even before resource commands
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

	t.Run("accepts N arg(s) - ExactArgs", func(t *testing.T) {
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
	t.Run("pflag invalid argument wraps as FlagError", func(t *testing.T) {
		// pflag emits: `invalid argument "foo" for "--limit" flag`
		err := MapCobraError(errors.New(`invalid argument "foo" for "--limit" flag: strconv.ParseInt: parsing "foo": invalid syntax`))
		var fe *cmdutil.FlagError
		assert.True(t, errors.As(err, &fe), "pflag-shaped invalid argument should become FlagError")
	})
	t.Run("domain invalid argument does not wrap", func(t *testing.T) {
		// Domain code writing fmt.Errorf("invalid argument: ...") must NOT become FlagError.
		err := MapCobraError(errors.New("invalid argument: kb id cannot be empty"))
		var fe *cmdutil.FlagError
		assert.False(t, errors.As(err, &fe), "domain-shaped invalid argument must not become FlagError")
	})
}

// TestRoot_ProfileFlagPropagation guards the cobra → Factory wiring of the
// global --profile flag. Without this, a future refactor that disconnects
// PersistentPreRun from f.ProfileOverride would only fail e2e - the
// per-package TestFactory_ProfileOverride only proves the Factory side.
func TestRoot_ProfileFlagPropagation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no flag", []string{"version"}, ""},
		{"global before subcmd", []string{"--profile", "staging", "version"}, "staging"},
		{"--profile=value form", []string{"--profile=prod", "version"}, "prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := cmdutil.New()
			root := NewRootCmd(f)
			root.SetArgs(tc.args)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			require.NoError(t, root.Execute())
			assert.Equal(t, tc.want, f.ProfileOverride)
		})
	}
}

// resolveFormatEarly must default to the JSON envelope when no --format/env is
// given, so cobra-side errors (unknown flag, arg-count) — which fire before
// PersistentPreRunE runs ResolveDefault — emit a machine-readable envelope on
// stderr, not bare prose. Regression for the success-path-defaults-to-json /
// error-path-defaults-to-prose asymmetry.
func TestResolveFormatEarly_DefaultsToEnvelope(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool // true ⇒ expect JSON envelope
	}{
		{"no format flag (default)", []string{"kb", "view"}, true},
		{"explicit --format json", []string{"kb", "view", "--format", "json"}, true},
		{"explicit --format text", []string{"kb", "view", "--format", "text"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolveFormatEarly(tc.args)
			err := MapCobraError(errors.New("accepts 1 arg(s), received 0"))
			var buf bytes.Buffer
			cmdutil.PrintError(&buf, err)
			isEnvelope := strings.HasPrefix(strings.TrimSpace(buf.String()), "{")
			assert.Equal(t, tc.want, isEnvelope,
				"args=%v: got %q", tc.args, buf.String())
		})
	}
}
