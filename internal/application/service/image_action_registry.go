package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

// imagePipeline is one image pipeline: a function over actions, not a data
// topology. Run owns the pipeline's whole control flow, which is why there is no
// scheduler and no round model in this package — see the image pipeline spec §2.
type imagePipeline interface {
	// ID is the stable id of the pipeline; it doubles as the value recorded on
	// the trace row.
	ID() types.ImagePipelineID
	// Name is the human-readable name shown in the settings panel.
	Name() string
	// Fields are the tunables of this pipeline; nil means it has none yet.
	Fields() []types.ImageFieldDef
	// Run executes the pipeline.
	Run(ctx context.Context, r *runContext) error
}

var imagePipelineRegistry = make(map[types.ImagePipelineID]imagePipeline)

func registerImagePipeline(p imagePipeline) {
	if _, dup := imagePipelineRegistry[p.ID()]; dup {
		// init() order inside a package is file-name order, so a silent
		// overwrite would sit here until someone noticed the wrong pipeline ran.
		panic(fmt.Sprintf("image pipeline: duplicate pipeline id %q", p.ID()))
	}
	imagePipelineRegistry[p.ID()] = p
}

// selectImagePipeline maps the attribute-observation switch onto the pipeline
// that answers it. Both pipelines are registered by their own file's init().
func selectImagePipeline(attrsEnabled bool) imagePipeline {
	return imagePipelineRegistry[types.ImagePipelineIDFor(attrsEnabled)]
}

// actionHandler is the executable half of an image action. There is deliberately
// no service receiver: every current action is a pure function of the run
// context, so running one needs no wiring.
type actionHandler func(ctx context.Context, r *runContext) error

// actionSpec joins the declarative half declared in types with its handler.
type actionSpec struct {
	types.ImageActionSpec
	handler actionHandler
}

var imageActionRegistry = make(map[types.ImageActionID]actionSpec)

func registerAction(spec actionSpec) {
	if _, dup := imageActionRegistry[spec.ID]; dup {
		panic(fmt.Sprintf("image pipeline: duplicate action id %q", spec.ID))
	}
	imageActionRegistry[spec.ID] = spec
}

// init binds the shared actions to their handlers. The bindings live here rather
// than next to the declarations so that "every action is registered in exactly
// one place" stays true as pipelines grow.
func init() {
	handlers := map[types.ImageActionID]actionHandler{
		types.ImageActionCaption:            runCaptionAction,
		types.ImageActionObservationCaption: runObservationCaptionAction,
		types.ImageActionOCR:                runOCRAction,
	}
	for _, spec := range types.SharedImageActions {
		handler, bound := handlers[spec.ID]
		if !bound {
			panic(fmt.Sprintf("image pipeline: no handler bound to shared action %q", spec.ID))
		}
		registerAction(actionSpec{ImageActionSpec: spec, handler: handler})
	}
}

// runContext is the only framework entry point a pipeline or an action gets. It
// hands out no VLM handle: talking to a model is the action's business, reached
// only through execute.
type runContext struct {
	payload    *types.ImageMultimodalPayload
	model      vlm.VLM
	imageBytes []byte
	vlmCfg     types.VLMConfig
	imageInfo  *types.ImageInfo
	out        types.JSONMap
	// actions records the actions this run actually executed, in order. It is
	// the per-image answer to "what did this image go through", which a trace
	// row's pipeline label alone cannot tell.
	actions []types.ImageActionID
}

// execute runs one action by id. A missing or unbound action costs that one
// field, not the whole image: interrupting would drop every later slot of the
// same image for the sake of a warning.
func (r *runContext) execute(ctx context.Context, id types.ImageActionID) error {
	spec, ok := imageActionRegistry[id]
	if !ok || spec.handler == nil {
		logger.Warnf(ctx, "[ImageMultimodal] Action %q is not implemented; skipping", id)
		r.out["action_skipped"] = string(id)
		return nil
	}
	r.actions = append(r.actions, id)
	return spec.handler(ctx, r)
}

// runCaptionAction asks for a one-line description and stores it as the caption.
func runCaptionAction(ctx context.Context, r *runContext) error {
	raw, err := r.model.Predict(ctx, [][]byte{r.imageBytes}, buildVLMCaptionPrompt(ctx, r.vlmCfg))
	if err != nil {
		// Only recorded, not logged: an observation failure below is the same
		// kind of event and logs its own line, and a caption miss must not be
		// louder than the rest of the run.
		r.out["caption_error"] = err.Error()
		return nil
	}
	if text := strings.TrimSpace(raw); text != "" {
		r.imageInfo.Caption = text
		r.out["caption_chars"] = len([]rune(text))
		r.out["caption_preview"] = previewText(text, 200)
	}
	return nil
}

// runObservationCaptionAction observes the registered attributes and describes
// the image in one answer. The description reaches the caption slot only when the
// run wants a caption — the observation itself always happens, because the
// attributes are the point of this action and the OCR policy reads them back.
func runObservationCaptionAction(ctx context.Context, r *runContext) error {
	raw, err := r.model.Predict(ctx, [][]byte{r.imageBytes}, buildImageAttrsPrompt(ctx, r.vlmCfg))
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Describe and observe failed for %s: %v", r.payload.ImageURL, err)
		r.out["caption_error"] = err.Error()
		return nil
	}
	obs, ok := types.ParseImageAttrsResponse(raw)
	applyImageObservation(r.imageInfo, obs.Attrs, obs.Description, r.out, r.payload.EnableCaption)
	if !obs.Observed {
		// The model ignored the attribute protocol — a user custom instruction
		// may have derailed the format, or it answered in prose. Nothing is
		// invented to fill the gap: the attribute table stays empty and the
		// OCR policy falls back to its conservative OnUnobserved clause.
		r.out["observation_failed"] = true
	}
	if !ok {
		r.out["caption_missing"] = true
	}
	return nil
}

// runOCRAction extracts the text the image carries. It is a plain function
// rather than a method because the OCR prompt is system-owned (knowledge base
// custom instructions must never reach it) and no service state is involved.
func runOCRAction(ctx context.Context, r *runContext) error {
	prompt := buildVLMOCRPrompt(r.payload.ImageSourceType, r.vlmCfg)
	if r.payload.ImageSourceType == "scanned_pdf" {
		logger.Infof(ctx, "[ImageMultimodal] Using scanned PDF prompt for OCR: %s", r.payload.ImageURL)
		r.out["ocr_prompt"] = "scanned_pdf"
	} else {
		r.out["ocr_prompt"] = "default"
	}

	ocrText, err := r.model.Predict(ctx, [][]byte{r.imageBytes}, prompt)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] OCR failed for %s: %v", r.payload.ImageURL, err)
		r.out["ocr_error"] = err.Error()
		return nil
	}
	ocrText = sanitizeOCRText(ocrText)
	if ocrText != "" {
		r.imageInfo.OCRText = ocrText
		r.out["ocr_chars"] = len([]rune(ocrText))
		r.out["ocr_preview"] = previewText(ocrText, 200)
		return nil
	}
	logger.Warnf(ctx, "[ImageMultimodal] OCR returned empty/invalid content for %s, discarded", r.payload.ImageURL)
	r.out["ocr_chars"] = 0
	r.out["ocr_skipped"] = "empty_or_invalid"
	return nil
}
