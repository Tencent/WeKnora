package service

import (
	"context"
	"fmt"
	"sort"
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
	// Description tells the user how the pipeline works, shown under the
	// pick in the settings panel.
	Description() string
	// Fields are the tunables of this pipeline; nil means it has none yet.
	Fields() []types.ImageFieldDef
	// Rules are the conditions those tunables have to meet. Declared rather
	// than implemented because what makes a set of settings incoherent is a
	// fact about the pipeline, not about the panel that renders it: the manual
	// pipeline can have every action switched off, the smart one schedules its
	// own work and cannot be. The panel asks the backend anyway — the save
	// path re-checks what the panel approved — so nothing here has to trust it.
	Rules() []types.ImagePipelineRules
	// Validate reports whether a saved set of tunables can be run. Returning
	// nil means the pipeline accepts anything handed to it.
	Validate(params map[string]any) error
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

// selectImagePipeline resolves the pipeline a task asked for. Both pipelines
// register from their own file's init(). An empty or unregistered id falls back
// to the attribute-observation switch: that is what a payload enqueued before
// the pipeline field existed carries, and what a knowledge base saved before the
// selector existed asks for. Falling back rather than failing keeps a stored id
// that this build no longer ships from stranding its images.
func selectImagePipeline(payload *types.ImageMultimodalPayload) imagePipeline {
	id := types.ImagePipelineID(strings.TrimSpace(string(payload.ImagePipelineID)))
	return resolveImagePipeline(id, payload.ImageAttrsEnabled)
}

// resolveImagePipeline is selectImagePipeline's lookup, separated so the
// validation entry point can ask the same question about a pipeline that has
// not been turned into a payload yet.
func resolveImagePipeline(pipelineID types.ImagePipelineID, attrsEnabled bool) imagePipeline {
	fallback := types.ImagePipelineIDFor(attrsEnabled)
	// A stored id may name a pipeline by a spelling this build renamed; the
	// normalization keeps such a config on its successor instead of dropping
	// to the fallback silently.
	id := types.NormalizeImagePipelineID(pipelineID)
	if id == "" {
		id = fallback
	}
	if p, ok := imagePipelineRegistry[id]; ok {
		return p
	}
	return imagePipelineRegistry[fallback]
}

// ValidateImagePipelineParams asks the pipeline an id names whether the
// tunables a knowledge base is about to store can be run. The id is resolved
// exactly as a running task would resolve it, so the answer cannot disagree
// with the pipeline that would actually handle the images.
func ValidateImagePipelineParams(pipelineID types.ImagePipelineID, params map[string]any) error {
	return firstImagePipelineError(BrokenImagePipelineRules(pipelineID, params))
}

// BrokenImagePipelineRules lists every rule of the named pipeline that the
// stored tunables break. Unlike the single error ValidateImagePipelineParams
// returns, this keeps each violation separate so that the settings panel can
// show all of them at once — one rule per line, each pointing at the control
// that has to change. A pipeline that declares no rules yields none, which is
// how a pipeline whose actions run by themselves is never told it is wrong.
func BrokenImagePipelineRules(
	pipelineID types.ImagePipelineID,
	params map[string]any,
) []*types.ImagePipelineValidationError {
	pipeline := resolveImagePipeline(pipelineID, false)
	if pipeline == nil {
		return nil
	}
	return EvaluateImagePipelineRules(pipeline.Rules(), pipeline.Fields(), params)
}

// ListImagePipelines renders every registered pipeline as the settings panel
// sees it. Sorted by id so the panel's option order is stable and does not
// follow package init order.
func ListImagePipelines() []types.ImagePipelineSpec {
	ids := make([]types.ImagePipelineID, 0, len(imagePipelineRegistry))
	for id := range imagePipelineRegistry {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	specs := make([]types.ImagePipelineSpec, 0, len(ids))
	for _, id := range ids {
		p := imagePipelineRegistry[id]
		specs = append(specs, types.ImagePipelineSpec{
			ID:          p.ID(),
			Name:        p.Name(),
			Description: p.Description(),
			Fields:      p.Fields(),
			Rules:       p.Rules(),
		})
	}
	return specs
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

// Thinking-switch keys of the shared actions. An action may want the model to
// reason — a dense scanned page or a chart that has to be read before it can be
// transcribed — and must not pay for it on the simple majority, so the switch
// belongs to the action rather than to the model. Both pipelines read the same
// pair of keys with the same meaning, which is why they live here rather than
// beside the field declarations of either pipeline.
const (
	imageFieldKeyCaptionThinking = "caption_thinking"
	imageFieldKeyOCRThinking     = "ocr_thinking"
)

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
	// params is this pipeline's private tunables, as resolved from the
	// knowledge base. Nothing else may read it: a key understood by one
	// pipeline says nothing about another's, so the key only has to be
	// unambiguous within the pipeline that declared it.
	params map[string]any
	// declared is the pipeline's own imagePipeline.Fields(), carried along so
	// an untouched key resolves to the default the panel promised rather than
	// to a zero value. It is the same list the frontend renders.
	declared []types.ImageFieldDef
	// actions records the actions this run actually executed, in order. It is
	// the per-image answer to "what did this image go through", which a trace
	// row's pipeline label alone cannot tell.
	actions []types.ImageActionID
	// The tunable that switches thinking on for each action. A pipeline names
	// its controls after its own vocabulary — the manual pipeline speaks of a
	// caption, the observing one of a description — and no key may be declared
	// by two pipelines, so it is the pipeline, not the action, that says which
	// tunable a thinking switch is. Filled in by the pipeline before it
	// executes anything.
	captionThinkingKey string
	ocrThinkingKey     string
}

// Param reads one private tunable, falling back to the default the pipeline
// declared for it. A key the running pipeline never declared reads nil, which
// is deliberate: a stored knowledge base may carry parameters for a pipeline
// this build no longer has, and the run must not fail over a stale key.
func (r *runContext) Param(key string) any {
	if r.params != nil {
		if v, ok := r.params[key]; ok && v != nil {
			return v
		}
	}
	for _, field := range r.declared {
		if field.Key == key {
			return field.Default
		}
	}
	return nil
}

// BoolParam reads a boolean tunable. JSON hands a map[string]any an
// unmarshalled "true" as a real bool, but a config written by hand may use a
// string, so both are read; anything else falls back to false.
func (r *runContext) BoolParam(key string) bool {
	return r.BoolParamOr(key, false)
}

// BoolParamOr reads a boolean tunable the way BoolParam does, but falls back
// to def when neither the stored params nor a declared field default answers.
// A pipeline that reads a key it does not declare in Fields() — a control it
// deliberately keeps off the panel — pins its working default here, at the
// one place the key is read.
func (r *runContext) BoolParamOr(key string, def bool) bool {
	switch v := r.Param(key).(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "on"
	}
	return def
}

// ParamSnapshot renders the given keys as a plain map for the trace row.
func (r *runContext) ParamSnapshot(keys ...string) types.JSONMap {
	snapshot := make(types.JSONMap, len(keys))
	for _, key := range keys {
		if _, declared := r.fieldByKey(key); declared {
			snapshot[key] = r.Param(key)
		}
	}
	if len(snapshot) == 0 {
		return nil
	}
	return snapshot
}

// predictCaption asks for a caption, with the tunable the current pipeline
// offers for it.
func (r *runContext) predictCaption(ctx context.Context, prompt string) (string, error) {
	return r.think(ctx, prompt, r.captionThinkingKey)
}

// predictOCR transcribes text from an image, with the tunable the current
// pipeline offers for it.
func (r *runContext) predictOCR(ctx context.Context, prompt string) (string, error) {
	return r.think(ctx, prompt, r.ocrThinkingKey)
}

// think asks the model with the thinking switch the pipeline wired to this
// action. Thinking defaults to off because on a quantized reasoning model the
// reasoning spends the completion budget before a single visible token, and the
// caller cannot tell that empty answer from an image that genuinely carries no
// text — which is exactly how an OCR result comes to be discarded as invalid.
// Turning it on costs a longer run and is what a dense or degraded image wants,
// so the switch is per action, and the value reaches the trace as used.
func (r *runContext) think(ctx context.Context, prompt, fieldKey string) (string, error) {
	on := r.BoolParamOr(fieldKey, false)
	r.out[fieldKey] = on
	return r.model.PredictWithOptions(ctx, [][]byte{r.imageBytes}, prompt,
		&vlm.PredictOptions{Thinking: &on})
}

func (r *runContext) fieldByKey(key string) (types.ImageFieldDef, bool) {
	for _, field := range r.declared {
		if field.Key == key {
			return field, true
		}
	}
	return types.ImageFieldDef{}, false
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
	raw, err := r.predictCaption(ctx, buildVLMCaptionPrompt(ctx, r.vlmCfg))
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
	raw, err := r.predictCaption(ctx, buildImageAttrsPrompt(ctx, r.vlmCfg))
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Describe and observe failed for %s: %v", r.payload.ImageURL, err)
		r.out["caption_error"] = err.Error()
		return nil
	}
	obs, ok := types.ParseImageAttrsResponse(raw)
	// Whether the description reaches the caption slot is this pipeline's
	// business, so the observation reads its own key rather than a payload
	// field. The key is not declared in Fields(), so the working default is
	// pinned here: with it, the caption stays a by-product; an API caller
	// passing false observes the attributes and records nothing to caption.
	applyImageObservation(r.imageInfo, obs.Attrs, obs.Description, r.out,
		r.BoolParamOr(smartFieldKeyCaptureCaption, true))
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

	ocrText, err := r.predictOCR(ctx, prompt)
	if err != nil {
		// A model-side failure is not "this image carries no text". Any non-2xx
		// answer — 429, 5xx, a timeout, a transport reset, and the 4xx the
		// endpoint may reject a request with (400/413/431) — arrives here as one
		// error, and the caller deliberately does not classify it. Swallowing it
		// is what let a failed OCR be recorded as a finished image with no
		// content, the loss #4064 describes. Return it so the pipeline fails and
		// the task retries; transport-level retry is the manager's job, and an
		// image that genuinely carries nothing stays the empty branch below.
		logger.Warnf(ctx, "[ImageMultimodal] OCR failed for %s: %v", r.payload.ImageURL, err)
		r.out["ocr_error"] = err.Error()
		return err
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
