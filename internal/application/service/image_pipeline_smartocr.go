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

// Field keys of this pipeline. The fallback clause is PIPELINE-private: it says
// whether OCR is scheduled at all when the observation did not answer, which is
// orchestration rather than a step to run — so it does not borrow the OCR
// action's namespace (docs/design/image-pipeline-naming.md §2). The thinking
// switches are ACTION-level keys carried in from the actions that own them.
const (
	// smartFieldKeyOCROnUnobserved is the clause that decides whether an image
	// whose attributes were not observed is still read.
	smartFieldKeyOCROnUnobserved = "ocr_on_unobserved"
	// Not a param: a note is explanation text, and its key only names the entry.
	smartFieldKeyNoteFlow = "note_flow"
)

// smartPreprocessField is the page-straightening switch, declared by the
// preprocessing action itself and carried in here by reference: the action owns
// its tunable, the pipeline only says where it belongs on its own panel.
var smartPreprocessField = mustActionField(types.ImageActionPreprocess, "mode")

// smartOcrOnUnobservedField is the OCR policy's fallback clause as a control. It
// is declared here rather than by the OCR action because it decides whether that
// action runs at all — see the note on the key above.
var smartOcrOnUnobservedField = types.ImageFieldDef{
	Key:   smartFieldKeyOCROnUnobserved,
	Type:  types.ImageFieldTypeBool,
	Label: "OCR after an unobserved image",
	Description: "Still read an image whose attributes could not be observed, so a missed " +
		"block of text costs one extra call instead of being lost. Turn it off only with a " +
		"model reliable enough to answer every attribute line.",
	Default: true,
}

// Fields declares this pipeline's panel controls. The model decides from the
// observed attributes whether OCR is worth a second pass, and the caption the
// observation produced is always kept, so there is no ceiling and no caption
// switch to expose. Only the two thinking switches are offered: they change
// how the two model calls work, not which ones happen.
//
// The description here is the observation action's, not the caption action's:
// this pipeline reaches the caption through the observe round rather than
// through a caption action of its own, so it offers that round's switch. The
// OCR switch belongs to the OCR action. Both keys are the actions' own
// spellings, and each is stored under this pipeline's slot, so a knowledge base
// that never touched them still behaves as it always did.
//
// Declared in the three runs the panel renders: the standing switches (the
// straightening choice, which is this pipeline's whole reason to exist, and the
// clause that decides what happens when the observation does not answer), then
// the advanced thinking switches, then the note. The straightening switch lives
// here rather than in the manual pipeline's list because the manual pipeline
// has no observation round to drive it — nothing there could answer "does this
// page need it".
func (smartOcrPipeline) Fields() []types.ImageFieldDef {
	return []types.ImageFieldDef{
		smartPreprocessField,
		smartOcrOnUnobservedField,
		// The thinking switches are action-level fields, declared by the actions
		// themselves and carried in here by reference — this pipeline schedules
		// its own observe round, so it offers that round's switch, and the OCR
		// call's.
		observationThinkingField,
		ocrThinkingField,
		{
			Key:   smartFieldKeyNoteFlow,
			Type:  types.ImageFieldTypeNote,
			Group: types.ImageFieldGroupNote,
			Label: "The observation decides",
			Description: "The image is described and observed first; the attributes that come back " +
				"decide whether OCR is worth a second pass at all. When they say the page needs " +
				"straightening, it is straightened and its surroundings cropped before reading — " +
				"and if that step is unavailable or declines, the original photograph is read " +
				"instead, never something worse.",
		},
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
	// What this run was configured to do — including the fallback clause above —
	// under the keys the knowledge base stores it under, so the trace can be read
	// back against the settings that produced it.
	r.recordConfig()

	// The keys the shared actions read, set before the observation runs so the
	// OCR branch below leaves them filled in either way. The thinking switches
	// carry the spellings the actions declare — the manual pipeline offers the
	// same OCR switch — while the values come from this pipeline's own slot.
	// The describe side here is the observation action, so its own thinking switch
	// is the one this pipeline offers.
	r.captionThinkingKey = observationThinkingField.Key
	r.ocrThinkingKey = ocrThinkingField.Key

	// One request fills both the attribute slots and the caption slot, so there
	// is no separate caption action to schedule here.
	if err := r.execute(ctx, types.ImageActionObservation); err != nil {
		return err
	}

	// The policy reads the attributes the action above just wrote, and the
	// clause beside it answers for an image the observation could not describe.
	// The decision itself is recorded as ocr.result below — done means it was
	// taken, a skipped_* value says why it was not — so there is no separate
	// decision key to keep in step with it.
	want := DecideOCR(r.imageInfo.Attrs, r.payload.ImageActions, r.ocrOnUnobserved())
	r.out[keyImageAttrs] = r.imageInfo.Attrs.Attrs
	if !want {
		// WHY the policy said no: an observation that did not answer leaves no
		// attributes to decide on (the fallback clause declined), which is a
		// different story from attributes that were observed and voted against
		// OCR.
		// "No attributes to decide on" is not the same as "not all of them":
		// a partial answer is data the policy can act on, so only an observation
		// that produced nothing (or failed outright) counts as failed here.
		result := r.out[keyObservationResult]
		observationFailed := result == "none" || result == "failed"
		if observationFailed {
			logger.Infof(ctx, "[ImageMultimodal] Skipping OCR for %s (observation failed, attrs=%v)",
				r.payload.ImageURL, r.imageInfo.Attrs.Attrs)
			r.out[keyOCRResult] = "skipped_observation_failed"
		} else {
			logger.Infof(ctx, "[ImageMultimodal] Skipping OCR for %s (attrs=%v)",
				r.payload.ImageURL, r.imageInfo.Attrs.Attrs)
			r.out[keyOCRResult] = "skipped_attr_policy"
		}
		return nil
	}
	// Between the OCR decision and OCR itself: straighten the page when the
	// attributes say there is a page to straighten. A preprocessor that is
	// absent, declines, or fails leaves the image exactly as it was, so this
	// step can only help — the OCR below still reads the original bytes.
	if err := r.execute(ctx, types.ImageActionPreprocess); err != nil {
		return err
	}
	return r.execute(ctx, types.ImageActionOCR)
}
