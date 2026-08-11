package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The connectivity probe dials endpoints an admin typed into an unsaved form,
// so it is the one client whose target is fully attacker-influenced within a
// workspace. Save-time validation cannot cover it: a hostname can resolve to a
// public address while the form is validated and to the cloud metadata service
// by the time the probe connects. Both providers must therefore refuse
// link-local at dial time, and they must do so even under the private-endpoint
// opt-in that self-hosted Cube deployments need.
func TestNewRemoteClientForCheckRefusesLinkLocalAtDial(t *testing.T) {
	const metadata = "http://169.254.169.254"

	for _, tc := range []struct {
		name string
		cfg  *Config
	}{
		{
			name: "cube",
			cfg: &Config{
				AllowPrivateEndpoints: true,
				Type:                  SandboxTypeCube,
				CubeAPIURL:            metadata,
				CubeProxyURL:          metadata,
				CubeSandboxDomain:     "cube.app",
				CubeTemplate:          "tpl-test",
			},
		},
		{
			name: "e2b",
			cfg: &Config{
				AllowPrivateEndpoints: true,
				Type:                  SandboxTypeE2B,
				E2BAPIKey:             "key-test",
				E2BAPIURL:             metadata,
				E2BTemplate:           "base",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewRemoteClientForCheck(tc.cfg)
			require.NoError(t, err)

			err = client.Health(context.Background())
			require.Error(t, err, "probing the metadata service must fail")
			// The guard rejects before any packet leaves, so the refusal - not
			// a connection timeout - is what has to surface.
			require.True(t,
				strings.Contains(err.Error(), ErrUnsafeOutboundURL.Error()) ||
					strings.Contains(err.Error(), "never routable"),
				"expected the outbound guard to refuse the dial, got: %v", err)
		})
	}
}
