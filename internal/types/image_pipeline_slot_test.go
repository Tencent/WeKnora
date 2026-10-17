package types

import "testing"

// TestSlotKeyDoesNotDoubleThePipelineID pins the one subtlety in the slot rule:
// an action scoped to a single pipeline already carries that pipeline's id
// ("<pipeline>.<action>"), so prefixing it again would look up a key nothing
// ever wrote. The rule lives in one function and is used by both the writer (the
// settings panel, through its own mirror) and the reader below, which is why the
// doubling case is asserted rather than left to the happy path.
func TestSlotKeyDoesNotDoubleThePipelineID(t *testing.T) {
	cases := []struct {
		pipeline, key, want string
	}{
		{"smartocr", "ocr_on_unobserved", "smartocr.ocr_on_unobserved"},
		{"smartocr", "preprocess.mode", "smartocr.preprocess.mode"},
		{"smartocr", "ocr.thinking_enabled", "smartocr.ocr.thinking_enabled"},
		{"default", "caption_enabled", "default.caption_enabled"},
		// Already namespaced: left alone.
		{"smartocr", "smartocr.ocr_on_unobserved", "smartocr.ocr_on_unobserved"},
		// No pipeline chosen (multimodal switched off): the bare key is the key.
		{"", "caption_enabled", "caption_enabled"},
	}
	for _, tc := range cases {
		if got := SlotKey(tc.pipeline, tc.key); got != tc.want {
			t.Errorf("SlotKey(%q, %q) = %q, want %q", tc.pipeline, tc.key, got, tc.want)
		}
	}
}

// TestImagePipelineParamReadsOnlyTheSlot pins the lookup as the single-home
// rule it is: a pipeline reads its own slot, another pipeline's slot says
// nothing to it, and a key spelled without a slot is not a second home a stored
// value could hide in. "Not found" is the answer that lets the caller fall back
// to the default the field declares — the same answer the panel draws.
func TestImagePipelineParamReadsOnlyTheSlot(t *testing.T) {
	params := map[string]any{
		"default.caption_enabled":     false,
		"smartocr.ocr_on_unobserved":  true,
		"smartocr.preprocess.mode":    "always",
		"smartocr.ocr_enabled":        nil, // stored as nothing: not a value
		"an_unrelated_pipeline.value": "x",
		"caption_enabled":             true, // no slot: not a home
	}

	if v, ok := ImagePipelineParam(params, "default", "caption_enabled"); !ok || v != false {
		t.Errorf("the pipeline's own slot did not answer: (%v, %v)", v, ok)
	}
	// Another pipeline's slot says nothing to this one.
	if v, ok := ImagePipelineParam(params, "default", "ocr_on_unobserved"); ok {
		t.Errorf("a pipeline read another pipeline's setting: %v", v)
	}
	if v, ok := ImagePipelineParam(params, "smartocr", "ocr_on_unobserved"); !ok || v != true {
		t.Errorf("the pipeline's own slot did not answer: (%v, %v)", v, ok)
	}
	// A key with no slot is not a value, even when the map carries it.
	if v, ok := ImagePipelineParam(params, "smartocr", "caption_enabled"); ok {
		t.Errorf("a slotless key answered as %v", v)
	}
	// A nil slot entry is "nothing stored", not a stored nothing.
	if v, ok := ImagePipelineParam(params, "smartocr", "ocr_enabled"); ok {
		t.Errorf("a nil slot entry answered as %v", v)
	}
	if _, ok := ImagePipelineParam(params, "default", "never_written"); ok {
		t.Error("an unwritten key reported a value")
	}
	if _, ok := ImagePipelineParam(nil, "default", "caption_enabled"); ok {
		t.Error("a nil params map reported a value")
	}
}
