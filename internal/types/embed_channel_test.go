package types

import "testing"

func TestNormalizeEmbedWidgetPosition(t *testing.T) {
	cases := map[string]string{
		"bottom-right": DefaultEmbedWidgetPosition,
		"bottom-left":  "bottom-left",
		"top-right":    "top-right",
		"top-left":     "top-left",
		"":             DefaultEmbedWidgetPosition,
		"center":       DefaultEmbedWidgetPosition,
	}
	for in, want := range cases {
		if got := NormalizeEmbedWidgetPosition(in); got != want {
			t.Fatalf("NormalizeEmbedWidgetPosition(%q) = %q, want %q", in, got, want)
		}
	}
}
