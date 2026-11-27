package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestSandboxConnectionCheckE2BControlPlaneAuth(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("a", 32))
	gin.SetMode(gin.TestMode)
	const route = "/api/v1/system/sandbox-check"
	r := gin.New()
	r.POST(route, (&SystemHandler{}).CheckSandboxConfig)

	for _, tc := range []struct {
		name        string
		requireAuth bool
		invalidKey  bool
		wantCalls   int32
	}{
		{name: "bearer gateway", requireAuth: true, wantCalls: 2},
		{name: "native API key", wantCalls: 1},
		{name: "invalid API key", invalidKey: true, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const key = "test-e2b-api-key"
			const missingAuthorization = `{"code":401,"message":"authorization header is missing"}`
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				call := calls.Add(1)
				assert.Equal(t, http.MethodGet, req.Method)
				assert.Equal(t, "/gateway/v2/sandboxes", req.URL.Path)
				assert.Equal(t, "1", req.URL.Query().Get("limit"))
				assert.Equal(t, key, req.Header.Get("X-API-Key"))
				wantAuthorization := ""
				if tc.requireAuth && call > 1 {
					wantAuthorization = "Bearer " + key
				}
				assert.Equal(t, wantAuthorization, req.Header.Get("Authorization"))
				switch {
				case tc.invalidKey:
					http.Error(w, `{"code":401,"message":"invalid API key"}`, http.StatusUnauthorized)
				case tc.requireAuth && req.Header.Get("Authorization") != "Bearer "+key:
					t.Logf("provider returned %s", missingAuthorization)
					http.Error(w, missingAuthorization, http.StatusUnauthorized)
				default:
					_, _ = fmt.Fprint(w, "[]")
				}
			}))
			t.Cleanup(provider.Close)

			body := fmt.Sprintf(
				`{"config":{"sandbox_type":"e2b","allow_private_endpoints":true,`+
					`"e2b":{"api_url":%q,"api_key":%q,"sandbox_domain":"e2b.app"}},"deep":false}`,
				provider.URL+"/gateway", key,
			)
			req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(body))
			req = req.WithContext(context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 1}))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var payload struct {
				Data SandboxCheckResponse `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
			require.Equal(t, !tc.invalidKey, payload.Data.OK, w.Body.String())
			require.Equal(t, tc.wantCalls, calls.Load())
		})
	}
}

func TestSandboxConnectionCheckConfigAllowsTemplateDiscoveryAfterConnection(t *testing.T) {
	incoming := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "key"},
	}

	got := sandboxConnectionCheckConfig(incoming)

	require.Equal(t, "__connection_check__", got.E2B.TemplateID)
	require.Empty(t, incoming.E2B.TemplateID, "the submitted form must not be mutated")
}

func TestSandboxCheckReasonDockerUnavailableIncludesHost(t *testing.T) {
	msg := sandboxCheckReason(&sandbox.RemoteError{
		Kind:     sandbox.RemoteErrorKindUnavailable,
		Provider: sandbox.SandboxTypeDocker,
		Message:  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
	})
	require.Contains(t, msg, "unix:///var/run/docker.sock")
	require.Contains(t, msg, "docker context")
}

func TestRunStatelessSandboxCheckRemovedWithLocalBackend(t *testing.T) {
	incoming := &types.TenantSandboxConfig{SandboxType: "local"}

	_, err := sandbox.ResolveEffectiveConfig(incoming, sandbox.DefaultConfig())

	require.ErrorIs(t, err, sandbox.ErrUnsupportedSandboxType)
}

// The probe has to run under the policy the admin configured, or
// egress_available answers a question nobody asked: whether the provider's
// default allows egress.
func TestDeepSandboxCheckUsesConfiguredNetworkPolicy(t *testing.T) {
	incoming := &types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIURL: "https://203.0.113.20", ProxyURL: "https://203.0.113.20",
			SandboxDomain: "cube.app", TemplateID: "tpl-1",
		},
		Network: &types.SandboxNetworkPolicy{
			DenyEgressByDefault: true,
			AllowOut:            []string{"api.example.com"},
		},
	}

	effective, err := sandbox.ResolveEffectiveConfig(incoming, sandbox.DefaultConfig())
	require.NoError(t, err)

	require.NotNil(t, effective.Network.AllowInternetAccess)
	require.False(t, *effective.Network.AllowInternetAccess)
	require.Equal(t, []string{"api.example.com"}, effective.Network.AllowOut)
}

// Under a deny-by-default policy a blocked probe is the policy working, not a
// misconfiguration, so it must not be reported as a failure.
func TestDeepSandboxCheckReportsEgressRestrictedRatherThanFailed(t *testing.T) {
	result := &SandboxCheckResponse{OK: true, Provider: "cube"}
	denied := false

	reportEgressProbe(result, sandbox.RemoteNetworkPolicy{
		AllowInternetAccess: &denied,
		AllowOut:            []string{"api.example.com"},
	}, false, "curl: (28) timeout", 0)

	require.True(t, result.OK, "a policy-blocked probe must not fail the check")
	item := result.Checks[len(result.Checks)-1]
	require.Equal(t, "egress_available", item.Name)
	require.Nil(t, item.OK)
	require.Equal(t, skipReasonEgressRestrictedByPolicy, item.Reason)
	require.Empty(t, item.Message, "skip reasons are localized by the UI")
}

// ValidateSandboxNetworkPolicy accepts two equivalent spellings of the
// deny-all fallback — "默认拒绝" and a 0.0.0.0/0 entry in deny_out — and the
// drawer offers both. A config that used the second one is behaving exactly as
// designed when the probe is blocked, so reporting a hard failure would cry
// wolf over a correct configuration.
func TestDeepSandboxCheckReportsEgressRestrictedForDenyOutSpelling(t *testing.T) {
	result := &SandboxCheckResponse{OK: true, Provider: "cube"}
	allowed := true

	reportEgressProbe(result, sandbox.RemoteNetworkPolicy{
		AllowInternetAccess: &allowed,
		AllowOut:            []string{"api.example.com"},
		DenyOut:             []string{"0.0.0.0/0"},
	}, false, "curl: (28) timeout", 0)

	require.True(t, result.OK, "a policy-blocked probe must not fail the check")
	item := result.Checks[len(result.Checks)-1]
	require.Equal(t, "egress_available", item.Name)
	require.Nil(t, item.OK)
	require.Equal(t, skipReasonEgressRestrictedByPolicy, item.Reason)
	require.Empty(t, item.Message, "skip reasons are localized by the UI")
}

// A deny list that does not cover everything leaves the probe target reachable
// by default, so a blocked probe there is a genuine failure.
func TestDeepSandboxCheckStillFailsEgressWhenDenyListIsPartial(t *testing.T) {
	result := &SandboxCheckResponse{OK: true, Provider: "cube"}
	allowed := true

	reportEgressProbe(result, sandbox.RemoteNetworkPolicy{
		AllowInternetAccess: &allowed,
		DenyOut:             []string{"10.0.0.0/8"},
	}, false, "curl: (28) timeout", 0)

	require.False(t, result.OK)
	item := result.Checks[len(result.Checks)-1]
	require.NotNil(t, item.OK)
	require.False(t, *item.OK)
}

func TestDeepSandboxCheckStillFailsEgressWhenPolicyAllowsAll(t *testing.T) {
	result := &SandboxCheckResponse{OK: true, Provider: "cube"}
	allowed := true

	reportEgressProbe(result, sandbox.RemoteNetworkPolicy{
		AllowInternetAccess: &allowed,
	}, false, "curl: (28) timeout", 120)

	require.False(t, result.OK)
	item := result.Checks[len(result.Checks)-1]
	require.NotNil(t, item.OK)
	require.False(t, *item.OK)
}
