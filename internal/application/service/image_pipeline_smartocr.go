package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

func init() { registerImagePipeline(smartOcrPipeline{}) }

// smartOcrPipeline observes first, then decides what to spend next. The id
// promises that observation comes before the decision, not that caption and OCR
// are the only things that can happen afterwards — a refine or validate action
// between the two does not make this name a lie.
type smartOcrPipeline struct{}

func (smartOcrPipeline) ID() types.ImagePipelineID { return types.ImagePipelineSmartOCR }

func (smartOcrPipeline) Name() string { return "Observe, caption, then decide on OCR" }

func (smartOcrPipeline) Description() string {
	return "Observe the image's attributes and describe it first, then decide " +
		"from the attributes whether OCR is worth a pass, saving model calls."
}

// Field keys of this pipeline. allow_ocr and capture_caption are
// PIPELINE-private keys (this pipeline answers "may OCR be spent at all",
// which is a ceiling on the policy rather than a step to run) and deliberately
// do not reuse the default pipeline's step switches. The thinking switches are
// ACTION-level keys (caption.thinking / ocr.thinking) shared with the default
// pipeline: the same action means the same tunable wherever it runs, so there
// is nothing to isolate and no reason to invent per-pipeline names.
const (
	smartFieldKeyAllowOCR       = "allow_ocr"
	smartFieldKeyCaptureCaption = "capture_caption"
)

// Fields declares this pipeline's panel controls. The model decides from the
// observed attributes whether OCR is worth a second pass, and the caption the
// observation produced is always kept, so there is no ceiling and no caption
// switch to expose. Only the two thinking switches are offered: they change
// how the two model calls work, not which ones happen.
//
// The caption key here is the same one the manual pipeline renders, and it
// drives the same action — this pipeline reaches the caption through the
// observation call rather than through a caption action of its own. A knowledge
// base that never touched these switches still behaves as it always did.
func (smartOcrPipeline) Fields() []types.ImageFieldDef {
	return []types.ImageFieldDef{
		// The thinking switches are action-level fields, declared once at the
		// action layer and carried in here.
		imageActionThinkingFields[0],
		imageActionThinkingFields[1],
	}
}

// Rules returns nothing, and that is the whole answer to "what may not this
// pipeline be set to?": observation, description and OCR are scheduled by the
// attributes the model observed, not by the user, so no combination of the two
// switches it offers can leave an image untouched. The panel, which evaluates
// rules generically, therefore never warns here.
func (smartOcrPipeline) Rules() []types.ImagePipelineRules {
	return nil
}

// Validate accepts everything: see Rules above.
func (smartOcrPipeline) Validate(_ map[string]any) error {
	return nil
}

func (smartOcrPipeline) Run(ctx context.Context, r *runContext) error {
	// The keys the shared actions read, set before the observation runs so the
	// OCR branch below leaves them filled in either way. The thinking switches
	// are action-level keys shared with the default pipeline.
	r.captionThinkingKey = imageFieldKeyCaptionThinking
	r.ocrThinkingKey = imageFieldKeyOCRThinking

	// One request fills both the attribute slots and the caption slot, so there
	// is no separate caption action to schedule here. The allow_ocr and
	// capture_caption ceilings are deliberately NOT recorded on the trace: the
	// panel never declares them (only the two thinking switches), so they are
	// always their defaults — recording them read as if a decision had been
	// made ("allow_ocr=true" next to a declined OCR). The decision and the
	// inputs that actually drove it are recorded on attr_policy below.
	if err := r.execute(ctx, types.ImageActionObservationCaption); err != nil {
		return err
	}

	// The policy reads the attributes the action above just wrote; the ceiling
	// only says whether OCR is allowed at all. Both lines are recorded even
	// when OCR is skipped, because the trace has to show the decision that was
	// made rather than an OCR chunk that is simply missing. ocr_on_unobserved
	// travels with the decision: it is the clause that decides when the
	// observation failed or left attributes unanswered, and it is what tells
	// "declined on data nobody produced" apart from a confident negative.
	want := r.BoolParamOr(smartFieldKeyAllowOCR, true) &&
		DecideOCR(r.imageInfo.Attrs, r.payload.ImageActions)
	r.out["attr_policy"] = types.JSONMap{
		"ocr":               want,
		"ocr_on_unobserved": r.payload.ImageActions.OCR.OnUnobserved,
	}
	r.out["image_attrs"] = r.imageInfo.Attrs.Attrs
	if !want {
		// WHY the policy said no: a failed observation left no attributes to
		// decide on (the OnUnobserved clause declined), which is a different
		// story from attributes that were observed and voted against OCR. The
		// trace keeps the two apart instead of a generic "attr_policy" that
		// reads as if a decision was made on data nobody produced.
		observationFailed := r.out["observation_failed"] == true || r.out["caption_error"] != nil
		if observationFailed {
			logger.Infof(ctx, "[ImageMultimodal] Skipping OCR for %s (observation failed, attrs=%v)",
				r.payload.ImageURL, r.imageInfo.Attrs.Attrs)
			r.out["ocr_skipped"] = "no_ocr_after_observation_failed"
		} else {
			logger.Infof(ctx, "[ImageMultimodal] Skipping OCR for %s (attrs=%v)",
				r.payload.ImageURL, r.imageInfo.Attrs.Attrs)
			r.out["ocr_skipped"] = "attr_policy"
		}
		return nil
	}
	return r.execute(ctx, types.ImageActionOCR)
}
