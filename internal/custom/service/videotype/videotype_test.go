package videotype

import "testing"

func TestNormalizeUsesSingleBusinessVocabulary(t *testing.T) {
	tests := map[string]string{
		"interview":     Interview,
		"tutorial":      Training,
		"training":      Training,
		"lecture":       Salon,
		"salon":         Salon,
		"case_analysis": General,
		"general":       General,
		"unknown":       General,
	}
	for input, want := range tests {
		if got := Normalize(input); got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}
