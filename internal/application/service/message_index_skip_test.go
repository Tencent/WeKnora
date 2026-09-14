package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// The stats endpoint reports the feature as enabled based on
// ChatHistoryConfig.Enabled alone, but IndexMessageToKB only indexes when
// IsConfigured() holds - which also requires an embedding model and an
// auto-created KB. When those disagree the operator sees "indexed messages: 0"
// with no log line explaining it. These cases pin the reason string so each
// skip is attributable from the logs.
func TestDescribeChatHistorySkip(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() context.Context
		want string
	}{
		{
			name: "no tenant in context",
			ctx:  func() context.Context { return context.Background() },
			want: "no tenant in context",
		},
		{
			name: "config not set",
			ctx: func() context.Context {
				return contextWithTenant(&types.Tenant{})
			},
			want: "chat history config not set",
		},
		{
			name: "disabled",
			ctx: func() context.Context {
				return contextWithTenant(&types.Tenant{
					ChatHistoryConfig: &types.ChatHistoryConfig{Enabled: false},
				})
			},
			want: "chat history indexing disabled",
		},
		{
			name: "enabled without embedding model",
			ctx: func() context.Context {
				return contextWithTenant(&types.Tenant{
					ChatHistoryConfig: &types.ChatHistoryConfig{
						Enabled:         true,
						KnowledgeBaseID: "kb-1",
					},
				})
			},
			want: "enabled but no embedding model selected",
		},
		{
			name: "enabled without knowledge base",
			ctx: func() context.Context {
				return contextWithTenant(&types.Tenant{
					ChatHistoryConfig: &types.ChatHistoryConfig{
						Enabled:          true,
						EmbeddingModelID: "emb-1",
					},
				})
			},
			want: "enabled but chat history knowledge base not created yet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeChatHistorySkip(tt.ctx()); got != tt.want {
				t.Errorf("describeChatHistorySkip() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A fully configured tenant must not be reported as a skip reason: it is the
// one case where IndexMessageToKB proceeds, so the fallback string here would
// mean the helper and IsConfigured() disagree.
func TestDescribeChatHistorySkipFullyConfigured(t *testing.T) {
	cfg := &types.ChatHistoryConfig{
		Enabled:          true,
		EmbeddingModelID: "emb-1",
		KnowledgeBaseID:  "kb-1",
	}
	if !cfg.IsConfigured() {
		t.Fatal("fixture is not fully configured; test cannot check the agreement")
	}
	got := describeChatHistorySkip(contextWithTenant(&types.Tenant{ChatHistoryConfig: cfg}))
	if got != "chat history config incomplete" {
		t.Errorf("unexpected reason for a configured tenant: %q", got)
	}
}

func contextWithTenant(tenant *types.Tenant) context.Context {
	return context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
}
