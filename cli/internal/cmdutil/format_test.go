package cmdutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCheckFormatFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantMode FormatMode // FormatHuman | FormatJSON | FormatNDJSON | "" (unset)
		wantErr  bool
	}{
		// Unset → Mode is "" (caller resolves default via ResolveDefault).
		{"default", []string{}, "", false},
		{"explicit human", []string{"--format", "human"}, FormatHuman, false},
		{"json", []string{"--format", "json"}, FormatJSON, false},
		{"ndjson", []string{"--format", "ndjson"}, FormatNDJSON, false},
		{"invalid value", []string{"--format", "yaml"}, "", true},
		// v0.7 BREAKING: "text" is no longer a valid --format value.
		{"text rejected", []string{"--format", "text"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			// --format / --jq are persistent root flags in production; register
			// them on this bare cmd so the test can exercise CheckFormatFlag in
			// isolation.
			cmd.PersistentFlags().String("format", "", "")
			cmd.PersistentFlags().String("jq", "", "")
			cmd.SetArgs(tc.args)
			cmd.RunE = func(c *cobra.Command, _ []string) error {
				opts, err := CheckFormatFlag(c)
				if (err != nil) != tc.wantErr {
					t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
				}
				if err == nil && opts.Mode != tc.wantMode {
					t.Errorf("mode=%q want %q", opts.Mode, tc.wantMode)
				}
				return nil
			}
			_ = cmd.Execute()
		})
	}
}

func TestFormatOptions_NDJSONSplitsList(t *testing.T) {
	var buf bytes.Buffer
	fopts := &FormatOptions{Mode: FormatNDJSON}
	arr := []map[string]string{{"id": "a"}, {"id": "b"}}
	if err := fopts.Emit(&buf, arr, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	want := `{"id":"a"}` + "\n" + `{"id":"b"}` + "\n"
	if buf.String() != want {
		t.Errorf("got %q want %q", buf.String(), want)
	}
}

func TestFormatOptions_JSONEmitsArray(t *testing.T) {
	var buf bytes.Buffer
	fopts := &FormatOptions{Mode: FormatJSON}
	arr := []map[string]string{{"id": "a"}, {"id": "b"}}
	if err := fopts.Emit(&buf, arr, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// v0.7: JSON path wraps in success envelope {ok:true, data:[...]}.
	// Unmarshal the envelope and check data array length.
	var env struct {
		OK   bool                `json:"ok"`
		Data []map[string]string `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("not valid envelope JSON: %v\n%s", err, buf.String())
	}
	if !env.OK {
		t.Errorf("expected ok:true in envelope")
	}
	if len(env.Data) != 2 {
		t.Errorf("got %d items, want 2", len(env.Data))
	}
}

func TestFormatOptions_HumanModeReturnsError(t *testing.T) {
	fopts := &FormatOptions{Mode: FormatHuman}
	err := fopts.Emit(&bytes.Buffer{}, map[string]string{"a": "b"}, nil)
	if err == nil {
		t.Error("expected error for human mode, got nil")
	}
}

// TestResolveDefault_AlwaysJSON verifies v0.7 semantics: default is FormatJSON
// regardless of whether stdout is a TTY (BREAKING change from v0.6).
func TestResolveDefault_AlwaysJSON(t *testing.T) {
	for _, isTTY := range []bool{true, false} {
		o := &FormatOptions{}
		o.ResolveDefault(isTTY)
		if o.Mode != FormatJSON {
			t.Errorf("isTTY=%v: expected default FormatJSON, got %v", isTTY, o.Mode)
		}
		if o.TTY != isTTY {
			t.Errorf("isTTY=%v: TTY field not propagated", isTTY)
		}
	}
}

func TestResolveDefault(t *testing.T) {
	cases := []struct {
		name     string
		mode     FormatMode // pre-set Mode
		jq       string
		isTTY    bool
		wantMode FormatMode
	}{
		// v0.7: empty Mode always resolves to FormatJSON regardless of TTY.
		{"empty isTTY", "", "", true, FormatJSON},
		{"empty no-tty", "", "", false, FormatJSON},
		{"already set keeps value tty", FormatNDJSON, "", true, FormatNDJSON},
		{"already set keeps value no-tty", FormatJSON, "", false, FormatJSON},
		// --jq with unset --format promotes to JSON regardless of TTY so the
		// filter has somewhere to apply (silent human drop would surprise users).
		{"jq forces json on TTY", "", ".[]", true, FormatJSON},
		{"jq with explicit ndjson preserved", FormatNDJSON, ".[]", true, FormatNDJSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &FormatOptions{Mode: tc.mode, JQ: tc.jq}
			o.ResolveDefault(tc.isTTY)
			if o.Mode != tc.wantMode {
				t.Errorf("mode=%q want %q", o.Mode, tc.wantMode)
			}
		})
	}
}

// TestCheckFormatFlag_InvalidExitTwo guards the contract that flag-value
// validation maps to exit 2 (FlagError class), not the unclassified bucket.
func TestCheckFormatFlag_InvalidExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"invalid format value", []string{"--format", "yaml"}},
		// v0.7: "text" is rejected; "human" is the valid human-readable value.
		{"text rejected as invalid", []string{"--format", "text"}},
		{"jq with explicit human mode", []string{"--format", "human", "--jq", ".id"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			// --format / --jq are persistent root flags in production; register
			// them on this bare cmd so the test can exercise CheckFormatFlag in
			// isolation.
			cmd.PersistentFlags().String("format", "", "")
			cmd.PersistentFlags().String("jq", "", "")
			cmd.SetArgs(tc.args)
			var got error
			cmd.RunE = func(c *cobra.Command, _ []string) error {
				_, err := CheckFormatFlag(c)
				got = err
				return err
			}
			_ = cmd.Execute()
			if got == nil {
				t.Fatal("expected error, got nil")
			}
			var fe *FlagError
			if !errors.As(got, &fe) {
				t.Fatalf("error %v is not a *FlagError; would map to exit 1 instead of 2", got)
			}
			if ExitCode(got) != 2 {
				t.Errorf("ExitCode=%d, want 2", ExitCode(got))
			}
		})
	}
}

func TestCheckFormatFlag_InvalidValueRejected(t *testing.T) {
	for _, v := range []string{"text", "yaml", "xml", "table"} {
		v := v
		t.Run(v, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().String("format", "", "")
			_ = cmd.Flags().Set("format", v)
			_, err := CheckFormatFlag(cmd)
			if err == nil {
				t.Fatalf("expected error for --format %q", v)
			}
			if !strings.Contains(err.Error(), "human | json | ndjson") {
				t.Errorf("expected enum hint for --format %q; got %v", v, err)
			}
		})
	}
}

func TestCheckFormatFlag_HumanAccepted(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("format", "", "")
	_ = cmd.Flags().Set("format", "human")
	o, err := CheckFormatFlag(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.Mode != FormatHuman {
		t.Errorf("got %v, want FormatHuman", o.Mode)
	}
}

func TestFromEnv_AppliesWEKNORA_FORMAT(t *testing.T) {
	t.Setenv("WEKNORA_FORMAT", "ndjson")
	o := &FormatOptions{}
	o.FromEnv()
	if o.Mode != FormatNDJSON {
		t.Errorf("expected FormatNDJSON from env; got %v", o.Mode)
	}
}

func TestFromEnv_PrecedenceFlagOverridesEnv(t *testing.T) {
	t.Setenv("WEKNORA_FORMAT", "ndjson")
	o := &FormatOptions{Mode: FormatJSON}
	o.FromEnv()
	if o.Mode != FormatJSON {
		t.Errorf("flag should win over env; got %v", o.Mode)
	}
}

func TestFromEnv_InvalidValueIgnored(t *testing.T) {
	t.Setenv("WEKNORA_FORMAT", "yaml")
	o := &FormatOptions{}
	o.FromEnv()
	if o.Mode != "" {
		t.Errorf("invalid env should be ignored; got %v", o.Mode)
	}
}
