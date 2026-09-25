package types

// This file is the vocabulary of the image pipeline layer: the ids of the
// actions a pipeline can run, the declarative shape of an action, and the ids of
// the pipelines themselves. What an action *does* is bound by the action
// registry in application/service (image_action_registry.go) — keeping the two
// halves apart is what lets this package stay free of any transport dependency.

// ImageActionID is the dispatch key of an image action. A pipeline references an
// action by this id; which code runs for that id is decided by the registry, so
// adding an action is one entry here plus a binding, not a new branch in a loop.
type ImageActionID string

const (
	// ImageActionCaption asks the model for a short description of the image
	// and stores it as the caption slot.
	ImageActionCaption ImageActionID = "caption"
	// ImageActionObservationCaption observes the registered attributes
	// (contain.text, contain.data_visual, ...) and describes the image in the
	// same answer. One VLM request fills the attribute slots and the caption
	// slot, which is why there is no standalone observation action and no
	// "merge observation with caption" rule anywhere in the framework.
	ImageActionObservationCaption ImageActionID = "observation_caption"
	// ImageActionOCR extracts the text the image carries, when the policy wants
	// it.
	ImageActionOCR ImageActionID = "ocr"
)

// ImageActionImpl records how an action does its work. The flag is not
// decoration: an action that calls a model to check an existing value has
// stopped checking and started generating again.
type ImageActionImpl string

const (
	// ImageActionImplPure runs without a model.
	ImageActionImplPure ImageActionImpl = "pure"
	// ImageActionImplVLM spends one or more model requests.
	ImageActionImplVLM ImageActionImpl = "vlm"
)

// ImageActionSpec is the declarative half of an action: what it is allowed to
// write, what it declares to read, and the knobs a settings panel may expose.
// The executable half is bound by the registry.
type ImageActionSpec struct {
	// ID dispatches the action inside a pipeline; it is also how a pipeline
	// refers to it and how the gallery and the settings panel name it.
	ID ImageActionID
	// Impl says whether the action may call a model.
	Impl ImageActionImpl
	// Writes lists the output slots the action fills; nil means it writes none.
	// Two actions of one pipeline may write the same slot on purpose — the
	// iterate and validate shapes both overwrite what they just read — so
	// uniqueness is deliberately not asserted here.
	Writes []string
	// Reads lists the slots the action consumes. Declaration only: it produces
	// trace output, it does not order the pipeline.
	Reads []string
	// Version fingerprints the prompts and rules the action encodes, so a trace
	// row can be matched back to the revision that produced an image rather
	// than only to the pipeline that ran.
	Version string
	// Fields are the per-action tunables the settings panel renders.
	Fields []ImageFieldDef
}

// ImageFieldDef is one tunable of an action as the settings panel sees it.
type ImageFieldDef struct {
	// Key is the name the action reads with r.Param(key).
	Key string
	// Type selects the control the panel renders.
	Type ImageFieldType
	// Label and Description are written in the project's default language; the
	// frontend overlays its translations and falls back to these.
	Label       string
	Description string
	// Default applies when the knowledge base does not override the field.
	Default any
	// Options enumerates the allowed values of an enum field.
	Options []string
}

// ImageFieldType is the control a tunable renders as.
type ImageFieldType string

const (
	ImageFieldTypeBool   ImageFieldType = "bool"
	ImageFieldTypeString ImageFieldType = "string"
	ImageFieldTypeEnum   ImageFieldType = "enum"
)

// SharedImageActions are the actions every pipeline starts from. They are
// declared here and bound to handlers by the registry; an action that belongs to
// a single pipeline instead registers under a "<pipeline_id>.<action>" id.
//
// Version carries the placeholder revision "v1" until the prompts move into the
// actions and can be fingerprinted there.
var SharedImageActions = []ImageActionSpec{
	{
		ID:      ImageActionCaption,
		Impl:    ImageActionImplVLM,
		Writes:  []string{"caption"},
		Version: "v1",
	},
	{
		ID:     ImageActionObservationCaption,
		Impl:   ImageActionImplVLM,
		Writes: []string{"attrs", "caption"},
		// Observing reads the image and the registered attribute table; the
		// caption is a by-product of that same request.
		Reads:   []string{"attrs"},
		Version: "v1",
	},
	{
		ID:     ImageActionOCR,
		Impl:   ImageActionImplVLM,
		Writes: []string{"ocr_text"},
		// The OCR prompt is deliberately not built from the knowledge base
		// custom instructions, so it does not read the same slot the other two
		// does. See buildVLMOCRPrompt.
		Version: "v1",
	},
}

// ImagePipelineID labels which image pipeline produced a run. It is recorded on
// the per-image subspan input, because reading the stored image_info alone cannot
// distinguish an image whose pipeline observed the attributes and then skipped
// OCR from one whose pipeline never observed anything.
type ImagePipelineID string

const (
	// ImagePipelineObCapOCR observes the registered attributes first, captions
	// in the same answer, then OCRs only when the attribute policy allows it.
	// The id promises that observation comes before the decision; it does not
	// promise that those three actions are the whole story.
	ImagePipelineObCapOCR ImagePipelineID = "ob_cap_ocr"
	// ImagePipelineCaptionOCR is the plain path: caption every image, then OCR
	// every image, with no observation and no policy.
	ImagePipelineCaptionOCR ImagePipelineID = "caption_ocr"
)

// ImagePipelineIDFor maps the attribute-observation switch to a pipeline id, so
// the trace label and the pipeline that actually runs can never disagree.
func ImagePipelineIDFor(attrsEnabled bool) ImagePipelineID {
	if attrsEnabled {
		return ImagePipelineObCapOCR
	}
	return ImagePipelineCaptionOCR
}
