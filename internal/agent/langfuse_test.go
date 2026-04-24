package agent

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestTruncateForLangfuse verifies rune-aware truncation.
// We specifically check CJK and ASCII inputs to ensure the "…" marker is
// appended exactly once without splitting multi-byte characters.
func TestTruncateForLangfuse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"empty", "", 10, ""},
		{"zero-budget", "abcdef", 0, "abcdef"},
		{"under-budget", "hello", 10, "hello"},
		{"at-budget", "hello", 5, "hello"},
		{"over-budget-ascii", "hello world", 5, "hello…"},
		{"over-budget-cjk", "你好世界测试", 3, "你好世…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateForLangfuse(tc.in, tc.n)
			if got != tc.want {
				t.Fatalf("truncateForLangfuse(%q, %d) = %q; want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

// TestArgKeysSorted ensures argKeys returns a deterministic (sorted) list so
// Langfuse span inputs diff cleanly across runs.
func TestArgKeysSorted(t *testing.T) {
	args := map[string]any{"zeta": 1, "alpha": 2, "mu": 3}
	got := argKeys(args)
	want := []string{"alpha", "mu", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argKeys order = %v; want %v", got, want)
	}
}

// TestDataKeysSorted mirrors TestArgKeysSorted for tool result Data maps.
func TestDataKeysSorted(t *testing.T) {
	data := map[string]interface{}{"z": nil, "a": nil, "m": nil}
	got := dataKeys(data)
	want := []string{"a", "m", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dataKeys order = %v; want %v", got, want)
	}
}

// TestFinishToolSpanNilSafe is a regression check: callers always invoke
// finishToolSpan unconditionally (Langfuse is may-be-disabled) so a nil span
// must not panic.
func TestFinishToolSpanNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("finishToolSpan panicked on nil span: %v", r)
		}
	}()
	finishToolSpan(nil, types.ToolCall{}, errors.New("boom"), 123)
	finishToolSpan(nil, types.ToolCall{Result: &types.ToolResult{Success: true}}, nil, 0)
}

// TestIterOutcomeString locks in the string labels that surface in Langfuse
// span outputs so dashboards built on those values don't silently break.
func TestIterOutcomeString(t *testing.T) {
	cases := []struct {
		o    iterOutcome
		want string
	}{
		{iterOutcomeNext, "next"},
		{iterOutcomeContinue, "continue"},
		{iterOutcomeBreak, "break"},
		{iterOutcome(42), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.o.String(); got != tc.want {
			t.Errorf("iterOutcome(%d).String() = %q; want %q", tc.o, got, tc.want)
		}
	}
}

// TestTruncateRunesEngine verifies the engine-local rune truncator (separate
// budget from act.go's helper) behaves identically for the common cases.
func TestTruncateRunesEngine(t *testing.T) {
	if got := truncateRunes("abcde", 3); got != "abc…" {
		t.Fatalf("truncateRunes short = %q; want %q", got, "abc…")
	}
	if got := truncateRunes("abc", 10); got != "abc" {
		t.Fatalf("truncateRunes under-budget = %q; want abc", got)
	}
	if got := truncateRunes("", 10); got != "" {
		t.Fatalf("truncateRunes empty = %q; want empty", got)
	}
}
