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

// TestImagePipelineParamReadsSlotThenBareKey pins the lookup order the whole
// migration rests on: the running pipeline's slot first, then the bare key a
// config written before the slots existed carries. The slot winning is what lets
// an operator override a setting without the old spelling coming back to life.
func TestImagePipelineParamReadsSlotThenBareKey(t *testing.T) {
	params := map[string]any{
		"caption_enabled":             true,  // written before namespacing
		"default.caption_enabled":     false, // written since
		"smartocr.ocr_on_unobserved":  true,
		"smartocr.preprocess.mode":    "always",
		"smartocr.ocr_enabled":        nil, // stored as nothing: not a value
		"an_unrelated_pipeline.value": "x",
	}

	if v, ok := ImagePipelineParam(params, "default", "caption_enabled"); !ok || v != false {
		t.Errorf("the slot must win over the bare key: got (%v, %v)", v, ok)
	}
	// Another pipeline's slot says nothing to this one, and neither does a key
	// spelled for somebody else.
	if v, ok := ImagePipelineParam(params, "default", "ocr_on_unobserved"); ok {
		t.Errorf("a pipeline read another pipeline's setting: %v", v)
	}
	if v, ok := ImagePipelineParam(params, "smartocr", "ocr_on_unobserved"); !ok || v != true {
		t.Errorf("the pipeline's own slot did not answer: (%v, %v)", v, ok)
	}
	// A nil value is "nothing stored"; falling through to the bare key is the
	// behaviour a caller depends on to reach a config written before slots.
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

// TestResolvePreprocessModePrefersTheSlot is the round trip of the setting that
// moved into the pipeline slot: a value the panel wrote under the running
// pipeline's slot is what the run reads, the knowledge base field it lived in
// before is the fallback, and nothing usable in either means auto.
func TestResolvePreprocessModePrefersTheSlot(t *testing.T) {
	cases := []struct {
		name string
		cfg  ImageProcessingConfig
		want string
	}{
		{
			"the slot answers for the pipeline that runs",
			ImageProcessingConfig{
				ImagePipelineID:     string(ImagePipelineSmartOCR),
				PreprocessMode:      PreprocessModeOff,
				ImagePipelineParams: map[string]any{"smartocr.preprocess.mode": PreprocessModeAlways},
			},
			PreprocessModeAlways,
		},
		{
			"another pipeline's slot is not this run's setting",
			ImageProcessingConfig{
				ImagePipelineID:     string(ImagePipelineSmartOCR),
				ImagePipelineParams: map[string]any{"default.preprocess.mode": PreprocessModeOff},
			},
			PreprocessModeAuto,
		},
		{
			"a config written before the slot falls back to the field",
			ImageProcessingConfig{
				ImagePipelineID: string(ImagePipelineSmartOCR),
				PreprocessMode:  PreprocessModeOff,
			},
			PreprocessModeOff,
		},
		{
			"nothing stored anywhere means auto, the mode the panel offers first",
			ImageProcessingConfig{ImagePipelineID: string(ImagePipelineSmartOCR)},
			PreprocessModeAuto,
		},
		{
			"a value the run would not understand means auto",
			ImageProcessingConfig{
				ImagePipelineID:     string(ImagePipelineSmartOCR),
				ImagePipelineParams: map[string]any{"smartocr.preprocess.mode": "sometimes"},
			},
			PreprocessModeAuto,
		},
	}
	for _, tc := range cases {
		if got := ResolvePreprocessMode(&tc.cfg); got != tc.want {
			t.Errorf("%s: ResolvePreprocessMode = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := ResolvePreprocessMode(nil); got != PreprocessModeAuto {
		t.Errorf("a nil config resolved to %q, want auto", got)
	}
}
