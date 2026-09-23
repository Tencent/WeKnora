package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

func whitelistOnly(t *testing.T, whitelist, only string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", whitelist)
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	t.Setenv("SSRF_DNS_WHITELIST_ONLY", only)
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
}

// In whitelist-only mode a tenant-supplied endpoint outside the whitelist is
// refused by name, before Validate resolves it (#3378). The refusal keeps the
// package's own error so callers classify it as they always did.
func TestPolicyRefusesNonWhitelistedHostBeforeResolving(t *testing.T) {
	whitelistOnly(t, "api.internal,10.0.0.0/8", "true")

	for _, raw := range []string{
		"https://outside.invalid/hook",
		"https://198.51.100.7/hook",
		"http://localhost:8080/hook",
	} {
		err := denyPrivate.Validate(raw)
		if !errors.Is(err, ErrUnsafeOutboundURL) || !errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
			t.Fatalf("%s: expected an unsafe-URL whitelist refusal, got %v", raw, err)
		}
	}

	// The loopback opt-in does not reopen what the whitelist closed.
	if err := allowPrivate.Validate("http://localhost:8080/hook"); !errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("AllowPrivate must not bypass the whitelist, got %v", err)
	}

	// A whitelisted IP literal still goes through the address checks.
	if err := denyPrivate.Validate("http://10.1.2.3:8080/hook"); errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("a whitelisted host must reach the usual checks, got %v", err)
	}
}

func TestPolicyLeavesNonWhitelistOnlyDeploymentsAlone(t *testing.T) {
	whitelistOnly(t, "api.internal", "false")

	if err := denyPrivate.Validate("https://outside.invalid/hook"); errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("with the mode off nothing is refused for being off-whitelist, got %v", err)
	}
}

// Every guarded transport dials through GuardedDialContext, so a name outside
// the whitelist is refused before the dialer resolves it; an address is left
// to the policy's own address checks.
func TestGuardedDialContextRefusesNonWhitelistedName(t *testing.T) {
	whitelistOnly(t, "api.internal", "true")
	dial := GuardedDialContext(denyPrivate)

	_, err := dial(context.Background(), "tcp", "outside.invalid:443")
	if !errors.Is(err, ErrUnsafeOutboundURL) || !errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("expected an unsafe-URL whitelist refusal, got %v", err)
	}

	_, err = dial(context.Background(), "tcp", "198.51.100.7:443")
	if errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("an address must be left to the address checks, got %v", err)
	}

	whitelistOnly(t, "api.internal", "false")
	_, err = GuardedDialContext(denyPrivate)(context.Background(), "tcp", "outside.invalid:443")
	if errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("with the mode off nothing is refused for being off-whitelist, got %v", err)
	}
}

// The HTTP data plane dials the gateway the same way the websocket one does,
// so whitelist-only mode has to reach it too (#3378).
func TestGatewayDataTransportRefusesNonWhitelistedGateway(t *testing.T) {
	whitelistOnly(t, "gateway.internal", "true")

	transport := newGatewayDataTransportWithPolicy("outside.invalid:443", denyPrivate)
	_, err := transport.DialContext(context.Background(), "tcp", "ignored.example:443")

	if !errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("expected the whitelist refusal, got %v", err)
	}

	// The SDK's authority is ignored, so a whitelisted authority cannot be
	// used to smuggle a non-whitelisted gateway past the check.
	_, err = newGatewayDataTransportWithPolicy("outside.invalid:443", denyPrivate).
		DialContext(context.Background(), "tcp", "gateway.internal:443")
	if !errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("the target, not the authority, must be judged; got %v", err)
	}

	whitelistOnly(t, "gateway.internal", "false")
	_, err = newGatewayDataTransportWithPolicy("outside.invalid:443", denyPrivate).
		DialContext(context.Background(), "tcp", "ignored.example:443")
	if errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("with the mode off nothing is refused for being off-whitelist, got %v", err)
	}
}
