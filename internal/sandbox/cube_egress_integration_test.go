//go:build integration

package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestIntegrationCubeClient_PublicEgress is the same check the settings
// wizard runs as "出网可用". It needs cube-egress listening on the cube-dev
// gateway and FORWARD/MASQUERADE for sandbox DNS.
func TestIntegrationCubeClient_PublicEgress(t *testing.T) {
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
		if err := client.Delete(cleanupCtx, handle.ID()); err != nil {
			t.Logf("cleanup delete sandbox %s: %v", handle.ID(), err)
		}
	})

	result, err := client.Exec(ctx, handle, RemoteExecRequest{
		Command: `if curl -fsS -o /dev/null -m 8 -I 'https://www.baidu.com'; then echo 'cn:baidu'; exit 0; fi; echo "all probes failed" >&2; exit 1`,
		Shell:   true,
		User:    DefaultSandboxExecUser,
		Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("egress Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("sandbox egress failed exit=%d stdout=%q stderr=%q",
			result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "cn:baidu") {
		t.Fatalf("egress stdout missing marker: %q", result.Stdout)
	}
}
