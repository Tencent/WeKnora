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

// Fields returns nil: this pipeline exposes no tunable yet.
func (captionOcrPipeline) Fields() []types.ImageFieldDef { return nil }

func (captionOcrPipeline) Run(ctx context.Context, r *runContext) error {
	if r.payload.EnableCaption {
		if err := r.execute(ctx, types.ImageActionCaption); err != nil {
			return err
		}
	}
	if r.payload.EnableOCR {
		return r.execute(ctx, types.ImageActionOCR)
	}
	return nil
}
