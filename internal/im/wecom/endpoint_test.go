package wecom

import (
	"os"
	"strings"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// setWhitelistForTest swaps the SSRF whitelist singleton for the duration of
// the test, following the pattern in internal/utils/security_test.go.
func setWhitelistForTest(t *testing.T, entries string) {
	t.Helper()
	secutils.ResetSSRFWhitelistForTest()
	os.Setenv("SSRF_WHITELIST", entries)
	t.Cleanup(func() {
		os.Unsetenv("SSRF_WHITELIST")
		secutils.ResetSSRFWhitelistForTest()
	})
}

// A custom wss:// endpoint whose host is on the SSRF whitelist must be
// accepted. Before the https-mask validation every custom ws/wss endpoint
// failed with "invalid scheme: wss (only http/https allowed)" (#4047).
func TestValidateEndpointURLAcceptsWhitelistedCustomWssEndpoint(t *testing.T) {
	setWhitelistForTest(t, "im.example.com")

	err := validateEndpointURL("wss://im.example.com/im_open/longconn", defaultWSEndpoint, "wss")
	if err != nil {
		t.Fatalf("whitelisted custom wss endpoint should be accepted, got: %v", err)
	}
}

// The scheme pin is untouched: an https:// URL is still rejected where a wss
// endpoint is required, and vice versa.
func TestValidateEndpointURLSchemePinUnchanged(t *testing.T) {
	if err := validateEndpointURL("https://im.example.com", defaultWSEndpoint, "wss"); err == nil || !strings.Contains(err.Error(), "wss://") {
		t.Fatalf("https endpoint must be rejected for a wss slot, got: %v", err)
	}
	if err := validateEndpointURL("wss://api.example.com", defaultAPIBaseURL, "https"); err == nil || !strings.Contains(err.Error(), "https://") {
		t.Fatalf("wss endpoint must be rejected for an https slot, got: %v", err)
	}
}

// The https mask must not weaken host-level guards: private-IP endpoints stay
// blocked.
func TestValidateEndpointURLStillBlocksPrivateIPWssEndpoint(t *testing.T) {
	setWhitelistForTest(t, "")

	err := validateEndpointURL("wss://192.168.1.10/ws", defaultWSEndpoint, "wss")
	if err == nil {
		t.Fatal("private-IP wss endpoint must stay blocked")
	}
}

// The default endpoint keeps its short-circuit (no validation, no DNS).
func TestValidateEndpointURLDefaultWSEndpointShortCircuits(t *testing.T) {
	if err := validateEndpointURL(defaultWSEndpoint, defaultWSEndpoint, "wss"); err != nil {
		t.Fatalf("default endpoint should short-circuit, got: %v", err)
	}
}

// The https path (API base URL) keeps its original behavior end to end.
func TestValidateEndpointURLHttpsPathUnchanged(t *testing.T) {
	setWhitelistForTest(t, "api.example.com")

	if err := validateEndpointURL("https://api.example.com", defaultAPIBaseURL, "https"); err != nil {
		t.Fatalf("whitelisted https base URL should keep working, got: %v", err)
	}
}
