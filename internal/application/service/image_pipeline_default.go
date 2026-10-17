package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

func init() { registerImagePipeline(defaultPipeline{}) }

// defaultPipeline is the manual path: the switches on the panel decide which
// parsing actions run, and each chosen action runs for every image, with no
// observation and no policy. The id is "default" on purpose — it is what a
// knowledge base falls back to when nothing else was picked, and it is where
// every registered action becomes a manually selectable switch as they land.
type defaultPipeline struct{}

func (defaultPipeline) ID() types.ImagePipelineID { return types.ImagePipelineDefault }

func (defaultPipeline) Name() string { return "Manual: pick the parsing actions" }

func (defaultPipeline) Description() string {
	return "The user switches the parsing actions on or off to fit the task."
}

// Field keys of this pipeline. They are declared as constants because the
// default lives in the same struct as the key: a panel that renders the field
// under one spelling and the run that reads it under another is the exact bug
// this shape is meant to make impossible.
const (
	captionFieldKeyCaptionEnabled = "caption_enabled"
	captionFieldKeyOCREnabled     = "ocr_enabled"
	// Not a param: a note is explanation text, and its key only names the entry.
	captionFieldKeyNoteActions = "note_actions"
)

// ImageCaptionSwitchKey and ImageOCRSwitchKey are this pipeline's two action
// switches, under the names it declares them. They are exported because the
// knowledge-base PATCH handler maps the API's image_caption_enabled /
// image_ocr_enabled onto them: a second spelling of the key at that call site is
// exactly how a rename comes to write a setting nothing reads.
const (
	ImageCaptionSwitchKey = captionFieldKeyCaptionEnabled
	ImageOCRSwitchKey     = captionFieldKeyOCREnabled
)

// Fields declares the switches the panel renders for this pipeline. Caption and
// OCR default to true, which is what a knowledge base configured before these
// fields existed gets: both actions run, as before. The two thinking switches
// default to false for the opposite reason — they are new work, and an action
// that reasons costs a second full pass before it can say anything at all.
//
// Declared in the three runs the panel renders: the two standing switches, then
// the advanced thinking switches, then the note. The thinking switches are
// action-level fields, declared once at the action layer and carried in here —
// a pipeline does not re-declare an action's tunable, only where that tunable
// belongs in its own panel. What it carries is the declaration: the value a
// knowledge base stores for it sits in this pipeline's own slot.
func (defaultPipeline) Fields() []types.ImageFieldDef {
	return []types.ImageFieldDef{
		{
			Key:           captionFieldKeyCaptionEnabled,
			Type:          types.ImageFieldTypeBool,
			Label:         "Enable caption",
			Description:   "Ask the model for a one-line description of every image and store it as the caption.",
			Default:       true,
			DecidesAction: true,
		},
		{
			Key:           captionFieldKeyOCREnabled,
			Type:          types.ImageFieldTypeBool,
			Label:         "Enable OCR",
			Description:   "Extract the text that appears in the image.",
			Default:       true,
			DecidesAction: true,
		},
		// The thinking switches are action-level fields, declared once by the
		// actions themselves and carried in here by reference — the value each
		// one reads is this pipeline's own, stored under its slot.
		captionThinkingField,
		ocrThinkingField,
		{
			Key:   captionFieldKeyNoteActions,
			Type:  types.ImageFieldTypeNote,
			Group: types.ImageFieldGroupNote,
			Label: "The two actions are independent",
			Description: "Switch them separately: with OCR off the image is still described, " +
				"and with caption off its text is still read. Only switching both off leaves " +
				"an image untouched, which is why the panel refuses that combination.",
		},
	}
}

// Rules is what the panel has to be told: caption and OCR can each be turned
// off by hand, and a knowledge base that turned off both would process every
// image by doing nothing to it, so at least one has to stay on. The thinking
// switches are deliberately absent — they tune the calls, and the actions they
// tune run whether they are on or off.
//
// The pair reads as one rule and not two because "off and off" is the only
// forbidden combination: caption off alone still leaves the image OCRed, and
// OCR off alone still leaves it described.
func (defaultPipeline) Rules() []types.ImagePipelineRules {
	return []types.ImagePipelineRules{{
		AtLeastOne: []string{captionFieldKeyCaptionEnabled, captionFieldKeyOCREnabled},
		MessageKey: "imagePipeline.errors.noActionEnabled",
	}}
}

// Validate is the rule above, enforced where it cannot be argued with.
func (defaultPipeline) Validate(params map[string]any) error {
	return firstImagePipelineError(EvaluateImagePipelineRules(
		defaultPipeline{}.ID(), defaultPipeline{}.Rules(), defaultPipeline{}.Fields(), params,
	))
}

func (defaultPipeline) Run(ctx context.Context, r *runContext) error {
	// The keys the shared actions read. Set before anything runs, so that a
	// guard returning early below still leaves them filled in.
	r.captionThinkingKey = captionThinkingField.Key
	r.ocrThinkingKey = ocrThinkingField.Key
	// This pipeline's own switches, handed to the actions: each action reads its
	// gate and records that it was switched off, so "off" and "never scheduled"
	// cannot look alike in the trace.
	r.captionGateKey = captionFieldKeyCaptionEnabled
	r.ocrGateKey = captionFieldKeyOCREnabled

	// What this run was configured to do, under the keys the knowledge base
	// stores it under: the config may change after the run, and a trace that
	// cannot be read back against the settings that produced it cannot be
	// audited.
	r.recordConfig()

	// Both actions always run: each reads its own gate and records that it was
	// switched off. Scheduling around the switch instead would leave the trace
	// unable to tell "off" from "never ran" — the pair this pipeline used to
	// need a params snapshot to explain.
	if err := r.execute(ctx, types.ImageActionCaption); err != nil {
		return err
	}
	return r.execute(ctx, types.ImageActionOCR)
}
