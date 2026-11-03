package service

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// DecideOCR is a pure function: no ctx, no repo, no model. Input the observed
// attributes, the policy table and the caller's clause for an unobserved image;
// output whether OCR runs. It is the single place the "need OCR" decision lives,
// so it can be unit-tested and replayed.
//
// The default policy encodes, for every attribute the table reads:
//
//	ocr = (contain.text == "block")
//	    || (contain.data_visual == true)
//	    || the attribute was not observed (conservative: onUnobserved)
//
// where "not observed" covers both a line the model never wrote and a value that
// was not one of the allowed ones — the parser leaves such an attribute absent
// rather than inventing a default. An incomplete observation therefore keeps
// extracting, so a missed block of text costs one extra call instead of being
// lost.
//
// The clause arrives as an argument rather than inside the table because it is
// not an attribute match: it decides whether OCR is scheduled at all, which is
// the scheduling pipeline's decision (its ocr_on_unobserved setting).
//
// The result is equivalent to the old 7-class table: decorative / logo / photo
// (text reliably absent or captured by the description) skip OCR;
// text_screenshot / table / chart / other run OCR.
func DecideOCR(attrs types.ImageAttrs, actions types.ImageActionsConfig, onUnobserved bool) bool {
	if len(actions.OCR.On) == 0 {
		// An empty condition list is never a resolved policy (ResolveImageActions
		// always fills it), only a payload that carries none. Fall back to the
		// built-in table rather than reading an empty policy as "never OCR".
		actions = types.DefaultImageActions()
	}
	incomplete := false
	for _, cond := range actions.OCR.On {
		v, ok := attrs.Attrs[cond.Prop]
		if !ok {
			// The observation does not answer for this attribute, so it cannot
			// decide on its own — and it cannot be read as "condition not met"
			// either, which would silently treat a failed observation as a
			// confident negative. Remember it and let the clause decide.
			incomplete = true
			continue
		}
		if fmt.Sprint(v) == cond.Is {
			return true
		}
	}
	// Nothing matched. Either the observation answered for every attribute the
	// policy reads (a real, complete negative), or it did not — in which case
	// the conservative clause decides.
	if incomplete {
		return onUnobserved
	}
	return false
}
