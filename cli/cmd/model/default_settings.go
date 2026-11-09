package modelcmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	sdk "github.com/Tencent/WeKnora/client"
)

// workspaceDefaultSettingsKey is the tenant KV entry behind
// PUT /api/v1/tenants/kv/default-settings. The value is the workspace's
// default model per type; the server projects it into the is_default flag of
// model list responses.
const workspaceDefaultSettingsKey = "default-settings"

// tenantDefaultSettings mirrors the server's types.DefaultSettings JSON body.
// The PUT replaces the whole object, so every slot is carried over from GET.
type tenantDefaultSettings struct {
	ChatModelID      string `json:"chat_model_id"`
	UtilityModelID   string `json:"utility_model_id"`
	EmbeddingModelID string `json:"embedding_model_id"`
	RerankModelID    string `json:"rerank_model_id"`
	VLMModelID       string `json:"vlm_model_id"`
	ASRModelID       string `json:"asr_model_id"`
}

// defaultSettingsService is the tenant-KV surface the model commands use to
// change the workspace default. The server does not read is_default from the
// model create/update bodies, so this is the supported write path.
type defaultSettingsService interface {
	GetTenantKV(ctx context.Context, key string) (json.RawMessage, error)
	UpdateTenantKV(ctx context.Context, key string, value any) (json.RawMessage, error)
}

// workspaceDefaultSlot returns the settings field that backs a model type.
// A type without a slot has no workspace default, so --default cannot apply.
func workspaceDefaultSlot(settings *tenantDefaultSettings, modelType sdk.ModelType) *string {
	switch modelType {
	case sdk.ModelTypeKnowledgeQA:
		return &settings.ChatModelID
	case sdk.ModelTypeEmbedding:
		return &settings.EmbeddingModelID
	case sdk.ModelTypeRerank:
		return &settings.RerankModelID
	case sdk.ModelTypeVLLM:
		return &settings.VLMModelID
	case sdk.ModelTypeASR:
		return &settings.ASRModelID
	default:
		return nil
	}
}

// setWorkspaceDefaultModel points the workspace default slot for modelType at
// modelID, or clears it when enabled is false and the slot already holds this
// model. Other slots and another model's default are left untouched: the
// request body is read first so the full-object PUT preserves them.
func setWorkspaceDefaultModel(
	ctx context.Context,
	svc defaultSettingsService,
	modelType sdk.ModelType,
	modelID string,
	enabled bool,
) error {
	raw, err := svc.GetTenantKV(ctx, workspaceDefaultSettingsKey)
	if err != nil {
		return cmdutil.WrapHTTP(err, "read workspace default settings")
	}
	var settings tenantDefaultSettings
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("decode workspace default settings: %w", err)
		}
	}
	slot := workspaceDefaultSlot(&settings, modelType)
	if slot == nil {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			fmt.Sprintf("model type %s has no workspace default", modelType))
	}
	switch {
	case enabled && *slot == modelID:
		return nil // already the default; keep the stored object as it is
	case !enabled && *slot != modelID:
		return nil // --default=false must not clear a different model's default
	case enabled:
		*slot = modelID
	default:
		*slot = ""
	}
	if _, err := svc.UpdateTenantKV(ctx, workspaceDefaultSettingsKey, settings); err != nil {
		return cmdutil.WrapHTTP(err, "update workspace default settings")
	}
	return nil
}

// defaultFailure keeps the typed error code while saying that the model write
// itself succeeded. A failed default must not read as a failed create/update.
func defaultFailure(succeeded string, err error) error {
	if typed := cmdutil.AsError(err); typed != nil {
		return cmdutil.Wrapf(typed.Code, nil, "%s, but the workspace default was not set: %s",
			succeeded, typed.Message)
	}
	return fmt.Errorf("%s, but the workspace default was not set: %w", succeeded, err)
}
