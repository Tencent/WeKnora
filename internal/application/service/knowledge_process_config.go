package service

import (
	"context"
	"strconv"
	"strings"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const xlsxFirstRowAsHeaderOverride = "xlsx_first_row_as_header"

func applyParserRuleOverrides(
	overrides map[string]string,
	config types.ChunkingConfig,
	fileType string,
) {
	fileType = normalizeParserFileType(fileType)
	if fileType != "xlsx" && fileType != "xls" {
		return
	}
	rule := config.ResolveParserEngineRule(fileType)
	if rule == nil || rule.XLSXFirstRowAsHeader == nil {
		return
	}
	engine := strings.TrimSpace(rule.Engine)
	if engine != "" && engine != "builtin" {
		return
	}
	overrides[xlsxFirstRowAsHeaderOverride] = strconv.FormatBool(*rule.XLSXFirstRowAsHeader)
}

func normalizeParserFileType(fileType string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fileType)), ".")
}

// ResolveProcessConfig merges KB defaults with per-upload overrides for the parse pipeline.
// resolveImageActions resolves the attribute policy and then lets the pipeline's
// slot override the clause the panel exposes. The clause decides whether OCR is
// scheduled when the observation did not answer — orchestration, so it belongs to
// the pipeline that does the scheduling, while the knowledge base field it falls
// back to is the default every pipeline starts from.
func resolveImageActions(imageCfg types.ImageProcessingConfig) types.ImageActionsConfig {
	actions := types.ResolveImageActions(imageCfg.ImageActions)
	if v, ok := types.ImagePipelineParam(
		imageCfg.ImagePipelineParams, string(types.ResolveImagePipelineID(&imageCfg)), ImagePipelineKeyOCROnUnobserved,
	); ok {
		if b, isBool := v.(bool); isBool {
			actions.OCR.OnUnobserved = b
			return actions
		}
		if s, isString := v.(string); isString {
			actions.OCR.OnUnobserved = s == "true" || s == "1" || s == "on"
		}
	}
	return actions
}

// ImagePipelineKeyOCROnUnobserved is the OCR fallback clause as a pipeline setting:
// "read an image whose attributes could not be observed". Declared by the pipeline
// that observes (it is that pipeline's scheduling decision) and kept here so the
// config resolution and the panel agree on the name.
const ImagePipelineKeyOCROnUnobserved = "ocr_on_unobserved"

// applySlotResidentSettings folds the running pipeline's own slot into the two
// settings that live inside it: the page-straightening mode and the OCR fallback
// clause. Neither is touched when the slot says nothing about it — the caller has
// already resolved both from the knowledge base, and an override that does not
// mention a setting must not quietly reset it to the default.
func applySlotResidentSettings(cfg types.ImageProcessingConfig, eff *types.EffectiveProcessConfig) {
	pipelineID := string(types.ResolveImagePipelineID(&cfg))
	if _, ok := types.ImagePipelineParam(cfg.ImagePipelineParams, pipelineID, types.PreprocessModeKey); ok {
		eff.PreprocessMode = types.ResolvePreprocessMode(&cfg)
	}
	if _, ok := types.ImagePipelineParam(
		cfg.ImagePipelineParams, pipelineID, ImagePipelineKeyOCROnUnobserved,
	); ok {
		eff.ImageActions = resolveImageActions(cfg)
	}
}

func ResolveProcessConfig(kb *types.KnowledgeBase, overrides *types.KnowledgeProcessOverrides) types.EffectiveProcessConfig {
	imageCfg := kb.ImageProcessingConfig
	eff := types.EffectiveProcessConfig{
		SummaryEnabled:           true,
		ChunkingConfig:           kb.ChunkingConfig,
		EnableMultimodel:         kb.IsMultimodalEnabled(),
		VLMConfig:                kb.VLMConfig,
		ASRConfig:                kb.ASRConfig,
		QuestionGenerationConfig: defaultQuestionGenerationConfig(kb),
		GraphEnabled:             kb.IsGraphEnabled(),
		ExtractConfig:            derefExtractConfig(kb.ExtractConfig),
		ImageAttrsEnabled:        imageCfg.ImageAttrsEnabled,
		ImageVectorEnabled:       imageCfg.ImageVectorEnabled,
		ImageActions:             resolveImageActions(imageCfg),
		ImagePipelineID:          types.ResolveImagePipelineID(&imageCfg),
		ImagePipelineParams:      imageCfg.ImagePipelineParams,
		PreprocessMode:           types.ResolvePreprocessMode(&imageCfg),
	}
	if overrides == nil {
		return foldResolvedSlotSettings(eff)
	}
	if overrides.PreprocessMode != nil && strings.TrimSpace(*overrides.PreprocessMode) != "" {
		// Normalized through the same resolver the knowledge base path uses, so
		// an override cannot introduce a value the run would not understand.
		eff.PreprocessMode = types.ResolvePreprocessMode(&types.ImageProcessingConfig{
			PreprocessMode: *overrides.PreprocessMode,
		})
	}
	if overrides.SummaryEnabled != nil {
		eff.SummaryEnabled = *overrides.SummaryEnabled
	}

	if overrides.ChunkingConfig != nil {
		eff.ChunkingConfig = mergeChunkingConfig(eff.ChunkingConfig, overrides.ChunkingConfig)
	}
	if len(overrides.ParserEngineRules) > 0 {
		eff.ChunkingConfig.ParserEngineRules = overrides.ParserEngineRules
	}
	if overrides.EnableMultimodel != nil {
		eff.EnableMultimodel = *overrides.EnableMultimodel
	}
	if overrides.VLMConfig != nil {
		base := eff.VLMConfig
		eff.VLMConfig = *overrides.VLMConfig
		if eff.VLMConfig.DescriptionLanguage == "" {
			eff.VLMConfig.DescriptionLanguage = base.DescriptionLanguage
		}
		if eff.VLMConfig.CustomInstructions == "" {
			eff.VLMConfig.CustomInstructions = base.CustomInstructions
		}
	}
	if overrides.ASRConfig != nil {
		eff.ASRConfig = *overrides.ASRConfig
	}
	if overrides.QuestionGenerationConfig != nil {
		base := eff.QuestionGenerationConfig
		eff.QuestionGenerationConfig = *overrides.QuestionGenerationConfig
		if eff.QuestionGenerationConfig.CustomInstructions == "" {
			eff.QuestionGenerationConfig.CustomInstructions = base.CustomInstructions
		}
	}
	if overrides.GraphEnabled != nil {
		eff.GraphEnabled = *overrides.GraphEnabled
	}
	if overrides.ImageAttrsEnabled != nil {
		eff.ImageAttrsEnabled = *overrides.ImageAttrsEnabled
		if overrides.ImagePipelineID == nil {
			// Legacy document overrides still select a pipeline. Parameters
			// belong to the selected pipeline and cannot follow a switch.
			id := types.ImagePipelineIDFor(eff.ImageAttrsEnabled)
			if id != eff.ImagePipelineID {
				eff.ImagePipelineParams = nil
			}
			eff.ImagePipelineID = id
		}
	}
	if overrides.ImagePipelineID != nil {
		// The pick and its params travel as one unit: params keyed for one
		// pipeline say nothing to another, so an id without its params would
		// silently run the new pipeline on the old pipeline's switches.
		eff.ImagePipelineID = types.NormalizeImagePipelineID(*overrides.ImagePipelineID)
		eff.ImagePipelineParams = overrides.ImagePipelineParams
		// The explicit pick also decides the legacy switch, so the fallback
		// path and the run agree on which pipeline this document asked for.
		eff.ImageAttrsEnabled = eff.ImagePipelineID == types.ImagePipelineSmartOCR
		// Two settings live inside the pipeline's own slot, and both were read
		// above from the knowledge base's slot — which the override has just
		// replaced. Read them again from the slot that will actually run, or a
		// dialog offering these controls would be offering the operator a knob
		// the run ignores.
		overridden := imageCfg
		overridden.ImagePipelineID = string(eff.ImagePipelineID)
		overridden.ImagePipelineParams = eff.ImagePipelineParams
		applySlotResidentSettings(overridden, &eff)
	}
	if overrides.ImageVectorEnabled != nil {
		eff.ImageVectorEnabled = *overrides.ImageVectorEnabled
	}
	if overrides.ImageActions != nil {
		base := eff.ImageActions
		// Same rule as types.MergeImageActions: the OCR clause is a unit keyed
		// on a non-empty On. OnUnobserved is a plain bool, so an override that
		// omits On cannot tell "false" from "unset" and is ignored rather than
		// flipping the conservative default.
		if len(overrides.ImageActions.OCR.On) > 0 {
			base.OCR = overrides.ImageActions.OCR
		}
		eff.ImageActions = base
	}
	if overrides.ExtractConfig != nil {
		eff.ExtractConfig = mergeExtractConfig(eff.ExtractConfig, overrides.ExtractConfig)
	}

	// Match KnowledgeBase.IsGraphEnabled: graph fan-out requires extract to be on.
	eff.GraphEnabled = eff.GraphEnabled && eff.ExtractConfig.Enabled

	return foldResolvedSlotSettings(eff)
}

// foldResolvedSlotSettings gives the running pipeline's slot the settings this
// task will actually run with, so that one map answers for all of them: the run
// reads that map, and the trace echoes it. Without this, a task could run with a
// mode the map did not contain, and its trace would report the field's declared
// default instead of the mode that was used — a trace that cannot be read back
// against the settings that produced it.
//
// The preprocess mode is the case it exists for: it is stored in the pipeline's
// slot, but a knowledge base that predates the slot keeps it in the field beside
// it, and a per-task override may set it flat.
//
// Nothing is written when leaving the map alone would report the same value —
// an unset key already reports the default the field declares, and a key that
// already holds the mode needs no help. Only a pipeline that declares the setting
// is given it, and the map is copied rather than written through: it usually
// belongs to a loaded knowledge base, and nothing resolved for one task may land
// in the stored config.
func foldResolvedSlotSettings(eff types.EffectiveProcessConfig) types.EffectiveProcessConfig {
	pipeline := resolveImagePipeline(eff.ImagePipelineID, eff.ImageAttrsEnabled)
	if pipeline == nil {
		return eff
	}
	declared, ok := fieldOf(pipeline, types.PreprocessModeKey)
	if !ok {
		return eff
	}
	reported := declared.Default
	if v, ok := types.ImagePipelineParam(
		eff.ImagePipelineParams, string(eff.ImagePipelineID), declared.Key,
	); ok {
		reported = v
	}
	if reported == eff.PreprocessMode {
		return eff
	}
	eff.ImagePipelineParams = types.SetImagePipelineParam(
		eff.ImagePipelineParams, string(eff.ImagePipelineID), declared.Key, eff.PreprocessMode,
	)
	return eff
}

// fieldOf returns the field a pipeline declares under a key.
func fieldOf(pipeline imagePipeline, key string) (types.ImageFieldDef, bool) {
	for _, field := range pipeline.Fields() {
		if field.Key == key {
			return field, true
		}
	}
	return types.ImageFieldDef{}, false
}

// validateDefaultFileImportRequirements enforces the VLM/ASR prerequisites that
// ValidateProcessOverrides would otherwise cover, for imports that ship no
// per-import overrides and therefore fall back to the KB defaults.
func validateDefaultFileImportRequirements(
	ctx context.Context,
	kb *types.KnowledgeBase,
	eff types.EffectiveProcessConfig,
	fileType string,
) error {
	fileType = normalizeFileExtension(fileType)
	if IsImageType(fileType) && !eff.VLMConfig.IsEnabled() {
		logger.Error(ctx, "VLM model is not configured")
		return werrors.NewBadRequestError("上传图片文件需要设置VLM模型")
	}
	if IsAudioType(fileType) && !kb.ASRConfig.IsASREnabled() {
		logger.Error(ctx, "ASR model is not configured")
		return werrors.NewBadRequestError("上传音频文件需要设置ASR语音识别模型")
	}
	return nil
}

// resolveFileImportProcessConfig is the single gate every file import passes
// through: it rejects unsupported extensions, enforces the VLM/ASR
// prerequisites for the resolved type, and returns the effective processing
// config for task enqueue. Persisting overrides onto the knowledge record stays
// with the caller, which owns the record's lifecycle.
func resolveFileImportProcessConfig(
	ctx context.Context,
	kb *types.KnowledgeBase,
	fileType string,
	processOverrides *types.KnowledgeProcessOverrides,
	enableMultimodel *bool,
) (types.EffectiveProcessConfig, error) {
	if err := validateImportFileType(fileType); err != nil {
		return types.EffectiveProcessConfig{}, err
	}

	eff := ResolveProcessConfig(kb, processOverrides)
	if enableMultimodel != nil && (processOverrides == nil || processOverrides.EnableMultimodel == nil) {
		eff.EnableMultimodel = *enableMultimodel
	}

	if processOverrides != nil {
		if err := validateEffectiveProcessOverrides(eff, []string{fileType}); err != nil {
			return eff, err
		}
	} else if err := validateDefaultFileImportRequirements(ctx, kb, eff, fileType); err != nil {
		return eff, err
	}

	return eff, nil
}

// ValidateProcessOverrides validates batch overrides against file types in the upload.
func ValidateProcessOverrides(
	ctx context.Context,
	kb *types.KnowledgeBase,
	overrides *types.KnowledgeProcessOverrides,
	fileTypes []string,
) error {
	if overrides == nil {
		return nil
	}
	return validateEffectiveProcessOverrides(ResolveProcessConfig(kb, overrides), fileTypes)
}

// Validate the final config, including the legacy enableMultimodel argument
// applied by upload and reparse callers, before persisting or enqueueing it.
func validateEffectiveProcessOverrides(eff types.EffectiveProcessConfig, fileTypes []string) error {
	hasImage := false
	hasAudio := false
	for _, ft := range fileTypes {
		if IsImageType(ft) {
			hasImage = true
		}
		if IsAudioType(ft) {
			hasAudio = true
		}
	}

	if hasImage {
		if !eff.VLMConfig.IsEnabled() {
			return werrors.NewBadRequestError("上传图片文件需要设置VLM模型")
		}
	}

	if hasAudio && !eff.ASRConfig.IsASREnabled() {
		return werrors.NewBadRequestError("上传音频文件需要设置ASR语音识别模型")
	}

	if err := types.ValidateEffectiveProcessPromptInstructions(eff); err != nil {
		return werrors.NewBadRequestError(err.Error())
	}
	if eff.EnableMultimodel {
		if err := ValidateImagePipelineParams(eff.ImagePipelineID, eff.ImagePipelineParams); err != nil {
			return werrors.NewBadRequestError(err.Error())
		}
	}

	return nil
}

// ApplyKnowledgeProcessOverrides validates optional overrides, persists them on the
// knowledge record, and returns the effective config for task enqueue.
func ApplyKnowledgeProcessOverrides(
	ctx context.Context,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	processOverrides *types.KnowledgeProcessOverrides,
	fileTypes []string,
	enableMultimodel *bool,
) (types.EffectiveProcessConfig, error) {
	eff := ResolveProcessConfig(kb, processOverrides)
	if enableMultimodel != nil && (processOverrides == nil || processOverrides.EnableMultimodel == nil) {
		eff.EnableMultimodel = *enableMultimodel
	}
	if processOverrides == nil {
		return eff, nil
	}
	if err := validateEffectiveProcessOverrides(eff, fileTypes); err != nil {
		return eff, err
	}
	if err := knowledge.SetProcessOverrides(processOverrides); err != nil {
		return eff, err
	}
	return eff, nil
}

// reparseFileTypes derives the file types used to validate overrides on reparse.
// Manual knowledge has no file; URL imports validate as html.
func reparseFileTypes(k *types.Knowledge) []string {
	if k == nil || k.IsManual() {
		return nil
	}
	if k.Type == "url" {
		return []string{"html"}
	}
	ft := k.FileType
	if ft == "" && k.FileName != "" {
		ft = getFileType(k.FileName)
	}
	if ft == "" {
		return nil
	}
	return []string{ft}
}

func defaultQuestionGenerationConfig(kb *types.KnowledgeBase) types.QuestionGenerationConfig {
	if kb == nil || kb.QuestionGenerationConfig == nil {
		return types.QuestionGenerationConfig{}
	}
	return *kb.QuestionGenerationConfig
}

func derefExtractConfig(cfg *types.ExtractConfig) types.ExtractConfig {
	if cfg == nil {
		return types.ExtractConfig{}
	}
	return *cfg
}

func mergeChunkingConfig(base types.ChunkingConfig, override *types.ChunkingConfig) types.ChunkingConfig {
	if override == nil {
		return base
	}
	result := base
	if override.ChunkSize != 0 {
		result.ChunkSize = override.ChunkSize
	}
	if override.ChunkOverlap != 0 {
		result.ChunkOverlap = override.ChunkOverlap
	}
	if len(override.Separators) > 0 {
		result.Separators = override.Separators
	}
	if len(override.ParserEngineRules) > 0 {
		result.ParserEngineRules = override.ParserEngineRules
	}
	// EnableParentChild is authoritative: callers send a full chunking snapshot,
	// so an explicit false must be able to turn parent-child off (not just on).
	result.EnableParentChild = override.EnableParentChild
	if override.ParentChunkSize != 0 {
		result.ParentChunkSize = override.ParentChunkSize
	}
	if override.ChildChunkSize != 0 {
		result.ChildChunkSize = override.ChildChunkSize
	}
	if override.Strategy != "" {
		result.Strategy = override.Strategy
	}
	if override.TokenLimit != 0 {
		result.TokenLimit = override.TokenLimit
	}
	if len(override.Languages) > 0 {
		result.Languages = override.Languages
	}
	if override.TableMetadataInstructions != "" {
		result.TableMetadataInstructions = override.TableMetadataInstructions
	}
	return result
}

func mergeExtractConfig(base types.ExtractConfig, override *types.ExtractConfig) types.ExtractConfig {
	if override == nil {
		return base
	}
	result := base
	result.Enabled = override.Enabled
	if override.Text != "" {
		result.Text = override.Text
	}
	if len(override.Tags) > 0 {
		result.Tags = override.Tags
	}
	if len(override.Nodes) > 0 {
		result.Nodes = override.Nodes
	}
	if len(override.Relations) > 0 {
		result.Relations = override.Relations
	}
	if override.CustomInstructions != "" {
		result.CustomInstructions = override.CustomInstructions
	}
	return result
}

// MergeParserEngineOverrides merges upload overrides on top of tenant overrides safely.
func MergeParserEngineOverrides(tenantOverrides map[string]string, uploadOverrides map[string]string) map[string]string {
	merged := make(map[string]string)
	for k, v := range tenantOverrides {
		merged[k] = v
	}
	for k, v := range uploadOverrides {
		merged[k] = v
	}
	return merged
}
