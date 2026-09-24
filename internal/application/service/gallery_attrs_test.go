package service

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// rules builds the per-value verdicts the gallery panel sends as
// attr_rules: {"<attr id>": {"<value>": "off"|"on"}}.
func rules(t *testing.T, raw string) *types.ImageListFilter {
	t.Helper()
	var m map[string]map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("bad rules %s: %v", raw, err)
	}
	return &types.ImageListFilter{AttrRules: m}
}

func TestMatchAttrRules(t *testing.T) {
	// An image two attributes have been looked at for: it carries both values.
	observed := types.ImageAsset{ID: "img-block", Attrs: map[string]any{
		"contain.text":       "block",
		"contain.data_visual": "true",
	}}
	// An image no pipeline has ever examined, so the keys are absent.
	unobserved := types.ImageAsset{ID: "img-blank", Attrs: map[string]any{}}

	cases := []struct {
		name     string
		asset    types.ImageAsset
		raw      string
		expected bool
		why      string
	}{
		{
			name:     "no rules leaves the image alone",
			asset:    observed,
			raw:      `{}`,
			expected: true,
			why:      "Nothing was configured, so nothing to judge.",
		},
		{
			name:     "off hides the images carrying that value",
			asset:    observed,
			raw:      `{"system:contain.text":{"block":"off"}}`,
			expected: false,
			why:      "The panel asked to hide images like this one.",
		},
		{
			name:     "on forces the images carrying that value back in",
			asset:    observed,
			raw:      `{"system:contain.text":{"block":"on"}}`,
			expected: true,
			why:      "A forced display outranks everything else.",
		},
		{
			name: "on outranks off for the same image",
			asset: observed,
			// Both rules speak about this image; one wants it gone, one wants
			// it kept, and the two verdicts come from different attributes.
			raw:      `{"system:contain.text":{"block":"on"},"system:contain.data_visual":{"true":"off"}}`,
			expected: true,
			why:      "The panel explicitly wanted to see this image.",
		},
		{
			name:     "off still wins for an image only off speaks about",
			asset:    observed,
			raw:      `{"system:contain.data_visual":{"true":"off"}}`,
			expected: false,
			why:      "Nothing claims this image, so the hide stands.",
		},
		{
			name:     "an unobserved value is not a verdict",
			asset:    unobserved,
			raw:      `{"system:contain.text":{"block":"off"}}`,
			expected: true,
			why:      "The image carries no such value, so the rule cannot speak.",
		},
		{
			name:     "unobserved with an on rule stays visible too",
			asset:    unobserved,
			raw:      `{"system:contain.text":{"block":"on"}}`,
			expected: true,
			why:      "Forcing a display cannot invent a value it has never seen.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchAttrRules(tc.asset, rules(t, tc.raw))
			if got != tc.expected {
				t.Errorf("matchAttrRules = %v, want %v (%s)", got, tc.expected, tc.why)
			}
		})
	}
}

// A filter that hides everything must not empty the list just because a
// pipeline has not reached these images yet — that is the whole point of
// treating absence as silence rather than as a mismatch.
func TestFilterImageAssetsKeepsUnobserved(t *testing.T) {
	assets := []types.ImageAsset{
		{ID: "a", Attrs: map[string]any{}},
		{ID: "b", Attrs: map[string]any{}},
	}
	filter := &types.ImageListFilter{
		AttrFilters: map[string][]string{"system:contain.text": {"block"}},
	}
	if got := filterImageAssets(assets, filter); len(got) != len(assets) {
		t.Errorf("kept %d of %d images; unobserved images must not be filtered out",
			len(got), len(assets))
	}
}
