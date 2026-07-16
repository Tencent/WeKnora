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
// dev defaults (127.0.0.1:33000 for the CubeAPI, 127.0.0.1:12088 for the
// CubeProxy). A ready template is auto-discovered from /templates unless
// CUBE_TEMPLATE_ID is supplied.
//
// Every test hands its sandboxes back through Cleanup / DestroySession, so a
// clean run should leave no live MicroVMs behind.
package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"

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
)

// integrationConfig builds a Config suitable for talking to the on-host Cube
// deployment. It probes /templates so tests survive template ID rotations,
// and applies short timeouts so a broken environment fails loudly instead of
// hanging.
func integrationConfig(t *testing.T) *Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeCube
	cfg.FallbackEnabled = false

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
	cfg.CubeEnvdPort = DefaultCubeEnvdPort
	cfg.CubeHTTPTimeout = 30 * time.Second
	cfg.CubeSandboxTTL = 5 * time.Minute
	cfg.CubeIdleTTL = time.Minute
	cfg.DefaultTimeout = 60 * time.Second

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
// Client-level tests (thin wrapper directly over cubeClient)
// -----------------------------------------------------------------------------

// TestIntegrationCubeClient_HealthAndList sanity-checks that the /health
// endpoint responds and ListSandboxes deserialises. Failure here almost
// always means CubeAPI isn't running on the expected port.
func TestIntegrationCubeClient_HealthAndList(t *testing.T) {
	cfg := integrationConfig(t)
	client := newCubeClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := client.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}
	summaries, err := client.ListSandboxes(ctx)
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	t.Logf("current live sandboxes: %d", len(summaries))
}

// TestIntegrationCubeClient_ConnectRoundTripRequiresTimeout verifies the
// real CubeAPI /sandboxes/{id}/connect path against the local deployment.
// CubeAPI v0.5.11 rejects the official SDK's empty connect body with HTTP 422
// (missing timeout). WeKnora injects that timeout in connectBodyRewriter; if
// the patch regresses, this test fails against the real service instead of a
// mock server.
func TestIntegrationCubeClient_ConnectRoundTripRequiresTimeout(t *testing.T) {
	cfg := integrationConfig(t)
	client := newCubeClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	info, err := client.CreateSandbox(ctx, cfg.CubeTemplate, cfg.CubeSandboxTTL)
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Logf("created sandbox %s (domain=%s)", info.ID, info.Domain)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.killSandboxByInfo(cleanupCtx, info); err != nil {
			t.Logf("cleanup kill sandbox %s: %v", info.ID, err)
		}
	})

	reattached, err := client.reattach(ctx, info.ID)
	if err != nil {
		t.Fatalf("Connect existing sandbox via real CubeAPI: %v", err)
	}
	if reattached == nil || reattached.SandboxID != info.ID {
		t.Fatalf("reattached sandbox ID = %#v, want %s", reattached, info.ID)
	}

	remoteInfo, err := reattached.GetInfo(ctx)
	if err != nil {
		t.Fatalf("GetInfo after real connect: %v", err)
	}
	if remoteInfo == nil || remoteInfo.SandboxID != info.ID {
		t.Fatalf("GetInfo sandbox ID = %#v, want %s", remoteInfo, info.ID)
	}
}

// TestIntegrationOfficialSDK_ConnectWithoutTimeout_ShowsFailure calls the
// official CubeSandbox SDK directly, without WeKnora's connectBodyRewriter.
// It is intentionally written to FAIL so the raw CubeAPI error is visible in
// `go test -v` output. Run it alone when you want to inspect the official SDK
// /sandboxes/{id}/connect failure shape.
func TestIntegrationOfficialSDK_ConnectWithoutTimeout_ShowsFailure(t *testing.T) {
	cfg := integrationConfig(t)
	client := newCubeClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	info, err := client.CreateSandbox(ctx, cfg.CubeTemplate, cfg.CubeSandboxTTL)
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Logf("created sandbox %s (domain=%s)", info.ID, info.Domain)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.killSandboxByInfo(cleanupCtx, info); err != nil {
			t.Logf("cleanup kill sandbox %s: %v", info.ID, err)
		}
	})

	sdkCfg := cubesandbox.Config{
		APIURL:         strings.TrimRight(cfg.CubeAPIURL, "/"),
		APIKey:         cfg.CubeAPIKey,
		TemplateID:     cfg.CubeTemplate,
		SandboxDomain:  cfg.CubeSandboxDomain,
		RequestTimeout: cfg.CubeHTTPTimeout,
	}
	if proxyHost, proxyPort, proxyScheme, ok := parseProxyURL(cfg.CubeProxyURL); ok {
		sdkCfg.ProxyNodeIP = proxyHost
		sdkCfg.ProxyPortHTTP = proxyPort
		sdkCfg.ProxyScheme = proxyScheme
	}

	officialSDK := cubesandbox.NewClient(sdkCfg)
	_, err = officialSDK.Connect(ctx, info.ID)
	if err != nil {
		t.Fatalf("official SDK Connect failed without WeKnora timeout patch, raw error: %v", err)
	}
	t.Fatalf("official SDK Connect unexpectedly succeeded without WeKnora timeout patch; sandbox=%s", info.ID)
}

// TestIntegrationCubeClient_LifecycleRoundTrip exercises the full lifecycle
// against a real sandbox: Create → WriteFile → RunCommand → ReadFile → Kill.
func TestIntegrationCubeClient_LifecycleRoundTrip(t *testing.T) {
	cfg := integrationConfig(t)
	client := newCubeClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	info, err := client.CreateSandbox(ctx, cfg.CubeTemplate, cfg.CubeSandboxTTL)
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Logf("created sandbox %s (domain=%s)", info.ID, info.Domain)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.killSandboxByInfo(cleanupCtx, info); err != nil {
			t.Logf("cleanup kill sandbox %s: %v", info.ID, err)
		}
	})

	path := "/tmp/weknora-integration.txt"
	payload := []byte("hello from weknora integration\n")
	if err := client.WriteFile(ctx, info, path, payload); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Read the file back through envd's /files GET path.
	got, err := client.ReadFile(ctx, info, path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("read back mismatch: got=%q want=%q", string(got), string(payload))
	}

	// Run a shell command that echoes the file to stdout, so we cover both
	// the process.Process/Start streaming path and the filesystem write.
	result, err := client.RunCommand(ctx, info, "cat", []string{path}, "", nil, "/tmp")
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("RunCommand exit code: %d stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "hello from weknora integration") {
		t.Fatalf("stdout missing marker: %q", result.Stdout)
	}

	// Kill leg is handled by t.Cleanup above; do an explicit kill so we can
	// assert that a subsequent GetSandbox surfaces the absence gracefully.
	if err := client.killSandboxByInfo(ctx, info); err != nil {
		t.Fatalf("kill sandbox: %v", err)
	}
	summary, err := client.GetSandbox(ctx, info.ID)
	if err != nil {
		t.Fatalf("GetSandbox after kill: %v", err)
	}
	if summary != nil {
		t.Logf("sandbox still visible right after kill (state=%s) — acceptable eventual-consistency window", summary.State)
	}
}

// TestIntegrationCubeClient_FilesystemOps covers the filesystem RPCs we
// wrap: MakeDir, ListDir, Move, Stat and Remove. It's kept separate from
// the lifecycle test so failures point at the right subsystem.
func TestIntegrationCubeClient_FilesystemOps(t *testing.T) {
	cfg := integrationConfig(t)
	client := newCubeClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	info, err := client.CreateSandbox(ctx, cfg.CubeTemplate, cfg.CubeSandboxTTL)
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = client.killSandboxByInfo(cleanupCtx, info)
	})

	base := "/tmp/weknora-fs"
	if err := client.MakeDir(ctx, info, base); err != nil {
		t.Fatalf("MakeDir %s: %v", base, err)
	}

	src := base + "/one.txt"
	dst := base + "/two.txt"
	if err := client.WriteFile(ctx, info, src, []byte("aaa")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := client.Move(ctx, info, src, dst); err != nil {
		t.Fatalf("Move %s -> %s: %v", src, dst, err)
	}

	entries, err := client.ListDir(ctx, info, base)
	if err != nil {
		t.Fatalf("ListDir %s: %v", base, err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "two.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListDir did not surface 'two.txt': %#v", entries)
	}

	stat, err := client.Stat(ctx, info, dst)
	if err != nil {
		t.Fatalf("Stat %s: %v", dst, err)
	}
	if stat == nil {
		t.Fatalf("Stat returned nil for existing path %s", dst)
	}
	if stat.Type != "file" {
		t.Fatalf("Stat type=%q, want file", stat.Type)
	}

	missing, err := client.Stat(ctx, info, base+"/does-not-exist")
	if err != nil {
		t.Fatalf("Stat missing: unexpected error %v", err)
	}
	if missing != nil {
		t.Fatalf("Stat missing returned entry: %#v", missing)
	}

	if err := client.Remove(ctx, info, dst); err != nil {
		t.Fatalf("Remove %s: %v", dst, err)
	}
	if err := client.Remove(ctx, info, base); err != nil {
		t.Fatalf("Remove %s: %v", base, err)
	}
}

// -----------------------------------------------------------------------------
// End-to-end tests through the WeKnora sandbox surface (CubeSandbox +
// SessionBoundManager)
// -----------------------------------------------------------------------------

// TestIntegrationRemoteSandbox_EphemeralExecute exercises the empty-SessionID
// path through SessionBoundManager: the manager allocates a fresh MicroVM,
// runs the script, and tears it down — same wire behaviour Docker/Local
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
