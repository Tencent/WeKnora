package approval

import (
	"context"
	"testing"
)

// TestIntentRequirementRoundTrip：ctx 载体写入/读取往返一致；未标记返回零值。
func TestIntentRequirementRoundTrip(t *testing.T) {
	if _, ok := IntentRequirementFromContext(context.Background()); ok {
		t.Fatal("unmarked ctx must report no requirement")
	}
	ctx := WithIntentRequirement(context.Background(), "违反策略约束「X」")
	reason, ok := IntentRequirementFromContext(ctx)
	if !ok || reason != "违反策略约束「X」" {
		t.Fatalf("round trip = %q/%v", reason, ok)
	}
	// 子 ctx 继承标记（engine 的执行 ctx 是从挂标记的 ctx 派生的）。
	type key struct{}
	child := context.WithValue(ctx, key{}, 1)
	if reason, ok := IntentRequirementFromContext(child); !ok || reason == "" {
		t.Fatalf("child ctx must inherit requirement, got %q/%v", reason, ok)
	}
}
