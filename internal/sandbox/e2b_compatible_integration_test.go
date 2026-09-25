//go:build e2b_integration

// Conformance test for E2B-protocol control planes.
//
// WeKnora treats "E2B protocol" as the single integration contract for remote
// sandboxes, so the same suite must pass against every implementation of it:
// E2B Cloud, a self-hosted e2b-dev/infra, CubeSandbox's CubeAPI, or a
// container-backed gateway such as Agent-Sandbox. It drives the same public
// surface the agent runtime uses — session-scoped script execution, shell
// commands, attachment staging, artifact listing, teardown — rather than the
// individual client methods, so a backend that passes here is usable by the
// product and not merely reachable.
//
// Run with:
//
//	E2B_INTEGRATION_API_URL=http://127.0.0.1:18080/e2b/v1 \
//	E2B_INTEGRATION_API_KEY=<token> \
//	E2B_INTEGRATION_TEMPLATE=code-interpreter \
//	E2B_INTEGRATION_SANDBOX_DOMAIN=localhost \
//	E2B_INTEGRATION_PROXY_URL=http://127.0.0.1:18080 \
//	go test -tags=e2b_integration ./internal/sandbox \
//	  -run '^TestE2BCompatibleControlPlaneConformance' -count=1 -v -timeout=15m
//
// E2B_INTEGRATION_PROXY_URL is the data-plane gateway. Leave it empty for E2B
// Cloud, whose sandbox domain resolves through public DNS over TLS.
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	conformanceTenantID   = 1
	conformanceTTL        = 10 * time.Minute
	conformanceHTTPTimeut = 60 * time.Second
	conformanceExecUser   = "E2B_INTEGRATION_EXEC_USER"
)

func TestE2BCompatibleControlPlaneConformance(t *testing.T) {
	cfg := e2bCompatibleConfig(t)
	client, err := NewE2BRemoteClientWithPool(
		cfg,
		NewSandboxGatewayTransportPoolWithPolicy(nil, OutboundURLPolicy{AllowPrivate: true}),
	)
	if err != nil {
		t.Fatalf("build E2B-protocol client: %v", err)
	}

	ctx, cancel := context.WithTimeout(
		types.WithSandboxTenantID(context.Background(), conformanceTenantID),
		12*time.Minute,
	)
	defer cancel()

	if err := client.Health(ctx); err != nil {
		t.Fatalf("control plane health: %v", err)
	}

	manager, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config:          cfg,
		Client:          client,
		Store:           NewMemorySessionSandboxBindingStore(),
		Checker:         PermissiveSessionExistenceChecker{},
		ConfigID:        "conformance",
		SkipHealthProbe: true,
	})
	if err != nil {
		t.Fatalf("NewSessionBoundManager: %v", err)
	}

	sessionID := fmt.Sprintf("conformance-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			types.WithSandboxTenantID(context.Background(), conformanceTenantID),
			2*time.Minute,
		)
		defer cleanupCancel()
		if err := manager.DestroySession(cleanupCtx, sessionID); err != nil {
			t.Errorf("DestroySession: %v", err)
		}
	})

	// State is written under the artifact directory rather than an arbitrary
	// path: that directory is the one WeKnora provisions and grants to the
	// script account, so the assertion tests session persistence instead of a
	// template's /workspace permissions.
	counterPath := path.Join(SessionOutputRoot, "counter.txt")
	t.Run("SessionScopedStatePersistsAcrossExecutions", func(t *testing.T) {
		first := runConformanceScript(t, ctx, manager, sessionID, fmt.Sprintf(`
with open(%q, 'w') as handle:
    handle.write('1')
print('wrote counter')
`, counterPath))
		if !first.IsSuccess() {
			t.Fatalf("first execution failed: %#v", first)
		}

		second := runConformanceScript(t, ctx, manager, sessionID, fmt.Sprintf(`
with open(%q) as handle:
    print('counter=' + handle.read())
`, counterPath))
		if !second.IsSuccess() {
			t.Fatalf("second execution failed: %#v", second)
		}
		if !strings.Contains(second.Stdout, "counter=1") {
			t.Fatalf("session state did not persist across executions: stdout=%q stderr=%q",
				second.Stdout, second.Stderr)
		}
	})

	t.Run("ShellExecSharesTheSessionSandbox", func(t *testing.T) {
		executor := manager.SessionShellExecutor()
		if executor == nil {
			t.Fatal("session shell executor is unavailable on a healthy remote backend")
		}
		result, err := executor.ExecShellCommand(
			ctx, sessionID, "cat "+counterPath, SessionWorkspaceRoot,
			time.Minute, nil,
		)
		if err != nil {
			t.Fatalf("ExecShellCommand: %v", err)
		}
		if !result.IsSuccess() || !strings.Contains(result.Stdout, "1") {
			t.Fatalf("shell command did not observe the session sandbox: %#v", result)
		}
	})

	t.Run("AttachmentStagingAndArtifactCollection", func(t *testing.T) {
		files := manager.SessionFileStore()
		if files == nil {
			t.Fatal("session file store is unavailable on a healthy remote backend")
		}
		inputPath := path.Join(SessionInputRoot, "attachment.txt")
		payload := []byte("attachment payload\n")
		if err := files.WriteSessionInputFile(ctx, sessionID, inputPath, payload); err != nil {
			t.Fatalf("WriteSessionInputFile: %v", err)
		}
		content, err := files.ReadSessionFile(ctx, sessionID, inputPath)
		if err != nil {
			t.Fatalf("ReadSessionFile: %v", err)
		}
		if !bytes.Equal(content, payload) {
			t.Fatalf("staged attachment mismatch: got=%q want=%q", content, payload)
		}

		result := runConformanceScript(t, ctx, manager, sessionID, fmt.Sprintf(`
import os
target = os.path.join(os.environ['%s'], 'report.txt')
with open(target, 'w') as handle:
    handle.write('artifact body')
print('artifact written')
`, skillOutputEnvVar))
		if !result.IsSuccess() {
			t.Fatalf("artifact-producing execution failed: %#v", result)
		}

		entries, err := files.ListSessionFiles(ctx, sessionID, SessionOutputRoot)
		if err != nil {
			t.Fatalf("ListSessionFiles: %v", err)
		}
		found := false
		for _, entry := range entries {
			if entry.Name == "report.txt" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("artifact directory did not contain report.txt: %#v", entries)
		}

		if err := files.RemoveSessionInputPath(ctx, sessionID, inputPath); err != nil {
			t.Fatalf("RemoveSessionInputPath: %v", err)
		}
	})

	t.Run("TimeoutIsReportedAsKilled", func(t *testing.T) {
		result := runConformanceScriptWithTimeout(t, ctx, manager, sessionID, `
import time
time.sleep(30)
`, 5*time.Second)
		if !result.Killed {
			t.Fatalf("expected a killed result for an over-running script: %#v", result)
		}
	})
}

// runConformanceScript executes source as a session-scoped Python script,
// mirroring how the skills runtime invokes the sandbox.
func runConformanceScript(
	t *testing.T,
	ctx context.Context,
	manager *SessionBoundManager,
	sessionID string,
	source string,
) *ExecuteResult {
	t.Helper()
	return runConformanceScriptWithTimeout(t, ctx, manager, sessionID, source, 2*time.Minute)
}

func runConformanceScriptWithTimeout(
	t *testing.T,
	ctx context.Context,
	manager *SessionBoundManager,
	sessionID string,
	source string,
	timeout time.Duration,
) *ExecuteResult {
	t.Helper()
	result, err := manager.Execute(ctx, &ExecuteConfig{
		Script:         "conformance.py",
		ScriptContent:  source,
		SessionID:      sessionID,
		Timeout:        timeout,
		SkipValidation: true,
		Env: map[string]string{
			skillOutputEnvVar: SessionOutputRoot,
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned no result")
	}
	t.Logf("execute exit=%d killed=%v stdout=%q stderr=%q err=%q",
		result.ExitCode, result.Killed, result.Stdout, result.Stderr, result.Error)
	return result
}

func e2bCompatibleConfig(t *testing.T) *Config {
	t.Helper()
	apiKey := firstNonEmptyEnvironment("E2B_INTEGRATION_API_KEY", "E2B_API_KEY")
	template := firstNonEmptyEnvironment("E2B_INTEGRATION_TEMPLATE", "E2B_TEMPLATE")
	if apiKey == "" || template == "" {
		t.Skip("E2B-protocol conformance requires an API key and a template")
	}

	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	cfg.AllowPrivateEndpoints = true
	cfg.E2BAPIKey = apiKey
	cfg.E2BTemplate = template
	cfg.E2BAPIURL = strings.TrimSpace(os.Getenv("E2B_INTEGRATION_API_URL"))
	cfg.E2BSandboxDomain = strings.TrimSpace(os.Getenv("E2B_INTEGRATION_SANDBOX_DOMAIN"))
	cfg.E2BProxyURL = strings.TrimSpace(os.Getenv("E2B_INTEGRATION_PROXY_URL"))
	cfg.E2BSandboxTTL = conformanceTTL
	cfg.E2BHTTPTimeout = conformanceHTTPTimeut
	cfg.DefaultTimeout = 2 * time.Minute
	return cfg
}
