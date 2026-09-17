package types

// OCR state values an image rule may require of an image.
const (
	// OCRStateAny places no condition on the OCR text.
	OCRStateAny = ""
	// OCRStateEmpty requires that OCR found nothing to transcribe.
	OCRStateEmpty = "empty"
	// OCRStateNonEmpty requires OCR text. Used as a cross-signal it says "this
	// image did yield text, whatever its class claims".
	OCRStateNonEmpty = "non_empty"
)

// MaxImageRuleRegexLength bounds an operator-supplied pattern. Go's regexp
// engine is RE2, so a hostile pattern costs linear time rather than exponential
// and the cap exists to bound compilation, not to prevent blow-up.
const MaxImageRuleRegexLength = 512

// ImageMatchSpec is a rule's condition set. Every populated field must hold and
// the values inside one list are alternatives, so a spec reads as "the class is
// one of these, and the caption is one of these, and ...".
//
// An empty spec matches nothing. A rule whose conditions were forgotten would
// otherwise act on every image in a document, and that is the one failure worth
// designing against rather than for.
type ImageMatchSpec struct {
	// Classes matches the class the describe round assigned (see ImageClass).
	// Values are normalised before comparison, so "Decorative logo" and
	// "decorative" agree. An image with no class never satisfies a class
	// condition: a row written before classification shipped was never
	// classified, and sweeping it up would be acting on an assumption.
	Classes []string `json:"classes,omitempty"`
	// CaptionEquals compares the normalised caption for equality. Equality and
	// not containment: "not a decorative image" must not read as a decorative
	// image.
	CaptionEquals []string `json:"caption_equals,omitempty"`
	// CaptionContains is a substring test on the normalised caption.
	CaptionContains []string `json:"caption_contains,omitempty"`
	// CaptionRegex is a pattern matched against the normalised caption.
	CaptionRegex string `json:"caption_regex,omitempty"`
	// OCRState requires the image to have, or lack, OCR text (see the OCRState
	// constants). The empty string places no condition.
	OCRState string `json:"ocr_state,omitempty"`
}

// ImageRule binds a set of conditions to one action. Rules are user data: they
// come from a knowledge base's configuration and the engine never invents one.
type ImageRule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Enabled is a pointer so that omitting it means "on". A rule a user wrote
	// and never toggled should run; the alternative silently ignores rules whose
	// author never knew the flag existed.
	Enabled *bool `json:"enabled,omitempty"`
	// Match is the condition set. It must not be empty.
	Match ImageMatchSpec `json:"match"`
	// Action names a registered image action.
	Action string `json:"action"`
	// Params is handed to the action untouched. Its shape is the action's
	// business, which is why it is not typed here.
	Params map[string]any `json:"params,omitempty"`
}

// IsEnabled reports whether the rule should be considered. An absent flag is on.
func (r ImageRule) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}
