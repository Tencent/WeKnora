package types

import "testing"

func TestNormalizeEmbedWidgetPosition(t *testing.T) {
	cases := map[string]string{
		"bottom-right":        DefaultEmbedWidgetPosition,
		"bottom-left":         "bottom-left",
		"top-right":           "top-right",
		"top-left":            "top-left",
		"":                    DefaultEmbedWidgetPosition,
		"center":              DefaultEmbedWidgetPosition,
		" bottom-left ":       "bottom-left",
		"BOTTOM-RIGHT":        DefaultEmbedWidgetPosition,
		"bottom-right-extra":  DefaultEmbedWidgetPosition,
		"middle":              DefaultEmbedWidgetPosition,
		"top-center":          DefaultEmbedWidgetPosition,
		"   ":                 DefaultEmbedWidgetPosition,
	}
	for in, want := range cases {
		if got := NormalizeEmbedWidgetPosition(in); got != want {
			t.Fatalf("NormalizeEmbedWidgetPosition(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeEmbedHeaderTitleMode(t *testing.T) {
	cases := map[string]string{
		"":        DefaultEmbedHeaderTitleMode,
		"channel": DefaultEmbedHeaderTitleMode,
		"session": EmbedHeaderTitleModeSession,
		"SESSION": DefaultEmbedHeaderTitleMode,
		"other":   DefaultEmbedHeaderTitleMode,
	}
	for in, want := range cases {
		if got := NormalizeEmbedHeaderTitleMode(in); got != want {
			t.Fatalf("NormalizeEmbedHeaderTitleMode(%q) = %q, want %q", in, got, want)
		}
	}
}
