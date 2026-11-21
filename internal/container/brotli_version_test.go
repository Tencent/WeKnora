//go:build !desktop

package container

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBrotliVersionStaysPatched guards against CVE-2026-111497 (critical,
// CVSS 9.8 out-of-bounds write in the Brotli decoder). The module is an
// indirect dependency pulled in by arrow-go, parquet-go, sentry-go and
// migrate/v4; minimal version selection previously resolved it to v1.2.0,
// which is compiled into the server binary and flagged by container
// scanners. The fix requires v1.2.7 or later (issue #5696).
func TestBrotliVersionStaysPatched(t *testing.T) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Version}}", "github.com/andybalholm/brotli")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	version := strings.TrimSpace(string(out))
	t.Logf("resolved brotli version: %s", version)
	// v1.2.7 is the first release carrying the decoder hardening that
	// closes CVE-2026-111497; anything older re-introduces the finding.
	require.NotEqual(t, "v1.2.0", version, "brotli reverted to the vulnerable version")
	require.True(t, version >= "v1.2.7", "brotli %s is older than the patched v1.2.7", version)
}
