package types

import "testing"

func TestDefaultImageClassPoliciesMarksOnlyDecorativeDisabled(t *testing.T) {
	t.Parallel()

	policies := DefaultImageClassPolicies()
	for _, class := range ImageClasses {
		policy, ok := policies[string(class)]
		if !ok {
			t.Fatalf("class %q missing from the default table", class)
		}
		wantDisabled := class == ImageClassDecorative
		if policy.Disabled != wantDisabled {
			t.Errorf("class %q disabled = %v, want %v", class, policy.Disabled, wantDisabled)
		}
	}
}

func TestNormalizeImageClassifyDownscale(t *testing.T) {
	t.Parallel()

	on := true
	off := false
	cases := []struct {
		name string
		in   *bool
		want bool
	}{
		{"nil selects the default (on)", nil, true},
		{"explicit on", &on, true},
		{"explicit off", &off, false},
	}
	for _, tc := range cases {
		if got := NormalizeImageClassifyDownscale(tc.in); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMergeImageClassPoliciesOverridesPerClass(t *testing.T) {
	t.Parallel()

	custom := map[string]ImageClassPolicy{
		// Turns the built-in disabled row back off: an explicit row replaces
		// the default one entirely.
		string(ImageClassDecorative): {OCR: false, Caption: true},
		// Disables a class the defaults keep.
		string(ImageClassPhoto): {OCR: false, Caption: true, Disabled: true},
	}
	merged := MergeImageClassPolicies(custom)

	if merged[string(ImageClassDecorative)].Disabled {
		t.Error("an explicit decorative row must replace the default (disabled) one")
	}
	if !merged[string(ImageClassPhoto)].Disabled {
		t.Error("the custom disabled photo row must survive the merge")
	}
	// Untouched classes keep the defaults, including decorative's original
	// neighbours in the table.
	if !merged[string(ImageClassChart)].OCR {
		t.Error("chart should keep the default OCR=true")
	}
	if merged[string(ImageClassLogo)].Disabled {
		t.Error("logo should keep the default disabled=false")
	}
}

func TestDisabledImageClassesFollowsEnumOrder(t *testing.T) {
	t.Parallel()

	custom := map[string]ImageClassPolicy{
		string(ImageClassPhoto): {OCR: false, Caption: true, Disabled: true},
	}
	disabled := DisabledImageClasses(MergeImageClassPolicies(custom))

	if len(disabled) != 2 {
		t.Fatalf("disabled = %v, want exactly decorative and photo", disabled)
	}
	if disabled[0] != ImageClassDecorative || disabled[1] != ImageClassPhoto {
		t.Errorf("disabled = %v, want enum order [decorative photo]", disabled)
	}
}
