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

func (obCapOcrPipeline) Description() string {
	return "Observe the image's attributes and describe it first, then decide " +
		"from the attributes whether OCR is worth a pass, saving model calls."
}

// Field keys of this pipeline. They deliberately do not reuse the names of
// the default pipeline's switches: this pipeline answers "may OCR be spent at
// all",
// which is a ceiling on the policy rather than a step to run, and folding the
// two into one field would make an image's fate depend on which pipeline it
// happened to be handled by.
const (
	obFieldKeyAllowOCR       = "allow_ocr"
	obFieldKeyCaptureCaption = "capture_caption"
)

// Fields declares this pipeline's panel controls. There are none on purpose:
// the model decides from the observed attributes whether OCR is worth a second
// pass, and the caption the observation produced is always kept. The two
// tunables below stay readable as parameters, so an API caller can still set a
// hard ceiling, but they are not offered as switches — an untouched knowledge
// base therefore behaves exactly as it does today, with both defaults true.
// The empty slice (not nil) keeps the endpoint's JSON an array rather than
// null, so the panel contract stays uniform across pipelines.
func (obCapOcrPipeline) Fields() []types.ImageFieldDef { return []types.ImageFieldDef{} }

func (obCapOcrPipeline) Run(ctx context.Context, r *runContext) error {
	// One request fills both the attribute slots and the caption slot, so there
	// is no separate caption action to schedule here. The effective values are
	// recorded before the branches below, not after: the skipped-OCR path is
	// exactly the one where knowing which switches were in force matters most.
	r.out["params"] = types.JSONMap{
		obFieldKeyAllowOCR:       r.BoolParamOr(obFieldKeyAllowOCR, true),
		obFieldKeyCaptureCaption: r.BoolParamOr(obFieldKeyCaptureCaption, true),
	}
	if err := r.execute(ctx, types.ImageActionObservationCaption); err != nil {
		return err
	}

	// The policy reads the attributes the action above just wrote; the ceiling
	// only says whether OCR is allowed at all. Both lines are recorded even
	// when OCR is skipped, because the trace has to show the decision that was
	// made rather than an OCR chunk that is simply missing.
	want := r.BoolParamOr(obFieldKeyAllowOCR, true) &&
		DecideOCR(r.imageInfo.Attrs, r.payload.ImageActions)
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
