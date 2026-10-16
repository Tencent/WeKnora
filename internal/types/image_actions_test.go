package types

import "testing"

// TestActionParamNamespacesByOwner pins the rule the whole parameter scheme
// rests on: a tunable's key is its owner's id plus the bare name the owner
// declared. Two actions may both declare "thinking" precisely because the
// namespace — not a naming convention someone has to remember — keeps them
// apart.
func TestActionParamNamespacesByOwner(t *testing.T) {
	if got := ActionParam(ImageActionCaption, ImageActionParamThinking); got != "caption.thinking" {
		t.Errorf("ActionParam(caption, thinking) = %q, want caption.thinking", got)
	}
	if got := ActionParam(ImageActionOCR, ImageActionParamThinking); got != "ocr.thinking" {
		t.Errorf("ActionParam(ocr, thinking) = %q, want ocr.thinking", got)
	}
	if got := ActionParam(ImageActionOCR, ImageActionParamOnUnobserved); got != "ocr.on_unobserved" {
		t.Errorf("ActionParam(ocr, on_unobserved) = %q, want ocr.on_unobserved", got)
	}
	// The two "thinking"s are different keys, which is the whole point.
	if ActionParam(ImageActionCaption, ImageActionParamThinking) ==
		ActionParam(ImageActionOCR, ImageActionParamThinking) {
		t.Error("two actions' thinking switches share a key")
	}
}

// TestDeclaredActionFieldsAreNamespaced walks what the actions actually
// declare: every key must be <its own action id>.<bare name>, with no further
// dot, so a hand-written key cannot drift away from the derivation.
func TestDeclaredActionFieldsAreNamespaced(t *testing.T) {
	for _, spec := range SharedImageActions {
		for _, field := range spec.Fields {
			prefix := string(spec.ID) + "."
			if len(field.Key) <= len(prefix) || field.Key[:len(prefix)] != prefix {
				t.Errorf("action %q declares %q, which is not namespaced by its own id",
					spec.ID, field.Key)
				continue
			}
			bare := field.Key[len(prefix):]
			if bare == "" || bare != ActionParam(spec.ID, bare)[len(prefix):] || containsDot(bare) {
				t.Errorf("action %q declares %q: the bare name must be a single segment",
					spec.ID, field.Key)
			}
		}
	}
}

// TestActionFieldFindsWhatWasDeclared is the lookup pipelines use to carry an
// action's control onto their panel without spelling the key.
func TestActionFieldFindsWhatWasDeclared(t *testing.T) {
	field, ok := ActionField(ImageActionOCR, ImageActionParamOnUnobserved)
	if !ok {
		t.Fatal("ActionField(ocr, on_unobserved) not found")
	}
	if field.Key != "ocr.on_unobserved" {
		t.Errorf("looked up %q, want ocr.on_unobserved", field.Key)
	}
	if _, ok := ActionField(ImageActionCaption, ImageActionParamOnUnobserved); ok {
		t.Error("ActionField found a tunable the caption action never declares")
	}
	if _, ok := ActionField(ImageActionPreprocess, "thinking"); ok {
		t.Error("ActionField found a tunable the preprocess action never declares")
	}
}

func containsDot(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return true
		}
	}
	return false
}
