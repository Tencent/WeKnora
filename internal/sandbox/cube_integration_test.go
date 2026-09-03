//go:build integration

// Package sandbox integration tests against a locally-running CubeSandbox
// deployment. These tests DO NOT use any mock — they connect straight to a
// live CubeAPI + CubeProxy pair. Enable them with the `integration` build tag:
//
//	CUBE_API_URL=http://127.0.0.1:33000 \
//	CUBE_PROXY_URL=http://127.0.0.1:12088 \
//	go test -tags=integration -run Integration -count=1 ./internal/sandbox/...
//
// If the environment variables are unset the tests fall back to the local
// dev defaults (127.0.0.1:33000 for the CubeAPI, 127.0.0.1:80 for the
// CubeProxy). A ready template is auto-discovered from /templates unless
// CUBE_TEMPLATE_ID is supplied.
//
// Every test hands its sandboxes back through Cleanup / Delete, so a
// clean run should leave no live MicroVMs behind.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// integrationDefaultAPIURL / integrationDefaultProxyURL match the user's
	// local Cube deployment: CubeAPI on 33000, CubeProxy (openresty inside
	// the cube-proxy docker container) on the host's port 80. The Dashboard
	// (cube-webui) is exposed on 12088 and MUST NOT be used as a data-plane
	// endpoint — POST requests against it return 405 because Dashboard is a
	// static SPA server, not a routing proxy.
	integrationDefaultAPIURL   = "http://127.0.0.1:33000"
	integrationDefaultProxyURL = "http://127.0.0.1:80"

	integrationSandboxTTL         = 5 * time.Minute
	integrationHTTPTimeout        = 30 * time.Second
	integrationDefaultExecTimeout = 60 * time.Second
)

// integrationConfig builds a Config suitable for talking to the on-host Cube
// deployment. It probes /templates so tests survive template ID rotations,
// and applies short timeouts so a broken environment fails loudly instead of
// hanging.
func integrationConfig(t *testing.T) *Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeCube

	if v := strings.TrimSpace(os.Getenv("CUBE_API_URL")); v != "" {
		cfg.CubeAPIURL = v
	} else {
		cfg.CubeAPIURL = integrationDefaultAPIURL
	}
	if v := strings.TrimSpace(os.Getenv("CUBE_PROXY_URL")); v != "" {
		cfg.CubeProxyURL = v
	} else {
		cfg.CubeProxyURL = integrationDefaultProxyURL
	}
	cfg.CubeSandboxDomain = DefaultCubeSandboxDomain
	cfg.CubeHTTPTimeout = integrationHTTPTimeout
	cfg.CubeSandboxTTL = integrationSandboxTTL
	cfg.DefaultTimeout = integrationDefaultExecTimeout

	if v := strings.TrimSpace(os.Getenv("CUBE_TEMPLATE_ID")); v != "" {
		cfg.CubeTemplate = v
	} else {
		cfg.CubeTemplate = discoverReadyTemplate(t, cfg.CubeAPIURL, cfg.CubeAPIKey)
	}

	t.Logf("Cube integration target api=%s proxy=%s template=%s domain=%s",
		cfg.CubeAPIURL, cfg.CubeProxyURL, cfg.CubeTemplate, cfg.CubeSandboxDomain)
	return cfg
}

// discoverReadyTemplate mirrors what the SDK's own integration suite does:
// pick the first READY template from the CubeAPI /templates listing so
// developers don't have to hard-code a template ID for local runs.
func discoverReadyTemplate(t *testing.T, apiURL, apiKey string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiURL, "/")+"/templates", nil)
	if err != nil {
		t.Fatalf("build templates request: %v", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list templates from %s: %v", apiURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list templates HTTP %d from %s", resp.StatusCode, apiURL)
	}

	var templates []struct {
		TemplateID string `json:"templateID"`
		Status     string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&templates); err != nil {
		t.Fatalf("decode templates: %v", err)
	}
	for _, tpl := range templates {
		if tpl.TemplateID != "" && strings.EqualFold(tpl.Status, "READY") {
			return tpl.TemplateID
		}
	}
	if len(templates) > 0 && templates[0].TemplateID != "" {
		return templates[0].TemplateID
	}
	t.Fatalf("no templates found at %s; set CUBE_TEMPLATE_ID", apiURL)
	return ""
}

// writeIntegrationScript drops a small Python script in a t.TempDir() so
// tests have a real filesystem path for ExecuteConfig.Script (the security
// validator still needs to read the file even though the sandbox executes
// the uploaded copy).
func writeIntegrationScript(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := dir + "/" + name
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// -----------------------------------------------------------------------------
// Client-level tests (CubeRemoteClient directly)
// -----------------------------------------------------------------------------

// TestIntegrationCubeClient_HealthAndList sanity-checks that the /health
// endpoint responds and ListSandboxes deserialises. Failure here almost
// always means CubeAPI isn't running on the expected port.
func TestIntegrationCubeClient_HealthAndList(t *testing.T) {
	cfg := integrationConfig(t)
	client, err := NewCubeRemoteClient(cfg)
	if err != nil {
		t.Fatalf("NewCubeRemoteClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := client.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}
	summaries, err := client.List(ctx, RemoteListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	t.Logf("current live sandboxes: %d", len(summaries))
}

// TestIntegrationCubeClient_CreateConnectRoundTrip validates that Create +
// Connect + Get form a consistent lifecycle round-trip through the real
// CubeAPI. It replaces the old ConnectRoundTripRequiresTimeout test which
// verified the SDK connect body patch — that patch is now handled internally
// by CubeRemoteClient.
func TestIntegrationCubeClient_CreateConnectRoundTrip(t *testing.T) {
	cfg := integrationConfig(t)
	client, err := NewCubeRemoteClient(cfg)
	if err != nil {
		t.Fatalf("NewCubeRemoteClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: cfg.CubeTemplate,
		Timeout: RemoteTimeoutPolicy{
			Mode:   RemoteTimeoutExplicit,
			Value:  integrationSandboxTTL,
			Action: RemoteOnTimeoutKill,
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sandboxID := handle.ID()
	t.Logf("created sandbox %s", sandboxID)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.Delete(cleanupCtx, sandboxID); err != nil {
			t.Logf("cleanup delete sandbox %s: %v", sandboxID, err)
		}
	})

	// Verify the sandbox is immediately visible via Get.
	summary, err := client.Get(ctx, sandboxID)
	if err != nil {
		t.Fatalf("Get after create: %v", err)
	}
	if summary == nil || summary.ID != sandboxID {
		t.Fatalf("Get returned unexpected summary: %#v", summary)
	}

	// Reconnect to the same sandbox — this is the critical path that
	// exercises CubeAPI's /sandboxes/{id}/connect endpoint.
	reattached, err := client.Connect(ctx, RemoteConnectRequest{SandboxID: sandboxID})
	if err != nil {
		t.Fatalf("Connect existing sandbox via real CubeAPI: %v", err)
	}
	if reattached == nil || reattached.ID() != sandboxID {
		t.Fatalf("reattached sandbox ID = %s, want %s", reattached.ID(), sandboxID)
	}

	// Verify the reattached handle can also be looked up.
	summary2, err := client.Get(ctx, sandboxID)
	if err != nil {
		t.Fatalf("Get after reconnect: %v", err)
	}
	if summary2 == nil || summary2.ID != sandboxID {
		t.Fatalf("Get after reconnect returned unexpected summary: %#v", summary2)
	}
}

// TestIntegrationCubeClient_LifecycleRoundTrip exercises the full lifecycle
// against a real sandbox: Create → WriteFile → ReadFile → Exec → Delete.
func TestIntegrationCubeClient_LifecycleRoundTrip(t *testing.T) {
	cfg := integrationConfig(t)
	client, err := NewCubeRemoteClient(cfg)
	if err != nil {
		t.Fatalf("NewCubeRemoteClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: cfg.CubeTemplate,
		Timeout: RemoteTimeoutPolicy{
			Mode:   RemoteTimeoutExplicit,
			Value:  integrationSandboxTTL,
			Action: RemoteOnTimeoutKill,
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sandboxID := handle.ID()
	t.Logf("created sandbox %s", sandboxID)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.Delete(cleanupCtx, sandboxID); err != nil {
			t.Logf("cleanup delete sandbox %s: %v", sandboxID, err)
		}
	})

	// Write a file inside the sandbox.
	path := "/tmp/weknora-integration.txt"
	payload := []byte("hello from weknora integration\n")
	if err := client.WriteFile(ctx, handle, path, payload); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Read the file back.
	got, err := client.ReadFile(ctx, handle, path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("read back mismatch: got=%q want=%q", string(got), string(payload))
	}

	// Run a shell command that echoes the file to stdout.
	result, err := client.Exec(ctx, handle, RemoteExecRequest{
		Command: "cat",
		Args:    []string{path},
		WorkDir: "/tmp",
		Timeout: integrationDefaultExecTimeout,
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Exec exit code: %d stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "hello from weknora integration") {
		t.Fatalf("stdout missing marker: %q", result.Stdout)
	}

	// Explicitly delete and verify it's gone.
	if err := client.Delete(ctx, sandboxID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	summary, err := client.Get(ctx, sandboxID)
	if err != nil {
		if IsRemoteNotFound(err) {
			t.Logf("sandbox %s confirmed deleted (not-found)", sandboxID)
			return
		}
		t.Fatalf("Get after delete: %v", err)
	}
	if summary != nil {
		t.Logf("sandbox still visible right after delete (state=%s) — acceptable eventual-consistency window", summary.State)
	}
}

// TestIntegrationCubeClient_FilesystemOps covers the filesystem RPCs:
// MakeDir, WriteFile, ListDir, Stat, and Remove. It's kept separate from
// the lifecycle test so failures point at the right subsystem.
func TestIntegrationCubeClient_FilesystemOps(t *testing.T) {
	cfg := integrationConfig(t)
	client, err := NewCubeRemoteClient(cfg)
	if err != nil {
		t.Fatalf("NewCubeRemoteClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: cfg.CubeTemplate,
		Timeout: RemoteTimeoutPolicy{
			Mode:   RemoteTimeoutExplicit,
			Value:  integrationSandboxTTL,
			Action: RemoteOnTimeoutKill,
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = client.Delete(cleanupCtx, handle.ID())
	})

	base := "/tmp/weknora-fs"
	if err := client.MakeDir(ctx, handle, base); err != nil {
		t.Fatalf("MakeDir %s: %v", base, err)
	}

	// Write two files to verify listing.
	src := base + "/one.txt"
	another := base + "/another.txt"
	if err := client.WriteFile(ctx, handle, src, []byte("aaa")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := client.WriteFile(ctx, handle, another, []byte("bbb")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries, err := client.ListDir(ctx, handle, base)
	if err != nil {
		t.Fatalf("ListDir %s: %v", base, err)
	}
	foundOne := false
	foundAnother := false
	for _, e := range entries {
		if e.Name == "one.txt" {
			foundOne = true
		}
		if e.Name == "another.txt" {
			foundAnother = true
		}
	}
	if !foundOne || !foundAnother {
		t.Fatalf("ListDir did not surface expected files: %#v", entries)
	}

	stat, err := client.Stat(ctx, handle, src)
	if err != nil {
		t.Fatalf("Stat %s: %v", src, err)
	}
	if stat == nil {
		t.Fatalf("Stat returned nil for existing path %s", src)
	}
	if stat.Type != RemoteEntryFile {
		t.Fatalf("Stat type=%q, want file", stat.Type)
	}

	missing, err := client.Stat(ctx, handle, base+"/does-not-exist")
	if err != nil {
		t.Fatalf("Stat missing: unexpected error %v", err)
	}
	if missing != nil {
		t.Fatalf("Stat missing returned entry: %#v", missing)
	}

	if err := client.Remove(ctx, handle, src); err != nil {
		t.Fatalf("Remove %s: %v", src, err)
	}
	remaining, err := client.ListDir(ctx, handle, base)
	if err != nil {
		t.Fatalf("ListDir after remove: %v", err)
	}
	for _, e := range remaining {
		if e.Name == "one.txt" {
			t.Fatalf("'one.txt' still present after Remove")
		}
	}

	if err := client.Remove(ctx, handle, base); err != nil {
		t.Fatalf("Remove %s: %v", base, err)
	}
}

// -----------------------------------------------------------------------------
// End-to-end tests through SessionBoundManager
// -----------------------------------------------------------------------------

// TestIntegrationRemoteSandbox_EphemeralExecute exercises the empty-SessionID
// path through SessionBoundManager: the manager allocates a fresh MicroVM,
// runs the script, and tears it down — same wire behaviour Docker
// sandboxes present per Execute.
func TestIntegrationRemoteSandbox_EphemeralExecute(t *testing.T) {
	mgr := newIntegrationManager(t)
	script := writeIntegrationScript(t, "hello.py", "print('weknora-integration-hi')\n")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := mgr.Execute(ctx, &ExecuteConfig{
		Script:         script,
		SkipValidation: true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code %d stderr=%q err=%q", result.ExitCode, result.Stderr, result.Error)
	}
	if !strings.Contains(result.Stdout, "weknora-integration-hi") {
		t.Fatalf("stdout missing expected marker: %q", result.Stdout)
	}
}

// TestIntegrationSessionBoundManager_StatePersistsAcrossExecutes verifies
// the flagship feature of the Cube backend: two Execute calls that share the
// same SessionID must hit the same MicroVM, so packages installed / files
// created by the first call are visible to the second.
func TestIntegrationSessionBoundManager_StatePersistsAcrossExecutes(t *testing.T) {
	mgr := newIntegrationManager(t)

	baseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ctx := integrationTenantContext(baseCtx)

	first := writeIntegrationScript(t, "write.py", strings.Join([]string{
		"with open('/tmp/weknora-session-marker', 'w') as f:",
		"    f.write('session-state-ok')",
		"print('wrote marker')",
		"",
	}, "\n"))
	second := writeIntegrationScript(t, "read.py", strings.Join([]string{
		"with open('/tmp/weknora-session-marker') as f:",
		"    print('marker=' + f.read())",
		"",
	}, "\n"))

	sess := "integration-sess-alpha"
	if r, err := mgr.Execute(ctx, &ExecuteConfig{
		Script: first, SessionID: sess, SkipValidation: true,
	}); err != nil || r.ExitCode != 0 {
		t.Fatalf("first Execute: err=%v exit=%d stderr=%q", err, safeExit(r), safeStderr(r))
	}
	r2, err := mgr.Execute(ctx, &ExecuteConfig{
		Script: second, SessionID: sess, SkipValidation: true,
	})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if r2.ExitCode != 0 {
		t.Fatalf("second Execute exit=%d stderr=%q", r2.ExitCode, r2.Stderr)
	}
	if !strings.Contains(r2.Stdout, "marker=session-state-ok") {
		t.Fatalf("session state didn't persist across executes; stdout=%q", r2.Stdout)
	}

	// Third leg: a *different* SessionID must NOT see the marker. This is
	// the negative half of the isolation contract; skipping it would let a
	// regression that collapses all sessions onto the same VM slip by.
	miss := writeIntegrationScript(t, "miss.py", strings.Join([]string{
		"import os",
		"print('exists=' + str(os.path.exists('/tmp/weknora-session-marker')))",
		"",
	}, "\n"))
	r3, err := mgr.Execute(ctx, &ExecuteConfig{
		Script: miss, SessionID: "integration-sess-beta", SkipValidation: true,
	})
	if err != nil {
		t.Fatalf("third Execute: %v", err)
	}
	if r3.ExitCode != 0 {
		t.Fatalf("third Execute exit=%d stderr=%q", r3.ExitCode, r3.Stderr)
	}
	if !strings.Contains(r3.Stdout, "exists=False") {
		t.Fatalf("session isolation broken; stdout=%q", r3.Stdout)
	}
}

// TestIntegrationSessionBoundManager_DestroySession asserts that
// DestroySession actually reaches CubeAPI and cleans the MicroVM up.
func TestIntegrationSessionBoundManager_DestroySession(t *testing.T) {
	mgr := newIntegrationManager(t)

	baseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ctx := integrationTenantContext(baseCtx)

	script := writeIntegrationScript(t, "touch.py", "print('destroy-me')\n")
	if _, err := mgr.Execute(ctx, &ExecuteConfig{
		Script:         script,
		SessionID:      "integration-destroy",
		SkipValidation: true,
	}); err != nil {
		t.Fatalf("prime Execute: %v", err)
	}

	if err := mgr.DestroySession(ctx, "integration-destroy"); err != nil {
		t.Fatalf("DestroySession: %v", err)
	}
	// Second destroy is a no-op.
	if err := mgr.DestroySession(ctx, "integration-destroy"); err != nil {
		t.Fatalf("second DestroySession: %v", err)
	}
}

// safeExit / safeStderr shield the assertion helpers above from nil results
// so a transport error doesn't crash the test before we've had a chance to
// report the real cause.
func safeExit(r *ExecuteResult) int {
	if r == nil {
		return -1
	}
	return r.ExitCode
}

func safeStderr(r *ExecuteResult) string {
	if r == nil {
		return ""
	}
	return r.Stderr
}

// newIntegrationManager wires a SessionBoundManager against the live Cube
// deployment described by integrationConfig. Every integration test uses this
// helper so provider adapter, binding store, and existence checker stay in
// one place.
func newIntegrationManager(t *testing.T) *SessionBoundManager {
	t.Helper()
	cfg := integrationConfig(t)
	client, err := NewCubeRemoteClient(cfg)
	if err != nil {
		t.Fatalf("NewCubeRemoteClient: %v", err)
	}
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:  cfg,
		Client:  client,
		Store:   NewMemorySessionSandboxBindingStore(),
		Checker: PermissiveSessionExistenceChecker{},
	})
	if err != nil {
		t.Fatalf("NewSessionBoundManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Cleanup(context.Background()) })
	return mgr
}

// integrationTenantContext supplies the tenant ID SessionBoundManager needs
// when resolving session-scoped operations.
func integrationTenantContext(parent context.Context) context.Context {
	return context.WithValue(parent, types.TenantIDContextKey, uint64(1))
}
