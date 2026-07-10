//go:build integration

package sandbox

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestIntegrationCubeNetworkDiagnostics(t *testing.T) {
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
		_ = client.killSandboxByInfo(cleanupCtx, info)
	})

	// Read the actual sandbox_test.py from disk and upload it to the sandbox,
	// so we validate the real skill after our egress patch.
	src, err := os.ReadFile("/data/workspace/WeKnora/skills/preloaded/sandbox-test-python/scripts/sandbox_test.py")
	if err != nil {
		t.Fatalf("read local sandbox_test.py: %v", err)
	}
	if err := client.WriteFile(ctx, info, "/tmp/sandbox_test.py", src); err != nil {
		t.Fatalf("upload sandbox_test.py: %v", err)
	}

	result, err := client.RunCommand(ctx, info, "/bin/bash",
		[]string{"-lc", "python3 /tmp/sandbox_test.py --test net"}, "", nil, "/tmp")
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	t.Logf("exit=%d\nstdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
}
