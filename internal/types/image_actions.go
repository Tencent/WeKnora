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
	// ImageActionObservation observes the registered attributes
	// (contain.text, contain.data_visual, ...) and describes the image in the
	// same answer. One VLM request fills the attribute slots and the caption
	// slot, which is why there is no standalone observation action and no
	// "merge observation with caption" rule anywhere in the framework.
	ImageActionObservation ImageActionID = "observation"
	// ImageActionOCR extracts the text the image carries, when the policy wants
	// it.
	ImageActionOCR ImageActionID = "ocr"
	// ImageActionPreprocess straightens and crops a photographed page before
	// OCR runs, so the OCR pass sees an upright, content-tight image. It is
	// pure: it talks to the local document preprocessor, never to a VLM, and
	// a preprocessor that is absent or fails leaves the image untouched —
	// which is why it writes a slot instead of replacing the run's image
	// reference outright.
	ImageActionPreprocess ImageActionID = "preprocess"
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
	// Fields are the per-action tunables the settings panel renders. A field's
	// key is the action's own namespace plus its bare name — build it with
	// ActionParam(id, name), never by hand — so the caption's "thinking" and
	// the OCR's "thinking" are two keys that cannot collide. A pipeline carries
	// the declaration by reference (see ActionField) rather than re-declaring or
	// re-spelling it; the value is the carrying pipeline's own, stored under its
	// slot, so two pipelines may offer the same action's tunable without either
	// reading the other's answer.
	Fields []ImageFieldDef
}

// ImageActionParamThinking is the bare name of the thinking switch every shared
// action declares. The action id is added by ActionParam, so the same name in two
// actions stays two distinct keys.
const ImageActionParamThinking = "thinking_enabled"

// ActionParam namespaces one of an action's own tunables: the caption action
// declares a "thinking" switch and the key it is stored and traced under is
// "caption.thinking".
//
// The full naming rules — three families, the ownership test, the value
// vocabularies, and where to change keys when you rename one — live in
// docs/design/image-pipeline-naming.md. This comment keeps only what a caller
// needs at the call site.
//
// Declaring the bare name is the point. Two actions may each want a "thinking",
// and a pipeline may want one too, without either inventing a distinct spelling
// for the same idea: an action's params live in the action's namespace, a
// pipeline's private params live inside the pipeline's own stored map, and the
// two cannot meet. A key written by hand would need a naming rule remembered by
// everyone; derived here, it is a rule the compiler and the tests can hold.
func ActionParam(id ImageActionID, name string) string {
	return string(id) + "." + name
}

// ActionField returns the tunable an action declares under a bare name
// ("thinking"), and whether it declares one. Callers use it to carry an action's
// control into a pipeline's panel without spelling the key themselves.
func ActionField(id ImageActionID, param string) (ImageFieldDef, bool) {
	for _, spec := range SharedImageActions {
		if spec.ID != id {
			continue
		}
		for _, field := range spec.Fields {
			if field.Key == ActionParam(id, param) {
				return field, true
			}
		}
	}
	return ImageFieldDef{}, false
}

// ImageFieldDef is one tunable of an action as the settings panel sees it. Its
// json tags are snake_case to match the rest of the image API, including the
// enclosing ImagePipelineSpec.
//
// Key is either a bare name owned by the declaring pipeline ("caption_enabled")
// or an action-namespaced one built with ActionParam ("ocr.thinking_enabled").
// A key never carries a pipeline prefix: one run serves one pipeline, and the
// run's trace names it once. See docs/design/image-pipeline-naming.md.
type ImageFieldDef struct {
	// Key is the name the action reads with r.Param(key).
	Key string `json:"key"`
	// Type selects the control the panel renders.
	Type ImageFieldType `json:"type"`
	// Label and Description are written in the project's default language; the
	// frontend overlays its translations and falls back to these.
	Label       string `json:"label"`
	Description string `json:"description"`
	// Default applies when the knowledge base does not override the field.
	Default any `json:"default"`
	// Options enumerates the allowed values of an enum field.
	Options []string `json:"options"`
	// Group says which run of the panel the field belongs to: the switches
	// shown outright, the switches folded under "advanced", or the explanation
	// text folded under "notes". The zero value is ImageFieldGroupSwitch, so a
	// pipeline says nothing about groups until it has something to fold away.
	Group ImageFieldGroup `json:"group,omitempty"`
	// DecidesAction marks a field that turns one of the pipeline's actions on or
	// off, as opposed to one that tunes a call the pipeline always makes. Only
	// such a field can leave a pipeline with nothing left to do, and only such
	// a field is named by the rules of ImagePipelineRules.
	DecidesAction bool `json:"decides_action"`
}

// ImageFieldType is the control a tunable renders as.
type ImageFieldType string

const (
	// ImageFieldTypeBool renders as a switch or checkbox.
	ImageFieldTypeBool ImageFieldType = "bool"
	// ImageFieldTypeString renders as a text input.
	ImageFieldTypeString ImageFieldType = "string"
	// ImageFieldTypeEnum renders as a select over the field's Options.
	ImageFieldTypeEnum ImageFieldType = "enum"
	// ImageFieldTypeNote renders no control at all: the field is an explanation
	// the pipeline declares next to the switches it explains, and the panel
	// shows it in the folded notes block. It carries no value, so its Key is
	// only a name for the entry.
	ImageFieldTypeNote ImageFieldType = "note"
)

// ImageFieldGroup is the run of a panel a declared field belongs to. Fields are
// declared in three consecutive runs, and the panel renders them as three
// places:
//
//  1. ImageFieldGroupSwitch — the standing switches, always on screen;
//  2. ImageFieldGroupAdvanced — switches folded under "advanced";
//  3. ImageFieldGroupNote — explanations folded under "notes".
//
// A field declared outside its run would silently jump to another part of the
// panel, so the order is not a matter of taste: TestFieldDeclarationOrder
// enforces it for every registered pipeline.
type ImageFieldGroup string

const (
	// ImageFieldGroupSwitch is a switch the panel always shows. It is the zero
	// value, so a standing switch does not have to mention its group.
	ImageFieldGroupSwitch ImageFieldGroup = "switch"
	// ImageFieldGroupAdvanced is a switch that only matters to someone tuning
	// the pipeline; the panel folds it away by default.
	ImageFieldGroupAdvanced ImageFieldGroup = "advanced"
	// ImageFieldGroupNote is explanation text: no control, no value.
	ImageFieldGroupNote ImageFieldGroup = "note"
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
		Fields: []ImageFieldDef{
			{
				Key:   ActionParam(ImageActionCaption, ImageActionParamThinking),
				Type:  ImageFieldTypeBool,
				Group: ImageFieldGroupAdvanced,
				Label: "Caption with thinking",
				// Warning, not recommendation: on some models long reasoning can
				// crowd out the answer itself and leave the caption empty.
				Description: "Let the model think before writing the caption. " +
					"Rarely needed: it costs latency, and on some models long " +
					"reasoning truncates the caption. Enable only when necessary.",
				Default: false,
				// No DecidesAction: an image is still captioned with thinking off,
				// just faster and with less care, so this switch cannot be the one
				// that leaves the pipeline with nothing to do.
			},
		},
	},
	{
		ID:     ImageActionObservation,
		Impl:   ImageActionImplVLM,
		Writes: []string{"attrs", "caption"},
		// Observing reads the image and the registered attribute table; the
		// caption is a by-product of that same request.
		Reads:   []string{"attrs"},
		Version: "v1",
		Fields: []ImageFieldDef{
			{
				// The observe call is its own action, so it owns its own
				// thinking switch: the default pipeline's caption and this
				// round are different work on the same image, and tuning one
				// must not tune the other.
				Key:   ActionParam(ImageActionObservation, ImageActionParamThinking),
				Type:  ImageFieldTypeBool,
				Group: ImageFieldGroupAdvanced,
				Label: "Observe and describe with thinking",
				Description: "Let the model think before observing the attributes and describing " +
					"the image. Rarely needed: it costs latency, and on some models long " +
					"reasoning truncates the answer. Enable only when necessary.",
				Default: false,
			},
		},
	},
	{
		ID:     ImageActionOCR,
		Impl:   ImageActionImplVLM,
		Writes: []string{"ocr_text"},
		// The OCR prompt is deliberately not built from the knowledge base
		// custom instructions, so it does not read the same slot the other two
		// does. See buildVLMOCRPrompt.
		Version: "v1",
		Fields: []ImageFieldDef{
			{
				Key:   ActionParam(ImageActionOCR, ImageActionParamThinking),
				Type:  ImageFieldTypeBool,
				Group: ImageFieldGroupAdvanced,
				Label: "OCR with thinking",
				Description: "Let the model think before transcribing. " +
					"Rarely needed: it costs latency, and on some models long " +
					"reasoning truncates the text. Enable only when necessary.",
				Default: false,
				// Same reasoning: an image with no text still yields no OCR.
			},
		},
	},
	{
		ID:   ImageActionPreprocess,
		Impl: ImageActionImplPure,
		// Writes the slot it may fill; whether the slot is filled depends on the
		// observed attributes and on whether a preprocessor is reachable at all.
		Writes: []string{"preprocessed_image"},
		// Reads the attributes it decides on — the OCR policy's own signal
		// (contain.text), so a pipeline that never observed cannot preprocess.
		Reads:   []string{"attrs"},
		Version: "v1",
		Fields: []ImageFieldDef{
			{
				// How much say this action has in whether it runs. It is the
				// action's own tunable: any pipeline that schedules the action
				// offers this field under its own slot, and a pipeline that
				// schedules it for a different purpose still gets the same
				// meaning.
				Key:     ActionParam(ImageActionPreprocess, "mode"),
				Type:    ImageFieldTypeEnum,
				Default: PreprocessModeOff,
				Label:   "Document preprocessing",
				Description: "Straighten and crop a photographed page before OCR reads it. " +
					"Auto asks the describe round whether the page needs it; Off never does; " +
					"Always does it unconditionally.",
				Options: []string{PreprocessModeAuto, PreprocessModeOff, PreprocessModeAlways},
			},
		},
	},
}

// ImagePipelineID labels which image pipeline produced a run. It is recorded on
// the per-image subspan input, because reading the stored image_info alone cannot
// distinguish an image whose pipeline observed the attributes and then skipped
// OCR from one whose pipeline never observed anything.
type ImagePipelineID string

const (
	// ImagePipelineSmartOCR observes the registered attributes first, captions
	// in the same answer, then OCRs only when the attribute policy allows it.
	// The id promises that observation comes before the decision; it does not
	// promise that those three actions are the whole story.
	ImagePipelineSmartOCR ImagePipelineID = "smartocr"
	// ImagePipelineDefault is the manual path: the user picks which parsing
	// actions run, and each runs for every image with no observation and no
	// policy. It is the fallback whenever nothing else has been chosen.
	ImagePipelineDefault ImagePipelineID = "default"
	// ImagePipelineLegacyObCapOCR is the id ImagePipelineSmartOCR carried
	// before it was renamed to match the label the panel shows. Configs saved
	// since 2026-09 may name it, so it is normalized rather than honoured:
	// see NormalizeImagePipelineID.
	ImagePipelineLegacyObCapOCR ImagePipelineID = "ob_cap_ocr"
	// ImagePipelineLegacyCaptionOCR is the id ImagePipelineDefault carried
	// before the panel was reworked. Stored configs may still name it, so it
	// is normalized rather than honoured: see NormalizeImagePipelineID.
	ImagePipelineLegacyCaptionOCR ImagePipelineID = "caption_ocr"
)

// NormalizeImagePipelineID maps a stored pipeline id onto the id this build
// registers. A rename must not strand the knowledge bases that stored the old
// spelling, so every id that once named a shipped pipeline keeps resolving to
// its successor; unknown ids pass through unchanged and the caller falls back.
func NormalizeImagePipelineID(id ImagePipelineID) ImagePipelineID {
	switch id {
	case ImagePipelineLegacyObCapOCR:
		return ImagePipelineSmartOCR
	case ImagePipelineLegacyCaptionOCR:
		return ImagePipelineDefault
	}
	return id
}

// ImagePipelineSpec is how a pipeline presents itself to the settings panel.
// The frontend reads this shape instead of knowing any pipeline by name, so
// adding a pipeline is one registered file plus its translation, with no UI
// change; Fields carry the same shapes the panel renders today.
type ImagePipelineSpec struct {
	// ID matches imagePipeline.ID(); it is what the knowledge base stores.
	ID ImagePipelineID `json:"id"`
	// Name is the human-readable label; the frontend overlays its translation
	// and falls back to this text.
	Name string `json:"name"`
	// Description tells the user how this pipeline works, shown under the
	// pick. Like Name it is overlay-translated with this text as fallback.
	Description string `json:"description"`
	// Fields are this pipeline's private tunables. The frontend renders one
	// control per field and posts them back under the pipeline's own slot, so
	// two pipelines can each own a field named "ocr_enabled" without either
	// seeing the other's.
	Fields []ImageFieldDef `json:"fields"`
	// Rules are conditions over those fields that have to hold before the
	// knowledge base can save them. Empty means the pipeline accepts any
	// combination of what it offers.
	Rules []ImagePipelineRules `json:"rules,omitempty"`
}

// ImagePipelineIDFor maps the attribute-observation switch to a pipeline id, so
// the trace label and the pipeline that actually runs can never disagree.
func ImagePipelineIDFor(attrsEnabled bool) ImagePipelineID {
	if attrsEnabled {
		return ImagePipelineSmartOCR
	}
	return ImagePipelineDefault
}

// ImagePipelineRules are the conditions a pipeline's tunables have to meet
// before they can be saved. They travel with the pipeline's spec, and the
// settings panel evaluates them generically: the frontend knows no rule of its
// own, it only reads what the pipeline here declared and reports the result.
// A pipeline whose choices cannot conflict — the smart one schedules its own
// actions — declares none, and no switch it offers can ever be called invalid.
type ImagePipelineRules struct {
	// AtLeastOne is satisfied when at least one field of Fields is on, and
	// fails when all of them are off: a set of switches that would leave the
	// image with nothing done to it.
	AtLeastOne []string `json:"at_least_one,omitempty"`
	// MessageKey is the frontend i18n key for the warning shown when this rule
	// is not met.
	MessageKey string `json:"message_key"`
	// Field names the control the panel points at, so the user is told which
	// switch to turn back on rather than simply that something is wrong.
	// The first of Fields when it is left empty.
	Field string `json:"field,omitempty"`
}

// ImagePipelineValidationError is what a pipeline returns when the tunables a
// knowledge base saved for it cannot be run as they stand. It carries an i18n
// key rather than a sentence: the settings panel asks the backend whether what
// it shows can be saved, and every string on that panel is translated on the
// frontend — a bare English sentence here would be the one string in the UI
// that escapes the overlay.
type ImagePipelineValidationError struct {
	// MessageKey is the frontend i18n key naming what is wrong.
	MessageKey string `json:"message_key"`
	// Field names the field at fault when a single one is. The panel points at
	// that control instead of at the section, which is what makes "switch one
	// of them back on" actionable. Empty means the whole configuration is at
	// fault.
	Field string `json:"field,omitempty"`
}

func (e *ImagePipelineValidationError) Error() string {
	if e.Field == "" {
		return "image pipeline configuration is invalid: " + e.MessageKey
	}
	return "image pipeline field " + e.Field + " is invalid: " + e.MessageKey
}
