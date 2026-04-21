package types

import "testing"

func TestIsRedactedOrEmpty(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty string is redacted-or-empty", "", true},
		{"fixed placeholder is redacted-or-empty", RedactedSecretPlaceholder, true},
		{"short real secret is not redacted", "ab", false},
		{"real secret longer than placeholder is not redacted", "real-bearer-token", false},
		{"secret containing placeholder substring is not redacted", "prefix***suffix", false},
		{"single asterisk is not redacted", "*", false},
		{"double asterisk is not redacted", "**", false},
		{"four asterisks is not redacted (distinct from 3)", "****", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRedactedOrEmpty(tt.in); got != tt.want {
				t.Errorf("IsRedactedOrEmpty(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestPreserveIfRedacted(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		existing string
		want     string
	}{
		{"empty incoming preserves existing", "", "stored-secret", "stored-secret"},
		{"placeholder incoming preserves existing", RedactedSecretPlaceholder, "stored-secret", "stored-secret"},
		{"real incoming replaces existing", "new-secret", "stored-secret", "new-secret"},
		{"real incoming replaces empty existing", "new-secret", "", "new-secret"},
		{"empty incoming with empty existing stays empty", "", "", ""},
		{"placeholder incoming with empty existing stays empty", RedactedSecretPlaceholder, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PreserveIfRedacted(tt.incoming, tt.existing); got != tt.want {
				t.Errorf("PreserveIfRedacted(%q, %q) = %q, want %q",
					tt.incoming, tt.existing, got, tt.want)
			}
		})
	}
}
