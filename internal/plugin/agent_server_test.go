package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeController struct {
	mu      sync.Mutex
	started []StartPluginRequest
	stopped []string
	handle  *PluginHandle
}

func (f *fakeController) Start(_ context.Context, req StartPluginRequest) (*PluginHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, req)
	f.handle = &PluginHandle{PluginID: req.PluginID, ControlSocket: "/root/" + req.PluginID + "/control/plugin.sock", ContainerName: "weknora-plugin-" + req.PluginID}
	return f.handle, nil
}

func (f *fakeController) Stop(_ context.Context, pluginID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, pluginID)
	return nil
}

func (f *fakeController) CurrentHandle() *PluginHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handle
}

func (f *fakeController) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func (f *fakeController) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stopped)
}

func newFakeFactory() (ControllerFactory, *fakeController) {
	fc := &fakeController{}
	return func(string) PluginRuntimeController { return fc }, fc
}

func validStartRequest() StartPluginRequest {
	return StartPluginRequest{
		PluginID:        "weknora.dingtalk",
		Image:           "registry.example/dingtalk@sha256:abc",
		ExtensionType:   ExtensionDataSource,
		ProtocolVersion: ProtocolVersionV1,
		NetworkPolicy:   NetworkNone,
	}
}

func doStart(s *RuntimeAgentServer, token string, req StartPluginRequest) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/plugins/start", strings.NewReader(string(raw)))
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.handleStart(rec, httpReq)
	return rec
}

func doStop(s *RuntimeAgentServer, token, pluginID string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(map[string]string{"plugin_id": pluginID})
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/plugins/stop", strings.NewReader(string(raw)))
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.handleStop(rec, httpReq)
	return rec
}

func TestValidateStartRequest(t *testing.T) {
	require.NoError(t, validateStartRequest(validStartRequest()))

	bad := validStartRequest()
	bad.PluginID = "UPPER"
	require.Error(t, validateStartRequest(bad))

	bad = validStartRequest()
	bad.Image = ""
	require.Error(t, validateStartRequest(bad))

	bad = validStartRequest()
	bad.ExtensionType = "bogus"
	require.Error(t, validateStartRequest(bad))

	bad = validStartRequest()
	bad.ProtocolVersion = "v9"
	require.Error(t, validateStartRequest(bad))

	bad = validStartRequest()
	bad.NetworkPolicy = "egress"
	require.Error(t, validateStartRequest(bad))

	bad = validStartRequest()
	bad.NetworkPolicy = NetworkAllowlist
	bad.AllowedDestinations = nil
	require.Error(t, validateStartRequest(bad))
}

func TestImagePolicyCheck(t *testing.T) {
	policy := &ImagePolicy{allowlist: map[string]string{"weknora.dingtalk": "registry.example/dingtalk@sha256:abc"}}
	require.NoError(t, policy.Check("weknora.dingtalk", "registry.example/dingtalk@sha256:abc"))
	require.Error(t, policy.Check("weknora.dingtalk", "registry.example/dingtalk@sha256:def"))
	require.Error(t, policy.Check("weknora.other", "registry.example/dingtalk@sha256:abc"))

	dev := &ImagePolicy{development: true}
	require.NoError(t, dev.Check("anything", "any-image"))
}

func TestRebasePath(t *testing.T) {
	rel, ok := rebasePath("/run/root", "/run/root/a/b")
	require.True(t, ok)
	require.Equal(t, filepath.Join("a", "b"), rel)

	// prefix collision: /run/root-evil must not be treated as inside /run/root
	_, ok = rebasePath("/run/root", "/run/root-evil/a")
	require.False(t, ok)

	// escape
	_, ok = rebasePath("/run/root", "/run/other")
	require.False(t, ok)

	_, ok = rebasePath("/run/root", "/run/root/../etc")
	require.False(t, ok)
}

func TestAgentAuthorized(t *testing.T) {
	s := newRuntimeAgentServer("/root", "secret", nil, nil, "inst", nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/plugins/start", nil)
	require.False(t, s.authorized(req))
	req.Header.Set("Authorization", "Bearer wrong")
	require.False(t, s.authorized(req))
	req.Header.Set("Authorization", "Bearer secret")
	require.True(t, s.authorized(req))
}

func TestAgentStartIdempotent(t *testing.T) {
	factory, fc := newFakeFactory()
	s := newRuntimeAgentServer("/root", "secret", nil, nil, "inst", factory)
	req := validStartRequest()

	require.Equal(t, http.StatusOK, doStart(s, "secret", req).Code)
	require.Equal(t, http.StatusOK, doStart(s, "secret", req).Code)
	require.Equal(t, 1, fc.startCount())
}

func TestAgentStartReplacesOnChange(t *testing.T) {
	factory, fc := newFakeFactory()
	s := newRuntimeAgentServer("/root", "secret", nil, nil, "inst", factory)

	require.Equal(t, http.StatusOK, doStart(s, "secret", validStartRequest()).Code)

	changed := validStartRequest()
	changed.Image = "registry.example/dingtalk@sha256:def"
	require.Equal(t, http.StatusOK, doStart(s, "secret", changed).Code)

	require.Equal(t, 2, fc.startCount())
	require.Equal(t, 1, fc.stopCount()) // old instance stopped before new start
}

func TestAgentStartRejectsBadToken(t *testing.T) {
	factory, _ := newFakeFactory()
	s := newRuntimeAgentServer("/root", "secret", nil, nil, "inst", factory)
	require.Equal(t, http.StatusUnauthorized, doStart(s, "wrong", validStartRequest()).Code)
}

func TestAgentStartRejectsUnpinnedImage(t *testing.T) {
	factory, _ := newFakeFactory()
	policy := &ImagePolicy{allowlist: map[string]string{"weknora.dingtalk": "registry.example/dingtalk@sha256:abc"}}
	s := newRuntimeAgentServer("/root", "secret", nil, policy, "inst", factory)

	req := validStartRequest()
	req.Image = "registry.example/dingtalk@sha256:def"
	require.Equal(t, http.StatusForbidden, doStart(s, "secret", req).Code)
}

func TestAgentStopWithoutController(t *testing.T) {
	factory, fc := newFakeFactory()
	s := newRuntimeAgentServer("/root", "secret", nil, nil, "inst", factory)

	// Stop of a never-started plugin is idempotent (200) and does not panic.
	require.Equal(t, http.StatusOK, doStop(s, "secret", "weknora.dingtalk").Code)
	require.Equal(t, 0, fc.stopCount())

	// Start then stop reaches the controller.
	require.Equal(t, http.StatusOK, doStart(s, "secret", validStartRequest()).Code)
	require.Equal(t, http.StatusOK, doStop(s, "secret", "weknora.dingtalk").Code)
	require.Equal(t, 1, fc.stopCount())
}
