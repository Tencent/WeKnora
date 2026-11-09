package session

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestTraceMetadataFromRequestFullDimensions(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey,
		&types.Tenant{Name: "acme"})
	ctx = context.WithValue(ctx, types.LanguageContextKey, "zh-CN")

	reqCtx := &qaRequestContext{
		ctx:     ctx,
		channel: "api",
		customAgent: &types.CustomAgent{
			ID:   "agent-42",
			Name: "Support Bot",
			Config: types.CustomAgentConfig{
				AgentMode: types.AgentModeSmartReasoning,
			},
		},
	}

	kv := traceMetadataFromRequest(reqCtx)
	for k, want := range map[string]interface{}{
		"channel":     "api",
		"agent_id":    "agent-42",
		"agent_name":  "Support Bot",
		"agent_mode":  types.AgentModeSmartReasoning,
		"tenant_name": "acme",
		"language":    "zh-CN",
	} {
		if got, ok := kv[k]; !ok || got != want {
			t.Errorf("kv[%q] = %v, want %v", k, got, want)
		}
	}
}

func TestTraceMetadataFromRequestOmitsUnsetDimensions(t *testing.T) {
	// No agent, no channel, no tenant info on ctx — only language remains
	// (LanguageFromContextOrDefault always yields a value).
	reqCtx := &qaRequestContext{ctx: context.Background()}

	kv := traceMetadataFromRequest(reqCtx)
	for _, k := range []string{"channel", "agent_id", "agent_name", "agent_mode", "tenant_name"} {
		if _, exists := kv[k]; exists {
			t.Errorf("key %q must be omitted when unset", k)
		}
	}
	if kv["language"] == "" {
		t.Error("language must always be present (defaulted)")
	}
}

func TestTraceMetadataFromRequestPartialAgent(t *testing.T) {
	// An agent with an ID but no name/mode must contribute agent_id only.
	reqCtx := &qaRequestContext{
		ctx:         context.Background(),
		customAgent: &types.CustomAgent{ID: "agent-7"},
	}

	kv := traceMetadataFromRequest(reqCtx)
	if kv["agent_id"] != "agent-7" {
		t.Errorf("agent_id = %v, want agent-7", kv["agent_id"])
	}
	for _, k := range []string{"agent_name", "agent_mode"} {
		if _, exists := kv[k]; exists {
			t.Errorf("key %q must be omitted for a nameless agent", k)
		}
	}
}

func TestTraceMetadataFromRequestTenantWithoutName(t *testing.T) {
	// A tenant record without a name must not produce an empty tenant_name.
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey,
		&types.Tenant{Name: ""})
	reqCtx := &qaRequestContext{ctx: ctx}

	kv := traceMetadataFromRequest(reqCtx)
	if _, exists := kv["tenant_name"]; exists {
		t.Error("tenant_name must be omitted when the tenant has no name")
	}
}

func TestBackfillTraceMetadataNoopWithoutTrace(t *testing.T) {
	// Init installs a disabled manager (or none at all in other tests); the
	// backfill must be a safe no-op either way — no panic, no side effects.
	// The returns are irrelevant here: a disabled manager is the point.
	_, _ = langfuse.Init(langfuse.Config{Enabled: false})
	t.Cleanup(func() {
		_, _ = langfuse.Init(langfuse.Config{Enabled: false})
	})

	backfillTraceMetadata(nil)
	backfillTraceMetadata(&qaRequestContext{})
	backfillTraceMetadata(&qaRequestContext{ctx: context.Background()})
}
