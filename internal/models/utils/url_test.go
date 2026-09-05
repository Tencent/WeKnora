package utils

import "testing"

func TestAppendPathOnce(t *testing.T) {
	cases := []struct{ base, suffix, want string }{
		{"https://h/v1", "/responses", "https://h/v1/responses"},
		{"https://h/v1/responses", "/responses", "https://h/v1/responses"},
		{"https://h/v1/RESPONSES", "/responses", "https://h/v1/RESPONSES"},
		{"https://h/v1/", "/chat/completions", "https://h/v1/chat/completions"},
		{"https://h/v1/chat/completions", "/chat/completions", "https://h/v1/chat/completions"},
	}
	for _, tc := range cases {
		if got := AppendPathOnce(tc.base, tc.suffix); got != tc.want {
			t.Errorf("AppendPathOnce(%q,%q) = %q, want %q", tc.base, tc.suffix, got, tc.want)
		}
	}
}

func TestStripPathSuffix(t *testing.T) {
	suffixes := []string{"/api/v1/chat/completions", "/chat/completions", "/responses"}
	cases := []struct{ in, want string }{
		{"https://h/v1", "https://h/v1"},
		{"https://h/v1/responses", "https://h/v1"},
		{"https://h/api/v1/chat/completions", "https://h"},
		{"https://h/other", "https://h/other"},
	}
	for _, tc := range cases {
		if got := StripPathSuffix(tc.in, suffixes); got != tc.want {
			t.Errorf("StripPathSuffix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
