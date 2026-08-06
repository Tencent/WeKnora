package handler

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// --- Sandbox connectivity check ---

// SandboxCheckRequest is the body for POST /system/sandbox-check. Secrets may
// arrive redacted; they are resolved against the workspace's stored config so
// an admin can test without retyping an API key.
type SandboxCheckRequest struct {
	Config *types.TenantSandboxConfig `json:"config"`
	// ConfigID lets an edit form test stored credentials while overriding only
	// the fields the admin changed in the drawer.
	ConfigID string `json:"config_id"`
	// Deep additionally creates and destroys one sandbox, which is the only
	// way to validate the template ID, Cube's proxy/envd data plane,
	// in-sandbox execution, and outbound egress (CN + international probes,
	// any one success). It consumes real sandbox time, so it is opt-in.
	Deep bool `json:"deep"`
}

// SandboxCheckItem is one probe outcome. OK is nil when the probe was not
// executed, which distinguishes "skipped" from "failed" in the UI.
type SandboxCheckItem struct {
	Name      string `json:"name"`
	OK        *bool  `json:"ok"`
	Message   string `json:"message,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

// SandboxCheckResponse aggregates the probes for one sandbox configuration.
type SandboxCheckResponse struct {
	OK           bool               `json:"ok"`
	Provider     string             `json:"provider"`
	Checks       []SandboxCheckItem `json:"checks"`
	Capabilities map[string]bool    `json:"capabilities,omitempty"`
}

// add records an executed probe. A single failure fails the whole result.
func (r *SandboxCheckResponse) add(name string, ok bool, message string, latencyMS int64) {
	value := ok
	r.Checks = append(r.Checks, SandboxCheckItem{
		Name: name, OK: &value, Message: message, LatencyMS: latencyMS,
	})
	if !ok {
		r.OK = false
	}
}

// skip records a probe that was not run; it never affects OK.
func (r *SandboxCheckResponse) skip(name, message string) {
	r.Checks = append(r.Checks, SandboxCheckItem{Name: name, OK: nil, Message: message})
}

// CheckSandboxConfig tests a sandbox configuration without persisting it.
// @Summary      测试沙箱连通性
// @Description  使用当前填写的参数测试沙箱后端连通性，不保存配置；deep=true 会额外创建并销毁一个沙箱
// @Tags         系统
// @Accept       json
// @Produce      json
// @Param        body  body  SandboxCheckRequest  true  "沙箱配置"
// @Success      200   {object}  SandboxCheckResponse
// @Router       /system/sandbox-check [post]
func (h *SystemHandler) CheckSandboxConfig(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req SandboxCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "请求体格式错误"})
		return
	}
	tenant, _ := types.TenantInfoFromContext(c.Request.Context())
	if tenant == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "空间为空"})
		return
	}

	var stored *types.TenantSandboxConfig
	incoming := req.Config
	if req.ConfigID != "" {
		if h.sandboxConfigSvc == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "沙箱配置服务不可用"})
			return
		}
		entity, err := h.sandboxConfigSvc.Get(ctx, tenant.ID, req.ConfigID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		if entity == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "沙箱配置不存在"})
			return
		}
		stored = entity.Config
		if incoming == nil {
			incoming = stored
		}
	}
	merged, err := service.SanitizeSandboxConfig(incoming, stored)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	effective, err := sandbox.ResolveEffectiveConfig(merged, buildCheckBaselineConfig(merged))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": err.Error()})
		return
	}

	result := &SandboxCheckResponse{OK: true, Provider: string(effective.Type)}

	client, err := sandbox.NewRemoteClientForCheck(effective)
	if err != nil {
		result.add("client_build", false, err.Error(), 0)
		c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
		return
	}

	// Level 1: an authenticated control-plane call, which validates endpoint
	// reachability AND credentials in a single round-trip.
	start := time.Now()
	healthErr := client.Health(ctx)
	latency := time.Since(start).Milliseconds()
	if healthErr != nil {
		result.add("api_url_reachable", false, sandboxCheckReason(healthErr), latency)
		result.skip("credential_valid", "未检测（控制面不可达）")
	} else {
		result.add("api_url_reachable", true, "", latency)
		result.add("credential_valid", true, "", 0)
	}

	caps := client.Capabilities()
	result.Capabilities = map[string]bool{
		"supports_volumes":      caps.SupportsVolumes,
		"supports_pause_resume": caps.SupportsPauseResume,
		"supports_reconnect":    caps.SupportsReconnect,
	}

	if !req.Deep || healthErr != nil {
		result.skip("template_exists", "未检测（需完整验证）")
		result.skip("sandbox_exec", "未检测（需完整验证）")
		result.skip("egress_available", "未检测（需完整验证）")
		c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
		return
	}

	h.runDeepSandboxCheck(ctx, client, effective, result)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// runDeepSandboxCheck creates one throwaway sandbox and verifies a command can
// run inside it. The sandbox is always deleted, including on failure.
func (h *SystemHandler) runDeepSandboxCheck(
	ctx context.Context,
	client sandbox.RemoteSandboxClient,
	cfg *sandbox.Config,
	result *SandboxCheckResponse,
) {
	probeCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	handle, err := client.Create(probeCtx, sandbox.RemoteCreateRequest{
		TemplateID: sandbox.EffectiveTemplateID(cfg),
		Timeout: sandbox.RemoteTimeoutPolicy{
			Mode:   sandbox.RemoteTimeoutExplicit,
			Value:  2 * time.Minute,
			Action: sandbox.RemoteOnTimeoutKill,
		},
	})
	if err != nil {
		result.add("template_exists", false, sandboxCheckReason(err), 0)
		result.skip("sandbox_exec", "未检测（沙箱未创建）")
		result.skip("egress_available", "未检测（沙箱未创建）")
		return
	}
	defer func() {
		// Detach from ctx so cleanup still runs if the request was cancelled;
		// a leaked probe sandbox would otherwise sit there billing.
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.WithoutCancel(ctx), 30*time.Second)
		defer cleanupCancel()
		if err := client.Delete(cleanupCtx, handle.ID()); err != nil {
			logger.Warnf(ctx, "[SandboxCheck] failed to delete probe sandbox %s: %v",
				handle.ID(), err)
		}
	}()
	result.add("template_exists", true, "", 0)

	const marker = "weknora-ok"
	start := time.Now()
	execResult, err := client.Exec(probeCtx, handle, sandbox.RemoteExecRequest{
		Command: "echo",
		Args:    []string{marker},
		User:    sandbox.DefaultSandboxExecUser,
		Timeout: 30 * time.Second,
	})
	latency := time.Since(start).Milliseconds()
	switch {
	case err != nil:
		result.add("sandbox_exec", false, sandboxCheckReason(err), latency)
		result.skip("egress_available", "未检测（沙箱执行失败）")
		return
	case execResult == nil || !strings.Contains(execResult.Stdout, marker):
		result.add("sandbox_exec", false, "命令输出与预期不符", latency)
		result.skip("egress_available", "未检测（沙箱执行失败）")
		return
	default:
		result.add("sandbox_exec", true, "", latency)
	}

	h.probeSandboxEgress(probeCtx, client, handle, result)
}

// egressProbeTargets are tried in order; the first reachable one passes
// egress_available. Domestic and international endpoints cover regional
// egress policies without requiring both to succeed.
var egressProbeTargets = []struct {
	label string
	url   string
}{
	{label: "cn:baidu", url: "https://www.baidu.com"},
	{label: "intl:1.1.1.1", url: "https://1.1.1.1"},
}

// probeSandboxEgress verifies the sandbox can reach the public internet.
// Any single target succeeding is enough — skill installs only need some
// outbound path, not both CN and international reachability.
func (h *SystemHandler) probeSandboxEgress(
	ctx context.Context,
	client sandbox.RemoteSandboxClient,
	handle sandbox.RemoteSandboxHandle,
	result *SandboxCheckResponse,
) {
	// Echo which target succeeded so the UI message is actionable when
	// only one region is reachable. First success exits 0 immediately.
	var b strings.Builder
	for _, target := range egressProbeTargets {
		fmt.Fprintf(&b,
			`if curl -fsS -o /dev/null -m 8 -I %s; then echo %s; exit 0; fi; `,
			shellSingleQuote(target.url), shellSingleQuote(target.label))
	}
	b.WriteString(`echo "all probes failed" >&2; exit 1`)

	start := time.Now()
	execResult, err := client.Exec(ctx, handle, sandbox.RemoteExecRequest{
		Command: b.String(),
		Shell:   true,
		User:    sandbox.DefaultSandboxExecUser,
		Timeout: 30 * time.Second,
	})
	latency := time.Since(start).Milliseconds()
	switch {
	case err != nil:
		result.add("egress_available", false, sandboxCheckReason(err), latency)
	case execResult == nil:
		result.add("egress_available", false, "出网探测无返回", latency)
	case execResult.Killed:
		result.add("egress_available", false, "出网探测超时", latency)
	case execResult.ExitCode != 0:
		msg := strings.TrimSpace(execResult.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(execResult.Stdout)
		}
		if msg == "" {
			msg = "国内与国际探测目标均不可达"
		}
		result.add("egress_available", false, msg, latency)
	default:
		hit := strings.TrimSpace(execResult.Stdout)
		if hit == "" {
			hit = "ok"
		}
		result.add("egress_available", true, "reachable via "+hit, latency)
	}
}

// shellSingleQuote wraps s for safe inclusion in a single-quoted shell string.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildCheckBaselineConfig returns the baseline the submitted overrides are
// merged onto. The probe must exercise exactly what the tenant supplied, so
// the baseline only carries the selected provider's type.
func buildCheckBaselineConfig(cfg *types.TenantSandboxConfig) *sandbox.Config {
	baseline := sandbox.DefaultConfig()
	baseline.FallbackEnabled = false
	if cfg != nil && cfg.SandboxType != "" {
		if resolved, err := sandbox.ParseSandboxType(cfg.SandboxType); err == nil {
			baseline.Type = resolved
		}
	}
	return baseline
}

// sandboxCheckReason turns a provider error into a readable cause using the
// adapter's normalized RemoteError.Kind, so the UI never shows raw SDK text.
func sandboxCheckReason(err error) string {
	if err == nil {
		return ""
	}
	var remoteErr *sandbox.RemoteError
	if !stderrors.As(err, &remoteErr) {
		return err.Error()
	}
	switch remoteErr.Kind {
	case sandbox.RemoteErrorKindAuthentication:
		return "认证失败：API Key 无效或无权限"
	case sandbox.RemoteErrorKindNotFound:
		return "资源不存在：请检查模板 ID"
	case sandbox.RemoteErrorKindTimeout:
		return "请求超时：端点不可达或响应过慢"
	case sandbox.RemoteErrorKindUnavailable:
		return "服务不可用：端点拒绝连接"
	case sandbox.RemoteErrorKindCapacity:
		return "配额不足或触发限流"
	case sandbox.RemoteErrorKindUnsupported:
		return "该后端不支持此操作"
	case sandbox.RemoteErrorKindInvalidRequest:
		return "参数无效：" + remoteErr.Message
	default:
		return remoteErr.Message
	}
}
