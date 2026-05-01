package types

import "testing"

func TestIsRedactedOrEmpty(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"", true},
		{RedactedSecretPlaceholder, true},
		{"real-secret-value", false},
		{"abc****xyz", false}, // old leaky maskString format — not treated as placeholder
		{" ", false},
	}
	for _, c := range cases {
		got := IsRedactedOrEmpty(c.input)
		if got != c.want {
			t.Errorf("IsRedactedOrEmpty(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestPreserveIfRedacted(t *testing.T) {
	const existing = "original-secret"
	cases := []struct {
		incoming string
		want     string
	}{
		// empty → preserve
		{"", existing},
		// placeholder → preserve
		{RedactedSecretPlaceholder, existing},
		// real value → replace
		{"new-secret", "new-secret"},
		// whitespace is treated as a real (if odd) value → replace
		{" ", " "},
	}
	for _, c := range cases {
		got := PreserveIfRedacted(c.incoming, existing)
		if got != c.want {
			t.Errorf("PreserveIfRedacted(%q, %q) = %q, want %q", c.incoming, existing, got, c.want)
		}
	}
}
