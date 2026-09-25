package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

func init() { registerImagePipeline(obCapOcrPipeline{}) }

// obCapOcrPipeline observes first, then decides what to spend next. The id
// promises that observation comes before the decision, not that caption and OCR
// are the only things that can happen afterwards — a refine or validate action
// between the two does not make this name a lie.
type obCapOcrPipeline struct{}

func (obCapOcrPipeline) ID() types.ImagePipelineID { return types.ImagePipelineObCapOCR }

func (obCapOcrPipeline) Name() string { return "Observe, caption, then decide on OCR" }

// Fields returns nil: this pipeline exposes no tunable yet.
func (obCapOcrPipeline) Fields() []types.ImageFieldDef { return nil }

func (obCapOcrPipeline) Run(ctx context.Context, r *runContext) error {
	// One request fills both the attribute slots and the caption slot, so there
	// is no separate caption action to schedule here.
	if err := r.execute(ctx, types.ImageActionObservationCaption); err != nil {
		return err
	}

	// The policy reads the attributes the action above just wrote; the switch
	// only says whether OCR is allowed at all. Both lines are recorded even
	// when OCR is skipped, because the trace has to show the decision that was
	// made rather than an OCR chunk that is simply missing.
	want := r.payload.EnableOCR && DecideOCR(r.imageInfo.Attrs, r.payload.ImageActions)
	r.out["attr_policy"] = types.JSONMap{"ocr": want}
	r.out["image_attrs"] = r.imageInfo.Attrs.Attrs
	if !want {
		logger.Infof(ctx, "[ImageMultimodal] Skipping OCR for %s (attrs=%v)",
			r.payload.ImageURL, r.imageInfo.Attrs.Attrs)
		r.out["ocr_skipped"] = "attr_policy"
		return nil
	}
	return r.execute(ctx, types.ImageActionOCR)
}
