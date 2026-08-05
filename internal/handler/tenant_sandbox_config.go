package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// GetTenantSandboxConfig godoc
// @Summary      获取空间沙箱配置
// @Description  获取空间级别的沙箱后端配置（密钥以掩码返回）
// @Tags         空间管理
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Router       /api/v1/tenants/kv/sandbox-config [get]
func (h *TenantHandler) GetTenantSandboxConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
		return
	}
	// TODO(multi-sandbox-config): replaced in Task 6/9/10
	data := types.SandboxConfigForResponse(nil, true)
	if data == nil {
		data = &types.TenantSandboxConfig{}
	}
	// An empty override set is not "disabled" — the workspace runs on the
	// deployment defaults. Returning them lets the UI show what is actually in
	// effect instead of misrepresenting it as disabled.
	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"data":     data,
		"defaults": h.sandboxDefaults,
	})
}

// updateTenantSandboxConfigInternal updates the tenant's sandbox backend config.
func (h *TenantHandler) updateTenantSandboxConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()
	var cfg types.TenantSandboxConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Workspace is empty")
		c.Error(errors.NewBadRequestError("Workspace is empty"))
		return
	}

	// TODO(multi-sandbox-config): replaced in Task 6/9/10
	merged, err := SanitizeSandboxConfigForUpdate(&cfg, nil)
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// TODO(multi-sandbox-config): replaced in Task 6/9/10
	_ = merged
	c.Error(errors.NewBadRequestError("sandbox config updates are temporarily unavailable"))
}

// SanitizeSandboxConfigForUpdate resolves redacted secrets against the stored
// config and validates the result before it is persisted.
//
// URL validation happens here in addition to dial time so the admin gets an
// immediate, readable error instead of a failure at first sandbox use, and the
// sandbox type is parsed so a typo cannot silently disable the workspace.
func SanitizeSandboxConfigForUpdate(
	incoming, existing *types.TenantSandboxConfig,
) (*types.TenantSandboxConfig, error) {
	if incoming == nil {
		return nil, nil
	}
	merged := types.MergeSandboxConfigForUpdate(incoming, existing)

	if merged.SandboxType != "" {
		if _, err := sandbox.ParseSandboxType(merged.SandboxType); err != nil {
			return nil, err
		}
	}
	for _, endpoint := range sandboxConfigEndpoints(merged) {
		if err := sandbox.ValidateOutboundURL(endpoint); err != nil {
			return nil, err
		}
	}
	// Without an AES key the Value() hook would persist these secrets in
	// plaintext. Refuse instead of silently downgrading storage security.
	if sandboxConfigHasSecrets(merged) && utils.GetAESKey() == nil {
		return nil, errors.NewBadRequestError(
			"SYSTEM_AES_KEY is not configured; refusing to store sandbox credentials in plaintext",
		)
	}
	return merged, nil
}

// sandboxConfigEndpoints returns every non-empty tenant-supplied URL.
func sandboxConfigEndpoints(cfg *types.TenantSandboxConfig) []string {
	if cfg == nil {
		return nil
	}
	var endpoints []string
	if cfg.Cube != nil {
		for _, raw := range []string{cfg.Cube.APIURL, cfg.Cube.ProxyURL} {
			if raw != "" {
				endpoints = append(endpoints, raw)
			}
		}
	}
	if cfg.E2B != nil && cfg.E2B.APIURL != "" {
		endpoints = append(endpoints, cfg.E2B.APIURL)
	}
	return endpoints
}

// sandboxBackendIdentityChanged reports whether resources created under oldCfg
// become unreachable under newCfg, i.e. whether the provider or the account
// behind it changed.
func sandboxBackendIdentityChanged(oldCfg, newCfg *types.TenantSandboxConfig) bool {
	if oldCfg == nil {
		return false
	}
	if newCfg == nil {
		return true
	}
	if oldCfg.SandboxType != newCfg.SandboxType {
		return true
	}
	oldKey, oldURL := sandboxBackendIdentity(oldCfg)
	newKey, newURL := sandboxBackendIdentity(newCfg)
	return oldKey != newKey || oldURL != newURL
}

// sandboxBackendIdentity returns the (api key, endpoint) pair that determines
// which provider account owns a workspace's sandboxes.
func sandboxBackendIdentity(cfg *types.TenantSandboxConfig) (string, string) {
	switch sandbox.SandboxType(cfg.SandboxType) {
	case sandbox.SandboxTypeCube:
		if cfg.Cube != nil {
			return cfg.Cube.APIKey, cfg.Cube.APIURL
		}
	case sandbox.SandboxTypeE2B:
		if cfg.E2B != nil {
			return cfg.E2B.APIKey, cfg.E2B.APIURL
		}
	}
	return "", ""
}

// reapOutgoingSandboxes deletes every sandbox the workspace still holds on the
// outgoing backend. Every one of them is abandoned by definition — the
// workspace is moving away — so no binding set or grace window applies.
//
// Failures are logged, never propagated: the config update must not be blocked
// by an unreachable old backend.
func reapOutgoingSandboxes(
	ctx context.Context,
	tenantID uint64,
	oldCfg *types.TenantSandboxConfig,
) {
	effective, err := sandbox.ResolveEffectiveConfig(oldCfg, sandbox.DefaultConfig())
	if err != nil {
		logger.Warnf(ctx, "[sandbox] skip cleanup of outgoing backend: %v", err)
		return
	}
	switch effective.Type {
	case sandbox.SandboxTypeCube, sandbox.SandboxTypeE2B:
	default:
		return // stateless backends hold nothing to release
	}

	client, err := sandbox.NewRemoteClientForCheck(effective)
	if err != nil {
		logger.Warnf(ctx, "[sandbox] skip cleanup of outgoing backend: %v", err)
		return
	}
	reapCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()

	deleted, err := sandbox.ReapOrphanSandboxes(reapCtx, sandbox.OrphanReaperDeps{
		Client:   client,
		TenantID: tenantID,
		Grace:    0,
	})
	if err != nil {
		logger.Warnf(ctx,
			"[sandbox] cleanup of outgoing backend for workspace %d failed: %v",
			tenantID, err)
		return
	}
	if deleted > 0 {
		logger.Infof(ctx,
			"[sandbox] released %d sandbox(es) on the outgoing backend for workspace %d",
			deleted, tenantID)
	}
}

// sandboxConfigHasSecrets reports whether cfg carries any value that must be
// encrypted at rest.
func sandboxConfigHasSecrets(cfg *types.TenantSandboxConfig) bool {
	if cfg == nil {
		return false
	}
	if cfg.Cube != nil && cfg.Cube.APIKey != "" {
		return true
	}
	if cfg.E2B != nil && cfg.E2B.APIKey != "" {
		return true
	}
	for _, value := range cfg.EnvVars {
		if value != "" {
			return true
		}
	}
	return false
}
