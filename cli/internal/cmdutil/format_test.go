package cmdutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

func TestCheckFormatFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantMode string // "text" | "json" | "ndjson" | "" (unset)
		wantErr  bool
	}{
		// Unset → Mode is "" (caller resolves TTY default via ResolveDefault).
		{"default", []string{}, "", false},
		{"explicit text", []string{"--format", "text"}, "text", false},
		{"json", []string{"--format", "json"}, "json", false},
		{"ndjson", []string{"--format", "ndjson"}, "ndjson", false},
		{"invalid value", []string{"--format", "yaml"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			AddFormatFlag(cmd)
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
	fopts := &FormatOptions{Mode: "ndjson"}
	arr := []map[string]string{{"id": "a"}, {"id": "b"}}
	if err := fopts.Emit(&buf, arr); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	want := `{"id":"a"}` + "\n" + `{"id":"b"}` + "\n"
	if buf.String() != want {
		t.Errorf("got %q want %q", buf.String(), want)
	}
}

func TestFormatOptions_JSONEmitsArray(t *testing.T) {
	var buf bytes.Buffer
	fopts := &FormatOptions{Mode: "json"}
	arr := []map[string]string{{"id": "a"}, {"id": "b"}}
	if err := fopts.Emit(&buf, arr); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// Expect a single JSON array, e.g. [{"id":"a"},{"id":"b"}]
	var got []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 2 {
		t.Errorf("got %d items, want 2", len(got))
	}
}

func TestFormatOptions_TextModeReturnsError(t *testing.T) {
	fopts := &FormatOptions{Mode: "text"}
	err := fopts.Emit(&bytes.Buffer{}, map[string]string{"a": "b"})
	if err == nil {
		t.Error("expected error for text mode, got nil")
	}
}

func TestResolveDefault(t *testing.T) {
	cases := []struct {
		name     string
		mode     string // pre-set Mode
		jq       string
		isTTY    bool
		wantMode string
	}{
		{"empty isTTY", "", "", true, "text"},
		{"empty no-tty", "", "", false, "json"},
		{"already set keeps value tty", "ndjson", "", true, "ndjson"},
		{"already set keeps value no-tty", "json", "", false, "json"},
		// --jq with unset --format promotes to JSON regardless of TTY so the
		// filter has somewhere to apply (silent text drop would surprise users).
		{"jq forces json on TTY", "", ".[]", true, "json"},
		{"jq with explicit ndjson preserved", "ndjson", ".[]", true, "ndjson"},
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
		{"jq with explicit text mode", []string{"--format", "text", "--jq", ".id"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			AddFormatFlag(cmd)
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
