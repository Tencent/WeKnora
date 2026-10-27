package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

// imagePipeline is one image pipeline: a function over actions, not a data
// topology. Run owns the pipeline's whole control flow, which is why there is no
// scheduler and no round model in this package — see the image pipeline spec §2.
// The keys this file writes into runContext.out — and the ones a pipeline's own
// run writes — follow docs/design/image-pipeline-naming.md: settings live in the
// config container, an action's state and data are prefixed with the action that
// wrote them, and image-level facts carry no prefix. Read that document before
// adding a key; the rules it states are enforced by the tests named there.
type imagePipeline interface {
	// ID is the stable id of the pipeline; it doubles as the value recorded on
	// the trace row.
	ID() types.ImagePipelineID
	// Name is the human-readable name shown in the settings panel.
	Name() string
	// Description tells the user how the pipeline works, shown under the
	// pick in the settings panel.
	Description() string
	// Fields are the tunables and the explanations of this pipeline; nil means
	// it has none yet.
	//
	// DECLARE THEM IN THREE RUNS, IN THIS ORDER:
	//
	//	1. GroupSwitch   — the standing switches, in the order the panel shows
	//	                   them;
	//	2. GroupAdvanced — the switches that belong under "advanced", which the
	//	                   panel folds away;
	//	3. GroupNote     — the explanations, which the panel folds away under
	//	                   "notes".
	//
	// Within a run, declaration order is panel order, so a pipeline that wants
	// a switch above another says so here. Reordering or regrouping a panel is
	// a change to this file and to nothing else: the frontend cannot do it,
	// because it cannot tell a tunable of the describe call from a tunable of
	// the read call, and a panel that guessed would silently misplace them.
	// TestFieldDeclarationOrder holds every pipeline to the three runs.
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
	return EvaluateImagePipelineRules(pipeline.ID(), pipeline.Rules(), pipeline.Fields(), params)
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

// imagePreprocessor is the document-straightening capability a pipeline may
// use before OCR: given the run's image it returns a straightened, content-tight
// version of it. It is deliberately an interface local to this package — the
// action must not learn which transport (gRPC, in-process, …) serves it.
//
// A nil preprocessor is a normal state, not an error: builds without the
// document service wired simply OCR the original image.
type imagePreprocessor interface {
	// PreprocessImage returns (rectified, fallbackNeeded, err). fallbackNeeded
	// reports "the input is not a correctable page, or the result would be
	// worse than the original" — a verdict, not a failure, so the caller must
	// keep its original bytes without logging an error.
	PreprocessImage(ctx context.Context, image []byte) ([]byte, bool, error)
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
		types.ImageActionCaption:     runCaptionAction,
		types.ImageActionObservation: runObservationCaptionAction,
		types.ImageActionOCR:         runOCRAction,
		types.ImageActionPreprocess:  runPreprocessAction,
	}
	for _, spec := range types.SharedImageActions {
		handler, bound := handlers[spec.ID]
		if !bound {
			panic(fmt.Sprintf("image pipeline: no handler bound to shared action %q", spec.ID))
		}
		registerAction(actionSpec{ImageActionSpec: spec, handler: handler})
	}
}

// The action-level tunables this package schedules, looked up from the actions
// that declare them rather than re-declared here: an action owns its params, and
// a pipeline's panel offers the same field def the action published (see
// types.ActionField). The lookup panics rather than returning a zero field — a
// missing declaration is a programming error, not a mode of operation.
var (
	captionThinkingField     = mustActionField(types.ImageActionCaption, types.ImageActionParamThinking)
	observationThinkingField = mustActionField(types.ImageActionObservation, types.ImageActionParamThinking)
	ocrThinkingField         = mustActionField(types.ImageActionOCR, types.ImageActionParamThinking)
)

// keyObservationCaptionEnabled gates the observation round's caption sub-item:
// the round always observes, and this says whether it also writes the description
// down. It is an action-level key ("<action>.<name>") and is deliberately not
// declared in Fields() — no panel exposes it, so it stays at its working default
// (on) unless an API caller sets it.
var keyObservationCaptionEnabled = types.ActionParam(
	types.ImageActionObservation, "caption_enabled")

func mustActionField(id types.ImageActionID, param string) types.ImageFieldDef {
	field, ok := types.ActionField(id, param)
	if !ok {
		panic(fmt.Sprintf("image pipeline: action %q declares no %q tunable", id, param))
	}
	return field
}

// Trace keys that code reads back — the "contract" keys (docs/design/image-pipeline-naming.md §5).
// Writer and reader share the constant, so renaming one is a single edit and the
// readers are named right here:
//
//	keyOCRResult    read by resolve() below (success vs failure of the OCR span)
//	                and by the service layer (extract-nothing verdict, task error)
//	keyOCRErrorCode read by resolve() below; lands on the span's error_code, which
//	                the processing timeline turns into a localized title
//	keyOCRError     read by resolve() below; the span's error message
//	keyOCRChars     read by resolve() below; the span's summary
const (
	keyOCRResult    = "ocr.result"
	keyOCRErrorCode = "ocr.error_code"
	keyOCRError     = "ocr.error"
	keyOCRChars     = "ocr.chars"
)

// OCR's diagnostic keys — written and never read back. They stay literals
// because a single writer cannot drift out of step with itself.
const (
	keyOCRRawChars        = "ocr.raw_chars"
	keyOCRPreview         = "ocr.preview"
	keyOCRPrompt          = "ocr.prompt"
	keyOCRRejectionReason = "ocr.rejection_reason"
)

// The describe side's keys. The caption action writes the caption.* set; the
// observation action writes the observation.* set — the same description, but a
// different action produced it, and the trace names the action that did the work
// (docs/design/image-pipeline-naming.md §1).
const (
	keyCaptionResult             = "caption.result"
	keyCaptionChars              = "caption.chars"
	keyCaptionPreview            = "caption.preview"
	keyCaptionError              = "caption.error"
	keyObservationResult         = "observation.result"
	keyObservationCaptionResult  = "observation.caption_result"
	keyObservationCaptionChars   = "observation.caption_chars"
	keyObservationCaptionPreview = "observation.caption_preview"
	keyObservationError          = "observation.error"
)

// The preprocess action's keys.
const (
	keyPreprocessResult = "preprocess.result"
	keyPreprocessBytes  = "preprocess.bytes"
	keyPreprocessError  = "preprocess.error"
)

// imagePreprocessTimeout caps one page-straightening call. It is a guard
// against a document service that hangs, not a budget the algorithm is meant to
// fit in: a full-size photograph takes around 9 seconds once the recogniser is
// warm, and the first call in a fresh process measured 23 seconds (model load
// plus engine construction). The cap is set well above both, so a legitimate
// slow page is never cut short — and it stays an order of magnitude below the
// docreader RPC timeout, which is what a hung call would otherwise consume.
const imagePreprocessTimeout = 60 * time.Second

// Frame-level keys: the image's own facts, written by the service layer, so they
// carry no action prefix.
const (
	keyOutcome       = "outcome"
	keyActions       = "actions"
	keyConfig        = "config"
	keyImageAttrs    = "image_attrs"
	keyImageBytes    = "image_bytes"
	keyChunksCreated = "chunks_created"
	keyIndexed       = "indexed"
	keyReadError     = "error"
)

// The value vocabularies. They live in code (not only in the document) because a
// guard test checks every trace value against them: a new value has to be added
// here deliberately, which is what keeps the words readable a year from now.
var (
	captionResultValues     = []string{"done", "empty", "skipped_disabled", "failed"}
	observationResultValues = []string{"all_done", "partial", "none", "failed"}
	ocrResultValues         = []string{
		"done", "failed", "skipped_disabled", "skipped_no_text",
		"skipped_attr_policy", "skipped_observation_failed",
	}
	preprocessResultValues = []string{
		"done", "failed", "skipped_off", "skipped_no_text", "skipped_already_tidy",
		"skipped_declined", "skipped_no_capability",
	}
)

// The image-level verdicts. Unlike the action results above they are written by
// the service layer, at the several ends a run can reach, so the write sites
// name these constants rather than repeating the words: one of them is a
// sentence about an unreadable image, and a typo in a branch is exactly the kind
// of drift nobody reads back.
const (
	outcomeDone           = "done"
	outcomePartialFailure = "partial_failure"
	outcomeFailed         = "failed"
	outcomeUnreadable     = "skipped_unreadable_image"
	outcomeNoContent      = "skipped_no_content"
)

// outcomeValues is that set as a list, so the guard test has one place to hold
// the frame-level key to — see TestTraceResultsStayInTheirVocabulary.
var outcomeValues = []string{
	outcomeDone, outcomePartialFailure, outcomeFailed, outcomeUnreadable, outcomeNoContent,
}

// The one value several of those vocabularies share: the action ran, read its
// gate, and found itself switched off.
const resultSkippedDisabled = "skipped_disabled"

// The thinking switches are read by name at the call sites that wire them into a
// run: a pipeline offers the switch of the action it actually schedules, and the
// action's key is used for the parameter and for the trace (they are the same
// string because the switch is action-level). See pipelines' Run.

// runContext is the only framework entry point a pipeline or an action gets. It
// hands out no VLM handle: talking to a model is the action's business, reached
// only through execute.
type runContext struct {
	payload *types.ImageMultimodalPayload
	model   vlm.VLM
	// preprocessor straightens the page before OCR. Nil when the document
	// service is not wired into this build — see imagePreprocessor.
	preprocessor imagePreprocessor
	// preprocessTimeout caps one call to it. Zero means the package default
	// (imagePreprocessTimeout); a test sets it small so the timeout path can be
	// exercised without waiting for the real cap.
	preprocessTimeout time.Duration
	imageBytes        []byte
	vlmCfg            types.VLMConfig
	imageInfo         *types.ImageInfo
	// out is the per-image trace record. Keys follow
	// docs/design/image-pipeline-naming.md — see the note above imagePipeline.
	out types.JSONMap
	// pipelineID is the pipeline serving this run, used to namespace
	// pipeline-private param lookups ("<pipeline>.<key>"). Empty in tests
	// that build a runContext by hand, which then read bare keys only.
	pipelineID string
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
	// caption, the observing one of a description — so it is the pipeline, not
	// the action, that says which of its switches this is. The key itself is the
	// action's ("<action>.<name>"), which is what lets two pipelines offer the
	// same action's tunable; the value each of them reads is its own, because a
	// setting is stored under the slot of the pipeline that declares it (see
	// types.SlotKey). Filled in by the pipeline before it executes anything.
	captionThinkingKey string
	ocrThinkingKey     string
	// The gate keys the caption and OCR actions read to decide whether they were
	// switched on for this run. The pipeline owns those switches, so it hands
	// the keys over ("" means "never gated"); the ACTIONS read them, because an
	// action that never ran cannot record that it was switched off
	// (docs/design/image-pipeline-naming.md §4).
	captionGateKey string
	ocrGateKey     string
	// tracker and imgSpan carry the per-image trace plumbing so the OCR
	// action can raise and resolve its own subspan ("multimodal.image[n].ocr")
	// at the exact spot it runs — a failed OCR fails that subspan and the
	// outcome summary without failing or retrying the whole image. Both are
	// nil when the attempt has no parent span to hang a subspan on.
	tracker SpanTracker
	imgSpan *Span
	// vlmErr keeps the FIRST typed VLM error the run recorded. Actions
	// record failures instead of propagating them (#4132: a failed OCR must
	// not fail an image that already produced a valid caption), but when the
	// run ends with nothing produced at all, the task layer needs the typed
	// error to tell a permanent failure (SkipRetry) from a transient one.
	vlmErr error
}

// Param reads one private tunable, falling back to the default the pipeline
// declared for it. A key the running pipeline never declared reads nil, which
// is deliberate: a stored knowledge base may carry parameters for a pipeline
// this build no longer has, and the run must not fail over a stale key.
//
// Every key is read under the running pipeline's namespace first
// ("<pipeline>.<key>"), so two pipelines may one day declare the same field key
// without one reading the other's stored value. There is no second place a
// value is looked for: a params map holds the settings of the pipelines that
// wrote them, and the run only ever wants its own.
func (r *runContext) Param(key string) any {
	if v, ok := types.ImagePipelineParam(r.params, r.pipelineID, key); ok {
		return v
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

// ocrOnUnobserved is the scheduling clause of the pipeline that observed: "read
// an image whose attributes could not be observed". It decides whether OCR is
// worth a pass when the observation came back empty or incomplete — that is
// orchestration, so it is a setting of the pipeline doing the scheduling rather
// than of the OCR action, even though OCR is what it is about.
//
// It is read here, at the decision, from the same slot the trace echoes; the
// declared default (on) answers a knowledge base that never touched it, so a
// missed block of text costs one extra call instead of being lost.
func (r *runContext) ocrOnUnobserved() bool {
	return r.BoolParamOr(smartFieldKeyOCROnUnobserved, true)
}

// preprocessMode is this run's page-straightening setting: the mode the running
// pipeline declares, or "" when it declares none.
//
// Read rather than resolved into a field of its own, so the answer the actions
// act on and the answer the trace echoes are one lookup — see
// types.PreprocessModeKey. An empty answer is not a mode, and neither predicate
// below accepts it: a pipeline that does not offer the switch has no reason to
// ask the model about geometry it could not use.
func (r *runContext) preprocessMode() string {
	mode, _ := r.Param(types.PreprocessModeKey).(string)
	return mode
}

// recordConfig writes the settings this run actually used, under the same
// fully-qualified keys the knowledge base stores them under, so a trace row can
// be read back against the config that produced it — the config itself may have
// changed since. A note carries no value and is skipped.
func (r *runContext) recordConfig() {
	echo := make(types.JSONMap, len(r.declared))
	for _, field := range r.declared {
		if field.Type == types.ImageFieldTypeNote {
			continue
		}
		echo[slotKey(r.pipelineID, field.Key)] = r.Param(field.Key)
	}
	if len(echo) > 0 {
		r.out[keyConfig] = echo
	}
}

// actionIDs is the sequence of actions this run actually executed, in order.
func (r *runContext) actionIDs() []types.ImageActionID { return r.actions }

// slotKey is types.SlotKey, kept short here because the trace keys are written
// through it on nearly every line that mentions a setting.
func slotKey(pipelineID, key string) string { return types.SlotKey(pipelineID, key) }

// predictCaption asks for a caption, with the tunable the current pipeline
// offers for it. purpose labels the underlying VLM call in the usage log so the
// observation step can be told apart from OCR after the fact: a plain caption
// passes "image_caption", the attribute-observing caption passes
// "image_observation".
func (r *runContext) predictCaption(ctx context.Context, prompt, purpose string) (string, error) {
	return r.think(types.WithLLMCallMetadata(ctx, purpose, ""), prompt, r.captionThinkingKey)
}

// predictOCR transcribes text from an image, with the tunable the current
// pipeline offers for it. The image_ocr purpose labels the VLM call in the
// usage log so it can be told apart from the observation step.
func (r *runContext) predictOCR(ctx context.Context, prompt string) (string, error) {
	return r.think(types.WithLLMCallMetadata(ctx, "image_ocr", ""), prompt, r.ocrThinkingKey)
}

// think asks the model with the thinking switch the pipeline wired to this
// action. Thinking defaults to off because on a quantized reasoning model the
// reasoning spends the completion budget before a single visible token, and the
// caller cannot tell that empty answer from an image that genuinely carries no
// text — which is exactly how an OCR result comes to be discarded as invalid.
// Turning it on costs a longer run and is what a dense or degraded image wants,
// so the switch is per action. The key is the action's own
// ("<action>.thinking_enabled") and is used for both the parameter and the
// trace: naming it after the action is what lets two pipelines offer the same
// tunable without either re-spelling it, and a run's trace names it the way the
// action does. The value is NOT shared — each pipeline stores the setting under
// its own slot (types.SlotKey), so tuning the OCR call in one pipeline leaves
// the other pipeline's OCR call alone. Pipelines that schedule a different
// action offer that action's switch instead (the manual pipeline offers the
// caption's, the observing one offers its own round's).
func (r *runContext) think(ctx context.Context, prompt, key string) (string, error) {
	on := r.BoolParamOr(key, false)
	r.out[key] = on
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

// execute runs one action by id.
//
// An action this build does not implement is a programming error, not a state a
// run can be in: pipelines are code, and nothing stores an action id as data. It
// therefore fails the image loudly instead of skipping the step — a skipped step
// that never says so is how an image comes back "processed" with its OCR never
// having run. The guard is unreachable for the shipped pipelines, whose action
// sequences a golden test pins.
func (r *runContext) execute(ctx context.Context, id types.ImageActionID) error {
	spec, ok := imageActionRegistry[id]
	if !ok || spec.handler == nil {
		return fmt.Errorf("image pipeline %q schedules action %q, which this build does not implement",
			r.pipelineID, id)
	}
	r.actions = append(r.actions, id)
	return spec.handler(ctx, r)
}

// runCaptionAction asks for a one-line description and stores it as the caption.
// Its failure subspan mirrors the OCR one (#4132): a caption miss is recorded
// (caption_error + a red .caption subspan) without failing the image.
func runCaptionAction(ctx context.Context, r *runContext) error {
	// The gate is read here rather than by the pipeline that schedules it: a
	// caption switched off must still leave a result saying so, and an action
	// that was never executed cannot write one.
	if !r.BoolParamOr(r.captionGateKey, true) {
		r.out[keyCaptionResult] = resultSkippedDisabled
		return nil
	}
	var captionSpan *Span
	if r.tracker != nil && r.imgSpan != nil {
		captionSpan = r.tracker.BeginSubSpan(ctx, r.imgSpan, r.imgSpan.Name+".caption",
			types.SpanKindGeneration, nil)
	}
	resolve := func(failed bool) {
		if captionSpan == nil {
			return
		}
		if failed {
			message, _ := r.out[keyCaptionError].(string)
			r.tracker.FailSpan(ctx, captionSpan, "CAPTION_FAILED", message, nil)
			return
		}
		r.tracker.EndSpan(ctx, captionSpan, types.JSONMap{
			"status": r.out[keyCaptionResult], "chars": r.out[keyCaptionChars],
		})
	}
	raw, err := r.predictCaption(ctx, buildVLMCaptionPrompt(ctx, r.vlmCfg), "image_caption")
	if err != nil {
		// Only recorded, not logged: an observation failure below is the same
		// kind of event and logs its own line, and a caption miss must not be
		// louder than the rest of the run.
		r.out[keyCaptionError] = err.Error()
		r.out[keyCaptionResult] = "failed"
		if r.vlmErr == nil {
			r.vlmErr = err
		}
		resolve(true)
		return nil
	}
	if text := strings.TrimSpace(raw); text != "" {
		r.imageInfo.Caption = text
		r.out[keyCaptionChars] = len([]rune(text))
		r.out[keyCaptionPreview] = previewText(text, 200)
		r.out[keyCaptionResult] = "done"
	} else {
		r.out[keyCaptionResult] = "empty"
	}
	resolve(false)
	return nil
}

// runObservationCaptionAction observes the registered attributes and describes
// the image in one answer. The description reaches the caption slot only when the
// run wants a caption — the observation itself always happens, because the
// attributes are the point of this action and the OCR policy reads them back.
func runObservationCaptionAction(ctx context.Context, r *runContext) error {
	var obsSpan *Span
	if r.tracker != nil && r.imgSpan != nil {
		obsSpan = r.tracker.BeginSubSpan(ctx, r.imgSpan, r.imgSpan.Name+".observe_caption",
			types.SpanKindGeneration, nil)
	}
	resolve := func(failed bool) {
		if obsSpan == nil {
			return
		}
		if failed {
			message, _ := r.out[keyObservationError].(string)
			r.tracker.FailSpan(ctx, obsSpan, "OBSERVATION_FAILED", message, nil)
			return
		}
		r.tracker.EndSpan(ctx, obsSpan, types.JSONMap{"status": "succeeded"})
	}
	raw, err := r.predictCaption(ctx,
		buildImageAttrsPrompt(ctx, r.vlmCfg, types.PreprocessObservesGeometry(r.preprocessMode())),
		"image_observation")
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Describe and observe failed for %s: %v", r.payload.ImageURL, err)
		r.out[keyObservationError] = err.Error()
		// The request itself failed: nothing was observed and nothing was
		// described. Both dimensions say so, and the pipeline's OCR decision
		// reads the result to tell "observed and declined" from "no observation
		// to decide on".
		r.out[keyObservationResult] = "failed"
		r.out[keyObservationCaptionResult] = "failed"
		if r.vlmErr == nil {
			r.vlmErr = err
		}
		resolve(true)
		return nil
	}
	obs, _ := types.ParseImageAttrsResponse(raw)
	// The round always observes — the attributes are the whole point of it — and
	// the description is kept only when this run wants a caption. The switch is
	// the action's own and is not declared in Fields(), so its working default
	// (on) is pinned at the read.
	applyImageObservation(r.imageInfo, obs.Attrs, obs.Description, r.out,
		r.BoolParamOr(keyObservationCaptionEnabled, true))
	// How much of what was asked came back. The list comes from the same
	// function the prompt was built from, so "asked everything" and "answered
	// everything" are comparable instead of guessed. Nothing is invented to fill
	// a gap: an unanswered attribute stays absent, and the pipeline's
	// ocr_on_unobserved setting decides what that means for OCR.
	asked := types.InjectedImageAttrNames(types.PreprocessObservesGeometry(r.preprocessMode()))
	answered := 0
	for _, name := range asked {
		if _, present := obs.Attrs.Attrs[name]; present {
			answered++
		}
	}
	switch {
	case answered == 0:
		r.out[keyObservationResult] = "none"
	case answered < len(asked):
		r.out[keyObservationResult] = "partial"
	default:
		r.out[keyObservationResult] = "all_done"
	}
	resolve(false)
	return nil
}

// runPreprocessAction straightens and crops the page before OCR runs, when the
// attributes the observation produced say OCR is worth a pass.
//
// Every failure mode is a no-op by construction: the run keeps its own image
// bytes, so OCR falls back to the original photograph without a second code
// path. That is deliberate — a page cropper that cannot decide must not be able
// to make the run worse than not having run it at all.
func runPreprocessAction(ctx context.Context, r *runContext) error {
	// The mode is this pipeline's own setting, read here and echoed by the
	// trace from the same slot — "what was asked for" is the config, and what
	// happened is result; a trace that repeats the first under the second's
	// name is how a reader comes to think always and a lucky auto run differ.
	mode := r.preprocessMode()
	// Off is answered before the capability check, and the order is the point:
	// "the user switched this off" is the more specific answer, and reporting
	// "this build has no preprocessor" over it would hide the choice that was
	// actually made — on a deployment without the document service every
	// setting would read as the same missing capability. Off means off: the
	// describe round did not even ask for the geometry attributes, so there is
	// nothing to consult and nothing to do.
	if mode == types.PreprocessModeOff {
		r.out[keyPreprocessResult] = "skipped_off"
		return nil
	}
	// An unwired preprocessor is what remains to explain doing nothing, so it
	// is reported only when the mode did ask for work.
	if r.preprocessor == nil {
		r.out[keyPreprocessResult] = "skipped_no_capability"
		return nil
	}
	// Always skips the question entirely — it exists precisely for the case
	// where an operator does not trust the observation to make this call.
	if !types.PreprocessRunsUnconditionally(mode) {
		// Auto: the same signal the OCR policy reads (contain.text) says whether
		// there is a page at all, and the geometry attributes say whether
		// straightening it is worth the pass. Their vocabularies are owned by
		// types.TextExtent / the registry, not re-spelled here.
		text := ""
		margin := ""
		upright, uprightObserved := false, false
		if r.imageInfo != nil {
			text = r.imageInfo.Attrs.TextExtent()
			margin = r.imageInfo.Attrs.Margin()
			upright, uprightObserved = r.imageInfo.Attrs.Upright()
		}
		if text != "block" && text != "sparse" {
			r.out[keyPreprocessResult] = "skipped_no_text"
			return nil
		}
		// A page that already fills the frame and already reads upright has
		// nothing to gain: the cropper would spend its pass to hand back the
		// same picture. An unanswered attribute is treated as "worth a look",
		// because the whole point of the pass is to cover what the observation
		// could not see.
		croppable := margin == "" || margin == "large"
		rotated := uprightObserved && !upright
		if !croppable && !rotated {
			r.out[keyPreprocessResult] = "skipped_already_tidy"
			return nil
		}
	}
	// The call is about to happen, so this is where its own subspan belongs.
	// The neighbours that spend a model call (.caption, .ocr) each have one, and
	// the pass this one measures is not cheap — a full-size page takes seconds,
	// which a reader of the timeline should see rather than infer.
	var prepSpan *Span
	if r.tracker != nil && r.imgSpan != nil {
		prepSpan = r.tracker.BeginSubSpan(ctx, r.imgSpan, r.imgSpan.Name+".preprocess",
			types.SpanKindSubSpan, nil)
	}
	resolve := func(failed bool) {
		if prepSpan == nil {
			return
		}
		if failed {
			message, _ := r.out[keyPreprocessError].(string)
			r.tracker.FailSpan(ctx, prepSpan, "PREPROCESS_FAILED", message, nil)
			return
		}
		r.tracker.EndSpan(ctx, prepSpan, types.JSONMap{
			"status": r.out[keyPreprocessResult],
			"bytes":  r.out[keyPreprocessBytes],
		})
	}
	// The call gets a deadline of its own rather than the task's: a document
	// service that hangs must not hold a worker until the whole document's
	// budget is gone. A call that runs past the cap is recorded like any other
	// failure, and OCR reads the original bytes.
	timeout := r.preprocessTimeout
	if timeout <= 0 {
		timeout = imagePreprocessTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rectified, fallback, err := r.preprocessor.PreprocessImage(callCtx, r.imageBytes)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Preprocess failed for %s: %v", r.payload.ImageURL, err)
		r.out[keyPreprocessResult] = "failed"
		r.out[keyPreprocessError] = err.Error()
		resolve(true)
		return nil
	}
	if fallback || len(rectified) == 0 {
		logger.Infof(ctx, "[ImageMultimodal] Preprocess declined for %s; OCR will use the original",
			r.payload.ImageURL)
		r.out[keyPreprocessResult] = "skipped_declined"
		resolve(false)
		return nil
	}
	// The one line that does the work: everything downstream reads imageBytes.
	r.imageBytes = rectified
	r.out[keyPreprocessResult] = "done"
	// The corrected image's size in bytes — bytes, not characters: the
	// neighbours that end in _chars (ocr_chars, caption_chars) measure text,
	// while this measures a picture. It reads next to image_bytes (the
	// original), and the pair is what shows how much of the photograph the
	// crop and the perspective correction threw away.
	r.out[keyPreprocessBytes] = len(rectified)
	resolve(false)
	return nil
}

// runOCRAction extracts the text the image carries. It is a plain function
// rather than a method because the OCR prompt is system-owned (knowledge base
// custom instructions must never reach it) and no service state is involved.
//
// Failure semantics (the #4132 contract): an OCR failure is RECORDED — status,
// a stable error code, the raw message, and a failed ".ocr" subspan — but it
// does not fail the image. Retrying the whole image would re-run a caption
// that already succeeded and duplicate its chunk, so the outcome summary (not
// the task retry) is what surfaces the failure.
func runOCRAction(ctx context.Context, r *runContext) error {
	// Read here, not by the pipeline: a switched-off OCR still has to leave a
	// result saying so.
	if !r.BoolParamOr(r.ocrGateKey, true) {
		r.out[keyOCRResult] = resultSkippedDisabled
		return nil
	}
	// The OCR subspan rides on the image span; without a parent span there is
	// nothing to hang it on and the outcome fields carry the signal alone.
	var ocrSpan *Span
	if r.tracker != nil && r.imgSpan != nil {
		ocrSpan = r.tracker.BeginSubSpan(ctx, r.imgSpan, r.imgSpan.Name+".ocr",
			types.SpanKindGeneration, nil)
	}
	resolve := func() {
		if ocrSpan == nil {
			return
		}
		// The code reads back from out so the span always matches what the
		// outcome summary sees — the caller may have refined it (e.g. a
		// request failure re-coded as OCR_TRUNCATED) after the initial set.
		if code, _ := r.out[keyOCRErrorCode].(string); code != "" {
			message, _ := r.out[keyOCRError].(string)
			r.tracker.FailSpan(ctx, ocrSpan, code, message, nil)
			return
		}
		r.tracker.EndSpan(ctx, ocrSpan, types.JSONMap{
			"status": r.out[keyOCRResult], "chars": r.out[keyOCRChars],
		})
	}
	// A failed extraction must never leave text from a previous attempt behind.
	r.imageInfo.OCRText = ""
	r.out[keyOCRChars] = 0

	// The OCR prompt is system-owned: knowledge base custom instructions must
	// never reach it, or free-form business rules compete with the "No text
	// content" contract and poison image_ocr chunks. buildVLMOCRPrompt picks
	// the scanned-PDF or default prompt and ignores vlmCfg on purpose.
	prompt := buildVLMOCRPrompt(r.payload.ImageSourceType, r.vlmCfg)
	if r.payload.ImageSourceType == "scanned_pdf" {
		logger.Infof(ctx, "[ImageMultimodal] Using scanned PDF prompt for OCR: %s", r.payload.ImageURL)
		r.out[keyOCRPrompt] = "scanned_pdf"
	} else {
		r.out[keyOCRPrompt] = "default"
	}

	ocrText, err := r.predictOCR(ctx, prompt)
	if err != nil {
		// A model-side failure is not "this image carries no text" (the loss
		// #4064 describes), but retrying the whole image is not the answer
		// either — the caption may already be valid. Record the failure with
		// a stable code and let the outcome summary classify the image.
		logger.Warnf(ctx, "[ImageMultimodal] OCR failed for %s: %v", r.payload.ImageURL, err)
		if r.vlmErr == nil {
			r.vlmErr = err
		}
		r.out[keyOCRResult] = "failed"
		r.out[keyOCRErrorCode] = "OCR_REQUEST_FAILED"
		if errors.Is(err, vlm.ErrTruncatedCompletion) {
			r.out[keyOCRErrorCode] = "OCR_TRUNCATED"
		}
		r.out[keyOCRError] = err.Error()
		resolve()
		return nil
	}
	r.out[keyOCRRawChars] = len([]rune(ocrText))
	ocrText, vErr := validateOCRText(ocrText)
	if vErr != nil {
		logger.Warnf(ctx, "[ImageMultimodal] OCR rejected for %s: %v", r.payload.ImageURL, vErr)
		r.out[keyOCRResult] = "failed"
		r.out[keyOCRErrorCode] = "OCR_INVALID_OUTPUT"
		r.out[keyOCRError] = vErr.Error()
		var invalid *ocrValidationError
		if errors.As(vErr, &invalid) {
			r.out[keyOCRRejectionReason] = invalid.reason
		}
		resolve()
		return nil
	}
	if ocrText != "" {
		r.imageInfo.OCRText = ocrText
		r.out[keyOCRResult] = "done"
		r.out[keyOCRChars] = len([]rune(ocrText))
		r.out[keyOCRPreview] = previewText(ocrText, 200)
		resolve()
		return nil
	}
	logger.Warnf(ctx, "[ImageMultimodal] OCR returned no text for %s", r.payload.ImageURL)
	r.out[keyOCRResult] = "skipped_no_text"
	resolve()
	return nil
}
