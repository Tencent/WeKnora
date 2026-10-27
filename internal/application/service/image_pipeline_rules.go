// The rules a pipeline declares over its own tunables, evaluated from the
// values a knowledge base resolved. The panel requests this evaluation so it
// can warn before the user commits, and the save path runs it
// again on the backend so that a caller which never showed the panel cannot
// store something the pipeline would not run. Both sides read the same rules
// out of the spec, so a rule added here shows up in the UI with no frontend
// change.

package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// EvaluateImagePipelineRules walks a pipeline's rules and reports every one its
// tunables break, in declaration order. Reporting all of them rather than the
// first is the point: each rule carries its own wording and its own field, so a
// panel that shows three broken rules tells the user exactly what to change
// instead of making them fix one at a time and re-read the page. An empty
// result is a configuration the pipeline will run.
//
// A rule names the fields it is about rather than a condition to compute:
// "at least one of these is on" is what a set of action switches can mean, and
// a pipeline that offers none to switch off declares no rule at all rather than
// one that always holds.
//
// A field the parameters do not mention reads its declared default, the same
// way the run does: a knowledge base saved before a field existed is not told
// that it switched something off by never mentioning it.
func EvaluateImagePipelineRules(
	pipelineID types.ImagePipelineID,
	rules []types.ImagePipelineRules,
	fields []types.ImageFieldDef,
	params map[string]any,
) []*types.ImagePipelineValidationError {
	// Use the execution-time resolver for namespace precedence, defaults and
	// boolean coercion. A separate interpretation can accept a no-op pipeline.
	r := &runContext{pipelineID: string(pipelineID), declared: fields, params: params}
	broken := make([]*types.ImagePipelineValidationError, 0, len(rules))
	for _, rule := range rules {
		if len(rule.AtLeastOne) == 0 || rule.MessageKey == "" {
			continue
		}
		satisfied := false
		for _, key := range rule.AtLeastOne {
			if r.BoolParam(key) {
				satisfied = true
				break
			}
		}
		if satisfied {
			continue
		}
		field := rule.Field
		if field == "" {
			field = rule.AtLeastOne[0]
		}
		broken = append(broken, &types.ImagePipelineValidationError{
			MessageKey: rule.MessageKey,
			Field:      field,
		})
	}
	return broken
}

// firstImagePipelineError turns a list of broken rules into the error one save
// path reports. A rejection can only say what is wrong about the whole request,
// so the first broken rule stands for all of them.
func firstImagePipelineError(broken []*types.ImagePipelineValidationError) error {
	if len(broken) == 0 {
		return nil
	}
	return broken[0]
}

// validateImagePipelineConfig is the save path's own check, run after the
// settings panel has already asked and been answered. It is here so that a
// caller which never showed a panel — an API client, an import, a script —
// cannot store a combination the pipeline would not run either.
//
// The pipeline id is resolved the way the run resolves it rather than taken
// from the field as written: a knowledge base saved before the id field existed
// carries none, and that must not make the check a no-op.
func (s *knowledgeBaseService) validateImagePipelineConfig(
	ctx context.Context,
	kb *types.KnowledgeBase,
) error {
	cfg := kb.ImageProcessingConfig
	pipelineID := types.ResolveImagePipelineID(&cfg)
	err := ValidateImagePipelineParams(pipelineID, cfg.ImagePipelineParams)
	if err != nil {
		logger.Warnf(ctx, "[kb.image] rejecting pipeline %q params for knowledge base %q: %v",
			pipelineID, kb.ID, err)
	}
	return err
}
