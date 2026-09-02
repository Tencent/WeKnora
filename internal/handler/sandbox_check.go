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
	// Deep additionally runs a throwaway script. For remote backends this also
	// creates and destroys one sandbox, which is the only way to validate the
	// template ID, data plane, in-sandbox execution, and outbound egress. It may
	// consume real sandbox time, so it is opt-in.
	Deep bool `json:"deep"`
}

// SandboxCheckItem is one probe outcome. OK is nil when the probe was not
// executed, which distinguishes "skipped" from "failed" in the UI.
type SandboxCheckItem struct {
	Name string `json:"name"`
	OK   *bool  `json:"ok"`
	// Message carries free-form provider detail for an executed probe.
	Message string `json:"message,omitempty"`
	// Reason is a stable code explaining why a probe was skipped. It exists so
	// the UI can phrase the skip in the operator's language instead of echoing
	// a server-side sentence.
	Reason    string `json:"reason,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

// Why a probe was not executed. Kept as codes because the operator reads them
// in the frontend, which is localized.
const (
	// The probe needs a real sandbox, so it only runs on an opt-in deep check.
	skipReasonNeedsDeepCheck = "needs_deep_check"
	// An earlier probe failed and this one cannot be reached without it.
	skipReasonControlPlaneUnreachable = "control_plane_unreachable"
	skipReasonSandboxNotCreated       = "sandbox_not_created"
	skipReasonSandboxExecFailed       = "sandbox_exec_failed"
	// The config denies egress by default, so a probe that cannot reach the
	// internet is the policy working as configured, not a fault.
	skipReasonEgressRestrictedByPolicy = "egress_restricted_by_policy"
)

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
func (r *SandboxCheckResponse) skip(name, reason string) {
	r.Checks = append(r.Checks, SandboxCheckItem{Name: name, OK: nil, Reason: reason})
}

// CheckSandboxConfig tests a sandbox configuration without persisting it.
// @Summary      测试沙箱连通性
// @Description  使用当前填写的参数测试沙箱后端，不保存配置；deep=true 会执行临时脚本，远端后端还会创建并销毁一个沙箱
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
	if !req.Deep {
		incoming = sandboxConnectionCheckConfig(incoming)
	}
	merged, err := service.SanitizeSandboxConfig(incoming, stored)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	effective, err := sandbox.ResolveEffectiveConfig(merged, sandbox.DefaultConfig())
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
		result.skip("credential_valid", skipReasonControlPlaneUnreachable)
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
		reason := skipReasonNeedsDeepCheck
		if healthErr != nil {
			reason = skipReasonControlPlaneUnreachable
		}
		result.skip("template_exists", reason)
		result.skip("sandbox_exec", reason)
		result.skip("egress_available", reason)
		c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
		return
	}

	h.runDeepSandboxCheck(ctx, client, effective, result)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// sandboxConnectionCheckConfig supplies a private template placeholder for a
// shallow remote probe. Connectivity is deliberately checked before template
// discovery in the settings wizard, and Health never uses this value.
func sandboxConnectionCheckConfig(cfg *types.TenantSandboxConfig) *types.TenantSandboxConfig {
	if cfg == nil {
		return nil
	}
	copy := *cfg
	switch sandbox.SandboxType(copy.SandboxType) {
	case sandbox.SandboxTypeCube:
		cube := types.CubeSandboxConfig{}
		if copy.Cube != nil {
			cube = *copy.Cube
		}
		if strings.TrimSpace(cube.TemplateID) == "" {
			cube.TemplateID = "__connection_check__"
		}
		copy.Cube = &cube
	case sandbox.SandboxTypeE2B:
		e2b := types.E2BSandboxConfig{}
		if copy.E2B != nil {
			e2b = *copy.E2B
		}
		if strings.TrimSpace(e2b.TemplateID) == "" {
			e2b.TemplateID = "__connection_check__"
		}
		copy.E2B = &e2b
	}
	return &copy
}

// describeProbeMismatch reports why the probe script did not print its marker.
// "命令输出与预期不符" on its own hides the exit code and the stderr line that
// normally name the actual problem — a missing interpreter, an image entrypoint
// that swallowed the command, or a bind mount that never reached the Docker VM.
func describeProbeMismatch(exitCode int, killed bool, stdout, stderr, execErr string) string {
	parts := []string{fmt.Sprintf("退出码 %d", exitCode)}
	if killed {
		parts = append(parts, "执行超时被终止")
	}
	switch {
	case firstProbeLine(stderr) != "":
		parts = append(parts, "stderr: "+firstProbeLine(stderr))
	case firstProbeLine(stdout) != "":
		parts = append(parts, "stdout: "+firstProbeLine(stdout))
	default:
		parts = append(parts, "没有任何输出")
	}
	if trimmed := strings.TrimSpace(execErr); trimmed != "" {
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, "；")
}

// firstProbeLine picks the first non-empty line and caps it, so one runaway log
// line cannot flood the check panel.
func firstProbeLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 300 {
			return trimmed[:300] + "…"
		}
		return trimmed
	}
	return ""
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
		// Probe under the admin's policy: the point of this check is whether
		// THIS config works, not whether the provider's default does.
		Network: cfg.Network,
		Timeout: sandbox.RemoteTimeoutPolicy{
			Mode:   sandbox.RemoteTimeoutExplicit,
			Value:  2 * time.Minute,
			Action: sandbox.RemoteOnTimeoutKill,
		},
	})
	if err != nil {
		result.add("template_exists", false, explainSandboxCreateFailure(ctx, client, cfg, err), 0)
		result.skip("sandbox_exec", skipReasonSandboxNotCreated)
		result.skip("egress_available", skipReasonSandboxNotCreated)
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
	logger.Infof(ctx,
		"[SandboxCheck] probe sandbox created id=%s provider=%s template=%s",
		handle.ID(), result.Provider, sandbox.EffectiveTemplateID(cfg),
	)

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
		logger.Warnf(ctx,
			"[SandboxCheck] sandbox_exec failed sandbox=%s latency_ms=%d ui_reason=%s detail=%s",
			handle.ID(), latency, sandboxCheckReason(err), sandbox.RemoteErrorDiagnostics(err),
		)
		result.add("sandbox_exec", false, sandboxCheckReason(err), latency)
		result.skip("egress_available", skipReasonSandboxExecFailed)
		return
	case execResult == nil:
		result.add("sandbox_exec", false, "沙箱没有返回执行结果", latency)
		result.skip("egress_available", skipReasonSandboxExecFailed)
		return
	case !strings.Contains(execResult.Stdout, marker):
		logger.Warnf(ctx,
			"[SandboxCheck] sandbox_exec probe mismatch sandbox=%s exit=%d killed=%v stdout=%q stderr=%q",
			handle.ID(), execResult.ExitCode, execResult.Killed,
			firstProbeLine(execResult.Stdout), firstProbeLine(execResult.Stderr),
		)
		result.add("sandbox_exec", false, describeProbeMismatch(
			execResult.ExitCode, execResult.Killed,
			execResult.Stdout, execResult.Stderr, "",
		), latency)
		result.skip("egress_available", skipReasonSandboxExecFailed)
		return
	default:
		result.add("sandbox_exec", true, "", latency)
	}

	h.probeSandboxEgress(probeCtx, client, handle, cfg.Network, result)
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
	policy sandbox.RemoteNetworkPolicy,
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
		reportEgressProbe(result, policy, false, sandboxCheckReason(err), latency)
	case execResult == nil:
		reportEgressProbe(result, policy, false, "出网探测无返回", latency)
	case execResult.Killed:
		reportEgressProbe(result, policy, false, "出网探测超时", latency)
	case execResult.ExitCode != 0:
		msg := strings.TrimSpace(execResult.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(execResult.Stdout)
		}
		if msg == "" {
			msg = "国内与国际探测目标均不可达"
		}
		reportEgressProbe(result, policy, false, msg, latency)
	default:
		hit := strings.TrimSpace(execResult.Stdout)
		if hit == "" {
			hit = "ok"
		}
		reportEgressProbe(result, policy, true, "reachable via "+hit, latency)
	}
}

// reportEgressProbe records the outbound-connectivity probe. Under a
// deny-by-default policy a blocked probe is the expected outcome — the probe
// target is not on the admin's allow list — so it is reported as "restricted
// by policy" instead of failing the whole check. Only a config that allows
// public egress can fail this probe.
func reportEgressProbe(
	result *SandboxCheckResponse,
	policy sandbox.RemoteNetworkPolicy,
	reachable bool,
	detail string,
	latencyMS int64,
) {
	if reachable {
		result.add("egress_available", true, detail, latencyMS)
		return
	}
	if !policy.DeniesEgressByDefault() {
		result.add("egress_available", false, detail, latencyMS)
		return
	}
	allowed := "（未配置放行目标）"
	if len(policy.AllowOut) > 0 {
		allowed = strings.Join(policy.AllowOut, "、")
	}
	result.Checks = append(result.Checks, SandboxCheckItem{
		Name:      "egress_available",
		OK:        nil,
		Reason:    skipReasonEgressRestrictedByPolicy,
		Message:   "该配置默认拒绝出网，仅放行：" + allowed,
		LatencyMS: latencyMS,
	})
}

// explainSandboxCreateFailure turns a failed sandbox creation into a cause the
// operator can act on.
//
// A 404 from the provider is not proof that the template is gone: the E2B SDK
// discards the response body of any 404 on the create endpoint and substitutes
// a fixed "template not found" sentence, so the message alone cannot tell a
// deleted template apart from one whose build cannot boot yet. The catalog is
// the only place that distinction exists, so it is consulted before blaming the
// template ID the admin just picked from a list.
func explainSandboxCreateFailure(
	ctx context.Context,
	client sandbox.RemoteSandboxClient,
	cfg *sandbox.Config,
	err error,
) string {
	reason := sandboxCheckReason(err)
	var remoteErr *sandbox.RemoteError
	if !stderrors.As(err, &remoteErr) {
		return reason
	}
	// A create-time 404 is classified as InvalidRequest, not NotFound, because
	// for the lifecycle it means "bad argument" rather than "sandbox is gone".
	// Diagnosis needs the status itself.
	if remoteErr.Kind != sandbox.RemoteErrorKindNotFound &&
		remoteErr.StatusCode != http.StatusNotFound {
		return reason
	}
	templateID := strings.TrimSpace(sandbox.EffectiveTemplateID(cfg))
	catalog, ok := client.(sandbox.RemoteTemplateCatalog)
	if !ok || templateID == "" {
		return reason
	}
	templates, listErr := catalog.ListTemplates(ctx)
	if listErr != nil {
		logger.Warnf(ctx, "[SandboxCheck] template lookup after create 404 failed: %v", listErr)
		return reason
	}
	for _, tpl := range templates {
		if tpl.ID != templateID && !strings.EqualFold(tpl.Name, templateID) {
			continue
		}
		if tpl.Status == sandbox.TemplateStatusUntagged {
			return fmt.Sprintf(
				"模板 %s 的构建已完成，但没有一个构建带上 %q 标签，创建沙箱时无法解析；"+
					"请重新构建该模板（删除后由 WeKnora 重新创建即可）",
				templateID, sandbox.DefaultE2BTemplateTag,
			)
		}
		if tpl.Status == "ready" || tpl.Status == "" {
			return fmt.Sprintf(
				"模板 %s 在列表中已就绪，但集群拒绝创建沙箱（HTTP 404）；"+
					"通常是构建快照尚未生效，请稍后重试或重新构建模板",
				templateID,
			)
		}
		return fmt.Sprintf(
			"模板 %s 存在，但构建状态为 %s，还不能启动沙箱；请等待构建完成或查看构建日志",
			templateID, tpl.Status,
		)
	}
	return fmt.Sprintf(
		"当前 API Key 看不到模板 %s：模板可能已删除，或该 Key 属于其他团队/集群",
		templateID,
	)
}

// shellSingleQuote wraps s for safe inclusion in a single-quoted shell string.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func dockerUnavailableCheckReason(err *sandbox.RemoteError) string {
	host := dockerHostFromUnavailableMessage(err.Message)
	if host == "" {
		detail := firstProbeLine(err.Message)
		if detail == "" {
			return "无法连接 Docker 守护进程"
		}
		return "无法连接 Docker 守护进程：" + detail
	}
	return "无法连接 Docker 守护进程 " + host +
		"。留空地址时跟随本机 docker CLI（DOCKER_HOST 或当前 docker context）。" +
		"Colima 一般是 unix://$HOME/.colima/default/docker.sock；" +
		"WeKnora 跑在容器里时需要把该 socket 挂进 app。"
}

func dockerHostFromUnavailableMessage(message string) string {
	const prefix = "Cannot connect to the Docker daemon at "
	rest, ok := strings.CutPrefix(message, prefix)
	if !ok {
		return ""
	}
	if end := strings.IndexAny(rest, " \t"); end > 0 {
		rest = rest[:end]
	}
	return strings.TrimSuffix(strings.TrimSpace(rest), ".")
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
		if remoteErr.Provider == sandbox.SandboxTypeDocker {
			return dockerUnavailableCheckReason(remoteErr)
		}
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
