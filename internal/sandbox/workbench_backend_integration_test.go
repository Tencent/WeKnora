//go:build sandbox_terminal_integration || workbench_integration

package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Require dedicated test endpoints as well as credentials. Empty Docker hosts
// and E2B endpoints would otherwise consult the caller's environment or context.
func workbenchIntegrationConfig(
	backend SandboxType, getenv func(string) string,
) (*Config, []string, error) {
	cfg := DefaultConfig()
	cfg.Type = backend
	prefix := "WORKBENCH_TEST_" + strings.ToUpper(string(backend)) + "_"
	var missing []string
	read := func(name string) string { return strings.TrimSpace(getenv(prefix + name)) }
	required := func(name string) string {
		value := read(name)
		if value == "" {
			missing = append(missing, prefix+name)
		}
		return value
	}
	switch backend {
	case SandboxTypeDocker:
		cfg.DockerHost = required("HOST")
		cfg.DockerImage = required("IMAGE")
		cfg.DockerTLSCertPath = read("TLS_CERT_PATH")
		cfg.DockerNetworkMode = "none"
	case SandboxTypeE2B:
		cfg.E2BAPIURL = required("API_URL")
		cfg.E2BAPIKey = required("API_KEY")
		cfg.E2BSandboxDomain = required("SANDBOX_DOMAIN")
		cfg.E2BTemplate = required("TEMPLATE")
		cfg.E2BProxyURL = read("PROXY_URL")
		cfg.E2BSandboxTTL = 10 * time.Minute
		cfg.E2BHTTPTimeout = time.Minute
	default:
		return nil, nil, fmt.Errorf("unsupported workbench test backend: %s", backend)
	}
	if len(missing) > 0 {
		return nil, missing, nil
	}
	if value := read("ALLOW_PRIVATE"); value != "" {
		allowed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, nil, fmt.Errorf("%sALLOW_PRIVATE must be a boolean", prefix)
		}
		cfg.AllowPrivateEndpoints = allowed
	}
	return cfg, nil, nil
}

func workbenchRealBackendConfig(t *testing.T, backend SandboxType) *Config {
	t.Helper()
	cfg, missing, err := workbenchIntegrationConfig(backend, os.Getenv)
	require.NoError(t, err)
	if len(missing) > 0 {
		t.Skipf("%s integration requires dedicated test configuration: %s", backend, strings.Join(missing, ", "))
	}
	return cfg
}

func newWorkbenchIntegrationClient(t *testing.T, cfg *Config) RemoteSandboxClient {
	t.Helper()
	var client RemoteSandboxClient
	var err error
	if cfg.Type == SandboxTypeDocker {
		// Avoid daemon-wide idle sweeps; cleanup only destroys this test's session.
		client, err = NewDockerRemoteClientForCheck(cfg)
	} else {
		policy := OutboundURLPolicy{AllowPrivate: cfg.AllowPrivateEndpoints}
		client, err = NewE2BRemoteClientWithPool(cfg, NewSandboxGatewayTransportPoolWithPolicy(nil, policy))
	}
	require.NoError(t, err)
	return client
}

func TestWorkbenchIntegrationConfig(t *testing.T) {
	production := map[string]string{
		"DOCKER_HOST": "tcp://production.invalid:2376", "DOCKER_TLS_CERT_PATH": "/production/certs",
		"DOCKER_CONTEXT": "production", "E2B_API_KEY": "production-key",
		"E2B_TEMPLATE": "production-template", "E2B_API_URL": "https://production.invalid",
		"E2B_SANDBOX_URL": "production.invalid", "WORKBENCH_FILES_LOCAL_INTEGRATION": "1",
	}
	configs := map[SandboxType]map[string]string{
		SandboxTypeDocker: {
			"HOST": "tcp://test-daemon.invalid:2376", "IMAGE": "test-image:integration",
			"TLS_CERT_PATH": "/test/certs", "ALLOW_PRIVATE": "true",
		},
		SandboxTypeE2B: {
			"API_URL": "https://test-api.invalid", "API_KEY": "test-key", "TEMPLATE": "test-template",
			"SANDBOX_DOMAIN": "test-sandboxes.invalid", "PROXY_URL": "https://test-gateway.invalid",
		},
	}
	for backend, values := range configs {
		t.Run(string(backend), func(t *testing.T) {
			getenv := func(key string) string { return production[key] }
			cfg, required, err := workbenchIntegrationConfig(backend, getenv)
			require.NoError(t, err)
			require.Nil(t, cfg)
			require.NotEmpty(t, required)

			env := make(map[string]string)
			for key, value := range production {
				env[key] = value
			}
			prefix := "WORKBENCH_TEST_" + strings.ToUpper(string(backend)) + "_"
			for key, value := range values {
				env[prefix+key] = " " + value + " "
			}
			getenv = func(key string) string { return env[key] }
			cfg, missing, err := workbenchIntegrationConfig(backend, getenv)
			require.NoError(t, err)
			require.Empty(t, missing)
			require.Equal(t, backend, cfg.Type)
			if backend == SandboxTypeDocker {
				require.Equal(t, values["HOST"], cfg.DockerHost)
				require.Equal(t, values["IMAGE"], cfg.DockerImage)
				require.Equal(t, values["TLS_CERT_PATH"], cfg.DockerTLSCertPath)
				require.Equal(t, "none", cfg.DockerNetworkMode)
				require.True(t, cfg.AllowPrivateEndpoints)
			} else {
				require.Equal(t, values["API_URL"], cfg.E2BAPIURL)
				require.Equal(t, values["API_KEY"], cfg.E2BAPIKey)
				require.Equal(t, values["TEMPLATE"], cfg.E2BTemplate)
				require.Equal(t, values["SANDBOX_DOMAIN"], cfg.E2BSandboxDomain)
				require.Equal(t, values["PROXY_URL"], cfg.E2BProxyURL)
				require.False(t, cfg.AllowPrivateEndpoints)
			}
			for _, key := range required {
				original := env[key]
				env[key] = " "
				cfg, missing, err := workbenchIntegrationConfig(backend, getenv)
				require.NoError(t, err)
				require.Nil(t, cfg)
				require.Equal(t, []string{key}, missing)
				env[key] = original
			}
			env[prefix+"ALLOW_PRIVATE"] = "invalid"
			_, _, err = workbenchIntegrationConfig(backend, getenv)
			require.ErrorContains(t, err, prefix+"ALLOW_PRIVATE")
		})
	}
}
