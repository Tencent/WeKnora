package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

func init() { registerImagePipeline(captionOcrPipeline{}) }

// captionOcrPipeline is the plain path: caption every image, then OCR every
// image, with no observation and no policy. The whole-task switches live in the
// payload and gate each action here, so the pipeline stays a plain sequence.
type captionOcrPipeline struct{}

func (captionOcrPipeline) ID() types.ImagePipelineID { return types.ImagePipelineCaptionOCR }

func (captionOcrPipeline) Name() string { return "Caption every image, then OCR every image" }

// Field keys of this pipeline. They are declared as constants because the
// default lives in the same struct as the key: a panel that renders the field
// under one spelling and the run that reads it under another is the exact bug
// this shape is meant to make impossible.
const (
	captionFieldKeyEnableCaption = "enable_caption"
	captionFieldKeyEnableOCR     = "enable_ocr"
)

// Fields declares the two switches the panel renders for this pipeline. Both
// default to true, which is what a knowledge base configured before these
// fields existed gets: caption and OCR, as before.
func (captionOcrPipeline) Fields() []types.ImageFieldDef {
	return []types.ImageFieldDef{
		{
			Key:         captionFieldKeyEnableCaption,
			Type:        types.ImageFieldTypeBool,
			Label:       "Enable caption",
			Description: "Ask the model for a one-line description of every image and store it as the caption.",
			Default:     true,
		},
		{
			Key:         captionFieldKeyEnableOCR,
			Type:        types.ImageFieldTypeBool,
			Label:       "Enable OCR",
			Description: "Extract the text every image carries, whether or not the image contains any.",
			Default:     true,
		},
	}
}

func (captionOcrPipeline) Run(ctx context.Context, r *runContext) error {
	// Both switches belong to this pipeline alone. Neither is a whole-task
	// limit: an image handled by another pipeline may skip its caption while
	// this one still captures it, so the field names are only meaningful
	// relative to this pipeline and are recorded under those same names.
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
