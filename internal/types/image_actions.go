package types

// ActionKind enumerates the normalized image-pipeline actions. Under the
// unified action framework every image step is one of these; the dispatch loop
// treats them uniformly so adding an action is a registry entry, not a new
// if/else branch.
type ActionKind string

const (
	// ActionCaption asks the model for a short description of the image.
	ActionCaption ActionKind = "caption"
	// ActionObservation asks the model to observe the registered attributes
	// (contain.text, contain.data_visual, ...). It is the only action that
	// feeds a later decision — the OCR policy reads its result.
	ActionObservation ActionKind = "observation"
	// ActionOCR extracts the text the image carries, when the policy wants it.
	ActionOCR ActionKind = "ocr"
)

// PipelineMode labels which image pipeline a trace row ran under. It is
// recorded on the per-image subspan input so a reader of the processing trace
// can tell an attribute-observed run from the caption+OCR fallback: reading the
// stored image_info alone cannot, because a run that observed the attributes
// and then skipped OCR looks exactly like one that never observed anything.
type PipelineMode string

const (
	// PipelineObservationDriven observes the registered attributes first, then
	// OCRs only when the attribute policy says so.
	PipelineObservationDriven PipelineMode = "observation_driven"
	// PipelineCaptionOCR is the upstream path: caption every image, then OCR
	// every image, with no observation and no policy.
	PipelineCaptionOCR PipelineMode = "caption_ocr"
)

// PipelineModeFor maps the attribute-observation switch to its trace label, so
// the label and the action table below can never disagree about which pipeline
// a task ran under.
func PipelineModeFor(attrsEnabled bool) PipelineMode {
	if attrsEnabled {
		return PipelineObservationDriven
	}
	return PipelineCaptionOCR
}

// DefaultEnabledActions returns the per-action default-on flags for a pipeline
// mode. Observation mode observes and captions by default and lets the OCR
// decision gate OCR; caption+OCR mode captions and OCRs every image. This is
// the single source of default action membership — a new action is enabled
// here, never scattered across call sites.
//
// Note the deliberate parallel with DefaultImageActions: this is which actions
// run at all, that one is the attribute -> work policy that gates OCR.
func DefaultEnabledActions(attrsEnabled bool) map[ActionKind]bool {
	if attrsEnabled {
		return map[ActionKind]bool{
			ActionCaption:     true,
			ActionObservation: true,
			ActionOCR:         false,
		}
	}
	return map[ActionKind]bool{
		ActionCaption:     true,
		ActionObservation: false,
		ActionOCR:         true,
	}
}
