package intentgate

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// fakeCountingJudge 记录调用次数并按序返回预设 verdict 的 Judge 测试缝。
type fakeCountingJudge struct {
	calls    int
	inputs   []JudgeInput
	verdicts []Verdict
}

func (f *fakeCountingJudge) Judge(_ context.Context, in JudgeInput) (Verdict, error) {
	f.calls++
	f.inputs = append(f.inputs, in)
	if len(f.verdicts) == 0 {
		return Verdict{Action: ActionAllow, Layer: LayerJudge}, nil
	}
	v := f.verdicts[0]
	f.verdicts = f.verdicts[1:]
	return v, nil
}

func cacheTestInput() JudgeInput {
	return JudgeInput{
		TenantID:       7,
		SessionID:      "s-1",
		PolicyID:       "pol-1",
		ConstraintText: "约束",
		ToolName:       "refund",
		Args:           json.RawMessage(`{"amount":100}`),
	}
}

// 验收 [unit]：同 session 同 (policy_id, args_digest) 二次判定不重复
// 调用模型（issue #16）。
func TestCachingJudgeHitSkipsModel(t *testing.T) {
	inner := &fakeCountingJudge{verdicts: []Verdict{
		{Action: ActionDeny, Layer: LayerJudge, Reason: "第一次判定", JudgeTokens: 42},
	}}
	judge := NewCachingJudge(inner)

	first, err := judge.Judge(context.Background(), cacheTestInput())
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	second, err := judge.Judge(context.Background(), cacheTestInput())
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("model calls = %d, want 1（缓存应拦截第二次）", inner.calls)
	}
	if second.Action != first.Action || second.Reason != first.Reason {
		t.Fatalf("cached verdict = %+v, want %+v", second, first)
	}
	// 命中缓存时成本记账保持"只付过一次"：JudgeTokens 原样携带。
	if second.JudgeTokens != 42 {
		t.Fatalf("cached JudgeTokens = %d, want 42", second.JudgeTokens)
	}
}

// 键的任一维度不同都必须重新判定：args、session、policy、tenant。
func TestCachingJudgeKeyVariation(t *testing.T) {
	inner := &fakeCountingJudge{}
	judge := NewCachingJudge(inner)
	base := cacheTestInput()

	variants := []JudgeInput{
		func() JudgeInput { in := base; in.Args = json.RawMessage(`{"amount":101}`); return in }(),
		func() JudgeInput { in := base; in.SessionID = "s-2"; return in }(),
		func() JudgeInput { in := base; in.PolicyID = "pol-2"; return in }(),
		func() JudgeInput { in := base; in.TenantID = 8; return in }(),
	}
	judge.Judge(context.Background(), base)
	for _, v := range variants {
		if _, err := judge.Judge(context.Background(), v); err != nil {
			t.Fatalf("Judge: %v", err)
		}
	}
	if inner.calls != 1+len(variants) {
		t.Fatalf("model calls = %d, want %d（每个变体都应穿透缓存）", inner.calls, 1+len(variants))
	}
}

// 参数键序不同但逻辑相同 → digest 相同 → 命中缓存（与 verdict 表
// args_digest 同口径的规范化）。
func TestCachingJudgeDigestCanonicalization(t *testing.T) {
	inner := &fakeCountingJudge{}
	judge := NewCachingJudge(inner)

	in := cacheTestInput()
	in.Args = json.RawMessage(`{"a":1,"b":2}`)
	if _, err := judge.Judge(context.Background(), in); err != nil {
		t.Fatalf("Judge: %v", err)
	}
	reordered := in
	reordered.Args = json.RawMessage(`{"b":2,"a":1}`) // 同值不同物理序
	if _, err := judge.Judge(context.Background(), reordered); err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("model calls = %d, want 1（同 digest 应命中）", inner.calls)
	}
}

// 缓存过期后重新调用模型。
func TestCachingJudgeExpiry(t *testing.T) {
	inner := &fakeCountingJudge{}
	judge := NewCachingJudge(inner, WithJudgeCacheTTL(30*time.Millisecond))

	if _, err := judge.Judge(context.Background(), cacheTestInput()); err != nil {
		t.Fatalf("Judge: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := judge.Judge(context.Background(), cacheTestInput()); err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if inner.calls != 2 {
		t.Fatalf("model calls = %d, want 2（TTL 过期应重新判定）", inner.calls)
	}
}

// Enabled 委托内层能力档（T31 降级检查不被缓存包装短路）。
func TestCachingJudgeEnabledDelegation(t *testing.T) {
	// 内层支持能力档：false 原样透出。
	weak := NewCachingJudge(&fakeTieredJudge{enabled: false})
	if weak.Enabled(context.Background(), 7) {
		t.Fatal("内层弱档时 CachingJudge.Enabled 应返回 false")
	}
	// 内层不支持能力档：缺省恒可用。
	plain := NewCachingJudge(&fakeCountingJudge{})
	if !plain.Enabled(context.Background(), 7) {
		t.Fatal("内层无能力档时 CachingJudge.Enabled 应返回 true（缺省语义）")
	}
}

// judge 产出的 JudgeTokens 经 PolicyGate 透传到 verdict（落库前最后一站）。
func TestPolicyGatePassesJudgeTokens(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy("")}
	judge := &fakeJudge{verdict: Verdict{
		Action: ActionAllow, Layer: LayerJudge, Reason: "ok", JudgeTokens: 128,
	}}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID:  1,
		SessionID: "s-9",
		ToolName:  "wiki_delete_page",
		Args:      json.RawMessage(`{"page_id":"p_1"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.JudgeTokens != 128 {
		t.Fatalf("JudgeTokens = %d, want 128（LLMJudge 计量 → PolicyGate 透传）", v.JudgeTokens)
	}
}
