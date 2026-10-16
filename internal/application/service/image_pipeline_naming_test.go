package service

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestTraceResultsStayInTheirVocabulary holds every result a pipeline writes to
// the vocabulary it belongs to. The lists live in code (see the declarations
// above imagePipelineRun) precisely so this test can exist: a new value has to
// be added to one of them on purpose, which is what keeps a trace readable a
// year later instead of accumulating whatever words a branch happened to print.
//
// Every combination of the switches is exercised, because a value only one
// branch writes — skipped_off, skipped_no_capability — is exactly the kind that
// would otherwise drift unnoticed.
func TestTraceResultsStayInTheirVocabulary(t *testing.T) {
	vocabularies := map[string][]string{
		keyCaptionResult:     captionResultValues,
		keyObservationResult: observationResultValues,
		keyOCRResult:         ocrResultValues,
		keyPreprocessResult:  preprocessResultValues,
	}

	for _, attrsEnabled := range []bool{true, false} {
		for _, caption := range []bool{true, false} {
			for _, ocr := range []bool{true, false} {
				params := map[string]any{
					"default.caption_enabled":    caption,
					"default.ocr_enabled":        ocr,
					"smartocr.ocr_on_unobserved": true,
				}
				r, _ := runImagePipeline(t, attrsEnabled, params, types.ImageActionsConfig{})

				for key, allowed := range vocabularies {
					value, written := r.out[key]
					if !written {
						continue
					}
					text, isString := value.(string)
					if !isString {
						t.Errorf("%s holds %T; a result is one of %v", key, value, allowed)
						continue
					}
					if !slices.Contains(allowed, text) {
						t.Errorf("%s = %q is not in its vocabulary %v", key, text, allowed)
					}
				}
			}
		}
	}
}

// TestEveryVocabularyIsClosedAndDistinct pins the shape every list above shares:
// non-empty, no repeated word. A duplicate is the one mistake a list like this
// can make silently — the second spelling is unreachable, and the branch that
// writes it looks covered in review while nothing reads it.
//
// The frame-level outcome vocabulary is included even though no pipeline writes
// it: those verdicts are written by the service layer, and its write sites name
// these same constants rather than repeating the words.
func TestEveryVocabularyIsClosedAndDistinct(t *testing.T) {
	vocabularies := map[string][]string{
		"caption.result":     captionResultValues,
		"observation.result": observationResultValues,
		"ocr.result":         ocrResultValues,
		"preprocess.result":  preprocessResultValues,
		keyOutcome:           outcomeValues,
	}
	for key, values := range vocabularies {
		if len(values) == 0 {
			t.Errorf("%s has an empty vocabulary; every result must name a word", key)
			continue
		}
		seen := make(map[string]bool, len(values))
		for _, value := range values {
			if value == "" {
				t.Errorf("%s carries an empty value", key)
			}
			if seen[value] {
				t.Errorf("%s lists %q twice", key, value)
			}
			seen[value] = true
		}
	}
}

// resultKeyNames are the endings the trace reserves for what an action produced
// or spent. A setting must never be named like one of them: settings and results
// live in different containers, and a shared spelling is how a reader starts
// treating a switch as a value. See docs/design/image-pipeline-naming.md §3.
var resultKeyNames = []string{
	"result", "status", "chars", "bytes", "preview", "error", "error_code",
}

// TestNoSettingIsNamedLikeAResult enforces the boundary between the two families
// on the only keys that can drift: the ones a pipeline or an action declares.
// Taking the last segment is deliberate — a setting is judged by the name it
// gives itself, under whatever namespace it lives in.
func TestNoSettingIsNamedLikeAResult(t *testing.T) {
	check := func(owner, key string) {
		name := key
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if slices.Contains(resultKeyNames, name) {
			t.Errorf("%s declares the setting %q: a setting is not a result, and a key "+
				"that reads like one will be read as one", owner, key)
		}
	}
	for _, spec := range types.SharedImageActions {
		for _, field := range spec.Fields {
			check("action "+string(spec.ID), field.Key)
		}
	}
	for id, pipeline := range imagePipelineRegistry {
		for _, field := range pipeline.Fields() {
			check("pipeline "+string(id), field.Key)
		}
	}
}

// TestActionResultKeysAreNamedAfterTheirAction pins the shape of the contract
// keys: an action's result is "<action>.result", derived from the action id
// rather than spelled a second time, so renaming an action cannot leave a trace
// key behind under the old name.
func TestActionResultKeysAreNamedAfterTheirAction(t *testing.T) {
	for _, tc := range []struct {
		key string
		id  types.ImageActionID
	}{
		{keyCaptionResult, types.ImageActionCaption},
		{keyObservationResult, types.ImageActionObservation},
		{keyOCRResult, types.ImageActionOCR},
		{keyPreprocessResult, types.ImageActionPreprocess},
	} {
		if want := types.ActionParam(tc.id, "result"); tc.key != want {
			t.Errorf("the result key is %q, want %q — built from the action id", tc.key, want)
		}
	}
}

// TestEveryActionTraceKeyCarriesAnActionNamespace is the other half of the rule
// above: a key that describes what an action did belongs to that action, so the
// prefix has to be a registered action id. A key naming an action that does not
// exist is a trace entry nobody can attribute.
func TestEveryActionTraceKeyCarriesAnActionNamespace(t *testing.T) {
	registered := make(map[string]bool)
	for _, spec := range types.SharedImageActions {
		registered[string(spec.ID)] = true
	}

	for _, key := range []string{
		keyCaptionResult, keyCaptionChars, keyCaptionPreview, keyCaptionError,
		keyObservationResult, keyObservationCaptionResult, keyObservationCaptionChars,
		keyObservationCaptionPreview, keyObservationError,
		keyOCRResult, keyOCRErrorCode, keyOCRError, keyOCRChars,
		keyOCRRawChars, keyOCRPreview, keyOCRPrompt, keyOCRRejectionReason,
		keyPreprocessResult, keyPreprocessBytes, keyPreprocessError,
	} {
		action, _, found := strings.Cut(key, ".")
		if !found {
			t.Errorf("trace key %q carries no action namespace", key)
			continue
		}
		if !registered[action] {
			t.Errorf("trace key %q is namespaced with %q, which is not a registered action", key, action)
		}
	}
}

// TestTheRunReadsTheSettingItsTraceEchoes is the round trip the whole naming
// rule exists for: for every setting a pipeline declares, the value the run used
// and the value the trace reports have to be the same value.
//
// The preprocess mode is the one that can come from three places — the
// pipeline's slot, the knowledge base field that predates it, and a per-task
// override — and it is resolved into a field of its own on the way. So the task
// folds the answer back into the slot, and this test holds the two together: if
// the fold were dropped, the run would still straighten the page to the mode
// asked for while its trace reported the field's declared default.
func TestTheRunReadsTheSettingItsTraceEchoes(t *testing.T) {
	kbWith := func(cfg types.ImageProcessingConfig) *types.KnowledgeBase {
		return &types.KnowledgeBase{ImageProcessingConfig: cfg}
	}
	smart := types.ImageProcessingConfig{
		ImageAttrsEnabled: true,
		ImagePipelineID:   string(types.ImagePipelineSmartOCR),
	}

	cases := []struct {
		name     string
		kb       types.ImageProcessingConfig
		wantMode string
	}{
		{
			"the slot is the setting",
			types.ImageProcessingConfig{
				ImageAttrsEnabled:   true,
				ImagePipelineID:     string(types.ImagePipelineSmartOCR),
				ImagePipelineParams: map[string]any{"smartocr.preprocess.mode": types.PreprocessModeOff},
			},
			types.PreprocessModeOff,
		},
		{
			"the field that predates the slot still decides",
			types.ImageProcessingConfig{ImageAttrsEnabled: true, PreprocessMode: types.PreprocessModeAlways},
			types.PreprocessModeAlways,
		},
		{
			"the slot wins over the field",
			types.ImageProcessingConfig{
				ImageAttrsEnabled:   true,
				ImagePipelineID:     string(types.ImagePipelineSmartOCR),
				PreprocessMode:      types.PreprocessModeAlways,
				ImagePipelineParams: map[string]any{"smartocr.preprocess.mode": types.PreprocessModeOff},
			},
			types.PreprocessModeOff,
		},
		{
			"nothing stored anywhere means auto",
			smart,
			types.PreprocessModeAuto,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A per-task value must never be written back into the config it was
			// resolved from, so the map is compared before and after.
			before := fmt.Sprint(tc.kb.ImagePipelineParams)
			eff := ResolveProcessConfig(kbWith(tc.kb), nil)
			if got := fmt.Sprint(tc.kb.ImagePipelineParams); got != before {
				t.Fatalf("resolving wrote back into the stored config: %s -> %s", before, got)
			}
			if eff.PreprocessMode != tc.wantMode {
				t.Fatalf("resolved mode = %q, want %q", eff.PreprocessMode, tc.wantMode)
			}
			r, _ := runImagePipeline(t, true, eff.ImagePipelineParams, eff.ImageActions)
			echo, _ := r.out[keyConfig].(types.JSONMap)
			if got := echo["smartocr.preprocess.mode"]; got != tc.wantMode {
				t.Errorf("the trace reports %v for the mode the run used (%q); "+
					"read the trace back against the settings that produced it and "+
					"it must name the same setting", got, eff.PreprocessMode)
			}
		})
	}
}
