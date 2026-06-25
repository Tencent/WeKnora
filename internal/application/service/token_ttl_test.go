package service

import (
	"testing"
	"time"
)

// parseDuration extends time.ParseDuration with a "d" (day) unit. These tests
// are pure-function so they need no DB/repo plumbing.
func TestParseDuration(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "days", in: "30d", want: 30 * 24 * time.Hour},
		{name: "single day", in: "7d", want: 7 * 24 * time.Hour},
		{name: "fractional day", in: "0.5d", want: 12 * time.Hour},
		{name: "native hours", in: "24h", want: 24 * time.Hour},
		{name: "native minutes", in: "30m", want: 30 * time.Minute},
		{name: "native composite", in: "1h30m", want: 90 * time.Minute},
		{name: "garbage", in: "abc", wantErr: true},
		{name: "bare unit", in: "d", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDuration(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseDuration(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDuration(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("parseDuration(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// parseDurationEnv reads a duration from an env var, falling back to the given
// default when the var is unset, unparseable, or non-positive.
func TestParseDurationEnv(t *testing.T) {
	const key = "WEKNORA_TEST_TTL"
	fallback := 24 * time.Hour

	// An empty value exercises the same branch as an unset var (both trim to "").
	cases := []struct {
		name string
		val  string
		want time.Duration
	}{
		{name: "empty uses fallback", val: "", want: fallback},
		{name: "whitespace uses fallback", val: "   ", want: fallback},
		{name: "valid days override", val: "30d", want: 30 * 24 * time.Hour},
		{name: "valid hours override", val: "48h", want: 48 * time.Hour},
		{name: "invalid falls back", val: "nope", want: fallback},
		{name: "zero falls back", val: "0h", want: fallback},
		{name: "negative falls back", val: "-5h", want: fallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(key, tc.val)
			if got := parseDurationEnv(key, fallback); got != tc.want {
				t.Fatalf("parseDurationEnv(%s=%q) = %v, want %v", key, tc.val, got, tc.want)
			}
		})
	}
}
