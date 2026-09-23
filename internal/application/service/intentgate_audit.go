// IntentGate enforce 拦截的 audit 接线（T43，issue #20）。
//
// enforce 模式的 deny 写 audit_logs（action=intent_policy.enforced_deny）：
// 策略自动判定的拒绝是租户管理员必须可追溯的合规事件（谁在什么会话里
// 被哪条策略拦了什么调用）。observe 的 deny 只进 verdict 观测表——audit
// 是"动作"日志，observe 没有动作。
//
// 审计面必须 fail-open：写入失败只 warn，绝不影响已经发生的拦截和
// agent 的自我纠错路径。
package service

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// buildEnforceDenyAuditor 返回 engine 的 enforce-deny 审计回调。
// auditSvc 为 nil 时返回 nil（调用方据此不安装回调，行为零变化）。
func buildEnforceDenyAuditor(auditSvc interfaces.AuditLogService) func(context.Context, agent.EnforceDenyInfo) {
	if auditSvc == nil {
		return nil
	}
	return func(ctx context.Context, info agent.EnforceDenyInfo) {
		v := info.Verdict
		// Actor：优先 Caller（真实用户 id + 角色）；IM/embed 等无 Caller
		// 的通道退回 Principal.StorageID()（如 web_user:xxx）。
		actorID, actorRole := "", ""
		caller := types.CallerFromContext(ctx)
		if caller.UserID != "" {
			actorID = caller.UserID
			actorRole = string(caller.Role)
		} else if principal, ok := types.PrincipalFromContext(ctx); ok && principal.Valid() {
			// IM/embed 等无用户 Caller 的通道退回 Principal.StorageID()。
			actorID = principal.StorageID()
		}
		details, _ := json.Marshal(map[string]interface{}{
			"session_id":     info.SessionID,
			"tool_name":      info.ToolName,
			"tool_call_id":   info.ToolCallID,
			"policy_version": v.PolicyVersion,
			"layer":          string(v.Layer),
			"reason":         v.Reason,
		})
		entry := &types.AuditLog{
			TenantID:    info.TenantID,
			ActorUserID: actorID,
			ActorRole:   actorRole,
			Action:      types.AuditActionIntentPolicyEnforcedDeny,
			TargetType:  "intent_policy",
			TargetID:    v.PolicyID,
			Outcome:     types.AuditOutcomeDenied,
			Details:     types.JSON(details),
		}
		if err := auditSvc.Log(ctx, entry); err != nil {
			logger.Warnf(ctx, "[IntentGate] audit enforced deny failed (fail-open): %v", err)
		}
	}
}
