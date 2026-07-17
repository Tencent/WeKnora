package service

import (
	"context"
	"os"
	"strings"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// ResolveProcessConfig merges KB defaults with per-upload overrides for the parse pipeline.
func ResolveProcessConfig(kb *types.KnowledgeBase, overrides *types.KnowledgeProcessOverrides) types.EffectiveProcessConfig {
	eff := types.EffectiveProcessConfig{
		ChunkingConfig:           kb.ChunkingConfig,
		EnableMultimodel:         kb.IsMultimodalEnabled(),
		VLMConfig:                kb.VLMConfig,
		ASRConfig:                kb.ASRConfig,
		QuestionGenerationConfig: defaultQuestionGenerationConfig(kb),
		GraphEnabled:             kb.IsGraphEnabled(),
		ExtractConfig:            derefExtractConfig(kb.ExtractConfig),
	}
	if overrides == nil {
		return eff
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
	if overrides.ExtractConfig != nil {
		eff.ExtractConfig = mergeExtractConfig(eff.ExtractConfig, overrides.ExtractConfig)
	}

	// Match KnowledgeBase.IsGraphEnabled: graph fan-out requires extract to be on.
	eff.GraphEnabled = eff.GraphEnabled && eff.ExtractConfig.Enabled

	return eff
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

	eff := ResolveProcessConfig(kb, overrides)

	if hasImage {
		if err := validateImageMultimodalConfig(ctx, kb); err != nil {
			return err
		}
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
	if err := ValidateProcessOverrides(ctx, kb, processOverrides, fileTypes); err != nil {
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

func validateImageMultimodalConfig(ctx context.Context, kb *types.KnowledgeBase) error {
	// Concrete backends are validated and connectivity-tested when registered.
	// The checks below only apply to unmigrated provider-only bindings.
	if kb != nil && kb.StorageBackendID != nil && strings.TrimSpace(*kb.StorageBackendID) != "" {
		return nil
	}
	provider := kb.GetStorageProvider()
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if provider == "" && tenant != nil && tenant.StorageEngineConfig != nil {
		provider = strings.ToLower(strings.TrimSpace(tenant.StorageEngineConfig.DefaultProvider))
	}

	switch provider {
	case "cos":
		if tenant == nil || tenant.StorageEngineConfig == nil || tenant.StorageEngineConfig.COS == nil ||
			tenant.StorageEngineConfig.COS.SecretID == "" || tenant.StorageEngineConfig.COS.SecretKey == "" ||
			tenant.StorageEngineConfig.COS.Region == "" || tenant.StorageEngineConfig.COS.BucketName == "" {
			return werrors.NewBadRequestError("上传图片文件需要完整的对象存储配置信息, 请前往知识库存储设置或系统设置页面进行补全")
		}
	case "minio":
		ok := false
		if tenant != nil && tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.MinIO != nil {
			m := tenant.StorageEngineConfig.MinIO
			if m.Mode == "remote" {
				ok = m.Endpoint != "" && m.AccessKeyID != "" && m.SecretAccessKey != "" && m.BucketName != ""
			} else {
				ok = os.Getenv("MINIO_ENDPOINT") != "" && os.Getenv("MINIO_ACCESS_KEY_ID") != "" &&
					os.Getenv("MINIO_SECRET_ACCESS_KEY") != "" &&
					(m.BucketName != "" || os.Getenv("MINIO_BUCKET_NAME") != "")
			}
		}
		if !ok {
			return werrors.NewBadRequestError("上传图片文件需要完整的对象存储配置信息, 请前往知识库存储设置或系统设置页面进行补全")
		}
	}

	return nil
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
