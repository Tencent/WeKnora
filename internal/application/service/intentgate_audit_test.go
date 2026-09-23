package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/intentgate"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// capturingAuditService 捕获 Log 条目的测试缝。
type capturingAuditService struct {
	interfaces.AuditLogService
	entries []*types.AuditLog
	err     error
}

func (c *capturingAuditService) Log(_ context.Context, entry *types.AuditLog) error {
	c.entries = append(c.entries, entry)
	return c.err
}

func auditTestInfo() agent.EnforceDenyInfo {
	return agent.EnforceDenyInfo{
		TenantID:   42,
		SessionID:  "sess-1",
		ToolName:   "wiki_delete_page",
		ToolCallID: "call-9",
		Verdict: intentgate.Verdict{
			Action:        intentgate.ActionDeny,
			PolicyID:      "pol-audit-1",
			PolicyVersion: 7,
			Mode:          types.VerdictModeEnforce,
			Layer:         intentgate.LayerRule,
			Reason:        "违反策略约束「禁止删除」",
		},
	}
}

// TestBuildEnforceDenyAuditorWritesEntry：audit 行六要素——action 常量、
// target=policy、outcome=denied、actor 来自 Caller ctx、details 含
// tool/policy_version/reason、tenant 正确。
func TestBuildEnforceDenyAuditorWritesEntry(t *testing.T) {
	svc := &capturingAuditService{}
	fn := buildEnforceDenyAuditor(svc)
	if fn == nil {
		t.Fatal("auditor must be non-nil for non-nil service")
	}

	ctx := context.Background()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	ctx = context.WithValue(ctx, types.CallerContextKey, types.Caller{
		TenantID: 42, UserID: "user-7", Role: types.TenantRoleAdmin,
	})
	fn(ctx, auditTestInfo())

	if len(svc.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(svc.entries))
	}
	e := svc.entries[0]
	if e.Action != types.AuditActionIntentPolicyEnforcedDeny {
		t.Fatalf("action = %q, want intent_policy.enforced_deny", e.Action)
	}
	if e.TargetType != "intent_policy" || e.TargetID != "pol-audit-1" {
		t.Fatalf("target = %s/%s", e.TargetType, e.TargetID)
	}
	if e.Outcome != types.AuditOutcomeDenied {
		t.Fatalf("outcome = %q, want denied", e.Outcome)
	}
	if e.TenantID != 42 || e.ActorUserID != "user-7" || e.ActorRole != string(types.TenantRoleAdmin) {
		t.Fatalf("actor/tenant = %d/%s/%s", e.TenantID, e.ActorUserID, e.ActorRole)
	}
	var details map[string]interface{}
	if err := json.Unmarshal(e.Details, &details); err != nil {
		t.Fatalf("details not JSON: %v", err)
	}
	if details["tool_name"] != "wiki_delete_page" ||
		details["session_id"] != "sess-1" ||
		details["policy_version"] != float64(7) ||
		details["reason"] == "" {
		t.Fatalf("details wrong: %v", details)
	}
}

// TestBuildEnforceDenyAuditorNilService：nil 服务 → nil 回调（调用方不安装）。
func TestBuildEnforceDenyAuditorNilService(t *testing.T) {
	if fn := buildEnforceDenyAuditor(nil); fn != nil {
		t.Fatal("nil service must yield nil auditor")
	}
}

// TestBuildEnforceDenyAuditorFailOpen：写入失败只吞掉不上抛（拦截路径
// 绝不能再被审计面放大错误）。
func TestBuildEnforceDenyAuditorFailOpen(t *testing.T) {
	svc := &capturingAuditService{err: errors.New("db down")}
	fn := buildEnforceDenyAuditor(svc)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("auditor must not panic on write failure: %v", r)
		}
	}()
	fn(context.Background(), auditTestInfo())
}

// TestBuildEnforceDenyAuditorFallsBackToPrincipal：无用户 Caller 的通道
// （IM/embed）actor 用 Principal.StorageID()。
func TestBuildEnforceDenyAuditorFallsBackToPrincipal(t *testing.T) {
	svc := &capturingAuditService{}
	fn := buildEnforceDenyAuditor(svc)
	ctx := types.WithPrincipal(context.Background(), types.Principal{Type: "web_user", ID: "abc"})
	fn(ctx, auditTestInfo())
	if len(svc.entries) != 1 || svc.entries[0].ActorUserID != "web_user:abc" {
		t.Fatalf("actor fallback wrong: %+v", svc.entries)
	}
}
