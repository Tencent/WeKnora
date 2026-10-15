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
	captionFieldKeyEnableCaption = "enable_caption"
	captionFieldKeyEnableOCR     = "enable_ocr"
)

// Fields declares the switches the panel renders for this pipeline. Caption and
// OCR default to true, which is what a knowledge base configured before these
// fields existed gets: both actions run, as before. The two thinking switches
// default to false for the opposite reason — they are new work, and an action
// that reasons costs a second full pass before it can say anything at all.
func (defaultPipeline) Fields() []types.ImageFieldDef {
	return []types.ImageFieldDef{
		{
			Key:           captionFieldKeyEnableCaption,
			Type:          types.ImageFieldTypeBool,
			Label:         "Enable caption",
			Description:   "Ask the model for a one-line description of every image and store it as the caption.",
			Default:       true,
			DecidesAction: true,
		},
		{
			Key:           captionFieldKeyEnableOCR,
			Type:          types.ImageFieldTypeBool,
			Label:         "Enable OCR",
			Description:   "Extract the text that appears in the image.",
			Default:       true,
			DecidesAction: true,
		},
		// The thinking switches are action-level fields, declared once at the
		// action layer and carried in here — a pipeline does not re-declare
		// an action's tunable.
		imageActionThinkingFields[0],
		imageActionThinkingFields[1],
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
		AtLeastOne: []string{captionFieldKeyEnableCaption, captionFieldKeyEnableOCR},
		MessageKey: "imagePipeline.errors.noActionEnabled",
	}}
}

// Validate is the rule above, enforced where it cannot be argued with.
func (defaultPipeline) Validate(params map[string]any) error {
	return firstImagePipelineError(
		EvaluateImagePipelineRules(defaultPipeline{}.Rules(), defaultPipeline{}.Fields(), params),
	)
}

func (defaultPipeline) Run(ctx context.Context, r *runContext) error {
	// The keys the shared actions read. Set before anything runs, so that a
	// guard returning early below still leaves them filled in.
	r.captionThinkingKey = imageFieldKeyCaptionThinking
	r.ocrThinkingKey = imageFieldKeyOCRThinking

	// Both switches belong to this pipeline alone. Neither is a whole-task
	// limit: an image handled by another pipeline may skip its caption while
	// this one still captures it, so the field names are only meaningful
	// relative to this pipeline and are recorded under those same names.
	// Switching both off is guarded in the panel; if an API caller does it
	// anyway, the run simply records that nothing was configured to happen.
	if r.BoolParam(captionFieldKeyEnableCaption) {
		if err := r.execute(ctx, types.ImageActionCaption); err != nil {
			return err
		}
	}
	if !r.BoolParam(captionFieldKeyEnableOCR) {
		return nil
	}
	if err := r.execute(ctx, types.ImageActionOCR); err != nil {
		return err
	}
	// Recorded so a trace row shows which switches were actually in force.
	// Without it, a switch that read false and an action that never ran look
	// identical in the output.
	r.out["params"] = r.ParamSnapshot(captionFieldKeyEnableCaption, captionFieldKeyEnableOCR)
	return nil
}
