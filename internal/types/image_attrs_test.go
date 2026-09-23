package types

import (
	"reflect"
	"strings"
	"testing"
)

// TestImageAttrRegistryHasActiveAttributes pins the v1 observation set: the
// registry is the single source of truth that drives both the OCR decision and
// the frontend panel, so adding an attribute later is a one-line change here
// and nowhere else. It also pins that every row can explain itself — the
// settings panel is rendered from these fields, so a row without text would
// reach a non-technical operator as a blank line, and a value without a meaning
// as a bare code like "sparse".
func TestImageAttrRegistryHasActiveAttributes(t *testing.T) {
	t.Parallel()

	var haveText, haveDataVisual bool
	for _, spec := range ImageAttrRegistry {
		if strings.TrimSpace(spec.Label) == "" {
			t.Errorf("%s has no display label", spec.Name)
		}
		if strings.TrimSpace(spec.Description) == "" {
			t.Errorf("%s has no display description", spec.Name)
		}
		values := spec.Values
		if spec.Type == AttrTypePresence && len(values) == 0 {
			values = []string{"true", "false"}
		}
		if len(values) == 0 {
			t.Errorf("%s declares no values", spec.Name)
		}
		for _, v := range values {
			if strings.TrimSpace(spec.ValueLabels[v]) == "" {
				t.Errorf("%s value %q has no human explanation", spec.Name, v)
			}
		}
		switch spec.Name {
		case "contain.text":
			haveText = true
			if spec.Type != AttrTypeExtent {
				t.Errorf("contain.text type = %q, want extent", spec.Type)
			}
			if len(spec.Values) != 3 {
				t.Errorf("contain.text values = %v, want none|sparse|block", spec.Values)
			}
		case "contain.data_visual":
			haveDataVisual = true
			if spec.Type != AttrTypePresence {
				t.Errorf("contain.data_visual type = %q, want presence", spec.Type)
			}
		}
	}
	if !haveText || !haveDataVisual {
		t.Fatalf("registry missing an active attribute: text=%v data_visual=%v", haveText, haveDataVisual)
	}
}

// TestParseImageAttrsResponse covers the observe-and-describe parser: it pulls
// attribute lines and the description, drops attributes the model did not
// actually answer for (instead of inventing a default), and never stores
// protocol noise as the caption.
func TestParseImageAttrsResponse(t *testing.T) {
	t.Parallel()

	t.Run("reads attributes and description", func(t *testing.T) {
		obs, ok := ParseImageAttrsResponse(
			"contain.text: block\ncontain.data_visual: true\nDESCRIPTION: A pricing table.")
		if !ok {
			t.Fatal("expected a usable description")
		}
		if obs.Attrs.Attrs["contain.text"] != "block" {
			t.Errorf("contain.text = %v, want block", obs.Attrs.Attrs["contain.text"])
		}
		if obs.Attrs.Attrs["contain.data_visual"] != true {
			t.Errorf("contain.data_visual = %v, want true", obs.Attrs.Attrs["contain.data_visual"])
		}
		if obs.Description != "A pricing table." {
			t.Errorf("description = %q", obs.Description)
		}
	})

	t.Run("joins multi-line descriptions and tolerates emphasis", func(t *testing.T) {
		obs, ok := ParseImageAttrsResponse(
			"**contain.text**: sparse\ncontain.data_visual: false\nDESCRIPTION: first line\nsecond line\n")
		if !ok {
			t.Fatal("expected a usable description")
		}
		if obs.Attrs.Attrs["contain.text"] != "sparse" {
			t.Errorf("contain.text = %v, want sparse", obs.Attrs.Attrs["contain.text"])
		}
		if obs.Description != "first line second line" {
			t.Errorf("description = %q", obs.Description)
		}
	})

	t.Run("keeps a prose answer that ignores the protocol", func(t *testing.T) {
		// No attribute lines at all: the model still described the image, so it
		// becomes the caption, but nothing is claimed about the attributes —
		// they stay absent so the caller's OnUnobserved clause decides.
		obs, ok := ParseImageAttrsResponse("- A wiring diagram.\n")
		if !ok {
			t.Fatal("expected a usable description")
		}
		if obs.Description != "A wiring diagram." {
			t.Errorf("list marker not stripped: %q", obs.Description)
		}
		if obs.Observed {
			t.Error("a prose answer must not count as an observation")
		}
		if _, present := obs.Attrs.Attrs["contain.text"]; present {
			t.Errorf("an unanswered attribute must stay absent, got %v", obs.Attrs.Attrs["contain.text"])
		}
	})

	t.Run("drops a value it cannot read instead of inventing one", func(t *testing.T) {
		obs, _ := ParseImageAttrsResponse("contain.text: maybe_a_lot\nDESCRIPTION: x")
		if _, present := obs.Attrs.Attrs["contain.text"]; present {
			t.Errorf("an unusable value must not be stored, got %v", obs.Attrs.Attrs["contain.text"])
		}
		if obs.Observed {
			t.Error("a line with an unusable value is not an observation")
		}
	})

	t.Run("keeps only the attributes the model answered for", func(t *testing.T) {
		obs, ok := ParseImageAttrsResponse("contain.text: sparse\nDESCRIPTION: A logo.")
		if !ok {
			t.Fatal("expected a usable description")
		}
		if obs.Attrs.Attrs["contain.text"] != "sparse" {
			t.Errorf("contain.text = %v, want sparse", obs.Attrs.Attrs["contain.text"])
		}
		if _, present := obs.Attrs.Attrs["contain.data_visual"]; present {
			t.Errorf("a half-answered observation must not grow the missing attribute, got %v",
				obs.Attrs.Attrs["contain.data_visual"])
		}
		if !obs.Observed {
			t.Error("the answer did observe one attribute")
		}
	})

	t.Run("unformatted response is reported as unusable", func(t *testing.T) {
		if _, ok := ParseImageAttrsResponse(""); ok {
			t.Error("an empty answer must not count as a description")
		}
	})

	t.Run("label-only answer yields no description but real attributes", func(t *testing.T) {
		obs, ok := ParseImageAttrsResponse("contain.text: block\ncontain.data_visual: true")
		if ok {
			t.Error("an answer with no description must be reported as missing caption")
		}
		if obs.Attrs.Attrs["contain.text"] != "block" {
			t.Errorf("contain.text = %v, want block", obs.Attrs.Attrs["contain.text"])
		}
	})
}

// TestDefaultImageActions pins the conservative built-in table: OCR is gated on
// contain.text == block OR contain.data_visual == true, and the fallback for an
// unobserved text attribute is to keep OCR on. This is what makes a missed
// observation cost one extra call rather than losing text.
func TestDefaultImageActions(t *testing.T) {
	t.Parallel()

	actions := DefaultImageActions()

	if !actions.OCR.OnUnobserved {
		t.Error("default OCR.OnUnobserved must be true (conservative)")
	}
	wantOn := []ImageAttrCondition{
		{Prop: "contain.text", Is: "block"},
		{Prop: "contain.data_visual", Is: "true"},
	}
	if len(actions.OCR.On) != len(wantOn) {
		t.Fatalf("default OCR.On = %v, want %v", actions.OCR.On, wantOn)
	}
	for i, c := range wantOn {
		if actions.OCR.On[i] != c {
			t.Errorf("default OCR.On[%d] = %+v, want %+v", i, actions.OCR.On[i], c)
		}
	}
}

// TestMergeImageActions replaces the whole OCR condition list: an explicit
// action row is trusted wholesale, so a knowledge base tunes OCR without
// spelling out every condition, and a future action key added to the default is
// not wiped by an older stored config.
func TestMergeImageActions(t *testing.T) {
	t.Parallel()

	custom := ImageActionsConfig{
		OCR: ImageOCRAction{
			On:           []ImageAttrCondition{{Prop: "contain.text", Is: "block"}},
			OnUnobserved: false,
		},
	}
	merged := MergeImageActions(custom)
	if merged.OCR.OnUnobserved {
		t.Error("an explicit OCR.OnUnobserved must replace the default (true)")
	}
	if len(merged.OCR.On) != 1 {
		t.Errorf("custom OCR.On must replace the default list, got %v", merged.OCR.On)
	}

	// A nil/empty custom keeps the default whole.
	if got := MergeImageActions(ImageActionsConfig{}); !reflect.DeepEqual(got, DefaultImageActions()) {
		t.Error("an empty custom table must keep the default image actions")
	}
}
