package mcp

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/client/transport"
)

func TestAsOAuthRequired(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if got := asOAuthRequired(nil); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})

	t.Run("401 with RFC 9728 metadata is treated as OAuth required", func(t *testing.T) {
		meta := "https://example.com/.well-known/oauth-protected-resource"
		err := fmt.Errorf("wrap: %w", &transport.AuthorizationRequiredError{ResourceMetadataURL: meta})
		got := asOAuthRequired(err)
		if got == nil {
			t.Fatal("expected non-nil OAuthRequiredError")
		}
		if got.MetadataURL != meta {
			t.Errorf("MetadataURL = %q, want %q", got.MetadataURL, meta)
		}
	})

	t.Run("bare 401 without metadata is NOT OAuth required", func(t *testing.T) {
		err := &transport.AuthorizationRequiredError{ResourceMetadataURL: ""}
		if got := asOAuthRequired(err); got != nil {
			t.Fatalf("got %v, want nil (bare 401 should not suggest OAuth)", got)
		}
	})

	t.Run("unrelated error is ignored", func(t *testing.T) {
		if got := asOAuthRequired(errors.New("connection refused")); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
}

func TestApplyAuthHeaders(t *testing.T) {
	tests := []struct {
		name string
		ac   *types.MCPAuthConfig
		want map[string]string
	}{
		{
			name: "nil config injects nothing",
			ac:   nil,
			want: map[string]string{},
		},
		{
			name: "api_key uses default X-API-Key header",
			ac:   &types.MCPAuthConfig{AuthType: types.MCPAuthAPIKey, APIKey: "k1"},
			want: map[string]string{"X-API-Key": "k1"},
		},
		{
			name: "api_key honors custom header name (e.g. raw token in Authorization)",
			ac: &types.MCPAuthConfig{
				AuthType:     types.MCPAuthAPIKey,
				APIKey:       "f7bfde",
				APIKeyHeader: "Authorization",
			},
			want: map[string]string{"Authorization": "f7bfde"},
		},
		{
			name: "bearer adds Bearer prefix",
			ac:   &types.MCPAuthConfig{AuthType: types.MCPAuthBearer, Token: "t1"},
			want: map[string]string{"Authorization": "Bearer t1"},
		},
		{
			name: "selected strategy is exclusive — stale token is not emitted",
			ac: &types.MCPAuthConfig{
				AuthType: types.MCPAuthAPIKey,
				APIKey:   "k1",
				Token:    "stale",
			},
			want: map[string]string{"X-API-Key": "k1"},
		},
		{
			name: "empty AuthType keeps legacy behavior (infer from fields)",
			ac: &types.MCPAuthConfig{
				AuthType: types.MCPAuthNone,
				APIKey:   "k1",
				Token:    "t1",
			},
			want: map[string]string{"X-API-Key": "k1", "Authorization": "Bearer t1"},
		},
		{
			name: "custom headers are always layered on top",
			ac: &types.MCPAuthConfig{
				AuthType:      types.MCPAuthBearer,
				Token:         "t1",
				CustomHeaders: map[string]string{"X-Trace": "abc"},
			},
			want: map[string]string{"Authorization": "Bearer t1", "X-Trace": "abc"},
		},
		{
			name: "oauth strategy emits no static header (handled elsewhere)",
			ac:   &types.MCPAuthConfig{AuthType: types.MCPAuthOAuth, Token: "ignored"},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := map[string]string{}
			applyAuthHeaders(headers, tt.ac)
			if len(headers) != len(tt.want) {
				t.Fatalf("header count = %d, want %d (%v)", len(headers), len(tt.want), headers)
			}
			for k, v := range tt.want {
				if headers[k] != v {
					t.Errorf("header[%q] = %q, want %q", k, headers[k], v)
				}
			}
		})
	}
}
