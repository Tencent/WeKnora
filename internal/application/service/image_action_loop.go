package service

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

// ActionRound is one dispatch step: a set of actions executed together. The
// dispatch layer may physically merge co-scheduled actions (observation and
// caption share one VLM call); the set is what the loop reasons about.
type ActionRound struct {
	Actions []types.ActionKind
}

// Contains reports whether the round schedules the given action.
func (r ActionRound) Contains(k types.ActionKind) bool {
	for _, a := range r.Actions {
		if a == k {
			return true
		}
	}
	return false
}

// selectImageActionRounds turns the payload switches into the initial plan.
// Observation mode opens with an observation+caption round and lets the result
// processor append an OCR round; caption+OCR mode schedules caption and OCR as
// independent rounds (caption first, then OCR) with no observation. Default
// action membership lives in types.DefaultEnabledActions; the whole-task
// switches (EnableCaption/EnableOCR) only gate caption and OCR on top of it.
func selectImageActionRounds(p *types.ImageMultimodalPayload) []ActionRound {
	on := types.DefaultEnabledActions(p.ImageAttrsEnabled)
	if !p.EnableCaption {
		on[types.ActionCaption] = false
	}
	if !p.EnableOCR {
		on[types.ActionOCR] = false
	}

	if p.ImageAttrsEnabled {
		// Observation and caption share one VLM call in attribute mode.
		r := ActionRound{}
		if on[types.ActionCaption] {
			r.Actions = append(r.Actions, types.ActionCaption)
		}
		if on[types.ActionObservation] {
			r.Actions = append(r.Actions, types.ActionObservation)
		}
		if len(r.Actions) == 0 {
			return nil
		}
		return []ActionRound{r}
	}

	var rounds []ActionRound
	if on[types.ActionCaption] {
		rounds = append(rounds, ActionRound{Actions: []types.ActionKind{types.ActionCaption}})
	}
	if on[types.ActionOCR] {
		rounds = append(rounds, ActionRound{Actions: []types.ActionKind{types.ActionOCR}})
	}
	return rounds
}

// executeActionRound runs one dispatch step. Observation and caption are
// physically merged into a single VLM call (the describe round); a caption-only
// round (caption+OCR mode) uses the plain caption prompt; an OCR-only round uses
// prompt. The transport here is the direct vlmModel.Predict shim — PR-E will
// route it through a VLM request manager without touching this dispatch logic.
func (s *ImageMultimodalService) executeActionRound(
	ctx context.Context,
	payload *types.ImageMultimodalPayload,
	model vlm.VLM,
	imgBytes []byte,
	vlmCfg types.VLMConfig,
	imageInfo *types.ImageInfo,
	out types.JSONMap,
	round ActionRound,
) {
	if round.Contains(types.ActionObservation) {
		raw, capErr := model.Predict(ctx, [][]byte{imgBytes}, buildImageAttrsPrompt(ctx, vlmCfg))
		if capErr != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Describe and observe failed for %s: %v", payload.ImageURL, capErr)
			out["caption_error"] = capErr.Error()
			return
		}
		obs, ok := types.ParseImageAttrsResponse(raw)
		applyImageObservation(imageInfo, obs.Attrs, obs.Description, out, payload.EnableCaption)
		if !obs.Observed {
			// The model ignored the attribute protocol — a user custom
			// instruction may have derailed the format, or it answered in prose.
			// Nothing is invented to fill the gap: the attribute table stays
			// empty and the OCR policy falls back to its conservative
			// OnUnobserved clause. Flag it so the trace shows the observation
			// did not actually happen, instead of the empty table being read as
			// a confident "nothing to see here".
			out["observation_failed"] = true
		}
		if !ok {
			out["caption_missing"] = true
		}
		return
	}
	if round.Contains(types.ActionCaption) {
		raw, capErr := model.Predict(ctx, [][]byte{imgBytes}, buildVLMCaptionPrompt(ctx, vlmCfg))
		if capErr != nil {
			out["caption_error"] = capErr.Error()
			return
		}
		if text := strings.TrimSpace(raw); text != "" {
			imageInfo.Caption = text
			out["caption_chars"] = len([]rune(text))
			out["caption_preview"] = previewText(text, 200)
		}
		return
	}
	if round.Contains(types.ActionOCR) {
		s.runImageOCR(ctx, payload, model, imgBytes, imageInfo, out, vlmCfg)
	}
}
